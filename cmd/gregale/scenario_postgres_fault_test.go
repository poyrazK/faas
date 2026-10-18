// adr: 20261018000200
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestPostgresFaultRecipeRequiresWarmContainedFixture(t *testing.T) {
	for _, mode := range []string{"valid", "no_fixture", "no_checkpoint", "cold", "restored", "local", "simulated", "no_permission"} {
		t.Run(mode, func(t *testing.T) {
			ss, dir, err := readTestManifest("../../tests/scenario-acceptance/rehearsal-running-job/gregale-postgres-fault-test.yaml")
			if err != nil {
				t.Fatal(err)
			}
			s := ss["running-import"]
			engine, profile := "real-vm", "warm"
			switch mode {
			case "no_fixture":
				s.PostgresFixture = nil
			case "no_checkpoint":
				s.Actions.Simulation.Fixtures = s.Actions.Simulation.Fixtures[1:]
			case "cold", "restored":
				profile = mode
			case "local", "simulated":
				engine = mode
			case "no_permission":
				s.ManagedWork.Participants[1].Postgres = ""
			}
			if err = validateTestCommandExecution(s, engine, profile); (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "valid" {
				r := testRunReceipt{}
				if _, err = prepareTestPostgres(dir, s, &r); err != nil {
					t.Fatal(err)
				}
				old := testManagedWorkDigest(s)
				s.ManagedWork.PostgresFault = nil
				if old == testManagedWorkDigest(s) {
					t.Fatal("fault scope was not bound into the recipe")
				}
			}
		})
	}
}

func postgresFaultCollectedRun(t *testing.T, mode string) testRunReceipt {
	t.Helper()
	r := runningSuspendCollectedRun(t, "real_retry")
	p := r.SuspendResume.Receipts[0]
	s := r.Actions.Evidence.Snapshot
	r.SuspendResume, s.SuspendResume = nil, nil
	r.Actions.SuspendResumeSHA256 = ""
	w := s.ManagedWork
	w.Plan.PostgresFault = &rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 1000, RecoverWithinMS: 5000}
	r.Postgres = postgresEvidenceForTest()
	r.Postgres.ResourceID = uuid.NewString()
	for i := range w.Plan.Participants {
		w.Plan.Participants[i].Postgres = "read_write"
		w.Bindings[i].PostgresResourceID = r.Postgres.ResourceID
	}
	_, h, err := rehearsalwork.Normalize(w.Plan, s.Workloads)
	if err != nil {
		t.Fatal(err)
	}
	w.PlanSHA256, r.Actions.ManagedWorkSHA256 = h, h
	r.Actions.PostgresFaultSHA256 = rehearsalpg.Hash(*w.Plan.PostgresFault)
	w.PostgresFault = &rehearsalfault.Receipt{ID: p.ID, AccountID: p.AccountID, RunID: p.RunID, AppID: p.AppID, ResourceID: r.Postgres.ResourceID, DeploymentID: p.DeploymentID, InstanceID: p.InstanceID, RecorderSessionID: p.RecorderSessionID, InvocationID: p.InvocationID, CheckpointAttemptID: p.CheckpointAttemptID, OwnerID: uuid.NewString(), BackendSHA256: strings.Repeat("d", 64), PlanSHA256: h, Spec: *w.Plan.PostgresFault, State: "recovered", Activated: true, Recovered: true, ClosedConnections: 1, OutageMS: 1100, RecoveryMS: 100}
	switch mode {
	case "budget":
		w.PostgresFault.RecoveryMS = 5001
	case "no_recovery":
		w.PostgresFault.Recovered = false
	case "wrong_enrollment":
		r.Actions.PostgresFaultSHA256 = strings.Repeat("f", 64)
	case "wrong_resource":
		w.PostgresFault.ResourceID = uuid.NewString()
	}
	newTestActionCollector(t.Context(), scenarioActionClientFunc(func(context.Context, string) (api.ScenarioActionSnapshot, error) { return *s, nil }), &r, s.Workloads)()
	return r
}

func TestPostgresFaultReportPreservesBudgetFailureAndRejectsMissingProof(t *testing.T) {
	before := postgresFaultCollectedRun(t, "complete")
	for _, tc := range []struct{ mode, want string }{{"complete", "passed"}, {"budget", "failed"}, {"no_recovery", "inconclusive"}, {"wrong_enrollment", "inconclusive"}, {"wrong_resource", "inconclusive"}} {
		t.Run(tc.mode, func(t *testing.T) {
			raw, _ := json.Marshal(postgresFaultCollectedRun(t, tc.mode))
			var r testRunReceipt
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			if c := compareTestActions(before, r); c.Status != tc.want {
				t.Fatalf("want %s: %+v", tc.want, c)
			}
			if tc.mode == "budget" {
				if r.Actions.Evidence.Status != "complete" || !testExecutionCompleteForEvidence(r) {
					t.Fatal("measured budget violation lost complete evidence")
				}
				r.CleanupError = "fixture retained"
				if c := compareTestActions(before, r); c.Status != "inconclusive" {
					t.Fatal("budget violation excused cleanup failure")
				}
				r.CleanupError = ""
				r.Error += "; operation failed"
				if c := compareTestActions(before, r); c.Status != "inconclusive" {
					t.Fatal("budget violation excused another operation failure")
				}
			}
		})
	}
}
