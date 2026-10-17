package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectionBudgetClientRequiresExplicitCounters(t *testing.T) {
	for _, kind := range []string{"app", "database"} {
		for _, missing := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-complete", true: "-missing"}[missing], func(t *testing.T) {
				value := ManagedPostgresConnectionBudget{Version: 1, DatabaseID: "11111111-1111-4111-8111-111111111111", DatabaseName: "primary", State: "unknown", Model: "canonical_runtime_pools", Capacity: ConnectionBudgetCapacity{State: "unknown"}, Reasons: []string{"capacity_unverified"}}
				bytes, _ := json.Marshal(value)
				var body map[string]any
				if err := json.Unmarshal(bytes, &body); err != nil {
					t.Fatal(err)
				}
				if missing {
					delete(body["inventory"].(map[string]any), "resident_instances")
				}
				var response any = body
				path := "/v1/postgres/databases/id/connection-budget"
				if kind == "app" {
					response = map[string]any{"version": 1, "app": "notes", "state": "unknown", "truncated": false, "databases": []any{body}}
					path = "/v1/apps/notes/data-api/connection-budget"
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error(r.Method, r.URL.Path)
					}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Error(err)
					}
				}))
				defer srv.Close()
				client := NewClient(srv.URL, "fixture")
				var err error
				if kind == "app" {
					_, err = client.GetAppDataAPIConnectionBudgets(context.Background(), "notes")
				} else {
					_, err = client.GetManagedPostgresConnectionBudget(context.Background(), "id")
				}
				if (err != nil) != missing {
					t.Fatal(err, missing)
				}
			})
		}
	}
}
