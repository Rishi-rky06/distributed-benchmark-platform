package services

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"github.com/Rishi-rky06/distributed-benchmark-platform/config"
	"github.com/Rishi-rky06/distributed-benchmark-platform/models"
	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// ScoringService computes composite scores after a benchmark run completes
// and upserts results into the leaderboard table.
type ScoringService struct {
	cfg *config.Config
	log *utils.Logger
	db  *sqlx.DB
	rdb *redis.Client
}

func NewScoringService(cfg *config.Config, log *utils.Logger, db *sqlx.DB, rdb *redis.Client) *ScoringService {
	return &ScoringService{cfg: cfg, log: log, db: db, rdb: rdb}
}

// ComputeAndStore aggregates metric_snapshots for a run, normalizes them,
// computes the weighted composite score, and upserts the leaderboard.
func (s *ScoringService) ComputeAndStore(ctx context.Context, runID string) error {
	// 1. Compute aggregate from snapshots
	agg, err := s.computeAggregate(ctx, runID)
	if err != nil {
		return fmt.Errorf("compute aggregate: %w", err)
	}

	// 2. Persist aggregate
	if err := s.storeAggregate(ctx, agg); err != nil {
		return fmt.Errorf("store aggregate: %w", err)
	}

	// 3. Normalize and compute composite score
	scores := s.normalizeScores(agg)

	// 4. Upsert leaderboard
	if err := s.upsertLeaderboard(ctx, runID, agg, scores); err != nil {
		return fmt.Errorf("upsert leaderboard: %w", err)
	}

	s.log.Infow("scoring complete",
		"run_id", runID,
		"composite", scores.Composite,
		"latency", scores.Latency,
		"throughput", scores.Throughput,
		"correctness", scores.Correctness,
		"stability", scores.Stability,
	)
	return nil
}

type normalizedScores struct {
	Latency     float64
	Throughput  float64
	Correctness float64
	Stability   float64
	Composite   float64
}

func (s *ScoringService) computeAggregate(ctx context.Context, runID string) (*models.MetricAggregate, error) {
	const q = `
		SELECT
			$1::uuid AS run_id,
			COALESCE(MAX(tps), 0)          AS peak_tps,
			COALESCE(AVG(tps), 0)          AS avg_tps,
			COALESCE(AVG(p50_ms), 0)       AS avg_p50_ms,
			COALESCE(AVG(p90_ms), 0)       AS avg_p90_ms,
			COALESCE(AVG(p99_ms), 0)       AS avg_p99_ms,
			CASE WHEN COUNT(*) > 0
				THEN COUNT(CASE WHEN orders_acked > 0 THEN 1 END)::FLOAT / COUNT(*)::FLOAT * 100
				ELSE 0
			END AS uptime_pct,
			CASE WHEN COALESCE(SUM(orders_sent), 0) > 0
				THEN 1.0 - (COALESCE(SUM(fill_errors), 0) + COALESCE(SUM(priority_errors), 0))::FLOAT
				     / COALESCE(SUM(orders_sent), 1)::FLOAT
				ELSE 0
			END AS correctness,
			NOW() AS computed_at
		FROM metric_snapshots
		WHERE run_id = $1`

	var agg models.MetricAggregate
	if err := s.db.GetContext(ctx, &agg, q, runID); err != nil {
		return nil, err
	}
	return &agg, nil
}

func (s *ScoringService) storeAggregate(ctx context.Context, agg *models.MetricAggregate) error {
	const q = `
		INSERT INTO metric_aggregates
			(run_id, peak_tps, avg_tps, avg_p50_ms, avg_p90_ms, avg_p99_ms, uptime_pct, correctness)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (run_id) DO UPDATE SET
			peak_tps = EXCLUDED.peak_tps,
			avg_tps = EXCLUDED.avg_tps,
			avg_p50_ms = EXCLUDED.avg_p50_ms,
			avg_p90_ms = EXCLUDED.avg_p90_ms,
			avg_p99_ms = EXCLUDED.avg_p99_ms,
			uptime_pct = EXCLUDED.uptime_pct,
			correctness = EXCLUDED.correctness,
			computed_at = NOW()`

	_, err := s.db.ExecContext(ctx, q,
		agg.RunID, agg.PeakTPS, agg.AvgTPS,
		agg.AvgP50Ms, agg.AvgP90Ms, agg.AvgP99Ms,
		agg.UptimePct, agg.Correctness,
	)
	return err
}

// normalizeScores maps raw metrics to 0-100 scale.
//   - Latency: 100 at ≤1ms p99, 0 at ≥100ms p99 (log scale)
//   - Throughput: 100 at ≥100k TPS, 0 at ≤100 TPS (log scale)
//   - Correctness: direct percentage (×100)
//   - Stability: direct percentage (uptime_pct)
func (s *ScoringService) normalizeScores(agg *models.MetricAggregate) normalizedScores {
	// Latency score: lower is better
	// log10(1) = 0 → 100, log10(100) = 2 → 0
	latencyScore := 100.0
	if agg.AvgP99Ms > 1.0 {
		p99Log := logBase10(agg.AvgP99Ms)
		latencyScore = clamp(100.0*(1.0-p99Log/2.0), 0, 100)
	}

	// Throughput score: higher is better
	// log10(100) = 2 → 0, log10(100000) = 5 → 100
	throughputScore := 0.0
	if agg.PeakTPS > 100 {
		tpsLog := logBase10(agg.PeakTPS)
		throughputScore = clamp(100.0*(tpsLog-2.0)/3.0, 0, 100)
	}

	correctnessScore := clamp(agg.Correctness*100, 0, 100)
	stabilityScore := clamp(agg.UptimePct, 0, 100)

	composite := s.cfg.ScoreWeightLatency*latencyScore +
		s.cfg.ScoreWeightThroughput*throughputScore +
		s.cfg.ScoreWeightCorrectness*correctnessScore +
		s.cfg.ScoreWeightStability*stabilityScore

	return normalizedScores{
		Latency:     latencyScore,
		Throughput:  throughputScore,
		Correctness: correctnessScore,
		Stability:   stabilityScore,
		Composite:   composite,
	}
}

func (s *ScoringService) upsertLeaderboard(ctx context.Context, runID string, agg *models.MetricAggregate, scores normalizedScores) error {
	// Get submission info for denormalization
	var sub struct {
		SubmissionID string `db:"submission_id"`
		TeamName     string `db:"team_name"`
		Language     string `db:"language"`
	}
	err := s.db.GetContext(ctx, &sub, `
		SELECT br.submission_id, s.team_name, s.language
		FROM benchmark_runs br JOIN submissions s ON s.id = br.submission_id
		WHERE br.id = $1`, runID)
	if err != nil {
		return fmt.Errorf("get submission info: %w", err)
	}

	const q = `
		INSERT INTO leaderboard
			(submission_id, run_id, team_name, language,
			 peak_tps, avg_p99_ms, correctness, uptime_pct,
			 latency_score, throughput_score, correctness_score, stability_score,
			 composite_score, scored_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (submission_id) DO UPDATE SET
			run_id = EXCLUDED.run_id,
			peak_tps = EXCLUDED.peak_tps,
			avg_p99_ms = EXCLUDED.avg_p99_ms,
			correctness = EXCLUDED.correctness,
			uptime_pct = EXCLUDED.uptime_pct,
			latency_score = EXCLUDED.latency_score,
			throughput_score = EXCLUDED.throughput_score,
			correctness_score = EXCLUDED.correctness_score,
			stability_score = EXCLUDED.stability_score,
			composite_score = EXCLUDED.composite_score,
			scored_at = NOW()
		WHERE EXCLUDED.composite_score >= leaderboard.composite_score`

	_, err = s.db.ExecContext(ctx, q,
		sub.SubmissionID, runID, sub.TeamName, sub.Language,
		agg.PeakTPS, agg.AvgP99Ms, agg.Correctness, agg.UptimePct,
		scores.Latency, scores.Throughput, scores.Correctness, scores.Stability,
		scores.Composite,
	)
	if err != nil {
		return err
	}

	// Publish leaderboard update via Redis pub/sub
	s.publishLeaderboardUpdate(ctx)
	return nil
}

func (s *ScoringService) publishLeaderboardUpdate(ctx context.Context) {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT rank, submission_id, run_id, team_name, language,
			   peak_tps, avg_p99_ms, correctness, uptime_pct,
			   composite_score, latency_score, throughput_score, correctness_score, stability_score,
			   scored_at
		FROM leaderboard_ranked ORDER BY rank`)
	if err != nil {
		s.log.Warnw("failed to fetch leaderboard for broadcast", "err", err)
		return
	}
	defer rows.Close()

	entries := make([]models.LeaderboardEntry, 0)
	for rows.Next() {
		var e models.LeaderboardEntry
		if err := rows.StructScan(&e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	snap := models.LeaderboardSnapshot{
		GeneratedAt: time.Now().UTC(),
		Entries:     entries,
	}

	data, _ := jsonMarshal(snap)
	_ = s.rdb.Publish(ctx, s.cfg.RedisTelemetryChannel, string(data)).Err()
}
