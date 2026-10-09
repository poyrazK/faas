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
