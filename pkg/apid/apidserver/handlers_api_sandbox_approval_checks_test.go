package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxApprovalCheckTestsHeldArtifactAndRechecksEvidence(t *testing.T) {
	env, parent, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	env.store.SetDeploymentTrafficForTest(baseline.ID, 100, true)
	if _, err := env.store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
		AccountID: env.acct.ID, AppID: parent.ID, RequiredApprovals: 1, AfterStage: 1,
		TimeoutMinutes: 60, Approvers: "admin", AllowSelfApproval: true,
	}, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	candidate, err := env.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: parent.ID, Kind: state.DeploymentKindTarball,
		Status: state.DeploySnapshotting, ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/held.ext4", "/apps/held.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	path := "/v1/deployments/" + candidate.ID + "/approvals/sandbox-checks"
	var list api.APISandboxApprovalChecksResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if list.Availability != api.APISandboxApprovalNoScenario || list.BaselineDeploymentID != baseline.ID {
		t.Fatalf("approval did not find the live baseline: %+v", list)
	}
	owner := "/v1/apps/" + parent.Slug + "/deployments/"
	definition := json.RawMessage(`{
		"name":"Refund an order","seed":{"order":42},
		"requests":[{"title":"Read refund status","method":"GET","path":"/orders/42",
			"expected_status":200,"assertions":[{"pointer":"/status","equals":"refunded"}]}]
	}`)
	var saved api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, owner+baseline.ID+"/api-sandbox-scenario", definition, nil), http.StatusOK, &saved)
	if saved.Scenario == nil {
		t.Fatal("baseline scenario missing")
	}
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if list.Availability != api.APISandboxApprovalReady || list.ScenarioRevision != saved.Scenario.Revision {
		t.Fatalf("approval did not load the baseline scenario: %+v", list)
	}

	input := api.APISandboxComparisonRequest{BaselineDeploymentID: baseline.ID, ScenarioRevision: saved.Scenario.Revision}
	id := uuid.NewString()
	var bad api.APISandboxComparisonRequest = input
	bad.BaselineDeploymentID = uuid.NewString()
	if res := env.do(t, http.MethodPut, path+"/"+id, bad, nil); res.Code != http.StatusConflict {
		t.Fatalf("approval accepted an arbitrary baseline: %d", res.Code)
	}
	var opened api.APISandboxApprovalCheck
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, path+"/"+id, input, nil), http.StatusAccepted, &opened)
	if opened.Verdict != api.APISandboxComparisonRunning || opened.Comparison.CandidateDeploymentID != candidate.ID {
		t.Fatalf("wrong check opened: %+v", opened)
	}
	var retried api.APISandboxApprovalCheck
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, path+"/"+id, input, nil), http.StatusOK, &retried)
	if retried.Comparison.Candidate.ID != opened.Comparison.Candidate.ID {
		t.Fatal("repeating the check created another sandbox")
	}
	if res := env.do(t, http.MethodGet, owner+candidate.ID+"/api-sandbox-comparisons/"+id, nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("approval check bypassed its freshness handling: %d", res.Code)
	}

	pins := map[string]string{}
	for _, leg := range []api.APISandboxRunReport{opened.Comparison.Baseline, opened.Comparison.Candidate} {
		run, err := env.store.GetAPISandboxRun(ctx, leg.ID)
		if err != nil {
			t.Fatal(err)
		}
		app, err := env.store.AppByID(ctx, run.AppID)
		if err != nil {
			t.Fatal(err)
		}
		copy, err := env.store.CreateAPISandboxDeployment(ctx, env.acct.ID, run.AppID, leg.SourceDeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		source, err := env.store.DeploymentByID(ctx, leg.SourceDeploymentID)
		if err != nil || source.RootfsKey != copy.RootfsKey || source.ID == copy.ID {
			t.Fatalf("run used the wrong artifact: %v", err)
		}
		if err := env.store.UpdateDeploymentStatus(ctx, copy.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		pins[app.Slug] = copy.ID
	}
	env.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		slug, _, _ := strings.Cut(req.URL.Host, ".")
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, pins[slug])
		headers.Set(api.RequestIDHeader, req.Header.Get(api.RequestIDHeader))
		body := `{"status":"refunded"}`
		if req.URL.Path == "/openapi.json" {
			body = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{}}`
		}
		return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for range 4 {
		if err := env.s.processAPISandboxOperation(ctx, dueSandboxOperations{env.store}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := env.s.processAPISandboxRun(ctx, env.store); err != nil {
			t.Fatal(err)
		}
	}
	approvalPath := "/v1/deployments/" + candidate.ID + "/approvals"
	var approvalStatus api.ReleaseApprovalStatus
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, approvalPath, nil, nil), http.StatusOK, &approvalStatus)
	holding, err := env.store.DeploymentByID(ctx, candidate.ID)
	if err != nil || holding.Status != state.DeploySnapshotting || approvalStatus.State != api.ReleaseApprovalStateWaiting || !approvalStatus.BeforeReleaseCommand {
		t.Fatalf("sandbox check advanced production: %+v (%v)", approvalStatus, err)
	}
	if _, err := env.store.ReleaseAppTaskByDeployment(ctx, candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("sandbox check admitted the production release command: %v", err)
	}

	var report api.APISandboxApprovalCheck
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path+"/"+id, nil, nil), http.StatusOK, &report)
	if report.Verdict != api.APISandboxComparisonPassed {
		t.Fatalf("held comparison failed: %+v", report)
	}
	changed := json.RawMessage(strings.Replace(string(definition), `"equals":"refunded"`, `"equals":"settled"`, 1))
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, owner+baseline.ID+"/api-sandbox-scenario", changed, nil), http.StatusOK, &saved)
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path+"/"+id, nil, nil), http.StatusOK, &report)
	if report.Verdict != api.APISandboxApprovalStale || report.StaleReason != "baseline_scenario_changed" ||
		report.Comparison.Verdict != api.APISandboxComparisonPassed {
		t.Fatalf("scenario drift lost historical evidence or counted as current: %+v", report)
	}
	env.store.SetDeploymentTrafficForTest(baseline.ID, 0, true)
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path+"/"+id, nil, nil), http.StatusOK, &report)
	if report.StaleReason != "live_baseline_changed" {
		t.Fatalf("the old baseline no longer serves traffic: %+v", report)
	}
	if err := env.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/rebuilt.ext4", "/apps/rebuilt.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path+"/"+id, nil, nil), http.StatusOK, &report)
	if report.StaleReason != "candidate_artifact_changed" {
		t.Fatalf("the old artifact was retained as current evidence: %+v", report)
	}
}
