package models

import "time"

// LeaderboardEntry is one row in the ranked leaderboard.
type LeaderboardEntry struct {
	Rank         int     `db:"rank"          json:"rank"`
	SubmissionID string  `db:"submission_id" json:"submission_id"`
	RunID        string  `db:"run_id"        json:"run_id"`
	TeamName     string  `db:"team_name"     json:"team_name"`
	Language     string  `db:"language"      json:"language"`

	// Raw metrics
	PeakTPS     float64 `db:"peak_tps"     json:"peak_tps"`
	AvgP99Ms    float64 `db:"avg_p99_ms"   json:"avg_p99_ms"`
	Correctness float64 `db:"correctness"  json:"correctness"`
	UptimePct   float64 `db:"uptime_pct"   json:"uptime_pct"`

	// Weighted composite (0–100)
	CompositeScore float64 `db:"composite_score" json:"composite_score"`

	// Individual weighted sub-scores (0–100 each, for transparency)
	LatencyScore     float64 `db:"latency_score"     json:"latency_score"`
	ThroughputScore  float64 `db:"throughput_score"  json:"throughput_score"`
	CorrectnessScore float64 `db:"correctness_score" json:"correctness_score"`
	StabilityScore   float64 `db:"stability_score"   json:"stability_score"`

	ScoredAt time.Time `db:"scored_at" json:"scored_at"`
}

// LeaderboardSnapshot is what the frontend receives in full on connect
// and re-receives when any score changes.
type LeaderboardSnapshot struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Entries     []LeaderboardEntry `json:"entries"`
}
