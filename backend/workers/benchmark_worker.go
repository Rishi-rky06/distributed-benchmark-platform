package workers

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/models"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services/botfleet"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// BenchmarkWorker is the central orchestrator that processes benchmark runs.
// It listens on the Redis queue and coordinates:
//   build → deploy → load test → collect → score → teardown
type BenchmarkWorker struct {
	cfg        *config.Config
	log        *utils.Logger
	db         *sqlx.DB
	rdb        *redis.Client
	queue      *services.QueueService
	sandbox    *services.SandboxService
	scoring    *services.ScoringService
}

func NewBenchmarkWorker(
	cfg *config.Config,
	log *utils.Logger,
	db *sqlx.DB,
	rdb *redis.Client,
	queue *services.QueueService,
	sandbox *services.SandboxService,
	scoring *services.ScoringService,
) *BenchmarkWorker {
	return &BenchmarkWorker{
		cfg: cfg, log: log, db: db, rdb: rdb,
		queue: queue, sandbox: sandbox, scoring: scoring,
	}
}

// Run starts the worker loop. Blocks until ctx is cancelled.
func (w *BenchmarkWorker) Run(ctx context.Context) {
	w.log.Info("benchmark worker: starting BLPOP loop")

	for {
		select {
		case <-ctx.Done():
			w.log.Info("benchmark worker: shutting down")
			return
		default:
		}

		// Block for up to 5 seconds waiting for work
		runID, err := w.queue.DequeueRun(ctx, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled
			}
			w.log.Warnw("dequeue error", "err", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if runID == "" {
			continue // timeout, no work
		}

		w.log.Infow("benchmark worker: processing run", "run_id", runID)
		if err := w.processRun(ctx, runID); err != nil {
			w.log.Errorw("benchmark run failed", "run_id", runID, "err", err)
			_ = w.queue.MoveToDeadLetter(ctx, runID, err.Error())
		}
	}
}

// processRun executes the full benchmark pipeline for a single run.
func (w *BenchmarkWorker) processRun(ctx context.Context, runID string) error {
	// 1. Load run and submission info
	run, sub, err := w.loadRunAndSubmission(ctx, runID)
	if err != nil {
		return w.failRun(ctx, runID, "load failed: "+err.Error())
	}

	// 2. Update run status → running
	if err := w.updateRunStatus(ctx, runID, models.BenchmarkRunning); err != nil {
		return err
	}
	if err := w.updateSubmissionStatus(ctx, sub.ID, models.SubmissionBuilding); err != nil {
		return err
	}

	// 3. Build Docker image — enforce a hard timeout so a hanging pip/apt
	// step cannot block the worker indefinitely in "building" status.
	submissionDir := filepath.Join(w.cfg.SubmissionsDir, sub.ID)
	buildCtx, buildCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer buildCancel()
	imageTag, err := w.sandbox.BuildImage(buildCtx, sub.ID, sub.Language, submissionDir)
	if err != nil {
		return w.failRun(ctx, runID, "build failed: "+err.Error())
	}

	// 4. Launch container
	info, err := w.sandbox.LaunchContainer(ctx, imageTag, sub.ID)
	if err != nil {
		return w.failRun(ctx, runID, "launch failed: "+err.Error())
	}

	// Store container info
	w.updateContainerInfo(ctx, sub.ID, info.ContainerID, imageTag)
	w.updateRunTarget(ctx, runID, info.Host, info.Port)

	// 5. Wait for container to be healthy
	if err := w.sandbox.WaitForHealthy(ctx, info.Host, info.Port, 30*time.Second); err != nil {
		logs, _ := w.sandbox.GetContainerLogs(ctx, info.ContainerID, 50)
		_ = w.sandbox.TearDown(ctx, info.ContainerID, imageTag)
		return w.failRun(ctx, runID, fmt.Sprintf("health check failed: %s\nLogs: %s", err, logs))
	}

	// 6. Update submission → running
	_ = w.updateSubmissionStatus(ctx, sub.ID, models.SubmissionRunning)

	// 7. Create bot fleet and telemetry ingester
	startedAt := time.Now()
	w.updateRunStarted(ctx, runID, startedAt)

	target := fmt.Sprintf("%s:%d", info.Host, info.Port)

	fleetCfg := botfleet.FleetConfig{
		MinWorkers:   run.BotWorkers,
		MaxWorkers:   w.cfg.BotFleetMaxWorkers,
		RampDuration: w.cfg.BotRampDuration,
		OrderRate:    run.BotOrderRate,
		Protocol:     run.BotProtocol,
		RunID:        runID,
		SubmissionID: sub.ID,
		TeamName:     sub.TeamName,
	}

	fleet := botfleet.New(fleetCfg, w.log, target)

	// Create telemetry ingester wired to the fleet's sample channel
	ingester := services.NewTelemetryIngester(
		w.cfg, w.log, w.db, w.rdb,
		fleet.Samples(),
		runID, sub.ID, sub.TeamName,
	)

	// Start telemetry collection in background
	ingesterCtx, ingesterCancel := context.WithCancel(ctx)
	defer ingesterCancel()
	go ingester.Run(ingesterCtx)

	// Start the bot fleet — ramps up workers and begins sending orders
	if err := fleet.Start(ctx); err != nil {
		_ = w.sandbox.TearDown(ctx, info.ContainerID, imageTag)
		return w.failRun(ctx, runID, "fleet start failed: "+err.Error())
	}

	w.log.Infow("benchmark running",
		"run_id", runID,
		"duration", w.cfg.BenchmarkDuration,
		"target", target,
		"min_workers", fleetCfg.MinWorkers,
		"max_workers", fleetCfg.MaxWorkers,
		"protocol", fleetCfg.Protocol,
	)

	// 8. Wait for benchmark duration
	select {
	case <-ctx.Done():
		fleet.Stop()
		ingester.Stop()
		_ = w.sandbox.TearDown(ctx, info.ContainerID, imageTag)
		return ctx.Err()
	case <-time.After(w.cfg.BenchmarkDuration):
		// Benchmark window complete
	}

	// 9. Stop fleet and flush final telemetry
	stats := fleet.Stop()
	ingester.Stop()

	endedAt := time.Now()
	durationMs := endedAt.Sub(startedAt).Milliseconds()

	w.log.Infow("fleet stopped",
		"run_id", runID,
		"orders_sent", stats.TotalOrdersSent,
		"orders_acked", stats.TotalOrdersAcked,
		"fill_errors", stats.TotalFillErrors,
		"priority_errors", stats.TotalPriorityErr,
	)

	// 10. Compute scores and update leaderboard
	if err := w.scoring.ComputeAndStore(ctx, runID); err != nil {
		w.log.Warnw("scoring failed", "err", err, "run_id", runID)
	}

	// 11. Teardown container
	_ = w.sandbox.TearDown(ctx, info.ContainerID, imageTag)

	// 12. Mark run completed
	w.completeRun(ctx, runID, endedAt, durationMs)
	_ = w.updateSubmissionStatus(ctx, sub.ID, models.SubmissionCompleted)

	w.log.Infow("benchmark run completed",
		"run_id", runID,
		"duration_ms", durationMs,
	)
	return nil
}

// ── Database helpers ────────────────────────────────────────────────────────────

func (w *BenchmarkWorker) loadRunAndSubmission(ctx context.Context, runID string) (*models.BenchmarkRun, *models.Submission, error) {
	var run models.BenchmarkRun
	if err := w.db.GetContext(ctx, &run,
		`SELECT id, submission_id, status, bot_workers, bot_protocol, bot_order_rate, created_at
		 FROM benchmark_runs WHERE id = $1`, runID); err != nil {
		return nil, nil, fmt.Errorf("load run: %w", err)
	}

	var sub models.Submission
	if err := w.db.GetContext(ctx, &sub,
		`SELECT id, user_id, team_name, filename, language, status FROM submissions WHERE id = $1`,
		run.SubmissionID); err != nil {
		return nil, nil, fmt.Errorf("load submission: %w", err)
	}

	return &run, &sub, nil
}

func (w *BenchmarkWorker) updateRunStatus(ctx context.Context, runID string, status models.BenchmarkStatus) error {
	_, err := w.db.ExecContext(ctx, `UPDATE benchmark_runs SET status = $1 WHERE id = $2`, status, runID)
	return err
}

func (w *BenchmarkWorker) updateSubmissionStatus(ctx context.Context, subID string, status models.SubmissionStatus) error {
	_, err := w.db.ExecContext(ctx, `UPDATE submissions SET status = $1 WHERE id = $2`, status, subID)
	return err
}

func (w *BenchmarkWorker) updateContainerInfo(ctx context.Context, subID, containerID, imageTag string) {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE submissions SET container_id = $1, image_tag = $2 WHERE id = $3`,
		containerID, imageTag, subID)
}

func (w *BenchmarkWorker) updateRunTarget(ctx context.Context, runID, host string, port int) {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE benchmark_runs SET target_host = $1, target_port = $2 WHERE id = $3`,
		host, port, runID)
}

func (w *BenchmarkWorker) updateRunStarted(ctx context.Context, runID string, startedAt time.Time) {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE benchmark_runs SET started_at = $1 WHERE id = $2`, startedAt, runID)
}

func (w *BenchmarkWorker) failRun(ctx context.Context, runID, errMsg string) error {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE benchmark_runs SET status = $1, error_msg = $2, ended_at = NOW() WHERE id = $3`,
		models.BenchmarkFailed, errMsg, runID)
	// Pull the submission ID from the run so we can flip it to failed too.
	// Without this the submission stays stuck in 'building' forever.
	var subID string
	_ = w.db.QueryRowContext(ctx,
		`SELECT submission_id FROM benchmark_runs WHERE id = $1`, runID).Scan(&subID)
	if subID != "" {
		_ = w.updateSubmissionStatus(ctx, subID, models.SubmissionFailed)
	}
	return fmt.Errorf("%s", errMsg)
}

func (w *BenchmarkWorker) completeRun(ctx context.Context, runID string, endedAt time.Time, durationMs int64) {
	_, _ = w.db.ExecContext(ctx,
		`UPDATE benchmark_runs SET status = $1, ended_at = $2, duration_ms = $3 WHERE id = $4`,
		models.BenchmarkCompleted, endedAt, durationMs, runID)
}

// Compile-time assertions
var _ = sql.ErrNoRows
