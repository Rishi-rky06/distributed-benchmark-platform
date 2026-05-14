package models

import "time"

// BenchmarkStatus tracks the state of a benchmark run.
type BenchmarkStatus string

const (
	BenchmarkQueued    BenchmarkStatus = "queued"
	BenchmarkRunning   BenchmarkStatus = "running"
	BenchmarkCompleted BenchmarkStatus = "completed"
	BenchmarkFailed    BenchmarkStatus = "failed"
	BenchmarkTimedOut  BenchmarkStatus = "timed_out"
)

// BenchmarkRun represents a single stress-test execution against a submission.
type BenchmarkRun struct {
	ID           string          `db:"id"            json:"id"`
	SubmissionID string          `db:"submission_id" json:"submission_id"`
	Status       BenchmarkStatus `db:"status"        json:"status"`

	// Timing
	StartedAt  *time.Time `db:"started_at"  json:"started_at,omitempty"`
	EndedAt    *time.Time `db:"ended_at"    json:"ended_at,omitempty"`
	DurationMs *int64     `db:"duration_ms" json:"duration_ms,omitempty"`

	// Bot fleet parameters snapshot (recorded at run start)
	BotWorkers    int    `db:"bot_workers"    json:"bot_workers"`
	BotProtocol   string `db:"bot_protocol"   json:"bot_protocol"`
	BotOrderRate  int    `db:"bot_order_rate" json:"bot_order_rate"`

	// Container info
	TargetHost string `db:"target_host" json:"target_host"`
	TargetPort int    `db:"target_port" json:"target_port"`

	ErrorMsg  *string   `db:"error_msg"  json:"error_msg,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// BenchmarkRunSummary is a lightweight projection used in list responses.
type BenchmarkRunSummary struct {
	ID           string          `db:"id"            json:"id"`
	SubmissionID string          `db:"submission_id" json:"submission_id"`
	TeamName     string          `db:"team_name"     json:"team_name"`
	Status       BenchmarkStatus `db:"status"        json:"status"`
	DurationMs   *int64          `db:"duration_ms"   json:"duration_ms,omitempty"`
	CreatedAt    time.Time       `db:"created_at"    json:"created_at"`
}
