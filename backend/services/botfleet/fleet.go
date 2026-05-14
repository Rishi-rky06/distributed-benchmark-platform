package botfleet

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// FleetConfig holds the parameters for a load generation run.
type FleetConfig struct {
	MinWorkers    int
	MaxWorkers    int
	RampDuration  time.Duration
	OrderRate     int    // orders per second per worker
	Protocol      string // "websocket" | "rest" | "fix"
	RunID         string
	SubmissionID  string
	TeamName      string
}

// FleetStats holds the aggregate results after a fleet run.
type FleetStats struct {
	TotalOrdersSent  int64
	TotalOrdersAcked int64
	TotalFillErrors  int64
	TotalPriorityErr int64
	ActiveWorkers    int32
	Duration         time.Duration
}

// LatencySample is a single round-trip measurement from bot → submission → bot.
type LatencySample struct {
	RunID      string
	LatencyNs  int64
	OrderType  string // "limit" | "market" | "cancel"
	Success    bool
	Timestamp  time.Time
}

// Fleet orchestrates the distributed bot fleet.
type Fleet struct {
	cfg       FleetConfig
	log       *zap.SugaredLogger
	target    string // host:port

	samples   chan LatencySample // telemetry sink
	workers   []*Worker
	mu        sync.Mutex
	active    atomic.Int32
	stats     FleetStats
	cancel    context.CancelFunc

	// Aggregate counters
	ordersSent  atomic.Int64
	ordersAcked atomic.Int64
	fillErrors  atomic.Int64
	priorityErr atomic.Int64
}

// New creates a new Fleet instance.
func New(cfg FleetConfig, log *zap.SugaredLogger, target string) *Fleet {
	return &Fleet{
		cfg:     cfg,
		log:     log,
		target:  target,
		samples: make(chan LatencySample, 100_000), // buffered for burst
	}
}

// Samples returns the channel where latency samples are published.
func (f *Fleet) Samples() <-chan LatencySample {
	return f.samples
}

// Start begins the load generation with linear ramp-up from min → max workers.
func (f *Fleet) Start(ctx context.Context) error {
	ctx, f.cancel = context.WithCancel(ctx)

	// Validate protocol before spawning workers
	if _, err := NewAdapter(f.cfg.Protocol); err != nil {
		return fmt.Errorf("protocol adapter: %w", err)
	}

	f.log.Infow("fleet starting",
		"min_workers", f.cfg.MinWorkers,
		"max_workers", f.cfg.MaxWorkers,
		"ramp", f.cfg.RampDuration,
		"protocol", f.cfg.Protocol,
		"target", f.target,
	)

	// Spawn minimum workers immediately
	for i := 0; i < f.cfg.MinWorkers; i++ {
		f.spawnWorker(ctx, i)
	}

	// Ramp up remaining workers over RampDuration
	remaining := f.cfg.MaxWorkers - f.cfg.MinWorkers
	if remaining > 0 && f.cfg.RampDuration > 0 {
		interval := f.cfg.RampDuration / time.Duration(remaining)
		go f.rampUp(ctx, f.cfg.MinWorkers, remaining, interval)
	}

	return nil
}

// Stop gracefully shuts down all workers and returns aggregate stats.
func (f *Fleet) Stop() FleetStats {
	if f.cancel != nil {
		f.cancel()
	}

	// Wait briefly for workers to drain
	time.Sleep(500 * time.Millisecond)
	close(f.samples)

	return FleetStats{
		TotalOrdersSent:  f.ordersSent.Load(),
		TotalOrdersAcked: f.ordersAcked.Load(),
		TotalFillErrors:  f.fillErrors.Load(),
		TotalPriorityErr: f.priorityErr.Load(),
		ActiveWorkers:    f.active.Load(),
	}
}

// CurrentStats returns a point-in-time snapshot of fleet counters.
func (f *Fleet) CurrentStats() FleetStats {
	return FleetStats{
		TotalOrdersSent:  f.ordersSent.Load(),
		TotalOrdersAcked: f.ordersAcked.Load(),
		TotalFillErrors:  f.fillErrors.Load(),
		TotalPriorityErr: f.priorityErr.Load(),
		ActiveWorkers:    f.active.Load(),
	}
}

func (f *Fleet) rampUp(ctx context.Context, startIdx, count int, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	spawned := 0
	for spawned < count {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.spawnWorker(ctx, startIdx+spawned)
			spawned++
		}
	}
	f.log.Infow("ramp-up complete", "total_workers", f.cfg.MaxWorkers)
}

// spawnWorker creates a new bot worker with its own protocol adapter.
// Each worker gets a dedicated adapter to avoid connection sharing/contention.
func (f *Fleet) spawnWorker(ctx context.Context, id int) {
	adapter, err := NewAdapter(f.cfg.Protocol)
	if err != nil {
		f.log.Warnw("failed to create adapter for worker", "worker_id", id, "err", err)
		return
	}

	w := &Worker{
		id:       id,
		fleet:    f,
		adapter:  adapter,
		target:   f.target,
		rate:     f.cfg.OrderRate,
		runID:    f.cfg.RunID,
		orderGen: NewOrderGenerator(),
	}

	f.mu.Lock()
	f.workers = append(f.workers, w)
	f.mu.Unlock()
	f.active.Add(1)

	go func() {
		defer f.active.Add(-1)
		w.Run(ctx)
	}()
}

