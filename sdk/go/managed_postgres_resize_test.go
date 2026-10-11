// adr: 623 — portable compute resize and durable UUID transport.
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestManagedPostgresResizeClient(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("auth")
		}
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/v1/postgres/databases/orders/resize" {
				t.Error("POST path")
			}
			var body faas.ResizeManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != id || body.ServiceClass != "burstable" {
				t.Error("stable request", body, err)
			}
			w.WriteHeader(202)
		case http.MethodGet:
			if r.URL.Path != "/v1/postgres/databases/orders/resizes/"+id {
				t.Error("GET path")
			}
		default:
			t.Error("method")
		}
		_ = json.NewEncoder(w).Encode(faas.ManagedPostgresResize{ID: id, DatabaseID: "orders", State: "pending", Generation: 2, ConnectionInterruptionExpected: true})
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	body := faas.ResizeManagedPostgresDatabaseRequest{RequestID: id, ServiceClass: "burstable"}
	for i := 0; i < 2; i++ {
		view, err := c.ResizeManagedPostgresDatabase(context.Background(), "orders", body)
		if err != nil || view.ID != id {
			t.Fatal(view, err)
		}
	}
	view, err := c.GetManagedPostgresResize(context.Background(), "orders", id)
	if err != nil || view.Generation != 2 || !view.ConnectionInterruptionExpected || count != 3 {
		t.Fatal(view, err, count)
	}
}
