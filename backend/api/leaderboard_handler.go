package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/models"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// wsUpgrader upgrades HTTP connections to WebSocket.
// CheckOrigin is permissive here; tighten to known origins in production.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // TODO: restrict to frontend origin in production
	},
}

// LeaderboardHandler handles /api/v1/leaderboard routes.
type LeaderboardHandler struct {
	cfg *config.Config
	log *utils.Logger
	db  *config.DB
	rdb *config.RedisClient
}

func NewLeaderboardHandler(
	cfg *config.Config,
	log *utils.Logger,
	db *config.DB,
	rdb *config.RedisClient,
) *LeaderboardHandler {
	return &LeaderboardHandler{cfg: cfg, log: log, db: db, rdb: rdb}
}

// ── GET /api/v1/leaderboard ──────────────────────────────────────────────────

// Snapshot godoc
// @Summary  Get the current ranked leaderboard
// @Tags     leaderboard
// @Produce  json
// @Success  200 {object} models.LeaderboardSnapshot
// @Router   /leaderboard [get]
func (h *LeaderboardHandler) Snapshot(c *gin.Context) {
	const q = `
		SELECT
			rank, submission_id, run_id, team_name, language,
			peak_tps, avg_p99_ms, correctness, uptime_pct,
			composite_score,
			latency_score, throughput_score, correctness_score, stability_score,
			scored_at
		FROM leaderboard_ranked
		ORDER BY rank`

	rows, err := h.db.QueryxContext(c.Request.Context(), q)
	if err != nil {
		h.log.Errorw("leaderboard query failed", "err", err)
		utils.InternalError(c, "database error")
		return
	}
	defer rows.Close()

	entries := make([]models.LeaderboardEntry, 0, 32)
	for rows.Next() {
		var e models.LeaderboardEntry
		if err := rows.StructScan(&e); err != nil {
			h.log.Errorw("leaderboard row scan", "err", err)
			continue
		}
		entries = append(entries, e)
	}

	utils.OK(c, models.LeaderboardSnapshot{
		GeneratedAt: time.Now().UTC(),
		Entries:     entries,
	})
}

// ── GET /api/v1/leaderboard/ws ────────────────────────────────────────────────

// Stream godoc
// @Summary  WebSocket stream of live leaderboard & telemetry updates
// @Description Upgrades to WebSocket. The server pushes JSON frames whenever
//              a new MetricSnapshot is published to Redis telemetry:stream.
//              The client receives the full leaderboard snapshot on connect,
//              then incremental LiveMetricEvent frames during active runs.
// @Tags     leaderboard
// @Router   /leaderboard/ws [get]
func (h *LeaderboardHandler) Stream(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Warnw("ws upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	h.log.Infow("ws client connected", "remote", conn.RemoteAddr())

	// ── Send initial leaderboard snapshot ─────────────────────────────────────
	if err := h.sendLeaderboardSnapshot(c.Request.Context(), conn); err != nil {
		h.log.Warnw("failed to send initial snapshot", "err", err)
		return
	}

	// ── Subscribe to Redis telemetry channel ──────────────────────────────────
	sub := h.rdb.Subscribe(c.Request.Context(), h.cfg.RedisTelemetryChannel)
	defer sub.Close()

	msgCh := sub.Channel()

	// ── Ping ticker — keep connection alive ───────────────────────────────────
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// ── Read loop (client → server) runs in background ────────────────────────
	// We don't expect messages from the client but must drain to detect close.
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// ── Main fan-out loop ──────────────────────────────────────────────────────
	for {
		select {
		case <-clientGone:
			h.log.Infow("ws client disconnected", "remote", conn.RemoteAddr())
			return

		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			// Forward raw Redis message to the WebSocket client as-is.
			// The telemetry ingester publishes JSON-encoded LiveMetricEvent.
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				h.log.Warnw("ws write failed", "err", err)
				return
			}

		case <-pingTicker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ── GET /api/v1/leaderboard/runs/:runID/metrics ───────────────────────────────

// RunMetrics godoc
// @Summary  Get metric snapshots for a specific benchmark run
// @Tags     leaderboard
// @Produce  json
// @Param    runID    path  string true  "Benchmark run UUID"
// @Param    limit    query int    false "Max snapshots to return (default 200)"
// @Success  200 {array} models.MetricSnapshot
// @Failure  404 {object} utils.Envelope
// @Router   /leaderboard/runs/{runID}/metrics [get]
func (h *LeaderboardHandler) RunMetrics(c *gin.Context) {
	runID := c.Param("runID")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	// Verify run exists
	var exists bool
	if err := h.db.QueryRowContext(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM benchmark_runs WHERE id = $1)`, runID,
	).Scan(&exists); err != nil || !exists {
		utils.NotFound(c, "benchmark run not found")
		return
	}

	const q = `
		SELECT id, run_id, p50_ms, p90_ms, p99_ms, tps,
		       orders_sent, orders_acked, fill_errors, priority_errors,
		       active_workers, recorded_at
		FROM metric_snapshots
		WHERE run_id = $1
		ORDER BY recorded_at ASC
		LIMIT $2`

	rows, err := h.db.QueryxContext(c.Request.Context(), q, runID, limit)
	if err != nil {
		h.log.Errorw("metrics query failed", "err", err, "run_id", runID)
		utils.InternalError(c, "database error")
		return
	}
	defer rows.Close()

	snapshots := make([]models.MetricSnapshot, 0, limit)
	for rows.Next() {
		var s models.MetricSnapshot
		if err := rows.StructScan(&s); err != nil {
			h.log.Errorw("snapshot row scan", "err", err)
			continue
		}
		snapshots = append(snapshots, s)
	}

	utils.OK(c, snapshots)
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// sendLeaderboardSnapshot fetches the current leaderboard from Postgres
// and writes it as a single WebSocket text frame to conn.
func (h *LeaderboardHandler) sendLeaderboardSnapshot(ctx context.Context, conn *websocket.Conn) error {
	const q = `
		SELECT rank, submission_id, run_id, team_name, language,
		       peak_tps, avg_p99_ms, correctness, uptime_pct,
		       composite_score,
		       latency_score, throughput_score, correctness_score, stability_score,
		       scored_at
		FROM leaderboard_ranked ORDER BY rank`

	rows, err := h.db.QueryxContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	entries := make([]models.LeaderboardEntry, 0)
	for rows.Next() {
		var e models.LeaderboardEntry
		if err := rows.StructScan(&e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	snap := models.LeaderboardSnapshot{
		GeneratedAt: time.Now().UTC(),
		Entries:     entries,
	}

	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, payload)
}
