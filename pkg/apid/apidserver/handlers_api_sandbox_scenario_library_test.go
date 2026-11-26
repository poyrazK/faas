package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestAPISandboxScenarioLibraryReusesDebuggerFixturesAcrossReleases(t *testing.T) {
	e, app, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	requestID := uuid.NewString()
	e.s.store = statetest.NewMem(&debugSandboxScenarioStore{
		MemStore: e.store, appID: app.ID,
		row: sqlc.GetRequestTelemetryByAppAndIdentifierRow{
			ID: stringToPgUUID(requestID), DeploymentID: stringToPgUUID(baseline.ID),
			Method: "GET", Route: "GET /exports/{id}", Count: 1, Status: 500,
		},
	})
	appURL := "/v1/apps/" + app.Slug
	library := appURL + "/api-sandbox-scenarios"
	firstID, secondID := uuid.NewString(), uuid.NewString()
	noRevision := ""
	input := api.APISandboxScenarioSaveRequest{
		APISandboxScenarioInput: api.APISandboxScenarioInput{
			Seed: json.RawMessage(`{"export":"synthetic-export"}`),
			APISandboxScenarioGuide: api.APISandboxScenarioGuide{
				Name:     "Export download",
				Requests: []api.APISandboxExampleRequest{{Title: "Read export", Method: "GET", Path: "/exports/demo", ExpectedStatus: 200}},
			},
		},
		ExpectedRevision: &noRevision, SourceRequestID: requestID,
	}
	var first, retry api.APISandboxLibraryScenario
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, library+"/"+firstID, input, nil), http.StatusOK, &first)
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, library+"/"+firstID, input, nil), http.StatusOK, &retry)
	if first.ID != firstID || first.SourceRequestID != requestID || first.Scenario.Revision != retry.Scenario.Revision {
		t.Fatalf("library save failed to keep provenance or its revision: %+v", first)
	}
	if res := e.do(t, http.MethodPut, library+"/"+secondID, input, nil); res.Code != http.StatusConflict {
		t.Fatalf("library allowed a duplicate name: %d %s", res.Code, res.Body)
	}
	input.Name, input.SourceRequestID = "Refund status", ""
	var second api.APISandboxLibraryScenario
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, library+"/"+secondID, input, nil), http.StatusOK, &second)
	var listing api.APISandboxScenarioLibraryResponse
	response := e.do(t, http.MethodGet, library, nil, nil)
	readAPISandboxTestResponse(t, response, http.StatusOK, &listing)
	if len(listing.Scenarios) != 2 || listing.Scenarios[0].ID != firstID || listing.Scenarios[1].ID != secondID ||
		strings.Contains(response.Body.String(), requestID) || strings.Contains(response.Body.String(), "synthetic-export") {
		t.Fatalf("library list lost entries or contained private detail: %s", response.Body)
	}
	other, err := e.store.CreateApp(ctx, state.App{AccountID: app.AccountID, Type: state.AppTypeApp, Slug: "another-api", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if res := e.do(t, http.MethodGet, "/v1/apps/"+other.Slug+"/api-sandbox-scenarios/"+firstID, nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("another app could read the library scenario: %d", res.Code)
	}

	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive,
		ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/candidate.ext4", "/apps/candidate.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	releaseURL := appURL + "/deployments/" + candidate.ID
	var local api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, releaseURL+"/api-sandbox-scenario", nil, nil), http.StatusOK, &local)
	if local.Scenario != nil {
		t.Fatal("a library scenario was copied to the release")
	}

	revision := first.Scenario.Revision
	compare := api.APISandboxComparisonRequest{BaselineDeploymentID: baseline.ID, ScenarioRevision: revision}
	compareURL := releaseURL + "/api-sandbox-comparisons/" + uuid.NewString()
	var report api.APISandboxComparisonReport
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, compareURL, compare, nil), http.StatusAccepted, &report)
	var run api.APISandboxRunReport
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, releaseURL+"/api-sandbox-runs/"+uuid.NewString(),
		api.APISandboxRunRequest{ScenarioRevision: second.Scenario.Revision}, nil), http.StatusAccepted, &run)
	if run.ScenarioRevision != second.Scenario.Revision {
		t.Fatalf("released run used the wrong scenario: %+v", run)
	}
	input.Name, input.ExpectedRevision = "Export download updated", &revision
	input.Seed = json.RawMessage(`{"export":"updated-fixture"}`)
	var edit api.APISandboxLibraryScenario
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, library+"/"+firstID, input, nil), http.StatusOK, &edit)
	if edit.Scenario.Revision == revision || edit.SourceRequestID != requestID {
		t.Fatalf("edit failed to preserve scenario identity: %+v", edit)
	}
	if res := e.do(t, http.MethodDelete, library+"/"+firstID+"?expected_revision="+revision, nil, nil); res.Code != http.StatusConflict {
		t.Fatalf("stale revision could delete the edited scenario: %d", res.Code)
	}
	if res := e.do(t, http.MethodDelete, library+"/"+firstID+"?expected_revision="+edit.Scenario.Revision, nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete library scenario: %d %s", res.Code, res.Body)
	}
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, compareURL, compare, nil), http.StatusOK, &report)
	for _, leg := range []api.APISandboxRunReport{report.Baseline, report.Candidate} {
		stored, err := e.store.GetAPISandboxRun(ctx, leg.ID)
		if err != nil {
			t.Fatal(err)
		}
		op, err := e.store.GetAPISandboxOperation(ctx, stored.AppID)
		if err != nil || op.Scenario == nil || op.Scenario.Revision != revision ||
			string(op.Scenario.Seed) != `{"export":"synthetic-export"}` || op.Scenario.SourceRequestID != "" {
			t.Fatalf("comparison changed its snapshot after editing and removing the library scenario: %+v %v", op, err)
		}
	}
}

func TestAPISandboxScenarioLibraryApprovalUsesSelectedRevision(t *testing.T) {
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
	id := uuid.NewString()
	scenario := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name:     "Export",
			Requests: []api.APISandboxExampleRequest{{Title: "Read export", Method: "GET", Path: "/exports/demo", ExpectedStatus: 200}},
		},
	}
	saved, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, id, scenario, state.APISandboxScenarioOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + held.ID + "/approvals/sandbox-checks"
	var list api.APISandboxApprovalChecksResponse
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if list.Availability != api.APISandboxApprovalReady || list.ScenarioRevision != "" || len(list.LibraryScenarios) != 1 {
		t.Fatalf("approval did not offer a library scenario without a release-local one: %+v", list)
	}
	input := api.APISandboxComparisonRequest{BaselineDeploymentID: baseline.ID, ScenarioRevision: saved.Scenario.Revision}
	checkURL := path + "/" + uuid.NewString()
	var opened api.APISandboxApprovalCheck
	readAPISandboxTestResponse(t, e.do(t, http.MethodPut, checkURL, input, nil), http.StatusAccepted, &opened)
	if opened.Verdict != api.APISandboxComparisonRunning || opened.Comparison.ScenarioRevision != saved.Scenario.Revision {
		t.Fatalf("requested library revision was not pinned: %+v", opened)
	}
	scenario.Name = "Changed export check"
	if _, err := e.store.PutAPISandboxLibraryScenario(ctx, app.ID, id, scenario,
		state.APISandboxScenarioOrigin{ExpectedRevision: saved.Scenario.Revision}); err != nil {
		t.Fatal(err)
	}
	var after api.APISandboxApprovalCheck
	readAPISandboxTestResponse(t, e.do(t, http.MethodGet, checkURL, nil, nil), http.StatusOK, &after)
	if after.Verdict != api.APISandboxApprovalStale || after.StaleReason != "baseline_scenario_changed" {
		t.Fatalf("edited library scenario left an outdated approval result current: %+v", after)
	}
}
