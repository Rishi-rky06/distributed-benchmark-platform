-- +goose Up
-- +goose StatementBegin

CREATE TYPE benchmark_status AS ENUM (
    'queued',
    'running',
    'completed',
    'failed',
    'timed_out'
);

-- One benchmark run per submission execution
CREATE TABLE benchmark_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id UUID        NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    status        benchmark_status NOT NULL DEFAULT 'queued',

    -- Timing
    started_at    TIMESTAMPTZ,
    ended_at      TIMESTAMPTZ,
    duration_ms   BIGINT,

    -- Bot fleet snapshot
    bot_workers   INT  NOT NULL DEFAULT 0,
    bot_protocol  TEXT NOT NULL DEFAULT 'websocket',
    bot_order_rate INT NOT NULL DEFAULT 0,

    -- Where the contestant engine is reachable
    target_host   TEXT NOT NULL DEFAULT '',
    target_port   INT  NOT NULL DEFAULT 0,

    error_msg     TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_bench_runs_submission ON benchmark_runs (submission_id);
CREATE INDEX idx_bench_runs_status     ON benchmark_runs (status);
CREATE INDEX idx_bench_runs_created    ON benchmark_runs (created_at DESC);

-- Rolling telemetry samples (one row per flush interval per run)
-- NOTE: Enable TimescaleDB and call create_hypertable for time-series perf
CREATE TABLE metric_snapshots (
    id              BIGSERIAL,
    run_id          UUID        NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,

    -- Latency (ms)
    p50_ms          DOUBLE PRECISION NOT NULL DEFAULT 0,
    p90_ms          DOUBLE PRECISION NOT NULL DEFAULT 0,
    p99_ms          DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Throughput
    tps             DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Correctness counters
    orders_sent     BIGINT NOT NULL DEFAULT 0,
    orders_acked    BIGINT NOT NULL DEFAULT 0,
    fill_errors     BIGINT NOT NULL DEFAULT 0,
    priority_errors BIGINT NOT NULL DEFAULT 0,

    active_workers  INT NOT NULL DEFAULT 0,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, recorded_at)
);

CREATE INDEX idx_snapshots_run_id ON metric_snapshots (run_id, recorded_at DESC);

-- Final aggregate per run (computed after run completes)
CREATE TABLE metric_aggregates (
    run_id       UUID PRIMARY KEY REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    peak_tps     DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_tps      DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_p50_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_p90_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_p99_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
    uptime_pct   DOUBLE PRECISION NOT NULL DEFAULT 0,
    correctness  DOUBLE PRECISION NOT NULL DEFAULT 0,
    computed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS metric_aggregates;
DROP TABLE IF EXISTS metric_snapshots;
DROP TABLE IF EXISTS benchmark_runs;
DROP TYPE  IF EXISTS benchmark_status;
