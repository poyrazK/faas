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

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxStripeWebhookRunWaitsForDuplicateAndState(t *testing.T) {
	_, restore := withTestIdentities(t)
	defer restore()
	env, parent, source := sandboxReleaseFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	fixture := json.RawMessage(`{
	  "name":"Apply an invoice",
	  "seed":{"private":"private-seed"},
	  "stripe_webhook":{
	    "delivery_path":"/internal/stripe",
	    "event_type":"invoice.paid",
	    "data_object":{"id":"in_demo","object":"invoice","private":"private-payload"}
	  },
	  "requests":[{
	    "title":"Check entitlement","method":"GET","path":"/subscriptions/42?fixture=private-query",
	    "expected_status":200,
	    "assertions":[{"pointer":"/status","equals":"private-assertion"},{"pointer":"/entitlements","equals":1}]
	  }]
	}`)
	saved := env.do(t, http.MethodPut, owner+"/api-sandbox-scenario", fixture, nil)
	var scenario api.APISandboxScenarioResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &scenario); err != nil || saved.Code != http.StatusOK || scenario.Scenario == nil {
		t.Fatalf("save webhook scenario: %d %s (%v)", saved.Code, saved.Body, err)
	}
	id := uuid.NewString()
	runPath := owner + "/api-sandbox-runs/" + id
	input := api.APISandboxRunRequest{ScenarioRevision: scenario.Scenario.Revision, Mode: api.APISandboxRunModePartner}
	if rejected := env.do(t, http.MethodPut, runPath, input, nil); rejected.Code != http.StatusBadRequest {
		t.Fatalf("webhook scenario could start as a partner run: %d", rejected.Code)
	}
	input.Mode = api.APISandboxRunModeAutomated
	if launched := env.do(t, http.MethodPut, runPath, input, nil); launched.Code != http.StatusAccepted {
		t.Fatalf("launch webhook scenario: %d %s", launched.Code, launched.Body)
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
	receiptID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+id+"\x00evt_sandbox_"+strings.ReplaceAll(id, "-", ""))).String()
	checks := 0
	env.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set(api.RevisionHeader, deployment.ID)
		header.Set(api.RequestIDHeader, r.Header.Get(api.RequestIDHeader))
		body := `{"openapi":"3.1.0","info":{"title":"Billing fixture","version":"1"},"paths":{}}`
		if r.URL.Path != "/openapi.json" {
			inv, err := env.store.InvocationByID(r.Context(), receiptID)
			if err != nil || inv.State != state.InvocationCompleted || r.Method != http.MethodGet {
				t.Error("checked final state before the webhook completed")
			}
			checks++
			body = `{"status":"pending","entitlements":0}`
			if checks > 1 {
				body = `{"status":"private-assertion","entitlements":1,"other":"private-response"}`
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for range 2 {
		if err := env.s.processAPISandboxOperation(ctx, dueSandboxOperations{env.store}); err != nil {
			t.Fatal(err)
		}
	}

	worker := make(chan error, 1)
	go func() { worker <- env.s.processAPISandboxRun(ctx, env.store) }()
	inv := waitSandboxWebhookTestInvocation(t, ctx, env.store, receiptID)
	if inv.AppID != app.ID || inv.AccountID != env.acct.ID || inv.Source != state.InvocationInboundWebhook ||
		inv.Path != "/internal/stripe" || !strings.Contains(string(inv.Payload), `"livemode":false`) ||
		!strings.Contains(string(inv.Payload), `"private-payload"`) || strings.Contains(strings.ToLower(string(inv.Headers)), "stripe-signature") {
		t.Fatal("queued fixture did not match the isolated delivery contract")
	}
	if _, version, err := state.ResolveInvocationVersion(ctx, env.store, inv); err != nil || version.DeploymentID != deployment.ID {
		t.Fatalf("webhook dispatch did not target the pinned sandbox: %+v (%v)", version, err)
	}
	if _, err := env.store.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatal(err)
	}
	if err := env.store.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"private":"private-invocation-result"}`)); err != nil {
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
	if checks != 2 {
		t.Fatalf("final state check was sent %d times", checks)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, env.store, inv); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the finished sandbox still accepted queued work: %v", err)
	}
	reportResponse := env.do(t, http.MethodGet, runPath, nil, nil)
	var report api.APISandboxRunReport
	if err := json.Unmarshal(reportResponse.Body.Bytes(), &report); err != nil || report.Status != api.APISandboxRunPassed || len(report.Steps) != 4 {
		t.Fatalf("unexpected webhook report: %d %s (%v)", reportResponse.Code, reportResponse.Body, err)
	}
	if report.Steps[0].Kind != api.APISandboxStepStripeWebhook || report.Steps[0].InvocationID != receiptID ||
		report.Steps[1].Kind != api.APISandboxStepWebhookReplay || report.Steps[1].InvocationID != receiptID ||
		report.Steps[2].Kind != api.APISandboxStepWebhookProcess || report.Steps[2].InvocationState != "completed" ||
		report.Steps[2].DeliveryAttempts != 1 || report.Steps[3].Path != "/subscriptions/42" {
		t.Fatalf("invalid webhook report evidence: %+v", report.Steps)
	}
	if strings.Contains(reportResponse.Body.String(), "private-") {
		t.Fatal("webhook report included private fixture or assertion evidence")
	}
}

func waitSandboxWebhookTestInvocation(t *testing.T, ctx context.Context, store state.Store, id string) state.Invocation {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		inv, err := store.InvocationByID(ctx, id)
		if err == nil {
			return inv
		}
		if !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("no durable webhook invocation appeared")
		case <-ticker.C:
		}
	}
}
