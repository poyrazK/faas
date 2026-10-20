package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
)

// SealScenarioTestActions closes a run's managed-effect window. Pending work
// returns status pending while receipts settle. For simulation runs it also
// commits an irreversible relay admission fence after managed work settles.
func (c *Client) SealScenarioTestActions(ctx context.Context, runID string) (ScenarioActionSnapshot, error) {
	var out ScenarioActionSnapshot
	if err := c.do(ctx, "POST", "/v1/dev/test-runs/"+url.PathEscape(runID)+"/actions/seal", nil, &out); err != nil {
		return out, err
	}
	body, err := json.Marshal(out)
	if err != nil || len(body) > MaxScenarioActionSnapshotBytes || !validScenarioActionSnapshotScope(out) || out.RunID != runID ||
		len(out.Workloads) == 0 || len(out.Workloads) > 16 || len(out.Intents) > MaxScenarioActionIntents || len(out.SimulatedAttempts) > MaxScenarioSimulationAttempts || out.RecorderSessions < 0 || out.RecorderSessions > 256 || out.PendingRecorderSessions > out.RecorderSessions || out.PendingRecorderSessions < 0 || out.IncompleteRecorderSessions < 0 || out.PendingSimulationAttempts < 0 || out.IncompleteSimulationAttempts < 0 || out.CapturedCount < 0 || out.PendingManagedWork < 0 || out.IncompleteManagedEffects < 0 || out.SuppressedDeliveries < 0 ||
		out.EventCaptureVersion != 0 && out.EventCaptureVersion != ScenarioEventCaptureVersion || len(out.PublishedEvents) > MaxScenarioCapturedEvents || out.IncompleteCapturedEvents < 0 {
		return ScenarioActionSnapshot{}, errors.New("server did not return a bounded action snapshot for this run")
	}
	if out.Status != "complete" && out.Status != "pending" && out.Status != "incomplete" || out.Status == "complete" && out.SealedAt == "" || out.Status == "pending" && (out.PendingManagedWork == 0 && out.PendingSimulationAttempts == 0 && out.PendingRecorderSessions == 0 || out.SealedAt != "") {
		return ScenarioActionSnapshot{}, errors.New("server did not acknowledge a valid action snapshot state")
	}
	if out.Version == ScenarioSimulationSnapshotVersion && out.Status == "complete" && (out.RecorderSessions < int64(len(out.Workloads)) || out.PendingRecorderSessions != 0 || out.IncompleteRecorderSessions != 0) {
		return ScenarioActionSnapshot{}, errors.New("server did not acknowledge complete durable recorder coverage")
	}
	return out, nil
}

func validScenarioActionSnapshotScope(s ScenarioActionSnapshot) bool {
	if s.Version == ScenarioActionSnapshotVersion {
		return s.Scope == ScenarioActionScope && s.SimulationVersion == 0 && s.SimulationSHA256 == "" && s.RecorderVersion == 0 && s.RecorderSessions == 0 && s.PendingRecorderSessions == 0 && s.IncompleteRecorderSessions == 0 && len(s.SimulatedAttempts) == 0 && s.PendingSimulationAttempts == 0 && s.IncompleteSimulationAttempts == 0
	}
	if s.Version != ScenarioSimulationSnapshotVersion || s.Scope != ScenarioSimulationScope || s.SimulationVersion != ScenarioSimulationVersion || s.RecorderVersion != 1 || len(s.SimulationSHA256) != 64 {
		return false
	}
	_, err := hex.DecodeString(s.SimulationSHA256)
	return err == nil
}
