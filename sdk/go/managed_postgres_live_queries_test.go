// adr: 1384 — live-query enablement and live secret rotation.
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestManagedPostgresLiveQueriesClient(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/postgres/databases/orders/live/rotate":
			var body faas.RotateManagedPostgresLiveSecretRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OverlapSeconds == nil || *body.OverlapSeconds != 0 {
				t.Error("rotation body", body, err)
			}
		case "DELETE /v1/postgres/databases/orders/live":
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(faas.ManagedPostgresLiveQueries{DatabaseID: "orders", Enabled: true, Secret: "glsk1_x",
			Keys: []faas.ManagedPostgresLiveKey{{KeyID: "0123456789abcdef", Active: true}}})
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	enabled, err := c.EnableManagedPostgresLiveQueries(ctx, "orders")
	if err != nil || enabled.Secret != "glsk1_x" || len(enabled.Keys) != 1 {
		t.Fatal(enabled, err)
	}
	if view, err := c.GetManagedPostgresLiveQueries(ctx, "orders"); err != nil || !view.Enabled {
		t.Fatal(view, err)
	}
	zero := int64(0)
	if _, err := c.RotateManagedPostgresLiveSecret(ctx, "orders", faas.RotateManagedPostgresLiveSecretRequest{OverlapSeconds: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := c.DisableManagedPostgresLiveQueries(ctx, "orders"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /v1/postgres/databases/orders/live", "GET /v1/postgres/databases/orders/live",
		"POST /v1/postgres/databases/orders/live/rotate", "DELETE /v1/postgres/databases/orders/live",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}
