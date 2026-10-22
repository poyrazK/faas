// adr: 20261017201015
package main_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

type runningSuspendCollectorClient struct {
	snapshot api.ScenarioActionSnapshot
	observed rehearsalresume.Evidence
}

func (c runningSuspendCollectorClient) SealScenarioTestActions(context.Context, string) (api.ScenarioActionSnapshot, error) {
	return c.snapshot, nil
}
func (c runningSuspendCollectorClient) GetScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error) {
	return c.observed, nil
}

func runningSuspendCollectedRun(t *testing.T, mode string) main.TestRunReceipt {
	t.Helper()
	r := suspendCollectedRun(t, 100)
	names := []string{"primary", "worker"}
	r.WorkloadAppIDs["worker"], r.WorkloadDeploymentIDs["worker"] = uuid.NewString(), uuid.NewString()
	spec := rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 10, WithinMS: 500}
	_, digest, err := rehearsalresume.Normalize(spec, names)
	if err != nil {
		t.Fatal(err)
	}
	receipt := rehearsalresume.Receipt{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: r.WorkloadAppIDs["worker"], RunID: r.RunID, Workload: "worker", DeploymentID: r.WorkloadDeploymentIDs["worker"], InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), State: "complete", Method: "in_place", PauseMS: 10, ResumeMS: 100, InvocationID: uuid.NewString(), InvocationAttempt: 1, CheckpointAttemptID: uuid.NewString()}
	if mode == "budget" {
		receipt.ResumeMS = 501
	}
	proof := rehearsalresume.Evidence{Version: 1, RunID: r.RunID, Spec: spec, PlanSHA256: digest, Status: "complete", Receipts: []rehearsalresume.Receipt{receipt}}
	r.Actions.SuspendResumeSHA256 = digest
	r.SuspendResume = nil
	plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}}
	_, workDigest, err := rehearsalwork.Normalize(plan, names)
	if err != nil {
		t.Fatal(err)
	}
	r.Actions.ManagedWorkSHA256 = workDigest
	job := rehearsalwork.Job{ID: receipt.InvocationID, Workload: "worker", AppID: receipt.AppID, DeploymentID: receipt.DeploymentID, Method: "POST", Path: "/import", PayloadSHA256: rehearsalwork.Hash([]byte(`{}`)), State: "completed", Attempts: []rehearsalwork.Attempt{{Number: 1, InstanceID: receipt.InstanceID, DeploymentID: receipt.DeploymentID, Outcome: "succeeded"}}}
	if mode == "real_retry" || mode == "replacement_retry" {
		job.Attempts[0].Outcome = "retry"
		job.Attempts = append(job.Attempts, rehearsalwork.Attempt{Number: 2, InstanceID: receipt.InstanceID, DeploymentID: receipt.DeploymentID, Outcome: "succeeded"})
	}
	if mode == "replacement_retry" {
		job.Attempts[1].InstanceID = uuid.NewString()
	}
	snapshot := testSealedActionSnapshot(r.RunID, 0)
	snapshot.Version, snapshot.Scope, snapshot.SimulationVersion, snapshot.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, r.Actions.SimulationSHA256
	snapshot.Workloads = names
	snapshot.RecorderVersion, snapshot.RecorderSessions = 1, 2
	snapshot.SuspendResume = &proof
	snapshot.ManagedWork = &rehearsalwork.Evidence{Version: 1, RunID: r.RunID, PlanSHA256: workDigest, Status: "complete", Closing: true, Plan: plan, Bindings: []rehearsalwork.Binding{{Workload: "primary", AppID: r.WorkloadAppIDs["primary"], DeploymentID: r.WorkloadDeploymentIDs["primary"]}, {Workload: "worker", AppID: receipt.AppID, DeploymentID: receipt.DeploymentID}}, Jobs: []rehearsalwork.Job{job}}
	snapshot.SimulatedAttempts = []api.ScenarioSimulationAttempt{{ID: receipt.CheckpointAttemptID, Workload: "worker", Fixture: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", RequestSHA256: rehearsalwork.Hash([]byte(`{}`)), Payload: []byte(`{}`), Attempt: 1, Status: "completed", ResponseStatus: 200}}
	observed := proof
	if mode == "observation_differs" {
		observed.Receipts = append([]rehearsalresume.Receipt(nil), proof.Receipts...)
		observed.Receipts[0].RecorderSessionID = uuid.NewString()
	}
	if mode == "missing_checkpoint" {
		snapshot.SimulatedAttempts = nil
	}
	if mode == "wrong_job" {
		snapshot.ManagedWork.Jobs[0].ID = uuid.NewString()
	}
	collector := runningSuspendCollectorClient{snapshot: snapshot, observed: observed}
	main.NewTestActionCollector(t.Context(), collector, &r, names)()
	return r
}

func TestRunningSuspendObservationAndJournalSurviveReports(t *testing.T) {
	before := runningSuspendCollectedRun(t, "complete")
	if before.Actions.Evidence.Status != "complete" {
		t.Fatalf("baseline proof: %+v", before.Actions.Evidence)
	}
	for _, tc := range []struct{ mode, want string }{{"complete", "passed"}, {"real_retry", "failed"}, {"replacement_retry", "inconclusive"}, {"budget", "failed"}, {"observation_differs", "inconclusive"}, {"missing_checkpoint", "inconclusive"}, {"wrong_job", "inconclusive"}} {
		t.Run(tc.mode, func(t *testing.T) {
			after := runningSuspendCollectedRun(t, tc.mode)
			raw, err := json.Marshal(after)
			if err != nil {
				t.Fatal(err)
			}
			var decoded main.TestRunReceipt
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if c := main.CompareTestActions(before, decoded); c.Status != tc.want {
				t.Fatalf("want %s: %+v evidence=%+v", tc.want, c, decoded.Actions.Evidence)
			}
		})
	}
}

func TestRunningSuspendPreparationObservesEnrollmentWithoutStartingPause(t *testing.T) {
	r := runningSuspendCollectedRun(t, "complete")
	proof := *r.SuspendResume
	proof.Status = "pending"
	proof.Receipts = nil
	client := &suspendPollingClient{proof: proof}
	if err := main.PrepareTestSuspendResume(t.Context(), client, []string{"primary", "worker"}, &r, &proof.Spec); err != nil {
		t.Fatal(err)
	}
	if client.begins != 0 || client.polls != 1 || r.SuspendResume.Status != "pending" {
		t.Fatal("CLI started a pause before the job reached its checkpoint")
	}
}
