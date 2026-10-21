// adr: 20261018000200
package actioncompare

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func postgresFaultEvidence() *Evidence {
	e := actionEvidence()
	e.Provenance = SimulationProvenance
	s := e.Snapshot
	s.Version, s.Scope, s.SimulationVersion = 2, api.ScenarioSimulationScope, 1
	s.SimulationSHA256, s.RecorderVersion, s.RecorderSessions = strings.Repeat("f", 64), 1, 2
	s.Workloads = []string{"primary", "worker"}
	p := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary", Postgres: "read_write"}, {Workload: "worker", Async: true, Postgres: "read_write"}}, PostgresFault: &rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 1000, RecoverWithinMS: 5000}}
	_, h, _ := rehearsalwork.Normalize(p, s.Workloads)
	f := &rehearsalfault.Receipt{ID: uuid.NewString(), AccountID: uuid.NewString(), RunID: s.RunID, AppID: uuid.NewString(), DeploymentID: uuid.NewString(), ResourceID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), InvocationID: uuid.NewString(), CheckpointAttemptID: uuid.NewString(), OwnerID: uuid.NewString(), BackendSHA256: strings.Repeat("c", 64), PlanSHA256: h, Spec: *p.PostgresFault, State: "recovered", Activated: true, Recovered: true, OutageMS: 1100, RecoveryMS: 100, ClosedConnections: 1}
	s.ManagedWork = &rehearsalwork.Evidence{Version: 1, RunID: s.RunID, PlanSHA256: h, Status: "complete", Closing: true, Plan: p, PostgresFault: f,
		Bindings: []rehearsalwork.Binding{{Workload: "primary", AppID: uuid.NewString(), DeploymentID: uuid.NewString(), PostgresResourceID: f.ResourceID}, {Workload: "worker", AppID: f.AppID, DeploymentID: f.DeploymentID, PostgresResourceID: f.ResourceID}},
		Jobs:     []rehearsalwork.Job{{ID: f.InvocationID, Workload: "worker", AppID: f.AppID, DeploymentID: f.DeploymentID, Method: "POST", Path: "/import", PayloadSHA256: rehearsalwork.PayloadHash([]byte(`{}`)), State: "completed", Attempts: []rehearsalwork.Attempt{{Number: 1, InstanceID: f.InstanceID, DeploymentID: f.DeploymentID, Outcome: "succeeded"}}}}}
	s.SimulatedAttempts = []api.ScenarioSimulationAttempt{{ID: f.CheckpointAttemptID, Workload: "worker", Fixture: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", RequestSHA256: strings.Repeat("d", 64), Payload: json.RawMessage(`{}`), Attempt: 1, Status: "completed", ResponseStatus: 200}}
	return e
}

func TestPostgresFaultComparisonRequiresIndependentProofAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*Evidence)
	}{
		{"fresh-identities-and-clock-jitter", "passed", func(e *Evidence) {
			e.Snapshot.ManagedWork.PostgresFault.OutageMS += 150
			e.Snapshot.ManagedWork.PostgresFault.RecoveryMS += 150
		}},
		{"recovery-budget", "failed", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.RecoveryMS = 5001 }},
		{"missing-activation", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.Activated = false }},
		{"missing-recovery", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.Recovered = false }},
		{"lost-owner", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.Error = "owner lost" }},
		{"different-database", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.ResourceID = uuid.NewString() }},
		{"replaced-worker", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.Jobs[0].Attempts[0].InstanceID = uuid.NewString() }},
		{"checkpoint-missing", "inconclusive", func(e *Evidence) { e.Snapshot.SimulatedAttempts = nil }},
		{"checkpoint-interrupted", "inconclusive", func(e *Evidence) { e.Snapshot.SimulatedAttempts[0].Status = "interrupted" }},
		{"outage-too-short", "inconclusive", func(e *Evidence) { e.Snapshot.ManagedWork.PostgresFault.OutageMS = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := postgresFaultEvidence(), postgresFaultEvidence()
			tc.mutate(b)
			if c := Compare(a, b); c.Status != tc.want {
				t.Fatalf("want %s: %+v", tc.want, c)
			}
		})
	}
	e := postgresFaultEvidence()
	e.Snapshot.ManagedWork.PostgresFault.RecoveryMS = 5001
	if c := Compare(e, e); c.Status != "failed" {
		t.Fatalf("matching budget violations passed: %+v", c)
	}
}
