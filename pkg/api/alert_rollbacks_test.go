package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlertRollbackClientUsesKnownFireAndReadOnlyStatus(t *testing.T) {
	const fire = "3e9f323a-ade6-442b-8444-c91da107fe44"
	receipt := AlertRollback{ID: fire, RuleID: "a2b9cc53-907f-4b5c-88a4-fd0c21214556", Status: "blocked", Code: "binding_verification_missing"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/internal/safe-deploy/alert-rollbacks/" + fire:
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer action-token" || r.ContentLength > 0 {
				t.Errorf("wrong durable callback %s %s", r.Method, r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(receipt)
		case "/v1/apps/demo/alert-rollbacks/" + fire:
			if r.Method != http.MethodGet {
				t.Errorf("mutating status %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(receipt)
		case "/v1/apps/demo/alert-rollbacks":
			if r.Method != http.MethodGet {
				t.Errorf("mutating status %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode([]AlertRollback{receipt})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	internal := NewInternalSafeDeployClient(server.URL, "canary-token", "action-token")
	if got, err := internal.ProcessAlertRollback(context.Background(), fire); err != nil || got.ID != fire || got.Status != "blocked" {
		t.Fatalf("durable callback %+v %v", got, err)
	}
	client := NewClient(server.URL, "customer-token")
	if got, err := client.GetAlertRollback(context.Background(), "demo", fire); err != nil || got.ID != fire {
		t.Fatalf("status %+v %v", got, err)
	}
	if got, err := client.ListAlertRollbacks(context.Background(), "demo"); err != nil || len(got) != 1 || calls != 3 {
		t.Fatalf("list %+v %v calls=%d", got, err, calls)
	}
}
