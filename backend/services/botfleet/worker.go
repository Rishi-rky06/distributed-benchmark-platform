package botfleet

import (
	"context"
	"time"
)

// Worker is a single trading bot that sends orders at a configured rate.
type Worker struct {
	id       int
	fleet    *Fleet
	adapter  ProtocolAdapter
	target   string
	rate     int // orders per second
	runID    string
	orderGen *OrderGenerator
}

// Run sends orders at the configured rate until context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	if w.rate <= 0 {
		w.rate = 10
	}

	interval := time.Second / time.Duration(w.rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Connect to target
	if err := w.adapter.Connect(ctx, w.target); err != nil {
		w.fleet.log.Warnw("worker connect failed", "worker_id", w.id, "err", err)
		return
	}
	defer w.adapter.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sendOrder(ctx)
		}
	}
}

func (w *Worker) sendOrder(ctx context.Context) {
	order := w.orderGen.Generate()
	sentAt := time.Now()

	w.fleet.ordersSent.Add(1)

	ack, err := w.adapter.SendOrder(ctx, order)

	sample := LatencySample{
		RunID:     w.runID,
		OrderType: string(order.Type),
		Timestamp: sentAt,
	}

	if err != nil {
		sample.Success = false
		sample.LatencyNs = time.Since(sentAt).Nanoseconds()
		w.fleet.fillErrors.Add(1)
	} else {
		sample.Success = true
		sample.LatencyNs = time.Since(sentAt).Nanoseconds()
		w.fleet.ordersAcked.Add(1)

		// Check for priority errors (simplified)
		if ack.FillPrice > 0 && order.Type == OrderTypeLimit {
			if order.Side == SideBuy && ack.FillPrice > order.Price {
				w.fleet.priorityErr.Add(1)
			} else if order.Side == SideSell && ack.FillPrice < order.Price {
				w.fleet.priorityErr.Add(1)
			}
		}
	}

	// Non-blocking send to telemetry
	select {
	case w.fleet.samples <- sample:
	default:
		// Drop sample if buffer is full — telemetry lag
	}
}
