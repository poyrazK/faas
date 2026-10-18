package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"strings"
	"testing"
)

func TestColdStartCollectorMatchesHostReceiptToWakeAndDeployment(t *testing.T) {
	for _, mode := range []string{"complete", "missing_plan", "wrong_wake", "wrong_deployment", "budget_exceeded"} {
		t.Run(mode, func(t *testing.T) {
			r := testRunReceipt{RunID: strings.Repeat("a", 32), Status: "passed", Profile: "cold", Engine: "real-vm", Actions: &testActionEvidence{SimulationSHA256: strings.Repeat("d", 64), Network: "contained", Webhooks: "capture", PolicyVersion: 1, Status: "applied"}}
			p := rehearsalcold.Preparation{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: r.RunID, Workload: "primary", DeploymentID: uuid.NewString(), State: "booted", BootInstanceID: uuid.NewString(), BootRecorderSessionID: uuid.NewString(), WakeID: uuid.NewString(), Method: "cold_boot", StartupMS: 100}
			spec := rehearsalcold.Spec{Version: 1, WithinMS: 500}
			_, digest, _ := rehearsalcold.Normalize(spec, []string{"primary"})
			r.Actions.ColdStartSHA256 = digest
			r.WorkloadAppIDs = map[string]string{"primary": p.AppID}
			r.WorkloadDeploymentIDs = map[string]string{"primary": p.DeploymentID}
			r.ColdWake = map[string]testServiceWakeEvidence{"primary": {WakeID: p.WakeID, Method: "cold_boot"}}
			if mode == "wrong_wake" {
				r.ColdWake["primary"] = testServiceWakeEvidence{WakeID: uuid.NewString(), Method: "cold_boot"}
			}
			if mode == "wrong_deployment" {
				r.WorkloadDeploymentIDs["primary"] = uuid.NewString()
			}
			if mode == "budget_exceeded" {
				p.StartupMS = 501
			}
			snapshot := testSealedActionSnapshot(r.RunID, 0)
			snapshot.Version, snapshot.Scope, snapshot.SimulationVersion, snapshot.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, r.Actions.SimulationSHA256
			snapshot.RecorderVersion, snapshot.RecorderSessions = 1, 1
			snapshot.ColdStart = &rehearsalcold.Evidence{Version: 1, RunID: r.RunID, Spec: spec, PlanSHA256: digest, Status: "complete", Preparations: []rehearsalcold.Preparation{p}}
			if mode == "missing_plan" {
				snapshot.ColdStart = nil
			}
			collect := newTestActionCollector(t.Context(), scenarioActionClientFunc(func(context.Context, string) (api.ScenarioActionSnapshot, error) { return snapshot, nil }), &r, []string{"primary"})
			collect()
			valid := mode == "complete"
			if (r.Status == "passed") != valid {
				t.Fatalf("collector: %+v", r)
			}
			proofComplete := mode == "complete" || mode == "budget_exceeded"
			if (r.Actions.Evidence.Status == "complete") != proofComplete {
				t.Fatal("missing evidence and observed budget failure were conflated")
			}
		})
	}
}

func coldCollectedRun(t *testing.T, ms int64) testRunReceipt {
	t.Helper()
	r := testRunReceipt{RunID: strings.Repeat("a", 32), Status: "passed", Profile: "cold", Engine: "real-vm", Actions: &testActionEvidence{SimulationSHA256: strings.Repeat("d", 64), Network: "contained", Webhooks: "capture", PolicyVersion: 1, Status: "applied"}, Behavior: &behaviorcompare.Evidence{ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64)}}
	p := rehearsalcold.Preparation{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), RunID: r.RunID, Workload: "primary", DeploymentID: uuid.NewString(), State: "booted", BootInstanceID: uuid.NewString(), BootRecorderSessionID: uuid.NewString(), WakeID: uuid.NewString(), Method: "cold_boot", StartupMS: ms}
	spec := rehearsalcold.Spec{Version: 1, WithinMS: 500}
	_, digest, _ := rehearsalcold.Normalize(spec, []string{"primary"})
	r.Actions.ColdStartSHA256 = digest
	r.WorkloadAppIDs = map[string]string{"primary": p.AppID}
	r.WorkloadDeploymentIDs = map[string]string{"primary": p.DeploymentID}
	r.ColdWake = map[string]testServiceWakeEvidence{"primary": {WakeID: p.WakeID, Method: "cold_boot"}}
	snapshot := testSealedActionSnapshot(r.RunID, 0)
	snapshot.Version, snapshot.Scope, snapshot.SimulationVersion, snapshot.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, r.Actions.SimulationSHA256
	snapshot.RecorderVersion, snapshot.RecorderSessions = 1, 1
	snapshot.ColdStart = &rehearsalcold.Evidence{Version: 1, RunID: r.RunID, Spec: spec, PlanSHA256: digest, Status: "complete", Preparations: []rehearsalcold.Preparation{p}}
	newTestActionCollector(t.Context(), scenarioActionClientFunc(func(context.Context, string) (api.ScenarioActionSnapshot, error) { return snapshot, nil }), &r, []string{"primary"})()
	return r
}

func TestColdStartBudgetFailureSurvivesReportComparisonAndSerialization(t *testing.T) {
	before, after := coldCollectedRun(t, 100), coldCollectedRun(t, 501)
	raw, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	var decoded testRunReceipt
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != "failed" || !testExecutionCompleteForEvidence(decoded) {
		t.Fatal("complete budget violation was treated as incomplete execution")
	}
	if c := compareTestActions(before, decoded); c.Status != "failed" {
		t.Fatalf("budget violation: %+v", c)
	}
	decoded.CleanupError = "cleanup failed"
	if c := compareTestActions(before, decoded); c.Status != "inconclusive" {
		t.Fatal("budget violation excused missing cleanup")
	}
	decoded.CleanupError = ""
	decoded.Error += "; application operation failed"
	if c := compareTestActions(before, decoded); c.Status != "inconclusive" {
		t.Fatal("budget violation excused another execution failure")
	}
	a, b := coldCollectedRun(t, 100), coldCollectedRun(t, 100)
	a.Actions.Evidence.Snapshot.ColdStart = nil
	b.Actions.Evidence.Snapshot.ColdStart = nil
	if c := compareTestActions(a, b); c.Status != "inconclusive" {
		t.Fatal("declared cold coverage passed without its proof")
	}
}

func TestContainedColdProfileUsesBoundedStartupBudget(t *testing.T) {
	s := testScenario{Project: "primary", CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained"}, Actions: &testActionSpec{Network: "contained", Webhooks: "capture", Simulation: &api.ScenarioSimulationPlan{Version: 1}}, Behavior: &testBehaviorSpec{Version: 1}, Requests: []testHTTPRequest{{Method: "GET", Path: "/run"}}}
	if err := validateTestCommandExecution(s, "real-vm", "cold"); err != nil {
		t.Fatal(err)
	}
	if spec := testColdStartSpec(s, "cold"); spec == nil || spec.WithinMS != 120000 {
		t.Fatalf("default budget: %+v", spec)
	}
	s.CommandExecution.StartupWithin = "3s"
	if spec := testColdStartSpec(s, "cold"); spec.WithinMS != 3000 {
		t.Fatal(spec)
	}
	for _, budget := range []string{"0s", "3m", "invalid", "1us"} {
		s.CommandExecution.StartupWithin = budget
		if validateTestCommandExecution(s, "real-vm", "cold") == nil {
			t.Fatalf("invalid budget %q accepted", budget)
		}
	}
	if testColdStartSpec(s, "warm") != nil {
		t.Fatal("warm run unexpectedly enrolled cold execution")
	}
}
