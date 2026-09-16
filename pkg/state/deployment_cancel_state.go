package state

import (
	"encoding/json"
	"fmt"
	"time"
)

const stageHistoryStatusCancelled = "cancelled"

// finalizeCancelledDeploymentState closes every customer-visible release
// projection in the same in-memory mutation as the deployment cancellation.
// PostgreSQL enforces the equivalent transition with the migration trigger so
// older binaries receive the same invariant during a rolling upgrade.
func finalizeCancelledDeploymentState(d *Deployment, at time.Time, reason CancelReason) error {
	d.TrafficPercent = 0
	d.RolloutState = "aborted"
	d.RolloutCompletedAt = nil
	d.RolloutAbortedAt = ptrTime(at)
	d.RolloutAbortedReason = "deployment cancelled: " + string(reason)
	if len(d.StageState) == 0 {
		return nil
	}
	var stages StageState
	if err := json.Unmarshal(d.StageState, &stages); err != nil {
		return fmt.Errorf("decode stage state: %w", err)
	}
	if stages.Current == "" {
		return nil
	}
	ensureDeploymentStageStarted(&stages, d.CreatedAt, at)
	durationMs := int64(0)
	if stages.CurrentStartedAt != nil {
		durationMs = at.Sub(*stages.CurrentStartedAt).Milliseconds()
		if durationMs < 0 {
			durationMs = 0
		}
	}
	endedAt := stageTimestamp(at)
	stages.History = append(stages.History, StageStateItem{
		Name:       stages.Current,
		StartedAt:  ptrTime(derefTime(stages.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durationMs,
		Status:     stageHistoryStatusCancelled,
		Reason:     d.RolloutAbortedReason,
	})
	if len(stages.History) > MaxStageHistory {
		stages.History = stages.History[len(stages.History)-MaxStageHistory:]
	}
	stages.Current = ""
	stages.CurrentStartedAt = nil
	encoded, err := json.Marshal(stages)
	if err != nil {
		return fmt.Errorf("encode stage state: %w", err)
	}
	d.StageState = encoded
	return nil
}
