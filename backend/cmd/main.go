package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rishi-rky06/distributed-benchmark-platform/api"
	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/services"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
	"github.com/Rishi-rky06/distributed-benchmark-platform/workers"
)

// @title           IICPC Distributed Benchmark Platform API
// @version         0.1.0
// @description     Secure submission ingestion, sandboxed execution, distributed load generation, and real-time scoring.
// @host            localhost:8080
// @BasePath        /api/v1
func main() {
	// ── 1. Load configuration ──────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// ── 2. Structured logger ──────────────────────────────────────────────────
	log := utils.NewLogger(cfg.LogLevel)
	defer log.Sync() //nolint:errcheck

	log.Infow("starting Distributed Benchmark Platform",
		"env", cfg.AppEnv,
		"port", cfg.BackendPort,
	)

	// ── 3. Data stores ────────────────────────────────────────────────────────
	db, err := config.NewPostgres(cfg)
	if err != nil {
		log.Fatalw("postgres connection failed", "err", err)
	}
	defer db.Close()

	rdb, err := config.NewRedis(cfg)
	if err != nil {
		log.Fatalw("redis connection failed", "err", err)
	}
	defer rdb.Close()

	log.Info("data stores connected")

	// ── 4. Migrations ─────────────────────────────────────────────────────────
	if err := config.RunMigrations(db); err != nil {
		log.Fatalw("migrations failed", "err", err)
	}
	log.Info("migrations applied")

	// ── 5. Services ───────────────────────────────────────────────────────────
	queueSvc := services.NewQueueService(rdb, log)

	sandboxSvc, err := services.NewSandboxService(cfg, log)
	if err != nil {
		log.Fatalw("sandbox service init failed", "err", err)
	}
	defer sandboxSvc.Close()

	scoringSvc := services.NewScoringService(cfg, log, db, rdb)

	log.Info("services initialized")

	// ── 6. Background workers ─────────────────────────────────────────────────
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	benchmarkWorker := workers.NewBenchmarkWorker(
		cfg, log, db, rdb,
		queueSvc, sandboxSvc, scoringSvc,
	)
	go benchmarkWorker.Run(workerCtx)
	log.Info("benchmark worker started")

	// ── 7. Router ─────────────────────────────────────────────────────────────
	router := api.NewRouter(cfg, log, db, rdb, queueSvc)

	srv := &http.Server{
		Addr:         ":" + cfg.BackendPort,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// ── 8. Serve ──────────────────────────────────────────────────────────────
	go func() {
		log.Infow("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalw("server error", "err", err)
		}
	}()

	// ── 9. Graceful shutdown ──────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received — draining connections…")

	// Cancel background workers first
	workerCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Errorw("forced shutdown", "err", err)
	}
	log.Info("server stopped cleanly")
}
