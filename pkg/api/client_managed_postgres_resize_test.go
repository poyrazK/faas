package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagedPostgresResizeClientPreservesRequestAndProgress(t *testing.T) {
	const requestID = "33333333-3333-4333-8333-333333333333"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing auth")
		}
		if r.Method == http.MethodPost {
			if r.URL.EscapedPath() != "/v1/postgres/databases/orders%2Ftest/resize" {
				t.Error("unescaped database", r.URL.EscapedPath())
			}
			var body ResizeManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != requestID || body.ServiceClass != "burstable" {
				t.Error("request mismatch", body, err)
			}
			w.WriteHeader(202)
		} else if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/postgres/databases/orders%2Ftest/resizes/"+requestID {
			t.Error("progress URL", r.Method, r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"id":"` + requestID + `","database_id":"orders/test","state":"pending","generation":2,"connection_interruption_expected":true}`))
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "fixture")
	req := ResizeManagedPostgresDatabaseRequest{RequestID: requestID, ServiceClass: "burstable"}
	for i := 0; i < 2; i++ {
		out, err := client.ResizeManagedPostgresDatabase(context.Background(), "orders/test", req)
		if err != nil || out.ID != requestID || out.Generation != 2 {
			t.Fatal(out, err)
		}
	}
	out, err := client.GetManagedPostgresResize(context.Background(), "orders/test", requestID)
	if err != nil || out.State != "pending" || !out.ConnectionInterruptionExpected || calls != 3 {
		t.Fatal(out, err, calls)
	}
}
