package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func emptyStoredWorkflowQueueHealth(at time.Time) *api.StoredWorkflowQueueHealth {
	return &api.StoredWorkflowQueueHealth{
		ObservedAt: at, AppDispatchLimit: api.WorkflowDispatchMaxPerApp, TenantDispatchLimit: api.WorkflowDispatchMaxPerTenant,
		ReasonCounts: map[string]int64{
			api.StoredWorkflowQueueReady: 0, api.StoredWorkflowQueueScheduled: 0, api.StoredWorkflowQueueRetryBackoff: 0,
			api.StoredWorkflowQueueParkedWait: 0, api.StoredWorkflowQueueAppCapacity: 0,
			api.StoredWorkflowQueueTenantCapacity: 0, api.StoredWorkflowQueueWorkflowCapacity: 0,
		},
	}
}

func (m *MemStore) storedWorkflowQueueHealthLocked(appID, name string, now time.Time) *api.StoredWorkflowQueueHealth {
	result := emptyStoredWorkflowQueueHealth(now)
	counts := m.workflowDispatchOccupancyLocked(now)
	result.AppRunningCount = int64(counts.apps[appID])
	result.AppAtCapacity = result.AppRunningCount >= int64(result.AppDispatchLimit)
	for _, run := range m.workflowRuns {
		if run.AppID != appID || run.WorkflowName != name {
			continue
		}
		dueAt, due := m.workflowQueueDueAtLocked(run, now)
		stale := run.Status == WorkflowRunStatusRunning && due
		if run.Status != WorkflowRunStatusPending && run.Status != WorkflowRunStatusAwaitingEvent && !stale {
			continue
		}
		result.WaitingRunCount++
		reason := api.StoredWorkflowQueueReady
		if !due {
			reason = m.workflowFutureQueueReasonLocked(run, now)
		} else {
			result.DueRunCount++
			if stale {
				result.StaleRunCount++
			}
			if age := now.Sub(dueAt).Seconds(); age > result.OldestDueAgeSeconds {
				result.OldestDueAgeSeconds = age
			}
			if blocked := counts.capacityReason(run); blocked != "" {
				reason = blocked
			}
		}
		result.ReasonCounts[reason]++
	}
	return result
}

// Eligibility age excludes intentional waits, live claims and terminal runs.
// Both queue diagnostics and backlog alerts call this under the store lock.
func (m *MemStore) workflowQueueDueAtLocked(run WorkflowRun, now time.Time) (time.Time, bool) {
	var at time.Time
	switch run.Status {
	case WorkflowRunStatusPending, WorkflowRunStatusAwaitingEvent:
		at = run.ScheduledFor
	case WorkflowRunStatusRunning:
		at = m.workflowDispatchDeadlineLocked(run)
	default:
		return time.Time{}, false
	}
	if at.After(now) {
		return time.Time{}, false
	}
	if at.Before(run.CreatedAt) {
		at = run.CreatedAt
	}
	return at, true
}

func (m *MemStore) workflowFutureQueueReasonLocked(run WorkflowRun, now time.Time) string {
	if run.Status == WorkflowRunStatusAwaitingEvent {
		return api.StoredWorkflowQueueParkedWait
	}
	for _, step := range m.workflowSteps[run.ID] {
		if step.Status == WorkflowStepStatusPending && step.NextRetryAt != nil && step.NextRetryAt.After(now) && step.NextRetryAt.Equal(run.ScheduledFor) {
			return api.StoredWorkflowQueueRetryBackoff
		}
	}
	return api.StoredWorkflowQueueScheduled
}
