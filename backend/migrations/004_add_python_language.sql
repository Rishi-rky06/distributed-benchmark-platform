-- +goose Up
-- +goose StatementBegin

ALTER TABLE submissions DROP CONSTRAINT submissions_language_check;
ALTER TABLE submissions ADD CONSTRAINT submissions_language_check
    CHECK (language IN ('go', 'cpp', 'rust', 'python'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE submissions DROP CONSTRAINT submissions_language_check;
ALTER TABLE submissions ADD CONSTRAINT submissions_language_check
    CHECK (language IN ('go', 'cpp', 'rust'));

-- +goose StatementEnd
