package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// WorkflowRetention runs the background retention sweeps for durable workflows (ADR-081 §9).
// It enforces 30 days retention for completed workflow runs and a 90-day
// event age threshold. Events for active runs remain until the run terminates.
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

// SweepOnce performs one bounded batch for each retention phase.
func (r *WorkflowRetention) SweepOnce(ctx context.Context) error {
	_, err := r.sweepOnce(ctx)
	return err
}

// sweepOnce visits every phase before requesting another pass, so a sustained
// run backlog cannot prevent history or event cleanup from making progress.
func (r *WorkflowRetention) sweepOnce(ctx context.Context) (bool, error) {
	if r.store == nil {
		return false, nil
	}
	var failures []error
	runsDeleted, err := r.store.SweepExpiredWorkflowRuns(ctx, 30*24*time.Hour)
	more := err == nil && runsDeleted >= api.WorkflowRetentionPruneBatch
	if err != nil {
		failures = append(failures, fmt.Errorf("sweep runs: %w", err))
	}
	historyDeleted := 0
	if history, ok := r.store.(state.WorkflowScheduleHistoryStore); ok && ctx.Err() == nil {
		before := time.Now().UTC().Add(-api.WorkflowScheduleHistoryRetention)
		historyDeleted, err = history.PruneWorkflowScheduleOccurrences(ctx, before, api.WorkflowScheduleHistoryPruneBatch)
		if err != nil {
			failures = append(failures, fmt.Errorf("prune schedule history: %w", err))
		} else if historyDeleted >= api.WorkflowScheduleHistoryPruneBatch {
			more = true
		}
	}
	eventsDeleted := 0
	if ctx.Err() == nil {
		eventsDeleted, err = r.store.SweepExpiredWorkflowEvents(ctx, 90*24*time.Hour)
		if err != nil {
			failures = append(failures, fmt.Errorf("sweep events: %w", err))
		}
		if eventsDeleted >= api.WorkflowRetentionPruneBatch {
			more = true
		}
	}
	if (runsDeleted > 0 || eventsDeleted > 0 || historyDeleted > 0) && r.log != nil {
		r.log.Info("workflow retention: sweep complete", "runs_deleted", runsDeleted, "events_deleted", eventsDeleted, "schedule_occurrences_deleted", historyDeleted)
	}
	return more && ctx.Err() == nil, errors.Join(errors.Join(failures...), ctx.Err())
}
