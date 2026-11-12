// adr: 1285
package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestStoredWorkflowConsoleEscapesEditorAndRunMetadata(t *testing.T) {
	attack := "</textarea><script>private</script>"
	data := dashboard.StoredWorkflowConsoleData{Slug: "demo", Name: "report", BaseURL: "/dashboard/apps/demo/workflows/stored", Definition: attack, Sample: attack, Scenarios: attack,
		CSRFToken: "csrf", Version: 1, Published: true, RuntimeEnabled: true,
		Run:        &dashboard.StoredWorkflowConsoleRun{ID: "run-1", Status: "pending"},
		Simulation: &api.SimulateStoredWorkflowResponse{Complete: true, DefinitionValid: true, Trace: []api.StoredWorkflowSimulationStep{{StepName: "main", State: "mocked", Input: []byte(`"private-input"`), Output: []byte(`"private-output"`)}}}}
	response := httptest.NewRecorder()
	if err := dashboard.Render(response, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Title: "Stored workflows", Body: "stored_workflows", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	for _, private := range []string{attack, "private-input", "private-output"} {
		if strings.Contains(body, private) {
			t.Fatalf("unescaped content or payload exposed: %q", private)
		}
	}
	for _, visible := range []string{"&lt;/textarea&gt;", "Check and publish", "perform real actions", "Refresh run status"} {
		if !strings.Contains(body, visible) {
			t.Fatalf("missing %q", visible)
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authoring data may be cached")
	}
}

func TestStoredWorkflowConsoleReadOnlyFailureDoesNotLookLikeEmptyHistory(t *testing.T) {
	response := httptest.NewRecorder()
	data := dashboard.StoredWorkflowConsoleData{Slug: "demo", Name: "report", RunsError: "Run history is temporarily unavailable.", HealthError: "Recent health is temporarily unavailable."}
	if err := dashboard.Render(response, slog.New(slog.NewTextHandler(io.Discard, nil)), "", dashboard.Page{Body: "stored_workflows", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if !strings.Contains(body, data.RunsError) || strings.Contains(body, "No recent runs are shown") {
		t.Fatal("failed read was presented as empty history")
	}
}
