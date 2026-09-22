package sched

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// DeadLetterRetention bounds the unified Failed Events projection. It removes
// old ledger rows in batches, but deliberately leaves the authoritative source
// row and append-only audit history intact.
type DeadLetterRetention struct {
	store     state.Store
	now       func() time.Time
	retention time.Duration
	log       *slog.Logger
	ops       *wire.OpsMetrics
}

const deadLetterRetentionBatch = 200

// NewDeadLetterRetention returns the sweep ready for the Loop retention
// ticker. Defaults to a 30-day projection window.
func NewDeadLetterRetention(store state.Store, log *slog.Logger) *DeadLetterRetention {
	if store == nil {
		panic("sched: DeadLetterRetention.store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DeadLetterRetention{
		store:     store,
		now:       time.Now,
		retention: api.DefaultDeadLetterRetention,
		log:       log,
	}
}

// WithClock injects a time source for deterministic tests.
func (r *DeadLetterRetention) WithClock(now func() time.Time) *DeadLetterRetention {
	if now != nil {
		r.now = now
	}
	return r
}

// WithRetention overrides the projection retention window. Non-positive
// values restore the default so a malformed config cannot delete everything.
func (r *DeadLetterRetention) WithRetention(d time.Duration) *DeadLetterRetention {
	if d <= 0 {
		d = api.DefaultDeadLetterRetention
	}
	r.retention = d
	return r
}

// WithOpsMetrics attaches the shared daemon metrics registry.
func (r *DeadLetterRetention) WithOpsMetrics(ops *wire.OpsMetrics) *DeadLetterRetention {
	r.ops = ops
	return r
}

// SweepOnce deletes every projection older than the configured cutoff in
// bounded store batches. Open and replayed events are treated uniformly: the
// retention policy governs the dashboard projection, while source lifecycle
// state and audit events remain available to operators.
func (r *DeadLetterRetention) SweepOnce(ctx context.Context) (int, error) {
	now := r.now()
	cutoff := now.Add(-r.retention)
	deleted := 0
	for {
		n, err := r.store.PurgeExpiredDeadLetterEvents(ctx, cutoff, deadLetterRetentionBatch)
		if err != nil {
			return deleted, err
		}
		deleted += n
		if n < deadLetterRetentionBatch {
			break
		}
	}
	if deleted > 0 {
		if r.ops != nil {
			r.ops.ObserveDLQRetention(deleted)
		}
		r.log.Info("dead-letter retention sweep", "deleted", deleted,
			"retention", r.retention, "cutoff", cutoff.Format(time.RFC3339))
	}
	return deleted, nil
}
