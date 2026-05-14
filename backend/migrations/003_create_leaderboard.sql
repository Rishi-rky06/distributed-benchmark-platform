-- +goose Up
-- +goose StatementBegin

-- Materialised leaderboard — upserted by the scoring service after each run
CREATE TABLE leaderboard (
    submission_id     UUID PRIMARY KEY REFERENCES submissions(id) ON DELETE CASCADE,
    run_id            UUID NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    team_name         TEXT NOT NULL,
    language          TEXT NOT NULL,

    -- Raw metrics (denormalised for fast reads)
    peak_tps          DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_p99_ms        DOUBLE PRECISION NOT NULL DEFAULT 0,
    correctness       DOUBLE PRECISION NOT NULL DEFAULT 0,
    uptime_pct        DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Weighted sub-scores (0–100 each)
    latency_score     DOUBLE PRECISION NOT NULL DEFAULT 0,
    throughput_score  DOUBLE PRECISION NOT NULL DEFAULT 0,
    correctness_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    stability_score   DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- Overall weighted composite (0–100)
    composite_score   DOUBLE PRECISION NOT NULL DEFAULT 0,

    scored_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Rank view — computed on the fly; no need to store rank as a column
CREATE OR REPLACE VIEW leaderboard_ranked AS
SELECT
    ROW_NUMBER() OVER (ORDER BY composite_score DESC, scored_at ASC)::INT AS rank,
    l.*
FROM leaderboard l;

CREATE INDEX idx_leaderboard_composite ON leaderboard (composite_score DESC);
CREATE INDEX idx_leaderboard_scored_at ON leaderboard (scored_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP VIEW  IF EXISTS leaderboard_ranked;
DROP TABLE IF EXISTS leaderboard;
