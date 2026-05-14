package services

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/models"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services/botfleet"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// TelemetryIngester drains the bot fleet's latency sample channel, computes
// rolling percentile histograms, persists MetricSnapshots to Postgres, and
// publishes LiveMetricEvents to Redis pub/sub for the WebSocket layer.
type TelemetryIngester struct {
	cfg  *config.Config
	log  *utils.Logger
	db   *sqlx.DB
	rdb  *redis.Client

	runID        string
	submissionID string
	teamName     string

	samples <-chan botfleet.LatencySample

	// Rolling histogram for the current flush window (nanosecond resolution)
	hist *hdrhistogram.Histogram
	mu   sync.Mutex

	// Window counters (reset each flush)
	windowSent     atomic.Int64
	windowAcked    atomic.Int64
	windowFillErr  atomic.Int64
	windowPrioErr  atomic.Int64

	// Total counters (cumulative)
	totalSent     atomic.Int64
	totalAcked    atomic.Int64
	totalFillErr  atomic.Int64
	totalPrioErr  atomic.Int64

	startTime time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewTelemetryIngester creates an ingester wired to the fleet's sample channel.
func NewTelemetryIngester(
	cfg *config.Config,
	log *utils.Logger,
	db *sqlx.DB,
	rdb *redis.Client,
	samples <-chan botfleet.LatencySample,
	runID, submissionID, teamName string,
) *TelemetryIngester {
	return &TelemetryIngester{
		cfg:          cfg,
		log:          log,
		db:           db,
		rdb:          rdb,
		runID:        runID,
		submissionID: submissionID,
		teamName:     teamName,
		samples:      samples,
		// 1ns–500ms range, 3 significant digits
		hist:   hdrhistogram.New(1, 500_000_000, 3),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Run starts the ingester. It drains samples and flushes metrics at the
// configured interval. Blocks until Stop() is called.
func (t *TelemetryIngester) Run(ctx context.Context) {
	defer close(t.doneCh)
	t.startTime = time.Now()

	ticker := time.NewTicker(t.cfg.TelemetryFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.flush(ctx) // final flush
			return

		case <-t.stopCh:
			// Drain remaining samples before final flush
			t.drainRemaining()
			t.flush(ctx)
			return

		case sample, ok := <-t.samples:
			if !ok {
				// Channel closed — fleet stopped
				t.flush(ctx)
				return
			}
			t.ingest(sample)

		case <-ticker.C:
			t.flush(ctx)
		}
	}
}

// Stop signals the ingester to perform a final flush and exit.
// Blocks until the flush completes.
func (t *TelemetryIngester) Stop() {
	close(t.stopCh)
	<-t.doneCh // wait for final flush
}

// ingest records a single latency sample into the histogram and counters.
func (t *TelemetryIngester) ingest(s botfleet.LatencySample) {
	t.totalSent.Add(1)
	t.windowSent.Add(1)

	if s.Success {
		t.totalAcked.Add(1)
		t.windowAcked.Add(1)

		t.mu.Lock()
		_ = t.hist.RecordValue(s.LatencyNs)
		t.mu.Unlock()
	} else {
		t.totalFillErr.Add(1)
		t.windowFillErr.Add(1)
	}
}

// drainRemaining reads any lingering samples from the channel.
func (t *TelemetryIngester) drainRemaining() {
	for {
		select {
		case s, ok := <-t.samples:
			if !ok {
				return
			}
			t.ingest(s)
		default:
			return
		}
	}
}

// flush computes percentiles from the histogram, persists a MetricSnapshot,
// publishes a LiveMetricEvent to Redis, and resets counters.
func (t *TelemetryIngester) flush(ctx context.Context) {
	t.mu.Lock()
	sent := t.windowSent.Swap(0)
	acked := t.windowAcked.Swap(0)
	fillErr := t.windowFillErr.Swap(0)
	prioErr := t.windowPrioErr.Swap(0)

	var p50Ns, p90Ns, p99Ns int64
	if t.hist.TotalCount() > 0 {
		p50Ns = t.hist.ValueAtQuantile(50)
		p90Ns = t.hist.ValueAtQuantile(90)
		p99Ns = t.hist.ValueAtQuantile(99)
	}
	t.hist.Reset()
	t.mu.Unlock()

	if sent == 0 && acked == 0 {
		return // nothing to report
	}

	// Convert nanoseconds to milliseconds
	p50Ms := float64(p50Ns) / 1_000_000.0
	p90Ms := float64(p90Ns) / 1_000_000.0
	p99Ms := float64(p99Ns) / 1_000_000.0

	// TPS = acknowledged orders / flush interval in seconds
	intervalSec := t.cfg.TelemetryFlushInterval.Seconds()
	tps := float64(acked) / intervalSec

	elapsed := int(time.Since(t.startTime).Seconds())

	// Persist to Postgres
	snapshot := models.MetricSnapshot{
		RunID:          t.runID,
		P50Ms:          p50Ms,
		P90Ms:          p90Ms,
		P99Ms:          p99Ms,
		TPS:            tps,
		OrdersSent:     sent,
		OrdersAcked:    acked,
		FillErrors:     fillErr,
		PriorityErrors: prioErr,
	}
	t.persistSnapshot(ctx, &snapshot)

	// Publish LiveMetricEvent to Redis pub/sub
	event := models.LiveMetricEvent{
		RunID:        t.runID,
		SubmissionID: t.submissionID,
		TeamName:     t.teamName,
		P50Ms:        p50Ms,
		P90Ms:        p90Ms,
		P99Ms:        p99Ms,
		TPS:          tps,
		ElapsedSec:   elapsed,
	}
	t.publishEvent(ctx, &event)
}

func (t *TelemetryIngester) persistSnapshot(ctx context.Context, s *models.MetricSnapshot) {
	const q = `
		INSERT INTO metric_snapshots
			(run_id, p50_ms, p90_ms, p99_ms, tps, orders_sent, orders_acked,
			 fill_errors, priority_errors, active_workers)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := t.db.ExecContext(ctx, q,
		s.RunID, s.P50Ms, s.P90Ms, s.P99Ms, s.TPS,
		s.OrdersSent, s.OrdersAcked, s.FillErrors, s.PriorityErrors,
		0, // active_workers filled from fleet stats externally if needed
	)
	if err != nil {
		t.log.Warnw("telemetry: failed to persist snapshot", "err", err, "run_id", s.RunID)
	}
}

func (t *TelemetryIngester) publishEvent(ctx context.Context, e *models.LiveMetricEvent) {
	data, err := jsonMarshal(e)
	if err != nil {
		t.log.Warnw("telemetry: marshal failed", "err", err)
		return
	}
	if err := t.rdb.Publish(ctx, t.cfg.RedisTelemetryChannel, string(data)).Err(); err != nil {
		t.log.Warnw("telemetry: publish failed", "err", err)
	}
}

// GetTotalStats returns cumulative counters for scoring.
func (t *TelemetryIngester) GetTotalStats() (sent, acked, fillErr, prioErr int64) {
	return t.totalSent.Load(), t.totalAcked.Load(), t.totalFillErr.Load(), t.totalPrioErr.Load()
}
