package state

import (
	"context"
	"time"
)

func (m *MemStore) WorkflowAlertSnapshot(_ context.Context, accountID, appID string, since, now time.Time) (WorkflowAlertSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := WorkflowAlertSnapshot{}
	owned := func(id string) bool {
		app, ok := m.apps[id]
		return ok && app.AccountID == accountID && (appID == "" || appID == id)
	}
	for _, run := range m.workflowRuns {
		if !owned(run.AppID) {
			continue
		}
		if run.FinishedAt != nil && !run.FinishedAt.Before(since) && run.CancelledAt == nil && (run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead) {
			result.Failures++
		}
		if run.Status == WorkflowRunStatusPending {
			readyAt := run.CreatedAt
			if run.ScheduledFor.After(readyAt) {
				readyAt = run.ScheduledFor
			}
			if !readyAt.After(now) {
				result.PendingAgeSeconds = max(result.PendingAgeSeconds, now.Sub(readyAt).Seconds())
			}
		}
		if run.Status != WorkflowRunStatusPending && run.Status != WorkflowRunStatusRunning && run.Status != WorkflowRunStatusAwaitingEvent {
			continue
		}
		for _, step := range m.workflowSteps[run.ID] {
			if step.Status == WorkflowStepStatusAwaitingEvent && step.StartedAt != nil {
				result.WaitingAgeSeconds = max(result.WaitingAgeSeconds, now.Sub(*step.StartedAt).Seconds())
			}
		}
	}
	for _, row := range m.workflowScheduleOccurrences {
		if owned(row.AppID) && row.Status == WorkflowScheduleSkippedQuota && !row.EvaluatedAt.Before(since) {
			result.QuotaSkips++
		}
	}
	return result, nil
}
