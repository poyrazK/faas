// adr: 20261017201006
package actioncompare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
)

func expectedProvenance(s *api.ScenarioActionSnapshot) string {
	if s != nil && s.Version == api.ScenarioSimulationSnapshotVersion {
		return SimulationProvenance
	}
	return Provenance
}

func validSnapshotScope(s *api.ScenarioActionSnapshot) bool {
	if s.Version == api.ScenarioActionSnapshotVersion {
		return s.ManagedWork == nil && s.Scope == api.ScenarioActionScope && s.SimulationVersion == 0 && s.SimulationSHA256 == "" && s.RecorderVersion == 0 && s.RecorderSessions == 0 && s.PendingRecorderSessions == 0 && s.IncompleteRecorderSessions == 0 && len(s.SimulatedAttempts) == 0 && s.PendingSimulationAttempts == 0 && s.IncompleteSimulationAttempts == 0
	}
	return s.Version == api.ScenarioSimulationSnapshotVersion && s.Scope == api.ScenarioSimulationScope && s.SimulationVersion == api.ScenarioSimulationVersion && digestPattern.MatchString(s.SimulationSHA256) && s.RecorderVersion == 1 && s.RecorderSessions >= int64(len(s.Workloads)) && s.RecorderSessions <= 256 && s.PendingRecorderSessions == 0 && s.IncompleteRecorderSessions == 0 && s.PendingSimulationAttempts == 0 && s.IncompleteSimulationAttempts == 0 && len(s.SimulatedAttempts) <= api.MaxScenarioSimulationAttempts
}

func addSimulationGroups(snapshot *api.ScenarioActionSnapshot, groups map[string]Change, ids map[string]bool, total int) error {
	workloads := map[string]bool{}
	for _, w := range snapshot.Workloads {
		workloads[w] = true
	}
	for _, a := range snapshot.SimulatedAttempts {
		id, err := uuid.Parse(a.ID)
		if err != nil || ids[id.String()] || !workloads[a.Workload] || !namePattern.MatchString(a.Fixture) || a.Status != "completed" || a.Attempt < 1 || a.Attempt > api.MaxScenarioSimulationAttempts || !digestPattern.MatchString(a.RequestSHA256) || a.IdempotencySHA256 != "" && !digestPattern.MatchString(a.IdempotencySHA256) {
			return errors.New("invalid simulated attempt identity or completion")
		}
		ids[id.String()] = true
		// Reuse the declared fixture grammar, including the provider adapter's
		// method/path boundary and mutually exclusive HTTP/fault responses.
		fixture := api.ScenarioSimulationFixture{Workload: a.Workload, Name: a.Fixture, Integration: a.Integration, Adapter: a.Adapter, Method: a.Method, Path: a.Path, Responses: []api.ScenarioSimulationResponse{{Status: a.ResponseStatus, Fault: a.Fault}}}
		if a.Fault == "timeout" {
			fixture.Responses[0].DelayMS = 1
		}
		if _, _, err := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{fixture}}, snapshot.Workloads); err != nil {
			return errors.New("invalid simulated adapter evidence")
		}
		canonical, err := behaviorcompare.CanonicalJSON(a.Payload)
		if err != nil {
			return errors.New("invalid simulated payload")
		}
		total += len(canonical)
		if total > api.MaxScenarioActionPayloadBytes {
			return errors.New("combined captured payload exceeds format bounds")
		}
		payload, _ := json.Marshal(struct {
			Payload           json.RawMessage `json:"payload"`
			RequestSHA256     string          `json:"request_sha256"`
			IdempotencySHA256 string          `json:"idempotency_sha256,omitempty"`
			Attempt           int             `json:"attempt"`
			ResponseStatus    int             `json:"response_status"`
			Fault             string          `json:"fault,omitempty"`
		}{canonical, a.RequestSHA256, a.IdempotencySHA256, a.Attempt, a.ResponseStatus, a.Fault})
		keyBody, _ := json.Marshal([]string{a.Workload, "outbound", "", "", a.Fixture})
		digest := sha256.Sum256(keyBody)
		key := hex.EncodeToString(digest[:])
		group := groups[key]
		if group.Key == "" {
			group = Change{Key: key, Workload: a.Workload, Source: "outbound", Name: a.Fixture}
		}
		destination, _ := json.Marshal([]string{"gregale-simulated-destination-v1", a.Integration, a.Adapter, a.Method, a.Path})
		group.Before = append(group.Before, Value{ReceiverSHA256: api.ScenarioSimulationDigest(destination), Type: "simulated." + a.Adapter, Payload: payload})
		groups[key] = group
	}
	return nil
}
