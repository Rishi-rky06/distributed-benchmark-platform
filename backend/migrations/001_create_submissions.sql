-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE submission_status AS ENUM (
    'pending',
    'building',
    'ready',
    'running',
    'completed',
    'failed'
);

CREATE TABLE submissions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      TEXT        NOT NULL,
    team_name    TEXT        NOT NULL,
    filename     TEXT        NOT NULL,
    language     TEXT        NOT NULL CHECK (language IN ('go', 'cpp', 'rust')),
    status       submission_status NOT NULL DEFAULT 'pending',
    container_id TEXT,
    image_tag    TEXT,
    error_msg    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Auto-update updated_at on every row change
CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

CREATE TRIGGER submissions_updated_at
    BEFORE UPDATE ON submissions
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE INDEX idx_submissions_user_id ON submissions (user_id);
CREATE INDEX idx_submissions_status  ON submissions (status);
CREATE INDEX idx_submissions_created ON submissions (created_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS submissions;
DROP TYPE IF EXISTS submission_status;
DROP FUNCTION IF EXISTS touch_updated_at;
