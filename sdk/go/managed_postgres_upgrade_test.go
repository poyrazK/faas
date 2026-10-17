// adr: 1027 — in-place major upgrades travel by durable request UUID.
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestManagedPostgresMajorUpgradeClient(t *testing.T) {
	const id = "44444444-4444-4444-8444-444444444444"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/orders/upgrade":
			var body faas.UpgradeManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != id || body.PostgresMajor != 17 {
				t.Error("request", body, err)
			}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/orders/upgrades/"+id:
		default:
			t.Error("unexpected", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(faas.ManagedPostgresMajorUpgrade{ID: id, DatabaseID: "orders", FromPostgresMajor: 14, TargetPostgresMajor: 17,
			State: "failed", LastErrorCode: "upgrade_refused", Generation: 2})
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if view, err := c.UpgradeManagedPostgresDatabase(context.Background(), "orders", faas.UpgradeManagedPostgresDatabaseRequest{RequestID: id, PostgresMajor: 17}); err != nil || view.ID != id {
		t.Fatal(view, err)
	}
	view, err := c.GetManagedPostgresMajorUpgrade(context.Background(), "orders", id)
	if err != nil || view.State != "failed" || view.LastErrorCode != "upgrade_refused" || view.FromPostgresMajor != 14 {
		t.Fatal(view, err)
	}
}
