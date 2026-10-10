package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRestoreValidationSDKPreservesIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request faas.DurableEntityRestoreRequest
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/counter/entities/restore/validate" || json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID != "stable" || request.ExpectedVersion != ^uint64(0) {
			t.Error("invalid validator invocation")
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityRestoreValidationResponse{Valid: true, DeploymentID: "deployment", ExpectedVersion: request.ExpectedVersion, SourceVersion: 1})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := faas.DurableEntityRestoreRequest{Namespace: "counters", Key: "counter", RequestID: "stable", ExpectedVersion: ^uint64(0), Export: faas.DurableEntityStateExport{Version: 1, Data: json.RawMessage(`{}`)}}
	verdict, err := client.ValidateDurableEntityRestore(ctx, "counter", request)
	if err != nil || !verdict.Valid || verdict.ExpectedVersion != ^uint64(0) {
		t.Fatal(verdict, err)
	}
	request.RequestID = ""
	if _, err := client.ValidateDurableEntityRestore(ctx, "counter", request); err == nil || calls != 1 {
		t.Fatal("missing ID reached API")
	}
}
