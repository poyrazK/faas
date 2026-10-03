package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCreatePlatformTenantWebhookEventFilter(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID,
		"webhook-customer-"+uuid.NewString()[:8], "Webhook customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/platform-tenants/" + tenant.ID + "/webhooks"

	defaultRequest := api.CreatePlatformTenantWebhookRequest{
		TargetURL: "https://example.com/billing", WebhookSecret: "test-secret",
	}
	defaultResponse := e.do(t, http.MethodPost, path, defaultRequest, nil)
	if defaultResponse.Code != http.StatusCreated {
		t.Fatalf("default create: %d %s", defaultResponse.Code, defaultResponse.Body)
	}
	var defaultHook api.PlatformTenantWebhookResponse
	if err := json.Unmarshal(defaultResponse.Body.Bytes(), &defaultHook); err != nil {
		t.Fatal(err)
	}
	if len(defaultHook.EventFilter) != 1 || defaultHook.EventFilter[0] != "platform_tenant.statement.finalized" {
		t.Fatalf("default event_filter = %v", defaultHook.EventFilter)
	}

	hostnameRequest := api.CreatePlatformTenantWebhookRequest{
		TargetURL: "https://example.com/hostnames", WebhookSecret: "test-secret",
		EventFilter: []string{"platform_tenant.hostname.verified"},
	}
	hostnameResponse := e.do(t, http.MethodPost, path, hostnameRequest, nil)
	if hostnameResponse.Code != http.StatusCreated {
		t.Fatalf("hostname create: %d %s", hostnameResponse.Code, hostnameResponse.Body)
	}
	var hostnameHook api.PlatformTenantWebhookResponse
	if err := json.Unmarshal(hostnameResponse.Body.Bytes(), &hostnameHook); err != nil {
		t.Fatal(err)
	}
	if len(hostnameHook.EventFilter) != 1 || hostnameHook.EventFilter[0] != "platform_tenant.hostname.verified" {
		t.Fatalf("hostname event_filter = %v", hostnameHook.EventFilter)
	}
	reconciliationRequest := api.CreatePlatformTenantWebhookRequest{
		TargetURL: "https://example.com/reconciliations", WebhookSecret: "test-secret",
		EventFilter: []string{"platform_tenant.reconciliation.applied"},
	}
	reconciliationResponse := e.do(t, http.MethodPost, path, reconciliationRequest, nil)
	if reconciliationResponse.Code != http.StatusCreated {
		t.Fatalf("reconciliation create: %d %s", reconciliationResponse.Code, reconciliationResponse.Body)
	}
	var reconciliationHook api.PlatformTenantWebhookResponse
	if err := json.Unmarshal(reconciliationResponse.Body.Bytes(), &reconciliationHook); err != nil {
		t.Fatal(err)
	}
	if len(reconciliationHook.EventFilter) != 1 || reconciliationHook.EventFilter[0] != "platform_tenant.reconciliation.applied" {
		t.Fatalf("reconciliation event_filter = %v", reconciliationHook.EventFilter)
	}

	invalidRequest := api.CreatePlatformTenantWebhookRequest{
		TargetURL: "https://example.com/invalid", WebhookSecret: "test-secret",
		EventFilter: []string{"platform_tenant.hostname.verified", "platform_tenant.hostname.verified"},
	}
	if got := e.do(t, http.MethodPost, path, invalidRequest, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("duplicate event filter status = %d, want 400: %s", got.Code, got.Body)
	}
	emptyRequest := json.RawMessage(`{"target_url":"https://example.com/empty","webhook_secret":"test-secret","event_filter":[]}`)
	if got := e.do(t, http.MethodPost, path, emptyRequest, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("empty event filter status = %d, want 400: %s", got.Code, got.Body)
	}
}
