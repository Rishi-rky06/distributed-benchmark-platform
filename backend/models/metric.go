package models

import "time"

// MetricSnapshot is one telemetry flush — a point-in-time aggregate
// emitted by the telemetry ingester every TelemetryFlushInterval.
type MetricSnapshot struct {
	ID      int64  `db:"id"       json:"id"`
	RunID   string `db:"run_id"   json:"run_id"`

	// Latency (milliseconds)
	P50Ms  float64 `db:"p50_ms"  json:"p50_ms"`
	P90Ms  float64 `db:"p90_ms"  json:"p90_ms"`
	P99Ms  float64 `db:"p99_ms"  json:"p99_ms"`

	// Throughput
	TPS float64 `db:"tps" json:"tps"` // transactions per second in this window

	// Correctness counters
	OrdersSent      int64 `db:"orders_sent"      json:"orders_sent"`
	OrdersAcked     int64 `db:"orders_acked"     json:"orders_acked"`
	FillErrors      int64 `db:"fill_errors"      json:"fill_errors"`
	PriorityErrors  int64 `db:"priority_errors"  json:"priority_errors"`

	// Active worker count at this instant
	ActiveWorkers int `db:"active_workers" json:"active_workers"`

	RecordedAt time.Time `db:"recorded_at" json:"recorded_at"`
}

// MetricAggregate is the final roll-up after a benchmark run completes,
// stored in the scores table and used for leaderboard ranking.
type MetricAggregate struct {
	RunID string `db:"run_id" json:"run_id"`

	// Peak / average over the full benchmark window
	PeakTPS    float64 `db:"peak_tps"    json:"peak_tps"`
	AvgTPS     float64 `db:"avg_tps"     json:"avg_tps"`
	AvgP50Ms   float64 `db:"avg_p50_ms"  json:"avg_p50_ms"`
	AvgP90Ms   float64 `db:"avg_p90_ms"  json:"avg_p90_ms"`
	AvgP99Ms   float64 `db:"avg_p99_ms"  json:"avg_p99_ms"`
	UptimePct  float64 `db:"uptime_pct"  json:"uptime_pct"`  // % of run with no errors
	Correctness float64 `db:"correctness" json:"correctness"` // 0.0–1.0

	ComputedAt time.Time `db:"computed_at" json:"computed_at"`
}

// LiveMetricEvent is the JSON payload pushed over WebSocket / Redis pub/sub
// to the frontend during an active run.
type LiveMetricEvent struct {
	RunID         string  `json:"run_id"`
	SubmissionID  string  `json:"submission_id"`
	TeamName      string  `json:"team_name"`
	P50Ms         float64 `json:"p50_ms"`
	P90Ms         float64 `json:"p90_ms"`
	P99Ms         float64 `json:"p99_ms"`
	TPS           float64 `json:"tps"`
	ActiveWorkers int     `json:"active_workers"`
	ElapsedSec    int     `json:"elapsed_sec"`
}
