package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestManagedPostgresRecoveryStatusClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/v1/postgres/databases/orders%2Fbranch/recovery" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(faas.ManagedPostgresRecoveryStatus{DatabaseID: "orders/branch", Status: "limits_known", Fresh: true, HistoryBoundsKnown: false, RetentionSeconds: 300})
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.GetManagedPostgresRecoveryStatus(context.Background(), "orders/branch")
	if err != nil || out.Status != "limits_known" || !out.Fresh || out.HistoryBoundsKnown || out.RetentionSeconds != 300 {
		t.Fatal(out, err)
	}
}
