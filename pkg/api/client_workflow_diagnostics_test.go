// adr: 644
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestClientWorkflowDiagnosticsAccountAndTenantRoutes(t *testing.T) {
	var paths []string
	fixture := WorkflowRunDiagnosticsResponse{RunID: "run", LegacyUnpinned: true, Resume: WorkflowResumePreview{ExpectedResumeCount: 0, Blockers: []WorkflowDiagnosticBlocker{WorkflowRecoveryBlocker("unsafe_mutation", "charge")}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Authorization") != "Bearer read-key" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer server.Close()
	client := NewClient(server.URL, "read-key")
	for _, read := range []func() (WorkflowRunDiagnosticsResponse, error){func() (WorkflowRunDiagnosticsResponse, error) {
		return client.GetWorkflowRunDiagnostics(t.Context(), "run")
	}, func() (WorkflowRunDiagnosticsResponse, error) {
		return client.GetPlatformTenantSelfWorkflowRunDiagnostics(t.Context(), "run")
	}} {
		result, err := read()
		if err != nil || !reflect.DeepEqual(result, fixture) {
			t.Fatalf("decoded=%+v %v", result, err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"/v1/workflows/runs/run/diagnostics", "/v1/platform-tenant-self/workflows/runs/run/diagnostics"}) {
		t.Fatal(paths)
	}
}
