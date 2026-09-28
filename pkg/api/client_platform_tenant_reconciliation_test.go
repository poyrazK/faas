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
		_, _ = w.Write([]byte(`{"tenant_id":"` + tenantID + `","receipt_id":"33333333-3333-4333-8333-333333333333","plan_hash":"` + req.ExpectedPlanHash + `","applied_at":"2026-09-28T12:00:00Z","applied":true,"changes":[{"resource_type":"consumer","action":"detached"}]}`))
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL, "test-token").ApplyPlatformTenantReconciliation(context.Background(), tenantID,
		ApplyPlatformTenantReconciliationRequest{ExpectedPlanHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TenantID != tenantID || result.ReceiptID != "33333333-3333-4333-8333-333333333333" || !result.Applied || len(result.Changes) != 1 || result.Changes[0].Action != "detached" {
		t.Fatalf("apply = %+v", result)
	}
}

func TestPlatformTenantReconciliationReceiptClient(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	receiptID := "33333333-3333-4333-8333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/account/platform-tenants/" + tenantID + "/reconciliations":
			if r.Method != http.MethodGet || r.URL.Query().Get("page_size") != "2" || r.URL.Query().Get("page_token") != "opaque-cursor" {
				t.Errorf("list request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"receipts":[{"receipt_id":"` + receiptID + `","plan_hash":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","applied_at":"2026-09-28T12:00:00Z","change_count":1}],"next_page_token":"next-cursor"}`))
		case "/v1/account/platform-tenants/" + tenantID + "/reconciliations/" + receiptID:
			if r.Method != http.MethodGet {
				t.Errorf("get request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"tenant_id":"` + tenantID + `","receipt_id":"` + receiptID + `","plan_hash":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","applied_at":"2026-09-28T12:00:00Z","changes":[{"resource_type":"consumer","action":"created"}]}`))
		default:
			t.Errorf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-token")
	list, err := client.ListPlatformTenantReconciliationReceipts(context.Background(), tenantID, ListPlatformTenantReconciliationReceiptsOptions{PageSize: 2, PageToken: "opaque-cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Receipts) != 1 || list.Receipts[0].ReceiptID != receiptID || list.NextPageToken != "next-cursor" {
		t.Fatalf("receipt list = %+v", list)
	}
	receipt, err := client.GetPlatformTenantReconciliationReceipt(context.Background(), tenantID, receiptID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TenantID != tenantID || receipt.ReceiptID != receiptID || len(receipt.Changes) != 1 || receipt.Changes[0].Action != "created" {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestPlanPlatformTenantOffboardingClient(t *testing.T) {
	tenantID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/account/platform-tenants/"+tenantID+"/offboarding-plan" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var req struct{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"` + tenantID + `","status":"active","plan_hash":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","actions":{"suspend_tenant":true,"revoke_consumer_keys":2,"revoke_access_tokens":1,"preserve_usage_history":true,"preserve_billing_statements":true,"preserve_reconciliation_history":true,"preserve_webhook_subscriptions":true}}`))
	}))
	defer srv.Close()

	plan, err := NewClient(srv.URL, "test-token").PlanPlatformTenantOffboarding(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TenantID != tenantID || plan.Status != "active" || len(plan.PlanHash) != 64 ||
		!plan.Actions.SuspendTenant || plan.Actions.RevokeConsumerKeys != 2 || plan.Actions.RevokeAccessTokens != 1 ||
		!plan.Actions.PreserveUsageHistory || !plan.Actions.PreserveBillingStatements ||
		!plan.Actions.PreserveReconciliationHistory || !plan.Actions.PreserveWebhookSubscriptions {
		t.Fatalf("offboarding plan = %+v", plan)
	}
}
