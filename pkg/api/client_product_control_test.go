package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductControlClient(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tenant" {
			t.Error("lost tenant credential")
		}
		switch calls {
		case 1:
			if r.Method != "GET" || r.URL.Path != "/v1/platform-tenant-self/product" || r.URL.Query().Get("app") != "my app" || r.URL.Query().Get("period") != "2026-10" {
				t.Errorf("portal request: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"customer":{"tenant_id":"alice"},"usage":{"period":"2026-10"},"operations":[]}`))
		case 2:
			if r.Method != "POST" || r.URL.Path != "/v1/platform-tenant-self/product/start" || r.Header.Get("Idempotency-Key") != "stable" {
				t.Errorf("start request: %s %s", r.Method, r.URL)
			}
			var req ProductStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Feature != "export" || req.DefinitionID != "definition" {
				t.Errorf("start payload: %+v %v", req, err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"operation"}`))
		case 3:
			if r.Method != "GET" || r.URL.Path != "/v1/apps/my app/product/operations/operation/explain" || r.URL.Query().Get("after") != "2" {
				t.Errorf("explanation request: %s %s", r.Method, r.URL)
			}
			_, _ = w.Write([]byte(`{"entitlement_decision":"allowed_at_admission","charge":{"state":"settled","amount_millicents":25000}}`))
		default:
			t.Error("unexpected request")
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "tenant")
	if _, err := client.StartProductFeature(t.Context(), ProductStartRequest{}, ""); err == nil || calls != 0 {
		t.Fatal("empty key sent work")
	}
	portal, err := client.GetProductPortal(t.Context(), "my app", "2026-10")
	if err != nil || portal.Customer.TenantID != "alice" {
		t.Fatalf("portal: %+v %v", portal, err)
	}
	accepted, err := client.StartProductFeature(t.Context(), ProductStartRequest{Feature: "export", DefinitionID: "definition", Input: json.RawMessage(`{}`)}, "stable")
	if err != nil || accepted.ID != "operation" {
		t.Fatalf("acceptance: %+v %v", accepted, err)
	}
	explanation, err := client.ExplainProductOperation(t.Context(), "my app", "operation", 2)
	if err != nil || explanation.Charge.State != "settled" || explanation.Charge.AmountMillicents != 25000 {
		t.Fatalf("explanation: %+v %v", explanation, err)
	}
}
