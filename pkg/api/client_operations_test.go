// ADR-521: retries preserve logical operation identity and decoded public results.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperationClientContract(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tenant-key" {
			t.Error("lost bearer")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/v1/platform-tenant-self/customer-operations" || r.Header.Get("Idempotency-Key") != "stable-export" {
				t.Errorf("submission changed: %s %s %v", r.Method, r.URL.Path, r.Header)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"export-id","status_url":"/status","events_url":"/events"}`))
		case 2:
			if r.URL.RequestURI() != "/v1/apps/customer%20app/operations/operation%2Fid" {
				t.Errorf("escaped path: %s", r.URL.RequestURI())
			}
			_, _ = w.Write([]byte(`{"id":"export-id","state":"succeeded"}`))
		case 3:
			if r.URL.RequestURI() != "/v1/platform-tenant-self/customer-operations/operation%2Fid/events?after=42" {
				t.Errorf("resume cursor: %s", r.URL.RequestURI())
			}
			_, _ = w.Write([]byte(`{"events":[],"latest_sequence":42,"resync_required":false}`))
		default:
			t.Error("unexpected request")
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "tenant-key")
	ctx := context.Background()
	if _, err := client.StartPlatformTenantSelfOperation(ctx, OperationStartRequest{}, ""); err == nil || calls != 0 {
		t.Fatal("empty key sent a request")
	}
	receipt, err := client.StartPlatformTenantSelfOperation(ctx, OperationStartRequest{}, "stable-export")
	if err != nil || receipt.ID != "export-id" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	status, err := client.GetOperation(ctx, "customer app", "operation/id")
	if err != nil || status.State != "succeeded" {
		t.Fatalf("status: %+v %v", status, err)
	}
	page, err := client.GetPlatformTenantSelfOperationEvents(ctx, "operation/id", 42)
	if err != nil || page.LatestSequence != 42 {
		t.Fatalf("page: %+v %v", page, err)
	}
}
