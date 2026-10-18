// adr: 20261018000200
package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func withPostgresFault(t *testing.T, r *run) {
	t.Helper()
	p := r.SuspendResume.Receipts[0]
	r.SuspendResume = nil
	r.Actions.SuspendResumeSHA256 = ""
	s := r.Actions.Evidence.Snapshot
	s.SuspendResume = nil
	w := s.ManagedWork
	w.Plan.PostgresFault = &rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 1000, RecoverWithinMS: 5000}
	_, h, err := rehearsalwork.Normalize(w.Plan, s.Workloads)
	if err != nil {
		t.Fatal(err)
	}
	w.PlanSHA256, r.Actions.ManagedWorkSHA256 = h, h
	r.Actions.PostgresFaultSHA256 = rehearsalpg.Hash(*w.Plan.PostgresFault)
	w.PostgresFault = &rehearsalfault.Receipt{ID: p.ID, AccountID: p.AccountID, RunID: p.RunID, AppID: p.AppID, ResourceID: r.Postgres.ResourceID, DeploymentID: p.DeploymentID, InstanceID: p.InstanceID, RecorderSessionID: p.RecorderSessionID, InvocationID: p.InvocationID, CheckpointAttemptID: p.CheckpointAttemptID, OwnerID: uuid.NewString(), BackendSHA256: h, PlanSHA256: h, Spec: *w.Plan.PostgresFault, State: "recovered", Activated: true, Recovered: true, ClosedConnections: 1, OutageMS: 1100, RecoveryMS: 100}
}

func TestPostgresRecoveryJourneyRequiresProofAndSpecificRegression(t *testing.T) {
	for _, mode := range []string{"complete", "no-cut", "no-recovery", "wrong-account", "wrong-resource", "budget", "wrong-enrollment", "replaced-worker", "owner-loss"} {
		t.Run(mode, func(t *testing.T) {
			a, b, base, candidate := fixtureReports(t)
			for _, r := range []*run{&a.BaselineRuns[0], &a.CandidateRuns[0], &b.BaselineRuns[0], &b.CandidateRuns[0]} {
				withPostgresFault(t, r)
			}
			r := &b.CandidateRuns[0]
			f := r.Actions.Evidence.Snapshot.ManagedWork.PostgresFault
			switch mode {
			case "no-cut":
				f.ClosedConnections = 0
			case "no-recovery":
				f.Recovered = false
			case "wrong-account":
				f.AccountID = uuid.NewString()
			case "wrong-resource":
				f.ResourceID = uuid.NewString()
			case "budget":
				f.RecoveryMS = 5001
			case "wrong-enrollment":
				r.Actions.PostgresFaultSHA256 = ""
			case "replaced-worker":
				r.Actions.Evidence.Snapshot.ManagedWork.Jobs[0].Attempts[1].InstanceID = uuid.NewString()
			case "owner-loss":
				f.Error = "owner lost"
			}
			if err := verifyJourney(a, b, base, candidate, account, "postgres-recovery"); (err == nil) != (mode == "complete") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "complete" {
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				var decoded report
				if err = json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if err = verifyJourney(a, decoded, base, candidate, account, "postgres-recovery"); err != nil {
					t.Fatalf("recovery report round trip: %v", err)
				}
			}
			if mode == "complete" && verify(a, b, base, candidate, account) == nil {
				t.Fatal("database recovery qualified a VM pause journey")
			}
		})
	}
}
