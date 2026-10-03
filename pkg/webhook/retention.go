package webhook

import (
	"context"
	"log/slog"
	"time"
)

const (
	DeliveryRetention    = 90 * 24 * time.Hour
	RetentionBatchSize   = 500
	RetentionInterval    = time.Minute
	RetentionPassTimeout = 15 * time.Second
)

type retentionStore interface {
	PruneAppWebhookDeliveries(context.Context, time.Time, int) (int64, error)
	AppWebhookDeliveryStorageBytes(context.Context) (int64, error)
}

// RetentionWorker removes one bounded batch per minute. Each pass commits
// independently, so a large backlog drains without a long transaction.
type RetentionWorker struct {
	Store   retentionStore
	Metrics *DeliveryHealthMetrics
	Log     *slog.Logger
	Now     func() time.Time
}

func (w *RetentionWorker) Run(ctx context.Context) {
	w.runOnce(ctx)
	ticker := time.NewTicker(RetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *RetentionWorker) runOnce(ctx context.Context) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, RetentionPassTimeout)
	defer cancel()
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	pruned, err := w.Store.PruneAppWebhookDeliveries(ctx, now().Add(-DeliveryRetention), RetentionBatchSize)
	if err == nil {
		w.Metrics.markPruned(pruned)
		var bytes int64
		bytes, err = w.Store.AppWebhookDeliveryStorageBytes(ctx)
		if err == nil {
			w.Metrics.markRetentionSucceeded(bytes)
			return
		}
	}
	if parent.Err() != nil {
		return
	}
	w.Metrics.markRetentionFailed()
	w.Log.Warn("webhook: delivery retention pass", "err", err)
}
