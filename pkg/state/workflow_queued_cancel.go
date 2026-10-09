package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	WorkflowQueuedRunCancelEligible         = "eligible"
	WorkflowQueuedRunCancelCancelled        = "cancelled"
	WorkflowQueuedRunCancelNotFound         = "not_found"
	WorkflowQueuedRunCancelWorkflowMismatch = "workflow_mismatch"
	WorkflowQueuedRunCancelAlreadyCancelled = "already_cancelled"
	WorkflowQueuedRunCancelNotQueued        = "not_queued"
	WorkflowQueuedRunCancelAlreadyStarted   = "already_started"
)

// WorkflowQueuedRunCancelResult reports the current classification for one
// selected run. Run metadata is omitted when the run does not belong to the
// requested app.
type WorkflowQueuedRunCancelResult struct {
	RunID        string
	WorkflowName string
	Outcome      string
	Status       string
	StartedAt    *time.Time
	ScheduledFor *time.Time
	CreatedAt    *time.Time
	CancelledAt  *time.Time
}

// WorkflowQueuedRunCancelStore provides atomic cancellation for a bounded
// selection. Implementations recheck pending status and StartedAt while
// holding the same lock used by the dispatcher claim path.
type WorkflowQueuedRunCancelStore interface {
	CancelUnstartedWorkflowRuns(context.Context, string, string, []string, string) ([]WorkflowQueuedRunCancelResult, error)
}

func normalizeWorkflowQueuedRunCancelSelection(appID string, ids []string) (string, []string, error) {
	appUUID, err := uuid.Parse(appID)
	if err != nil || appUUID == uuid.Nil || len(ids) == 0 || len(ids) > api.WorkflowQueuedRunCancelBatchMax {
		return "", nil, ErrInvalidArgument
	}
	normalized := make([]string, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for i, rawID := range ids {
		id, parseErr := uuid.Parse(rawID)
		if parseErr != nil || id == uuid.Nil {
			return "", nil, ErrInvalidArgument
		}
		normalized[i] = id.String()
		if _, exists := seen[normalized[i]]; exists {
			return "", nil, ErrInvalidArgument
		}
		seen[normalized[i]] = struct{}{}
	}
	return appUUID.String(), normalized, nil
}

func ClassifyWorkflowRunForQueuedCancel(run *WorkflowRun, workflowName string) string {
	if run == nil {
		return WorkflowQueuedRunCancelNotFound
	}
	if workflowName != "" && run.WorkflowName != workflowName {
		return WorkflowQueuedRunCancelWorkflowMismatch
	}
	if run.CancelledAt != nil {
		return WorkflowQueuedRunCancelAlreadyCancelled
	}
	if run.StartedAt != nil {
		return WorkflowQueuedRunCancelAlreadyStarted
	}
	if run.Status != WorkflowRunStatusPending || run.FinishedAt != nil {
		return WorkflowQueuedRunCancelNotQueued
	}
	return WorkflowQueuedRunCancelEligible
}

func workflowQueuedRunCancelResult(runID string, run *WorkflowRun, outcome string) WorkflowQueuedRunCancelResult {
	result := WorkflowQueuedRunCancelResult{RunID: runID, Outcome: outcome}
	if run == nil {
		return result
	}
	result.WorkflowName = run.WorkflowName
	result.Status = run.Status
	result.StartedAt = cloneWorkflowTime(run.StartedAt)
	scheduledFor := run.ScheduledFor
	result.ScheduledFor = &scheduledFor
	createdAt := run.CreatedAt
	result.CreatedAt = &createdAt
	result.CancelledAt = cloneWorkflowTime(run.CancelledAt)
	return result
}
