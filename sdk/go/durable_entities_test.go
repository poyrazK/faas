// adr: 712
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestDurableEntitySDKPreservesReplayIdentity(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request faas.DurableEntityInvokeRequest
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/counter/entities/invoke" || r.Header.Get("Authorization") != "Bearer fixture-token" || json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID != "stable-one" || request.Environment != "staging" || request.PlatformTenantID != "customer" || string(request.Payload) != `{"delta":1}` {
			t.Errorf("invalid entity transport: %s %+v", r.URL, request)
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityInvokeResponse{Value: json.RawMessage(`{"count":1}`), Version: 1, Replayed: calls > 1})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	request := faas.DurableEntityInvokeRequest{Namespace: "counters", Key: "document:123", RequestID: "stable-one", Payload: json.RawMessage(`{"delta":1}`), Environment: "staging", PlatformTenantID: "customer"}
	for i := 0; i < 2; i++ {
		result, err := client.InvokeDurableEntity(context.Background(), "counter", request)
		if err != nil || result.Version != 1 || result.Replayed != (i > 0) || string(result.Value) != `{"count":1}` {
			t.Fatalf("SDK result = %+v %v", result, err)
		}
	}
	request.RequestID = ""
	if _, err := client.InvokeDurableEntity(context.Background(), "counter", request); err == nil || calls != 2 {
		t.Fatal("missing replay identity reached the API")
	}
}

func TestDurableEntitySDKInspectionSelectorsAndVersion(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/counter/entities/inspect" || r.URL.Query().Get("key") != "document:/?+&雪" || r.URL.Query().Get("namespace") != "documents" || r.URL.Query().Get("environment") != "staging" || r.ContentLength > 0 {
			t.Error("invalid inspection transport", r.URL)
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityInspectResponse{Version: ^uint64(0), StateCommitted: true, Outbox: faas.DurableEntityOutboxInspection{Pending: 1, HeadDelivery: &faas.DurableEntityHeadDelivery{Status: "unknown"}}})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.InspectDurableEntity(t.Context(), "counter", faas.DurableEntityInspectRequest{Namespace: "documents", Key: "document:/?+&雪", Environment: "staging"})
	if err != nil || out.Version != ^uint64(0) || out.Outbox.HeadDelivery == nil || out.Outbox.HeadDelivery.Status != "unknown" {
		t.Fatal(out, err)
	}
	if _, err := client.InspectDurableEntity(t.Context(), "counter", faas.DurableEntityInspectRequest{}); err == nil || calls != 1 {
		t.Fatal("missing selectors reached server")
	}
}

func TestDurableEntitySDKRecoveryPreservesInspectionFence(t *testing.T) {
	calls := 0
	revision := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request faas.DurableEntityRetryRequest
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/counter/entities/retry" || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedVersion != ^uint64(0) || request.ExpectedRecoveryRevision != revision || request.Target != "outbox" || request.HeadID != "head" {
			t.Error("invalid recovery transport", r.URL, request)
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityRetryResponse{Version: request.ExpectedVersion, Target: request.Target, Rearmed: true})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := faas.DurableEntityRetryRequest{Namespace: "documents", Key: "document:123", Target: "outbox", ExpectedVersion: ^uint64(0), ExpectedRecoveryRevision: revision, HeadID: "head"}
	out, err := client.RetryDurableEntity(t.Context(), "counter", request)
	if err != nil || out.Version != request.ExpectedVersion || !out.Rearmed {
		t.Fatal(out, err)
	}
	request.ExpectedRecoveryRevision = ""
	if _, err := client.RetryDurableEntity(t.Context(), "counter", request); err == nil || calls != 1 {
		t.Fatal("unfenced recovery reached API")
	}
}
