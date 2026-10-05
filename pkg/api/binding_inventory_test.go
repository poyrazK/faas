package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAppBindingInventoryPreservesPartialResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/my-app/bindings" || r.URL.Query().Get("scope") != "staging" {
			t.Errorf("request = %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AppBindingInventory{App: "my-app", Scope: "staging", Complete: false,
			Bindings: []AppBindingInventoryItem{{Type: BindingTypeService, Name: "billing"}},
			Issues:   []BindingInventoryIssue{{Type: BindingTypePostgres, Code: "query_failed", Severity: "error"}},
		})
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, "test").GetAppBindingInventory(context.Background(), "my-app", "staging")
	if err != nil || got.App != "my-app" || got.Complete || !got.HasErrors() || len(got.Bindings) != 1 {
		t.Fatalf("inventory=%+v err=%v", got, err)
	}
}
