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

// finalizeActiveDeploymentStage moves the in-flight customer-visible stage
// into history and clears Current. Callers use it while holding their store's
// transaction/mutex so the terminal deployment status and stage projection
// are committed together. It returns false when no stage is active.
func finalizeActiveDeploymentStage(st *StageState, createdAt, at time.Time, status, reason string) bool {
	if st == nil || st.Current == "" {
		return false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = stageTimestamp(at)
	ensureDeploymentStageStarted(st, createdAt, at)
	durationMs := int64(0)
	if st.CurrentStartedAt != nil {
		durationMs = at.Sub(*st.CurrentStartedAt).Milliseconds()
		if durationMs < 0 {
			durationMs = 0
		}
	}
	endedAt := at
	st.History = append(st.History, StageStateItem{
		Name:       st.Current,
		StartedAt:  ptrTime(derefTime(st.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durationMs,
		Status:     status,
		Reason:     reason,
	})
	if len(st.History) > MaxStageHistory {
		st.History = st.History[len(st.History)-MaxStageHistory:]
	}
	st.Current = ""
	st.CurrentStartedAt = nil
	return true
}

func stageTimestamp(at time.Time) time.Time {
	return at.UTC()
}
