package main_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

func suspendCollectedRun(t *testing.T, ms int64) main.TestRunReceipt {
	t.Helper()
	r := coldCollectedRun(t, 100)
	r.Profile = "warm"
	r.Actions.ColdStartSHA256 = ""
	r.ColdWake = nil
	r.Actions.Evidence = nil
	spec := rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseMS: 10, WithinMS: 500}
	_, d, _ := rehearsalresume.Normalize(spec, []string{"primary"})
	r.Actions.SuspendResumeSHA256 = d
	r.SuspendResume = &rehearsalresume.Evidence{Version: 1, RunID: r.RunID, Spec: spec, PlanSHA256: d, Status: "complete", Receipts: []rehearsalresume.Receipt{{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: r.WorkloadAppIDs["primary"], DeploymentID: r.WorkloadDeploymentIDs["primary"], InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), RunID: r.RunID, Workload: "primary", State: "complete", Method: "in_place", PauseMS: 10, ResumeMS: ms}}}
	snapshot := testSealedActionSnapshot(r.RunID, 0)
	snapshot.Version, snapshot.Scope, snapshot.SimulationVersion, snapshot.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, r.Actions.SimulationSHA256
	snapshot.RecorderVersion, snapshot.RecorderSessions = 1, 1
	snapshot.SuspendResume = r.SuspendResume
	main.NewTestActionCollector(t.Context(), scenarioActionClientFunc(func(context.Context, string) (api.ScenarioActionSnapshot, error) { return snapshot, nil }), &r, []string{"primary"})()
	return r
}
func TestSuspendResumeBudgetAndBindingSurviveReportSerialization(t *testing.T) {
	before, after := suspendCollectedRun(t, 100), suspendCollectedRun(t, 501)
	raw, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	var decoded main.TestRunReceipt
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != "failed" || !main.TestExecutionCompleteForEvidence(decoded) {
		t.Fatal("measured resume budget discarded complete behavior evidence")
	}
	if c := main.CompareTestActions(before, decoded); c.Status != "failed" {
		t.Fatalf("budget failure: %+v", c)
	}
	decoded.CleanupError = "cleanup failed"
	if c := main.CompareTestActions(before, decoded); c.Status != "inconclusive" {
		t.Fatal("budget violation excused cleanup failure")
	}
	decoded.CleanupError = ""
	decoded.Error += "; operation failed"
	if c := main.CompareTestActions(before, decoded); c.Status != "inconclusive" {
		t.Fatal("budget violation excused another operation failure")
	}
	left, right := suspendCollectedRun(t, 100), suspendCollectedRun(t, 100)
	right.Actions.Evidence.Snapshot.SuspendResume = nil
	if c := main.CompareTestActions(left, right); c.Status != "inconclusive" {
		t.Fatal("missing enrolled proof passed")
	}
	right = suspendCollectedRun(t, 100)
	right.SuspendResume.Receipts[0].AppID = uuid.NewString()
	if c := main.CompareTestActions(left, right); c.Status != "inconclusive" {
		t.Fatal("wrong deployment binding passed")
	}
}
func TestSuspendResumeRequiresWarmContainedCheckpoint(t *testing.T) {
	s := main.TestScenario{Project: "primary", CommandExecution: &main.TestCommandExecutionSpec{Version: 1, Runtime: "contained"}, Actions: &main.TestActionSpec{Network: "contained", Webhooks: "capture", Simulation: &api.ScenarioSimulationPlan{Version: 1}}, Behavior: &main.TestBehaviorSpec{Version: 1}, Requests: []main.TestHTTPRequest{{Method: "GET", Path: "/run"}}, SuspendResume: &main.TestSuspendResumeSpec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseFor: "1s", ResumeWithin: "2s"}}
	if err := main.ValidateTestCommandExecution(s, "real-vm", "warm"); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"cold", "restored", "all"} {
		if main.ValidateTestCommandExecution(s, "real-vm", profile) == nil {
			t.Fatal("unsupported lifecycle combination accepted")
		}
	}
	s.SuspendResume.PauseFor = "31s"
	if main.ValidateTestCommandExecution(s, "real-vm", "warm") == nil {
		t.Fatal("unbounded pause accepted")
	}
	s.SuspendResume.PauseFor = "1.5ms"
	if main.ValidateTestCommandExecution(s, "real-vm", "warm") == nil {
		t.Fatal("fractional milliseconds silently shortened the pause")
	}
}

type suspendPollingClient struct {
	proof         rehearsalresume.Evidence
	begins, polls int
	wrongPlan     bool
}

func (c *suspendPollingClient) BeginScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error) {
	c.begins++
	e := c.proof
	e.Status = "pending"
	return e, nil
}
func (c *suspendPollingClient) GetScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error) {
	c.polls++
	e := c.proof
	if c.wrongPlan {
		e.PlanSHA256 = "different"
	}
	return e, nil
}

func TestSuspendResumeRunnerWaitsForExactObservedProof(t *testing.T) {
	for _, mode := range []string{"complete", "wrong_plan", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			r := suspendCollectedRun(t, 100)
			client := &suspendPollingClient{proof: *r.SuspendResume, wrongPlan: mode == "wrong_plan"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			err := main.RunTestSuspendResume(ctx, client, []string{"primary"}, &r)
			if (err == nil) != (mode == "complete") {
				t.Fatalf("checkpoint %s: %v", mode, err)
			}
			if client.begins != 1 || mode != "cancelled" && client.polls != 1 {
				t.Fatal("checkpoint intent was repeated instead of polling its proof")
			}
		})
	}
}
