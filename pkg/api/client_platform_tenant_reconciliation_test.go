package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlanPlatformTenantReconciliationClient(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/platform-tenants/"+tenantID+"/reconciliation-plan" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var req PlanPlatformTenantReconciliationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(req.Consumers) != 1 || req.Consumers[0].ExternalRef != "customer-42" {
			t.Errorf("request body = %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"` + tenantID + `","changes":[{"resource_type":"consumer","action":"keep"}]}`))
	}))
	defer srv.Close()

	plan, err := NewClient(srv.URL, "test-token").PlanPlatformTenantReconciliation(context.Background(), tenantID,
		PlanPlatformTenantReconciliationRequest{Consumers: []ApplyPlatformTenantConsumerRequest{{
			AppID: "22222222-2222-4222-8222-222222222222", ExternalRef: "customer-42", Name: "Customer 42",
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TenantID != tenantID || len(plan.Changes) != 1 || plan.Changes[0].Action != "keep" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestApplyPlatformTenantReconciliationClient(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/platform-tenants/"+tenantID+"/reconciliation-plan/apply" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Error("apply is missing its idempotency key")
		}
		var req ApplyPlatformTenantReconciliationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.ExpectedPlanHash != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
			t.Errorf("expected plan hash = %q", req.ExpectedPlanHash)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"` + tenantID + `","plan_hash":"` + req.ExpectedPlanHash + `","applied":true,"changes":[{"resource_type":"consumer","action":"detached"}]}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL, "test-token").ApplyPlatformTenantReconciliation(context.Background(), tenantID,
		ApplyPlatformTenantReconciliationRequest{ExpectedPlanHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TenantID != tenantID || !result.Applied || len(result.Changes) != 1 || result.Changes[0].Action != "detached" {
		t.Fatalf("apply = %+v", result)
	}
}
