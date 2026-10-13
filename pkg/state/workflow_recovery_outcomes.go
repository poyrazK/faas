package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Observations never follow a newer resume or turn missing history into success.
func workflowRecoveryExecution(now time.Time, status string, count, expected int, admitted *time.Time, updated time.Time, finished *time.Time, cancelled bool) api.EventRecoveryExecution {
	out := api.EventRecoveryExecution{ObservedAt: now, State: "unknown", Source: "unavailable"}
	if admitted == nil || admitted.After(now) || count < expected+1 || updated.Before(*admitted) || updated.After(now) {
		return out
	}
	out.Source = "workflow_run"
	if count > expected+1 {
		out.State = "superseded"
		return out
	}
	switch status {
	case WorkflowRunStatusPending:
		out.State = "queued"
	case WorkflowRunStatusRunning, WorkflowRunStatusAwaitingEvent:
		out.State = "running"
	case WorkflowRunStatusSucceeded:
		out.State = "succeeded"
	case WorkflowRunStatusFailed, WorkflowRunStatusDead:
		out.State = "failed"
	}
	if out.State == "failed" {
		if cancelled {
			out.State = "cancelled"
		}
	}
	if out.State == "succeeded" || out.State == "failed" || out.State == "cancelled" {
		if finished != nil && (finished.Before(*admitted) || finished.After(now)) {
			out.State = "unknown"
			return out
		}
		out.CompletedAt = cloneEventReceiptTime(finished)
	}
	return out
}
func observeWorkflowRecoveryExecutions(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, job string, after, through int64, now time.Time) (map[int64]api.EventRecoveryExecution, error) {
	rows, err := q.WorkflowRecoveryExecutionObservations(ctx, db, sqlc.WorkflowRecoveryExecutionObservationsParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(job), AfterPosition: after, ThroughPosition: through, NowAt: pgtypeFromTime(now)})
	if err != nil {
		return nil, fmt.Errorf("observe workflow recovery executions: %w", err)
	}
	out := make(map[int64]api.EventRecoveryExecution, len(rows))
	for _, r := range rows {
		if r.ResultState != "" && r.ResultRecordedAt.Valid {
			out[r.Position] = api.EventRecoveryExecution{ObservedAt: now, State: r.ResultState, Source: "recovery_result", EvidenceSource: "workflow_run", RecordedAt: timestamptzToTimePtr(r.ResultRecordedAt), CompletedAt: timestamptzToTimePtr(r.ResultCompletedAt)}
			continue
		}
		out[r.Position] = workflowRecoveryExecution(now, r.RunStatus, int(r.ResumeCount), int(r.ExpectedResumeCount), timestamptzToTimePtr(r.AdmittedAt), timeFromPgtype(r.RunUpdatedAt), timestamptzToTimePtr(r.FinishedAt), r.Cancelled)
	}
	return out, nil
}

func (m *MemStore) workflowRecoveryExecutionLocked(job *memEventRecoveryJob, item memEventRecoveryItem, now time.Time) api.EventRecoveryExecution {
	unknown := api.EventRecoveryExecution{ObservedAt: now, State: "unknown", Source: "unavailable"}
	if saved, ok := job.WorkflowResults[item.Position]; ok && saved.RunID == item.WorkflowRunID && item.ExpectedResumeCount != nil && saved.ResumeNumber == *item.ExpectedResumeCount+1 && saved.Execution.RecordedAt != nil && !saved.Execution.RecordedAt.After(now) {
		out := saved.Execution
		out.ObservedAt = now
		out.RecordedAt = cloneEventReceiptTime(out.RecordedAt)
		out.CompletedAt = cloneEventReceiptTime(out.CompletedAt)
		return out
	}
	run, ok := m.workflowRuns[item.WorkflowRunID]
	app, owned := m.eventSubscriptionAppLocked(job.Job.AppID)
	if !ok || !owned || !sameMemUUID(app.AccountID, job.AccountID) || !sameMemUUID(run.AppID, job.Job.AppID) || run.WorkflowName != job.Job.Selection.WorkflowRuns.Name || item.ExpectedResumeCount == nil {
		return unknown
	}
	var admitted *time.Time
	for _, r := range m.workflowResumes[run.ID] {
		if r.ResumeNumber == *item.ExpectedResumeCount+1 && sameMemUUID(r.AccountID, job.AccountID) && !r.CreatedAt.Before(job.Job.CreatedAt) {
			t := r.CreatedAt
			admitted = &t
			break
		}
	}
	cancelled := run.CancelledAt != nil || run.LastError != nil && *run.LastError == "cancelled by operator"
	return workflowRecoveryExecution(now, run.Status, run.ResumeCount, *item.ExpectedResumeCount, admitted, run.UpdatedAt, run.FinishedAt, cancelled)
}
