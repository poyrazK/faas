package state

import (
	"encoding/json"
	"time"
)

// deploymentStageStateForCreate returns the initial customer-visible stage
// state for a newly-created deployment. The migration default keeps legacy
// direct SQL inserts compatible, but store-owned creates must stamp the first
// stage when the deployment is enqueued so its duration includes source fetch
// and build time.
func deploymentStageStateForCreate(raw json.RawMessage, startedAt time.Time) ([]byte, error) {
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	startedAt = stageTimestamp(startedAt)
	if len(raw) == 0 {
		return json.Marshal(StageState{
			Current:          StageSourceDownload,
			CurrentStartedAt: &startedAt,
			History:          []StageStateItem{},
		})
	}

	var state StageState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.Current == "" {
		state.Current = StageSourceDownload
	}
	if state.CurrentStartedAt == nil && state.Current != "" {
		state.CurrentStartedAt = &startedAt
	}
	if state.History == nil {
		state.History = []StageStateItem{}
	}
	return json.Marshal(state)
}

// ensureDeploymentStageStarted repairs rows created before stage timing was
// enforced (or by direct SQL using the migration default). A completed stage
// must never be emitted with a null start or a negative duration; the
// deployment creation time is the best boundary available for such rows.
func ensureDeploymentStageStarted(state *StageState, createdAt, at time.Time) {
	if state.Current == "" || state.CurrentStartedAt != nil {
		return
	}
	startedAt := createdAt
	if startedAt.IsZero() {
		startedAt = at
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	startedAt = stageTimestamp(startedAt)
	state.CurrentStartedAt = &startedAt
}

func stageTimestamp(at time.Time) time.Time {
	return at.UTC()
}
