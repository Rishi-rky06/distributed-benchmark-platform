package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Rishi-rky06/distributed-benchmark-platform/utils"
)

// ── Queue keys ──────────────────────────────────────────────────────────────────
const (
	QueueBenchmarkRuns = "queue:benchmark_runs"
	QueueDeadLetter    = "queue:dead_letter"
)

// QueueService manages all Redis queue interactions.
type QueueService struct {
	rdb *redis.Client
	log *utils.Logger
}

func NewQueueService(rdb *redis.Client, log *utils.Logger) *QueueService {
	return &QueueService{rdb: rdb, log: log}
}

// EnqueueRun pushes a benchmark run ID onto the work queue.
func (q *QueueService) EnqueueRun(ctx context.Context, runID string) error {
	return q.rdb.LPush(ctx, QueueBenchmarkRuns, runID).Err()
}

// DequeueRun blocks until a run ID is available or the context is cancelled.
// Uses BRPOP for FIFO ordering (push left, pop right).
// Returns ("", nil) if the context is cancelled cleanly.
func (q *QueueService) DequeueRun(ctx context.Context, timeout time.Duration) (string, error) {
	result, err := q.rdb.BRPop(ctx, timeout, QueueBenchmarkRuns).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil // timeout — no work available
		}
		return "", err
	}
	// BRPop returns [key, value]
	if len(result) < 2 {
		return "", nil
	}
	return result[1], nil
}

// MoveToDeadLetter records a failed run ID so it can be retried or inspected.
func (q *QueueService) MoveToDeadLetter(ctx context.Context, runID string, reason string) error {
	entry := map[string]string{
		"run_id":    runID,
		"reason":    reason,
		"failed_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(entry)
	return q.rdb.LPush(ctx, QueueDeadLetter, string(data)).Err()
}

// QueueLength returns the current depth of the benchmark queue.
func (q *QueueService) QueueLength(ctx context.Context) (int64, error) {
	return q.rdb.LLen(ctx, QueueBenchmarkRuns).Result()
}

// PublishTelemetry pushes a JSON payload to the telemetry pub/sub channel.
func (q *QueueService) PublishTelemetry(ctx context.Context, channel string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.rdb.Publish(ctx, channel, string(data)).Err()
}

// PublishLeaderboardUpdate pushes a full leaderboard snapshot to Redis pub/sub.
func (q *QueueService) PublishLeaderboardUpdate(ctx context.Context, channel string, payload any) error {
	return q.PublishTelemetry(ctx, channel, payload)
}
