package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientAutomationRevisionRoutes(t *testing.T) {
	var seen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/billing/automations/paid-invoice/revisions":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "20" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"revisions":[{"version":42,"definition":{"name":"paid-invoice","steps":[]},"definition_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recorded_at":"2026-10-04T12:00:00Z","legacy_snapshot":false,"published_by_account_id":"00000000-0000-0000-0000-000000000001"}],"total":1,"limit":10,"offset":20}`))
		case "GET /v1/apps/billing/automations/paid-invoice/revisions/42":
			_, _ = w.Write([]byte(`{"version":42,"definition":{"name":"paid-invoice","steps":[]},"definition_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recorded_at":"2026-10-04T12:00:00Z","legacy_snapshot":false,"published_by_account_id":"00000000-0000-0000-0000-000000000001"}`))
		case "POST /v1/apps/billing/automations/paid-invoice/revisions/42/restore":
			var body RestoreAutomationRevisionRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedVersion != 47 {
				t.Errorf("restore request: %+v %v", body, err)
			}
			_, _ = w.Write([]byte(`{"name":"paid-invoice","version":48,"source":"dashboard","draft":{"name":"paid-invoice","steps":[]},"enabled":true}`))
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	ctx := context.Background()
	page, err := client.ListAutomationRevisions(ctx, "billing", "paid-invoice", 10, 20)
	if err != nil || page.Total != 1 || len(page.Revisions) != 1 || page.Revisions[0].Version != 42 {
		t.Fatal(page, err)
	}
	revision, err := client.GetAutomationRevision(ctx, "billing", "paid-invoice", 42)
	if err != nil || revision.Version != 42 {
		t.Fatal(revision, err)
	}
	restored, err := client.RestoreAutomationRevision(ctx, "billing", "paid-invoice", 42, RestoreAutomationRevisionRequest{ExpectedVersion: 47})
	if err != nil || restored.Version != 48 || restored.Draft.Name != "paid-invoice" {
		t.Fatal(restored, err)
	}
	if seen != 3 {
		t.Fatalf("requests=%d", seen)
	}
}
