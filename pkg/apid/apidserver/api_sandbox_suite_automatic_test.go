package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxSuiteAutomaticWaitsForCapacityAndRecoversLaunch(t *testing.T) {
	e, app, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	e.store.SetDeploymentTrafficForTest(baseline.ID, 100, true)
	if _, err := e.store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
		AccountID: e.acct.ID, AppID: app.ID, RequiredApprovals: 1, AfterStage: 1,
		TimeoutMinutes: 60, Approvers: "admin", AllowSelfApproval: true,
	}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeploySnapshotting,
		ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/held.ext4", "/apps/held.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	scenario, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, uuid.NewString(), api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Checkout", Requests: []api.APISandboxExampleRequest{{Title: "Check checkout", Method: "GET", Path: "/orders/demo", ExpectedStatus: 200}},
		},
	}, state.APISandboxScenarioOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	suite, err := e.store.PutAPISandboxSuite(ctx, app.ID, "", []string{scenario.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.EnqueueAPISandboxSuiteAutomatic(ctx, candidate.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetAPISandboxSuiteAutomaticStatus(ctx, candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("suite automation was not opt-in: %v", err)
	}
	config := "/v1/apps/" + app.Slug + "/api-sandbox-suite"
	var saved api.APISandboxSuiteResponse
	enabled := true
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, config, api.APISandboxSuiteSaveRequest{
		ExpectedRevision: &suite.Revision, ScenarioIDs: suite.ScenarioIDs, RunBeforeApproval: &enabled,
	}, nil), http.StatusOK, &saved)
	if saved.Suite == nil || !saved.Suite.RunBeforeApproval || saved.Suite.Revision == suite.Revision {
		t.Fatalf("automatic setting did not update the suite revision: %+v", saved)
	}
	if _, err := e.store.PutAPISandboxSuite(ctx, app.ID, saved.Suite.Revision, suite.ScenarioIDs, nil); err != nil {
		t.Fatalf("legacy suite save lost compatibility: %v", err)
	}
	path := "/v1/deployments/" + candidate.ID + "/approvals/sandbox-suite-runs"
	manual := uuid.NewString()
	var started api.APISandboxSuiteRunReport
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, path+"/"+manual, api.APISandboxSuiteRunRequest{
		BaselineDeploymentID: baseline.ID, SuiteRevision: saved.Suite.Revision,
	}, nil), http.StatusAccepted, &started)
	enqueueAt := time.Now().UTC()
	if err := e.store.EnqueueAPISandboxSuiteAutomatic(ctx, candidate.ID, enqueueAt); err != nil {
		t.Fatal(err)
	}
	if err := e.s.processAPISandboxSuiteAutomatic(ctx, e.store, e.store, enqueueAt); err != nil {
		t.Fatal(err)
	}
	var list api.APISandboxSuiteRunsResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if list.AutomaticStatus != "waiting_for_capacity" || len(list.Runs) != 1 || list.Runs[0].Automatic {
		t.Fatalf("unexpected suite queue state: %+v", list)
	}
	run, err := e.store.ClaimAPISandboxSuiteRun(ctx, enqueueAt, time.Minute)
	if err != nil || run.ID != manual {
		t.Fatalf("claim manual suite: %+v %v", run, err)
	}
	finishSandboxSuite(&run, enqueueAt, "approval_inputs_changed")
	if err := e.store.CheckpointAPISandboxSuiteRun(ctx, run, enqueueAt); err != nil {
		t.Fatal(err)
	}
	afterCapacity := enqueueAt.Add(sandboxSuiteAutoClaim / 2)
	req, err := e.store.ClaimAPISandboxSuiteAutomatic(ctx, afterCapacity, sandboxSuiteAutoClaim)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := e.s.startAPISandboxSuiteAutomatic(ctx, e.store, req, afterCapacity); err != nil || status != "started" {
		t.Fatalf("start automatic suite: %s %v", status, err)
	}
	// Replay the hold and simulate a worker restart after the run committed
	// but before it checkpointed the automatic request.
	if err := e.store.EnqueueAPISandboxSuiteAutomatic(ctx, candidate.ID, afterCapacity); err != nil {
		t.Fatal(err)
	}
	if err := e.s.processAPISandboxSuiteAutomatic(ctx, e.store, e.store, req.ClaimUntil); err != nil {
		t.Fatal(err)
	}
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if list.AutomaticStatus != "started" || len(list.Runs) != 2 ||
		list.Runs[0].ID != req.RunID || !list.Runs[0].Automatic || list.Runs[0].BaselineDeploymentID != baseline.ID {
		t.Fatalf("replay started a duplicate suite or changed its inputs: %+v", list)
	}
	if err := e.store.EnqueueAPISandboxSuiteAutomatic(ctx, candidate.ID, req.ClaimUntil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimAPISandboxSuiteAutomatic(ctx, req.ClaimUntil, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("accepted automatic request was requeued: %v", err)
	}
	if _, err := e.store.ReleaseAppTaskByDeployment(ctx, candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("automatic suite admitted the production release command: %v", err)
	}
}
