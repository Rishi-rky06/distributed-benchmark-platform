package api

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// HealthHandler handles readiness, liveness, and deep health probes.
type HealthHandler struct {
	cfg *config.Config
	db  *config.DB
	rdb *config.RedisClient
}

func NewHealthHandler(cfg *config.Config, db *config.DB, rdb *config.RedisClient) *HealthHandler {
	return &HealthHandler{cfg: cfg, db: db, rdb: rdb}
}

// ── Response shapes ───────────────────────────────────────────────────────────

type healthResponse struct {
	Status    string            `json:"status"`               // "ok" | "degraded" | "down"
	Version   string            `json:"version"`
	Env       string            `json:"env"`
	Timestamp string            `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]check  `json:"checks"`
	System    systemInfo        `json:"system"`
}

type check struct {
	Status  string `json:"status"`           // "ok" | "down"
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

type systemInfo struct {
	GoVersion  string `json:"go_version"`
	GoRoutines int    `json:"goroutines"`
	MemAllocMB uint64 `json:"mem_alloc_mb"`
}

var startTime = time.Now()

// Health godoc
// @Summary     Deep health check
// @Description Probes Postgres and Redis; returns system info. Used by monitoring.
// @Tags        ops
// @Produce     json
// @Success     200 {object} healthResponse
// @Success     207 {object} healthResponse "degraded — one dependency down"
// @Router      /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := make(map[string]check)
	overall := "ok"

	// ── Postgres ───────────────────────────────────────────────────────────────
	pgStart := time.Now()
	if err := h.db.PingContext(ctx); err != nil {
		checks["postgres"] = check{Status: "down", Error: err.Error()}
		overall = "degraded"
	} else {
		checks["postgres"] = check{
			Status:  "ok",
			Latency: time.Since(pgStart).String(),
		}
	}

	// ── Redis ──────────────────────────────────────────────────────────────────
	rdStart := time.Now()
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		checks["redis"] = check{Status: "down", Error: err.Error()}
		overall = "degraded"
	} else {
		checks["redis"] = check{
			Status:  "ok",
			Latency: time.Since(rdStart).String(),
		}
	}

	// ── System stats ──────────────────────────────────────────────────────────
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	resp := healthResponse{
		Status:    overall,
		Version:   "0.1.0",
		Env:       h.cfg.AppEnv,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(startTime).Round(time.Second).String(),
		Checks:    checks,
		System: systemInfo{
			GoVersion:  runtime.Version(),
			GoRoutines: runtime.NumGoroutine(),
			MemAllocMB: mem.Alloc / 1024 / 1024,
		},
	}

	statusCode := http.StatusOK
	if overall != "ok" {
		statusCode = http.StatusMultiStatus
	}

	c.JSON(statusCode, utils.Envelope{Success: overall == "ok", Data: resp})
}

// Readiness godoc
// @Summary     Kubernetes readiness probe
// @Description Returns 200 only when both Postgres and Redis are reachable.
// @Tags        ops
// @Produce     json
// @Success     200 {object} map[string]string
// @Failure     503 {object} map[string]string
// @Router      /readiness [get]
func (h *HealthHandler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		utils.ServiceUnavailable(c, "postgres unavailable: "+err.Error())
		return
	}

	if err := h.rdb.Ping(ctx).Err(); err != nil {
		utils.ServiceUnavailable(c, "redis unavailable: "+err.Error())
		return
	}

	utils.OK(c, gin.H{"status": "ready"})
}

// Liveness godoc
// @Summary     Kubernetes liveness probe
// @Description Always returns 200 if the process is alive.
// @Tags        ops
// @Produce     json
// @Success     200 {object} map[string]string
// @Router      /livez [get]
func (h *HealthHandler) Liveness(c *gin.Context) {
	utils.OK(c, gin.H{
		"status":  "alive",
		"uptime":  time.Since(startTime).Round(time.Second).String(),
	})
}

// Compile-time check: redis.Client implements Ping
var _ interface{ Ping(context.Context) *redis.StatusCmd } = (*redis.Client)(nil)
