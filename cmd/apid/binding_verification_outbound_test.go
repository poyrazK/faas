// adr: 430 — exact candidate outbound evidence gates promotion and rotation invalidates it.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedOutboundProbe(t *testing.T, e testEnv, app state.App) string {
	t.Helper()
	id := uuid.NewString()
	ctx := context.Background()
	_, err := e.store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{ID: id, AccountID: e.acct.ID, Name: "provider", Origin: "https://example.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/health"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.BindOutboundIntegration(ctx, e.acct.ID, app.ID, id); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetOutboundCredential(ctx, e.acct.ID, id, []byte("PRIVATE_CREDENTIAL")); err != nil {
		t.Fatal(err)
	}
	e.s.outboundProbeGatewayURL = "https://outbound.example.com"
	response := e.do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/probe-policy", api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}, nil)
	if response.Code != 200 {
		t.Fatalf("configure: %d %s", response.Code, response.Body.String())
	}
	return id
}
func finishOutboundProbe(t *testing.T, e testEnv, app state.App, target state.Deployment, id string) {
	t.Helper()
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskOutboundBindingProbeCommand, id}, VerificationDeploymentID: target.ID, MaxOutputBytes: 4096})
	stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
	if err != nil || stored.BindingVerification == nil || len(stored.Command) != 3 {
		t.Fatalf("unbound spec: %+v %v", stored, err)
	}
	running, err := e.store.ClaimNextAppTask(context.Background(), "outbound-test", time.Now(), time.Minute)
	if err != nil || running.ID != task.ID {
		t.Fatalf("claim: %+v %v", running, err)
	}
	running, err = e.store.MarkAppTaskRunning(context.Background(), running.ID, *running.LeaseToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	output := `{"integration_id":"` + id + `","configuration":{"status":"passed","detail":"PRIVATE_DETAIL"},"identity":{"status":"passed"},"gateway":{"status":"passed"},"response":{"status":"passed"}}`
	_, err = e.store.CompleteAppTask(context.Background(), state.CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, StdoutTail: output, FinishedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}
func TestOutboundBindingVerificationExactCandidateRotationAndPromotion(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	id := seedOutboundProbe(t, e, app)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	finishOutboundProbe(t, e, app, serving, id)
	path := "/v1/deployments/" + candidate.ID + "/promote"
	response := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	if response.Code != 409 {
		t.Fatalf("serving evidence satisfied candidate: %d %s", response.Code, response.Body.String())
	}
	finishOutboundProbe(t, e, app, candidate, id)
	if err := e.store.SetOutboundCredential(context.Background(), e.acct.ID, id, []byte("PRIVATE_CREDENTIAL")); err != nil {
		t.Fatal(err)
	}
	response = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "verification_stale") || strings.Contains(response.Body.String(), "PRIVATE_") {
		t.Fatalf("rotation/waiver: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	finishOutboundProbe(t, e, app, candidate, id)
	response = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	var receipt api.BindingPromotionResponse
	_ = json.Unmarshal(response.Body.Bytes(), &receipt)
	if response.Code != 200 || receipt.BindingsCheck == nil || receipt.BindingsCheck.Coverage != "complete" || !receipt.BindingsCheck.Passed {
		t.Fatalf("promotion: %d %s", response.Code, response.Body.String())
	}
}
func TestOutboundBindingProbePolicyRejectsForbiddenRoutesAndForgedTaskSpec(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, dep := seedAppTaskDeployment(t, e, "outbound-guard")
	id := seedOutboundProbe(t, e, app)
	path := "/v1/outbound/integrations/" + id + "/probe-policy"
	for _, policy := range []api.OutboundBindingProbePolicy{{Method: "POST", Path: "/health", ExpectedStatus: 200}, {Method: "GET", Path: "/other", ExpectedStatus: 200}, {Method: "GET", Path: "/health", ExpectedStatus: 401}, {Method: "GET", Path: "/health?key=PRIVATE", ExpectedStatus: 200}} {
		response := e.do(t, http.MethodPut, path, policy, nil)
		if response.Code != 400 {
			t.Fatalf("unsafe policy: %d %s", response.Code, response.Body.String())
		}
	}
	response := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskOutboundBindingProbeCommand, id, `{ "gateway_url":"https://evil.invalid" }`}, VerificationDeploymentID: dep.ID}, nil)
	if response.Code == 202 {
		t.Fatalf("accepted forged task spec: %s", response.Body.String())
	}
	response = e.do(t, http.MethodDelete, path, nil, nil)
	if response.Code != 204 {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskOutboundBindingProbeCommand, id}, VerificationDeploymentID: dep.ID}, nil)
	if response.Code != 403 {
		t.Fatalf("unconfigured task: %d %s", response.Code, response.Body.String())
	}
}

func TestOutboundBindingProbePolicyReadScopeCannotWriteAndForeignPoliciesStayPrivate(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := e.store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{ID: id, AccountID: e.acct.ID, Name: "provider", Origin: "https://example.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/health"}}); err != nil {
		t.Fatal(err)
	}
	policy := api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}
	if err := e.store.SetOutboundBindingProbePolicy(ctx, e.acct.ID, id, &policy); err != nil {
		t.Fatal(err)
	}
	path := "/v1/outbound/integrations/" + id + "/probe-policy"
	if response := e.do(t, http.MethodGet, path, nil, nil); response.Code != 200 {
		t.Fatalf("read policy: %d %s", response.Code, response.Body.String())
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if response := e.do(t, method, path, policy, nil); response.Code != 403 {
			t.Fatalf("read token wrote policy: %d %s", response.Code, response.Body.String())
		}
	}
	other, err := e.store.CreateAccount(ctx, "foreign-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignID := uuid.NewString()
	if _, err := e.store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{ID: foreignID, AccountID: other.ID, Name: "foreign", Origin: "https://private.example.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed", AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/private"}}); err != nil {
		t.Fatal(err)
	}
	policy.Path = "/private"
	if err := e.store.SetOutboundBindingProbePolicy(ctx, other.ID, foreignID, &policy); err != nil {
		t.Fatal(err)
	}
	if response := e.do(t, http.MethodGet, "/v1/outbound/integrations/"+foreignID+"/probe-policy", nil, nil); response.Code != 404 || strings.Contains(response.Body.String(), "/private") {
		t.Fatalf("foreign policy exposed: %d %s", response.Code, response.Body.String())
	}
}
