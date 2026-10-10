package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestStateExportRestoreSDKTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	calls := 0
	exported := faas.DurableEntityStateExport{Format: 1, Version: 1, Data: json.RawMessage(`{"count":1}`), Checksum: "opaque-checksum"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authorization")
		}
		if r.Method == http.MethodGet {
			if r.URL.Path != "/v1/apps/counter/entities/export" || r.URL.Query().Get("key") != "document:/?雪" {
				t.Error(r.URL)
			}
			_ = json.NewEncoder(w).Encode(exported)
			return
		}
		var request faas.DurableEntityRestoreRequest
		if r.URL.Path != "/v1/apps/counter/entities/restore" || json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID != "stable" || request.ExpectedVersion != 42 || string(request.Export.Data) != string(exported.Data) {
			t.Error("restore body changed")
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityRestoreResponse{Version: 43, Replayed: calls > 2})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.ExportDurableEntity(ctx, "counter", faas.DurableEntityInspectRequest{Namespace: "documents", Key: "document:/?雪"})
	if err != nil || out.Checksum != exported.Checksum {
		t.Fatal(out, err)
	}
	request := faas.DurableEntityRestoreRequest{Namespace: "documents", Key: "document:/?雪", RequestID: "stable", ExpectedVersion: 42, Export: out}
	for _, replay := range []bool{false, true} {
		result, err := client.RestoreDurableEntity(ctx, "counter", request)
		if err != nil || result.Version != 43 || result.Replayed != replay {
			t.Fatal(result, err)
		}
	}
	request.RequestID = ""
	if _, err := client.RestoreDurableEntity(ctx, "counter", request); err == nil || calls != 3 {
		t.Fatal("missing stable ID reached server")
	}
}
