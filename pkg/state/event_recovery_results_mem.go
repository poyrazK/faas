package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recoveryResultKey struct {
	JobID    string
	Position int64
}
type memRecoveryExecutionResult struct {
	InvocationID string
	Generation   int64
	CreatedAt    time.Time
	Execution    api.EventRecoveryExecution
}

// Called under the invocation mutation mutex, never by read-only observations.
// First confirmed terminal evidence wins and is retained with the owning job.
func (m *MemStore) captureRecoveryInvocationResultLocked(inv Invocation, now time.Time) {
	if inv.ID == "" || inv.CreatedAt.After(now) || inv.Outcome != nil && *inv.Outcome == OutcomeUncertain {
		return
	}
	state := recoveryExecution(now, string(inv.State), inv.Attempts, inv.CompletedAt, "", 0, nil)
	switch state.State {
	case "succeeded", "failed", "dead_lettered", "expired", "cancelled", "superseded":
	default:
		return
	}
	if state.CompletedAt != nil && (state.CompletedAt.After(now) || state.CompletedAt.Before(inv.CreatedAt)) {
		return
	}
	state.Source, state.EvidenceSource = "recovery_result", "invocation"
	state.RecordedAt = cloneEventReceiptTime(&now)
	for _, job := range m.eventRecoveryJobs {
		if job.Job.Selection.Mode != "execution" || !sameMemUUID(job.AccountID, inv.AccountID) || !sameMemUUID(job.Job.AppID, inv.AppID) {
			continue
		}
		for _, item := range job.Items {
			if item.State != "queued" || item.ReplayInvocationID != inv.ID || item.ReplayGeneration == nil || *item.ReplayGeneration != inv.ReplayGeneration || !item.ReplayCreatedAt.Equal(inv.CreatedAt) {
				continue
			}
			key := recoveryResultKey{job.Job.ID, item.Position}
			if _, saved := m.eventRecoveryExecutionResults[key]; saved {
				continue
			}
			if m.eventRecoveryExecutionResults == nil {
				m.eventRecoveryExecutionResults = map[recoveryResultKey]memRecoveryExecutionResult{}
			}
			m.eventRecoveryExecutionResults[key] = memRecoveryExecutionResult{inv.ID, inv.ReplayGeneration, inv.CreatedAt, state}
		}
	}
}
