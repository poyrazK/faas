package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
)

func TestAPISandboxOutboundWebhookUsesTheSignedDispatcherAndStableDelivery(t *testing.T) {
	identity, restore := withTestIdentities(t)
	defer restore()
	env, parent, source := sandboxReleaseFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	fixture := json.RawMessage(`{
	  "name":"Export completion",
	  "seed":{},
	  "requests":[{
	    "title":"Start export","method":"POST","path":"/exports","expected_status":202,
	    "body":{"export_id":"export_fixture","private":"private-request"}
	  }],
	  "outbound_webhook":{"retry_once":true,"assertions":[{"pointer":"/export_id","equals":"export_fixture"}]}
	}`)
	saved := env.do(t, http.MethodPut, owner+"/api-sandbox-scenario", fixture, nil)
	var scenario api.APISandboxScenarioResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &scenario); err != nil || saved.Code != http.StatusOK || scenario.Scenario == nil {
		t.Fatalf("save callback scenario: %d (%v)", saved.Code, err)
	}
	id := uuid.NewString()
	runPath := owner + "/api-sandbox-runs/" + id
	input := api.APISandboxRunRequest{ScenarioRevision: scenario.Scenario.Revision, Mode: api.APISandboxRunModePartner}
	if response := env.do(t, http.MethodPut, runPath, input, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("outbound webhook unexpectedly started in partner mode: %d", response.Code)
	}
	input.Mode = api.APISandboxRunModeAutomated
	if response := env.do(t, http.MethodPut, runPath, input, nil); response.Code != http.StatusAccepted {
		t.Fatalf("launch callback scenario: %d %s", response.Code, response.Body)
	}
	app, err := env.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := env.store.CreateAPISandboxDeployment(ctx, env.acct.ID, app.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	env.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/openapi.json" {
			t.Error("the async job was sent through the public sandbox probe")
		}
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, deployment.ID)
		body := io.NopCloser(strings.NewReader(`{"openapi":"3.1.0","info":{"title":"Exports","version":"1"},"paths":{}}`))
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body}, nil
	})}
	for range 2 {
		if err := env.s.processAPISandboxOperation(ctx, dueSandboxOperations{env.store}); err != nil {
			t.Fatal(err)
		}
	}

	dispatchCtx, stopDispatcher := context.WithCancel(ctx)
	dispatcher := webhook.NewDispatcher(env.store, nil, env.s.log).WithTick(20 * time.Millisecond).
		WithBackoffs(map[state.AppWebhookRetryPolicy][]time.Duration{state.AppWebhookRetryAggressive: {25 * time.Millisecond}})
	dispatcher.IdentityLoader = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	dispatcher.HTTPClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		t.Error("sandbox callback escaped to the external transport")
		return nil, errors.New("unexpected external request")
	})}
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- dispatcher.Run(dispatchCtx) }()
	defer func() {
		stopDispatcher()
		if err := <-dispatchDone; err != nil {
			t.Error(err)
		}
	}()

	worker := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker <- env.s.processAPISandboxRun(ctx, env.store)
	}()
	defer func() {
		cancel()
		<-done
	}()

	inv := waitSandboxWebhookTestInvocation(t, ctx, env.store, id)
	if inv.AppID != app.ID || inv.Source != state.InvocationAsyncInvoke || inv.Path != "/exports" || inv.OnSuccessDestinationID == "" {
		t.Fatalf("unexpected async invocation: app %s, source %s, path %s", inv.AppID, inv.Source, inv.Path)
	}
	if _, version, err := state.ResolveInvocationVersion(ctx, env.store, inv); err != nil || version.DeploymentID != deployment.ID {
		t.Fatalf("the job did not resolve to the pinned sandbox: %+v (%v)", version, err)
	}
	hook, err := env.store.AppWebhookByID(ctx, inv.OnSuccessDestinationID)
	if err != nil || hook.TargetURL != api.APISandboxWebhookReceiverPrefix+id {
		t.Fatalf("private callback receiver unavailable: %v", err)
	}
	newTarget := "https://external.example.test/callback"
	if _, err := env.store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{TargetURL: &newTarget}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("sandbox receiver could be redirected: %v", err)
	}
	if _, err := env.store.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatal(err)
	}
	if err := env.store.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"export_id":"export_fixture","private":"private-result"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-worker:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	response := env.do(t, http.MethodGet, runPath, nil, nil)
	var report api.APISandboxRunReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil || report.Status != api.APISandboxRunPassed || len(report.Steps) != 5 {
		t.Fatalf("unexpected webhook run: %d %s (%v)", response.Code, response.Body, err)
	}
	queued, first, second, checked := report.Steps[1], report.Steps[2], report.Steps[3], report.Steps[4]
	if queued.Kind != api.APISandboxStepOutboundQueued || queued.DeliveryID == "" || first.DeliveryID != queued.DeliveryID ||
		second.DeliveryID != queued.DeliveryID || checked.DeliveryID != queued.DeliveryID ||
		first.HTTPStatus != http.StatusServiceUnavailable || first.AttemptNumber != 1 || first.ObservedAt == nil ||
		second.HTTPStatus != http.StatusNoContent || second.AttemptNumber != 2 || second.ObservedAt == nil {
		t.Fatalf("callback timeline lost its delivery evidence: %+v", report.Steps)
	}
	if strings.Contains(response.Body.String(), "private-") || strings.Contains(response.Body.String(), "export_fixture") {
		t.Fatal("report exposed private job or assertion data")
	}
	if _, err := env.store.AppWebhookByID(ctx, hook.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("temporary receiver survived the run: %v", err)
	}
}
