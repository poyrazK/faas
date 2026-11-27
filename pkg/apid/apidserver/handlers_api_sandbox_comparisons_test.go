package apidserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxComparisonDetectsARegressionOnSeparatePinnedReleases(t *testing.T) {
	env, parent, baseline := sandboxReleaseFixture(t)
	ctx := t.Context()
	candidate, err := env.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive,
		ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetDeploymentRootfs(ctx, candidate.ID, "/apps/candidate.ext4", "/apps/candidate.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + candidate.ID
	path := owner + "/api-sandbox-comparisons"
	fixture := json.RawMessage(`{
		"name":"Refund an order",
		"seed":{"orders":[{"id":"fixture-only"}]},
		"requests":[{"title":"Read refund status","method":"GET","path":"/orders/42?test=private",
			"expected_status":200,"assertions":[{"pointer":"/status","equals":"refunded"}]}]
	}`)
	var scenario api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, owner+"/api-sandbox-scenario", fixture, nil), http.StatusOK, &scenario)
	if scenario.Scenario == nil {
		t.Fatal("scenario was not saved")
	}
	var list api.APISandboxComparisonsResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if len(list.Baselines) != 1 || list.Baselines[0].DeploymentID != baseline.ID || len(list.Comparisons) != 0 {
		t.Fatalf("unexpected baseline list: %+v", list)
	}

	id := uuid.NewString()
	input := api.APISandboxComparisonRequest{BaselineDeploymentID: baseline.ID, ScenarioRevision: scenario.Scenario.Revision}
	var queued, retried api.APISandboxComparisonReport
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, path+"/"+id, input, nil), http.StatusAccepted, &queued)
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, path+"/"+id, input, nil), http.StatusOK, &retried)
	if queued.ID != id || queued.Verdict != api.APISandboxComparisonRunning || queued.Baseline.ID == queued.Candidate.ID ||
		retried.Baseline.ID != queued.Baseline.ID || retried.Candidate.ID != queued.Candidate.ID ||
		queued.Baseline.ScenarioRevision != input.ScenarioRevision || queued.Candidate.ScenarioRevision != input.ScenarioRevision {
		t.Fatalf("comparison lost its version binding: %+v", queued)
	}
	wrong := input
	wrong.ScenarioRevision = uuid.NewString()
	if res := env.do(t, http.MethodPut, path+"/"+id, wrong, nil); res.Code != http.StatusConflict {
		t.Fatalf("comparison accepted a changed revision: %d %s", res.Code, res.Body)
	}
	wrongPath := "/v1/apps/" + parent.Slug + "/deployments/" + baseline.ID + "/api-sandbox-comparisons/" + id
	if res := env.do(t, http.MethodGet, wrongPath, nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("report appeared on the wrong release: %d", res.Code)
	}

	changed := json.RawMessage(strings.ReplaceAll(string(fixture), "refunded", "refunding"))
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, owner+"/api-sandbox-scenario", changed, nil), http.StatusOK, &scenario)
	type pinnedRun struct {
		app    state.App
		target state.Deployment
		result string
	}
	targets := make(map[string]pinnedRun)
	for i, leg := range []api.APISandboxRunReport{queued.Baseline, queued.Candidate} {
		run, err := env.store.GetAPISandboxRun(ctx, leg.ID)
		if err != nil {
			t.Fatal(err)
		}
		app, err := env.store.AppByID(ctx, run.AppID)
		if err != nil {
			t.Fatal(err)
		}
		op, err := env.store.GetAPISandboxOperation(ctx, app.ID)
		if err != nil || op.Scenario == nil || op.Scenario.Revision != input.ScenarioRevision ||
			string(op.Scenario.Seed) != `{"orders":[{"id":"fixture-only"}]}` {
			t.Fatalf("sandbox lost the shared scenario snapshot: %v", err)
		}
		target, err := env.store.CreateAPISandboxDeployment(ctx, env.acct.ID, app.ID, leg.SourceDeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.UpdateDeploymentStatus(ctx, target.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		result := `{"status":"refunded"}`
		if i == 1 {
			result = `{"status":"refunding"}`
		}
		targets[app.Slug] = pinnedRun{app, target, result}
	}
	env.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		slug, _, _ := strings.Cut(req.URL.Host, ".")
		leg, ok := targets[slug]
		if !ok {
			t.Errorf("probe went to an unrecognized sandbox: %s", req.URL.Host)
		}
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, leg.target.ID)
		headers.Set(api.RequestIDHeader, req.Header.Get(api.RequestIDHeader))
		body := leg.result
		if req.URL.Path == "/openapi.json" {
			body = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
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
	var report api.APISandboxComparisonReport
	response := env.do(t, http.MethodGet, path+"/"+id, nil, nil)
	readAPISandboxTestResponse(t, response, http.StatusOK, &report)
	if report.Verdict != api.APISandboxComparisonRegression || report.Baseline.Status != api.APISandboxRunPassed ||
		report.Candidate.Status != api.APISandboxRunFailed || report.FailureSide != "candidate" ||
		report.FirstFailingStep == nil || report.FirstFailingStep.FailedPointer != "/status" ||
		report.FirstFailingStep.RequestID == "" || report.ScenarioRevision != queued.ScenarioRevision ||
		report.Baseline.SandboxDeploymentID == report.Candidate.SandboxDeploymentID {
		t.Fatalf("comparison did not identify the candidate's failure: %s", response.Body)
	}
	for _, fixtureValue := range []string{"fixture-only", "refunded", "refunding", "test=private"} {
		if strings.Contains(response.Body.String(), fixtureValue) {
			t.Fatal("comparison report exposed scenario input or output")
		}
	}
	for _, leg := range targets {
		app, err := env.store.AppByID(ctx, leg.app.ID)
		if err != nil || app.PreviewPrState != state.PreviewPrStateTearingDown {
			t.Fatalf("completed leg did not enter cleanup: %v", err)
		}
	}
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, path, nil, nil), http.StatusOK, &list)
	if len(list.Comparisons) != 1 || list.Comparisons[0].ID != id || list.Comparisons[0].Verdict != report.Verdict {
		t.Fatalf("comparison history lost its verdict: %+v", list.Comparisons)
	}
}
