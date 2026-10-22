package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDurableManagementSDKPreservesScopeAndNumericRevisions(t *testing.T) {
	// Durable registry recovery requires exact selectors and uint64 revisions,
	// rather than a floating-point conversion through an untyped JSON map.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/apps/app%2Fname/entities/registry/cleanup/step" || r.Method != "POST" || r.URL.Query().Get("namespace") != "docs&other=value" || r.URL.Query().Get("environment") != "staging" {
			t.Error(r.URL, r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(body), "9007199254740993") {
			t.Error("revision rounded", string(body))
		}
		_, err = w.Write([]byte(`{"cleanup":{"revision":9007199254740993}}`))
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c := NewClient(server.URL, "token")
	got, err := c.StepDurableEntityRegistryCleanup(t.Context(), "app/name", DurableEntityInspectRequest{Namespace: "docs&other=value", Key: "a", Environment: "staging"}, json.RawMessage(`{"operation_id":"123","expected_revision":9007199254740993}`))
	if err != nil || !strings.Contains(string(got), "9007199254740993") {
		t.Fatal(string(got), err)
	}
}
func TestDurableManagementSDKRejectsMissingScopeBeforeRequest(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "token")
	if _, err := c.InspectDurableEntityRegistryCleanup(t.Context(), "app", DurableEntityInspectRequest{}); err == nil || !strings.Contains(err.Error(), "namespace and key") {
		t.Fatal(err)
	}
}
