package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type suiteQuotaOnce struct {
	state.APISandboxApprovalCheckStore
	waited bool
}

func (s *suiteQuotaOnce) CreateAPISandboxApprovalCheck(ctx context.Context, id string, baseline, candidate state.App, limits api.Limits, pins state.APISandboxApprovalPins) ([2]state.App, error) {
	if !s.waited {
		s.waited = true
		return [2]state.App{}, &state.QuotaError{Kind: state.QuotaErrorKindDeveloperApps}
	}
	return s.APISandboxApprovalCheckStore.CreateAPISandboxApprovalCheck(ctx, id, baseline, candidate, limits, pins)
}

func TestAPISandboxSuiteQueuesRunsAndReportsAllScenarios(t *testing.T) {
	e, app, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	e.store.SetDeploymentTrafficForTest(baseline.ID, 100, true)
	if _, err := e.store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
		AccountID: e.acct.ID, AppID: app.ID, RequiredApprovals: 1, AfterStage: 1,
		TimeoutMinutes: 60, Approvers: "admin", AllowSelfApproval: true,
	}, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	held, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeploySnapshotting,
		ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, held.ID, "/apps/held.ext4", "/apps/held.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	names := []string{"Charge order", "Refund order", "Download export"}
	ids := make([]string, 0, len(names))
	var last api.APISandboxLibraryScenario
	for _, name := range names {
		id := uuid.NewString()
		ids = append(ids, id)
		input := api.APISandboxScenarioInput{
			Seed: json.RawMessage(`{"order":"private-fixture"}`),
			APISandboxScenarioGuide: api.APISandboxScenarioGuide{
				Name:     name,
				Requests: []api.APISandboxExampleRequest{{Title: "Check state", Method: "GET", Path: "/orders/test-order", ExpectedStatus: 200}},
			},
		}
		last, err = e.store.PutAPISandboxLibraryScenario(ctx, app.ID, id, input, state.APISandboxScenarioOrigin{})
		if err != nil {
			t.Fatal(err)
		}
	}
	config := "/v1/apps/" + app.Slug + "/api-sandbox-suite"
	empty := ""
	bad := api.APISandboxSuiteSaveRequest{ExpectedRevision: &empty, ScenarioIDs: []string{ids[0], ids[0]}}
	if res := e.do(t, http.MethodPut, config, bad, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("accepted a repeated scenario: %d %s", res.Code, res.Body)
	}
	bad.ScenarioIDs = []string{uuid.NewString()}
	if res := e.do(t, http.MethodPut, config, bad, nil); res.Code != http.StatusConflict {
		t.Fatalf("accepted an unowned scenario: %d %s", res.Code, res.Body)
	}
	var suite api.APISandboxSuiteResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, config,
		api.APISandboxSuiteSaveRequest{ExpectedRevision: &empty, ScenarioIDs: ids}, nil), http.StatusOK, &suite)
	path := "/v1/deployments/" + held.ID + "/approvals/sandbox-suite-runs"
	runID := uuid.NewString()
	input := api.APISandboxSuiteRunRequest{SuiteRevision: suite.Suite.Revision, BaselineDeploymentID: baseline.ID}
	var report api.APISandboxSuiteRunReport
	response := e.do(t, http.MethodPut, path+"/"+runID, input, nil)
	readAPISandboxTestResponse(t, response, http.StatusAccepted, &report)
	if report.Verdict != "running" || report.ScenarioCount != 3 || strings.Contains(response.Body.String(), "private-fixture") {
		t.Fatalf("unexpected queued suite report: %s", response.Body)
	}
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, path+"/"+runID, input, nil), http.StatusOK, &report)
	if res := e.do(t, http.MethodPut, path+"/"+uuid.NewString(), input, nil); res.Code != http.StatusConflict {
		t.Fatalf("accepted two active suites for one app: %d", res.Code)
	}
	items, err := e.store.ListAPISandboxSuiteScenarios(ctx, runID)
	if err != nil || len(items) != len(ids) {
		t.Fatalf("missing pinned suite members: %v", err)
	}
	checks := &suiteQuotaOnce{APISandboxApprovalCheckStore: e.store}
	tick := func() {
		t.Helper()
		run, err := e.store.GetAPISandboxSuiteRun(ctx, runID)
		if err != nil || run.NextAttemptAt == nil {
			t.Fatalf("suite is not due for another step: %+v %v", run, err)
		}
		if err := e.s.processAPISandboxSuite(ctx, e.store, checks, *run.NextAttemptAt); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	postponed, err := e.store.GetAPISandboxSuiteRun(ctx, runID)
	if err != nil || postponed.FinishedAt != nil || postponed.CompletedCount != 0 ||
		postponed.NextAttemptAt == nil || !postponed.NextAttemptAt.Equal(postponed.CreatedAt.Add(apiSandboxSuiteQuotaPoll)) {
		t.Fatalf("suite did not wait for sandbox capacity: %+v %v", postponed, err)
	}
	for i, item := range items {
		tick()
		evidence, err := e.store.GetAPISandboxApprovalCheck(ctx, item.ComparisonID)
		if err != nil || evidence.Comparison.ScenarioRevision != item.ScenarioRevision ||
			evidence.Comparison.ScenarioName != names[i] || evidence.Comparison.Verdict != "running" {
			t.Fatalf("worker did not use pinned scenario %d: %+v %v", i, evidence, err)
		}
		if i+1 < len(items) {
			tick()
			if _, err := e.store.GetAPISandboxApprovalCheck(ctx, items[i+1].ComparisonID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("suite launched a second pair before the first finished")
			}
		}
		finishSuiteComparisonForTest(t, e, evidence.Comparison, i == 1)
		tick()
	}
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path+"/"+runID, nil, nil), http.StatusOK, &report)
	if report.FinishedAt == nil || report.CompletedCount != 3 || report.Verdict != "regression" ||
		report.Scenarios[0].Status != "passed" || report.Scenarios[1].Status != "regression" || report.Scenarios[2].Status != "passed" ||
		report.Scenarios[1].Comparison.FirstFailingStep.FailedPointer != "/status" {
		t.Fatalf("wrong aggregate report: %+v", report)
	}
	standalone, err := e.store.ListAPISandboxApprovalChecks(ctx, held.ID)
	if err != nil || len(standalone) != 0 {
		t.Fatalf("suite comparisons duplicated individual approval reports: %+v %v", standalone, err)
	}
	edit := last.Scenario.APISandboxScenarioInput
	edit.Name = "Changed export"
	if _, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, last.ID, edit,
		state.APISandboxScenarioOrigin{ExpectedRevision: last.Scenario.Revision}); err != nil {
		t.Fatal(err)
	}
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path+"/"+runID, nil, nil), http.StatusOK, &report)
	if report.Verdict != "stale" || report.StaleReason != "suite_scenario_changed" || report.Scenarios[2].ScenarioName != names[2] {
		t.Fatalf("reading current suite evidence lost the historical revision: %+v", report)
	}
}

func finishSuiteComparisonForTest(t *testing.T, e testEnv, report api.APISandboxComparisonReport, regress bool) {
	t.Helper()
	ctx := t.Context()
	var apps []string
	for range 2 {
		now := time.Now().UTC()
		op, err := e.store.ClaimAPISandboxOperation(ctx, now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		op.Phase, op.NextAttemptAt = state.APISandboxLive, nil
		if err := e.store.SaveAPISandboxOperation(ctx, op, now); err != nil {
			t.Fatal(err)
		}
		claimed, err := e.store.ClaimAPISandboxRun(ctx, now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, claimed.AppID)
		claimed.Status = api.APISandboxRunPassed
		if regress && claimed.ID == report.Candidate.ID {
			claimed.Status, claimed.FailureCode = api.APISandboxRunFailed, "json_assertion_failed"
			if err := e.store.AppendAPISandboxRunStep(ctx, claimed, api.APISandboxRunStep{
				Title: "Read refund status", Index: 1, Method: "GET", FailureCode: "json_assertion_failed", FailedPointer: "/status",
			}, now); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.store.FinishAPISandboxRun(ctx, claimed, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range apps {
		if err := e.store.DeleteApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPISandboxSuiteReleaseRequirement(t *testing.T) {
	e, app, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	e.store.SetDeploymentTrafficForTest(baseline.ID, 100, true)
	now := time.Now().UTC()
	required := true
	policy := state.ReleaseApprovalPolicy{
		AccountID: e.acct.ID, AppID: app.ID, RequiredApprovals: 1, AfterStage: 1,
		TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin, AllowSelfApproval: true,
		RequirePassingSandboxSuite: &required,
	}
	if _, err := e.store.SetReleaseApprovalPolicy(ctx, policy, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	// An existing client editing the approval settings can omit the new field.
	policy.RequirePassingSandboxSuite = nil
	if saved, err := e.store.SetReleaseApprovalPolicy(ctx, policy, now); err != nil || !saved.RequiresSandboxSuite() {
		t.Fatalf("approval policy lost the suite requirement: %+v, %v", saved, err)
	}
	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeploySnapshotting,
		ImageDigest: "sha256:candidate", DeployedBy: "deployer", ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/held.ext4", "/apps/held.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.RecordReleaseApproval(ctx, state.ReleaseApproval{
		AccountID: e.acct.ID, AppID: app.ID, DeploymentID: candidate.ID,
		ApproverAccountID: e.acct.ID, Approver: "reviewer", ApproverRole: state.ReleaseApproverRolePersonal, Decision: state.ReleaseApprovalApprove,
	}, now); err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + candidate.ID + "/approvals/sandbox-suite-runs"
	gate := func(want, id string) {
		t.Helper()
		var response api.APISandboxSuiteRunsResponse
		readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &response)
		if gate := response.ReleaseGate; gate == nil || gate.Status != want || gate.SuiteRunID != id {
			t.Fatalf("release gate = %+v, want %s for %s", gate, want, id)
		}
	}
	params := state.CreateAppTaskParams{
		AccountID: e.acct.ID, AppID: app.ID, DeploymentID: candidate.ID, Kind: state.AppTaskKindRelease,
		Command: candidate.ReleaseCommand, CheckPassingSandboxSuite: true,
	}
	block := func() {
		t.Helper()
		if _, err := e.store.CreateAppTask(ctx, params); !errors.Is(err, state.ErrPassingSandboxSuiteRequired) {
			t.Fatalf("release task admission error = %v, want missing passing suite", err)
		}
		if _, err := e.store.ReleaseAppTaskByDeployment(ctx, candidate.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("blocked command already admitted: %v", err)
		}
	}
	gate("no_suite", "")
	block()
	scenarioID := uuid.NewString()
	input := api.APISandboxScenarioInput{Seed: json.RawMessage(`{}`), APISandboxScenarioGuide: api.APISandboxScenarioGuide{
		Name:     "Check refund",
		Requests: []api.APISandboxExampleRequest{{Title: "Read refund", Method: "GET", Path: "/refund", ExpectedStatus: 200}},
	}}
	scenario, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, scenarioID, input, state.APISandboxScenarioOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	suite, err := e.store.PutAPISandboxSuite(ctx, app.ID, "", []string{scenarioID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate("waiting", "")
	block()
	if err := e.store.EnqueueAPISandboxSuiteAutomatic(ctx, candidate.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := e.s.processAPISandboxSuiteAutomatic(ctx, e.store, e.store, now); err != nil {
		t.Fatal(err)
	}
	runs, err := e.store.ListAPISandboxSuiteRuns(ctx, candidate.ID)
	if err != nil || len(runs) != 1 || !runs[0].Automatic {
		t.Fatalf("required suite did not start automatically after human approval: %+v, %v", runs, err)
	}
	finish := func(id string, regress bool) {
		t.Helper()
		tick := func() {
			t.Helper()
			run, err := e.store.GetAPISandboxSuiteRun(ctx, id)
			if err != nil || run.NextAttemptAt == nil {
				t.Fatalf("suite is not due: %+v, %v", run, err)
			}
			if err := e.s.processAPISandboxSuite(ctx, e.store, e.store, *run.NextAttemptAt); err != nil {
				t.Fatal(err)
			}
		}
		tick()
		items, err := e.store.ListAPISandboxSuiteScenarios(ctx, id)
		if err != nil || len(items) != 1 {
			t.Fatalf("suite items = %+v, %v", items, err)
		}
		evidence, err := e.store.GetAPISandboxApprovalCheck(ctx, items[0].ComparisonID)
		if err != nil {
			t.Fatal(err)
		}
		finishSuiteComparisonForTest(t, e, evidence.Comparison, regress)
		tick()
	}
	finish(runs[0].ID, true)
	gate("failed", runs[0].ID)
	block()
	rerun := func() string {
		t.Helper()
		id := uuid.NewString()
		var report api.APISandboxSuiteRunReport
		readAPISandboxTestResponse(t, e.do(t, http.MethodPut, path+"/"+id, api.APISandboxSuiteRunRequest{
			SuiteRevision: suite.Revision, BaselineDeploymentID: baseline.ID,
		}, nil), http.StatusAccepted, &report)
		finish(id, false)
		return id
	}
	firstPass := rerun()
	gate("passed", firstPass)
	input.Name = "Check current refund"
	if _, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, scenarioID, input,
		state.APISandboxScenarioOrigin{ExpectedRevision: scenario.Scenario.Revision}); err != nil {
		t.Fatal(err)
	}
	gate("stale", firstPass)
	block()
	freshPass := rerun()
	if _, err := e.store.CreateAppTask(ctx, params); err != nil {
		t.Fatalf("passing suite did not admit release task: %v", err)
	}
	if _, err := e.store.DeleteReleaseApprovalPolicy(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	gate("admitted", freshPass)
}
