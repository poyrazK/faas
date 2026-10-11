package state

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// workflowRunMaxConcurrentRuns reads the policy snapshot captured with a run.
// Old snapshots and internal fixtures without this field retain unlimited
// per-workflow concurrency (still bounded by the app's plan quota).
func workflowRunMaxConcurrentRuns(snapshot json.RawMessage) int {
	var definition struct {
		MaxConcurrentRuns int `json:"max_concurrent_runs"`
	}
	if json.Unmarshal(snapshot, &definition) != nil || definition.MaxConcurrentRuns < 0 || definition.MaxConcurrentRuns > api.WorkflowMaxConcurrentRunsLimit {
		return 0
	}
	return definition.MaxConcurrentRuns
}

// workflowRunMaxConcurrentActions reads the action budget captured with a run.
// Omission and zero retain the existing unbounded behavior; invalid stored
// values fail closed to one action, and oversized snapshots clamp to the API
// ceiling.
func workflowRunMaxConcurrentActions(snapshot json.RawMessage) int {
	var definition struct {
		MaxConcurrentActions int `json:"max_concurrent_actions"`
	}
	if json.Unmarshal(snapshot, &definition) != nil || definition.MaxConcurrentActions == 0 {
		return 0
	}
	if definition.MaxConcurrentActions < 0 {
		return 1
	}
	if definition.MaxConcurrentActions > api.WorkflowMaxConcurrentActionsLimit {
		return api.WorkflowMaxConcurrentActionsLimit
	}
	return definition.MaxConcurrentActions
}

// workflowRunConsumesConcurrency distinguishes queued, never-started pending
// runs from workflow instances that have already begun or are waiting to wake.
func workflowRunConsumesConcurrency(run WorkflowRun) bool {
	switch run.Status {
	case WorkflowRunStatusRunning, WorkflowRunStatusAwaitingEvent:
		return true
	case WorkflowRunStatusPending:
		return run.StartedAt != nil
	default:
		return false
	}
}
