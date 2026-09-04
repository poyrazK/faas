package sched

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// WorkflowRetention runs the background retention sweeps for durable workflows (ADR-081 §9).
// It enforces 30 days retention for completed workflow runs and 90 days for historical events.
type WorkflowRetention struct {
	store state.WorkflowStore
	log   *slog.Logger
}

// NewWorkflowRetention creates a new WorkflowRetention cleaner.
func NewWorkflowRetention(store state.WorkflowStore, log *slog.Logger) *WorkflowRetention {
	return &WorkflowRetention{
		store: store,
		log:   log,
	}
}

// SweepOnce runs one pass of retention cleanup.
func (r *WorkflowRetention) SweepOnce(ctx context.Context) error {
	if r.store == nil {
		return nil
	}

	// 30 days retention for finished workflow runs and their associated steps
	runsDeleted, err := r.store.SweepExpiredWorkflowRuns(ctx, 30*24*time.Hour)
	if err != nil {
		if r.log != nil {
			r.log.Warn("workflow retention: sweep runs failed", "err", err)
		}
		return err
	}

	// 90 days retention for workflow events
	eventsDeleted, err := r.store.SweepExpiredWorkflowEvents(ctx, 90*24*time.Hour)
	if err != nil {
		if r.log != nil {
			r.log.Warn("workflow retention: sweep events failed", "err", err)
		}
		return err
	}

	if (runsDeleted > 0 || eventsDeleted > 0) && r.log != nil {
		r.log.Info("workflow retention: sweep complete", "runs_deleted", runsDeleted, "events_deleted", eventsDeleted)
	}

	return nil
}
