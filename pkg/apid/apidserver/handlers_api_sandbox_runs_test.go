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

func TestAPISandboxRunLaunchCheckpointAndShare(t *testing.T) {
	e, parent, source := sandboxReleaseFixture(t)
	ctx := t.Context()
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	// Exercise the wire shape accepted by the scenario editor.
	definition := json.RawMessage(`{"name":"Refund fixture","seed":{"seed":"hidden-fixture"},"requests":[
		{"title":"Create refund","method":"POST","path":"/refunds","body":{"order":42,"note":"hidden-request"},"expected_status":201},
		{"title":"Read order","method":"GET","path":"/orders/42","expected_status":200,
		 "assertions":[{"pointer":"/status","equals":"refunded"}]}
	]}`)
	saved := e.do(t, http.MethodPut, owner+"/api-sandbox-scenario", definition, nil)
	var scenario api.APISandboxScenarioResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &scenario); err != nil || saved.Code != http.StatusOK || scenario.Scenario == nil {
		t.Fatalf("save scenario: %d %s (%v)", saved.Code, saved.Body, err)
	}
	id := uuid.NewString()
	runPath := owner + "/api-sandbox-runs/" + id
	launch := api.APISandboxRunRequest{ScenarioRevision: scenario.Scenario.Revision}
	response := e.do(t, http.MethodPut, runPath, launch, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("launch run: %d %s", response.Code, response.Body)
	}
	if response := e.do(t, http.MethodPost, runPath+"/report-link", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("shared unfinished report: %d", response.Code)
	}
	if response := e.do(t, http.MethodPost, owner+"/api-sandboxes/"+id+"/invites", nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("runner copy could expose an API key: %d", response.Code)
	}
	app, err := e.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil || !app.Manifest.APISandboxRun {
		t.Fatalf("private runner copy was not reserved: %v", err)
	}
	deployment, err := e.store.CreateAPISandboxDeployment(ctx, e.acct.ID, app.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	var requests []string
	e.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		header := make(http.Header)
		header.Set(api.RevisionHeader, deployment.ID)
		header.Set(api.RequestIDHeader, r.Header.Get(api.RequestIDHeader))
		status, body := 200, `{"status":"refunded","anything":"hidden-response"}`
		switch r.URL.Path {
		case "/openapi.json":
			body = `{"openapi":"3.1.0","info":{"title":"Notes","version":"1"},"paths":{}}`
		case "/refunds":
			status = 201
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for range 2 {
		if err := e.s.processAPISandboxOperation(ctx, dueSandboxOperations{e.store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.s.processAPISandboxRun(ctx, e.store); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(requests, ","); got != "GET /openapi.json,POST /refunds,GET /orders/42" {
		t.Fatalf("runner requests = %q", got)
	}
	ended, err := e.store.AppByID(ctx, app.ID)
	if err != nil || ended.PreviewPrState != state.PreviewPrStateTearingDown {
		t.Fatalf("run copy wasn't closed for cleanup: %v", err)
	}

	finished := e.do(t, http.MethodPut, runPath, launch, nil)
	var run api.APISandboxRunReport
	if err := json.Unmarshal(finished.Body.Bytes(), &run); err != nil || finished.Code != http.StatusOK ||
		run.Status != api.APISandboxRunPassed || len(run.Steps) != 2 || run.Steps[1].RequestID == "" {
		t.Fatalf("finished run could not be recovered: %d %s (%v)", finished.Code, finished.Body, err)
	}
	shareResponse := e.do(t, http.MethodPost, runPath+"/report-link", nil, nil)
	var share api.APISandboxRunShareResponse
	if err := json.Unmarshal(shareResponse.Body.Bytes(), &share); err != nil || shareResponse.Code != http.StatusCreated ||
		!strings.HasPrefix(share.Token, api.APISandboxRunSharePrefix) {
		t.Fatalf("issue report link: %d (%v)", shareResponse.Code, err)
	}
	public := "/v1/api-sandbox-runs/" + id + "/report"
	if denied := e.do(t, http.MethodGet, public, nil, map[string]string{"Authorization": "Bearer invalid"}); denied.Code != http.StatusUnauthorized {
		t.Fatalf("read without report token: %d", denied.Code)
	}
	shared := e.do(t, http.MethodGet, public, nil, map[string]string{"Authorization": "Bearer " + share.Token})
	if shared.Code != http.StatusOK || shared.Body.String() != finished.Body.String() || strings.Contains(shared.Body.String(), "hidden-") {
		t.Fatalf("unexpected shared report: %d", shared.Code)
	}
}

func TestAPISandboxPartnerRunVerifiesStateWithoutReplayingPartnerRequests(t *testing.T) {
	e, parent, source := sandboxReleaseFixture(t)
	ctx := t.Context()
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	saved := e.do(t, http.MethodPut, owner+"/api-sandbox-scenario", json.RawMessage(`{
		"name":"Refund an order",
		"seed":{"data":"hidden-fixture"},
		"requests":[
			{"title":"Create refund","method":"POST","path":"/refunds?fixture=hidden-query","body":{"order":42},"expected_status":201},
			{"title":"Verify order","method":"GET","path":"/orders/42","expected_status":200,
			 "assertions":[{"pointer":"/status","equals":"hidden-result"}]}
		]
	}`), nil)
	var scenario api.APISandboxScenarioResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &scenario); err != nil || saved.Code != http.StatusOK || scenario.Scenario == nil {
		t.Fatalf("save test scenario: %d %s (%v)", saved.Code, saved.Body, err)
	}
	id := uuid.NewString()
	runPath := owner + "/api-sandbox-runs/" + id
	launch := api.APISandboxRunRequest{ScenarioRevision: scenario.Scenario.Revision, Mode: api.APISandboxRunModePartner}
	if response := e.do(t, http.MethodPut, runPath, launch, nil); response.Code != http.StatusAccepted {
		t.Fatalf("start partner run: %d %s", response.Code, response.Body)
	}
	if response := e.do(t, http.MethodPut, runPath, api.APISandboxRunRequest{ScenarioRevision: launch.ScenarioRevision}, nil); response.Code != http.StatusConflict {
		t.Fatalf("retry with another mode returned %d", response.Code)
	}
	invited := e.do(t, http.MethodPost, runPath+"/invite", nil, nil)
	var invite api.APISandboxPartnerInviteResponse
	if err := json.Unmarshal(invited.Body.Bytes(), &invite); err != nil || invited.Code != http.StatusCreated || invite.Token == "" {
		t.Fatalf("create invite: %d (%v)", invited.Code, err)
	}
	redeemPath := "/v1/api-sandboxes/" + id + "/redeem"
	inviteHeaders := map[string]string{"Authorization": "Bearer " + invite.Token}
	if response := e.do(t, http.MethodPost, redeemPath, nil, inviteHeaders); response.Code != http.StatusConflict {
		t.Fatalf("unready invitation redeemed: %d", response.Code)
	}
	app, err := e.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil || !app.Manifest.APISandboxRun || !app.Manifest.APISandboxPartner {
		t.Fatalf("reserve isolated partner copy: %v", err)
	}
	deployment, err := e.store.CreateAPISandboxDeployment(ctx, e.acct.ID, app.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	var requests []string
	e.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		header := make(http.Header)
		header.Set(api.RevisionHeader, deployment.ID)
		header.Set(api.RequestIDHeader, r.Header.Get(api.RequestIDHeader))
		body := `{"status":"hidden-result","other":"hidden-response"}`
		if r.URL.Path == "/openapi.json" {
			body = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for range 2 {
		if err := e.s.processAPISandboxOperation(ctx, dueSandboxOperations{e.store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.s.processAPISandboxRun(ctx, e.store); err != nil {
		t.Fatal(err)
	}
	if err := e.s.processAPISandboxRun(ctx, e.store); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("worker advanced past the partner: %v", err)
	}
	redeemed := e.do(t, http.MethodPost, redeemPath, nil, inviteHeaders)
	var access api.APISandboxInviteResponse
	if err := json.Unmarshal(redeemed.Body.Bytes(), &access); err != nil || redeemed.Code != http.StatusOK || access.Token == "" ||
		access.Sandbox.PartnerStepCount != 1 || access.Sandbox.DeploymentID != deployment.ID {
		t.Fatalf("redeem run key: %d (%v)", redeemed.Code, err)
	}
	if reused := e.do(t, http.MethodPost, redeemPath, nil, inviteHeaders); reused.Code != http.StatusUnauthorized {
		t.Fatalf("redeemed invitation was reused: %d", reused.Code)
	}
	partner, err := e.store.GetAPISandboxPartner(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	step, err := e.store.ClaimAPISandboxPartnerStep(ctx, app.ID, deployment.ID, partner.ConsumerKeyID,
		http.MethodPost, "/refunds?fixture=hidden-query", now)
	if err != nil {
		t.Fatal(err)
	}
	step.Step.HTTPStatus = http.StatusCreated
	if err := e.store.CompleteAPISandboxPartnerStep(ctx, step, now); err != nil {
		t.Fatal(err)
	}
	if err := e.s.processAPISandboxRun(ctx, e.store); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(requests, ","); got != "GET /openapi.json,GET /orders/42" {
		t.Fatalf("unexpected worker requests: %s", got)
	}
	report := e.do(t, http.MethodGet, runPath, nil, nil)
	var run api.APISandboxRunReport
	if err := json.Unmarshal(report.Body.Bytes(), &run); err != nil || report.Code != http.StatusOK ||
		run.Status != api.APISandboxRunPassed || len(run.Steps) != 2 || !run.Steps[0].PartnerRequest ||
		run.Steps[1].PartnerRequest || run.Steps[1].Index != 2 || run.Steps[0].Path != "/refunds" {
		t.Fatalf("unexpected partner report: %d %s (%v)", report.Code, report.Body, err)
	}
	if strings.Contains(report.Body.String(), "hidden-") || strings.Contains(report.Body.String(), access.Token) {
		t.Fatal("partner report included private scenario data or credentials")
	}
}
