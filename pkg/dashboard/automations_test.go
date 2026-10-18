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

func TestAutomationConsoleEscapesEditorAndRunMetadata(t *testing.T) {
	attack := "</textarea><script>private</script>"
	data := dashboard.AutomationConsoleData{Slug: "demo", Name: "report", BaseURL: "/dashboard/apps/demo/automations", Definition: attack, Sample: attack, Scenarios: attack,
		CSRFToken: "csrf", Version: 1, Published: true, RuntimeEnabled: true,
		Run:        &dashboard.AutomationConsoleRun{ID: "run-1", Status: "pending"},
		Simulation: &api.SimulateAutomationResponse{Complete: true, DefinitionValid: true, Trace: []api.AutomationSimulationStep{{StepName: "main", State: "mocked", Input: []byte(`"private-input"`), Output: []byte(`"private-output"`)}}}}
	response := httptest.NewRecorder()
	if err := dashboard.Render(response, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Title: "Automations", Body: "automations", Data: data}); err != nil {
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

func TestAutomationConsoleReadOnlyFailureDoesNotLookLikeEmptyHistory(t *testing.T) {
	response := httptest.NewRecorder()
	data := dashboard.AutomationConsoleData{Slug: "demo", Name: "report", RunsError: "Run history is temporarily unavailable.", HealthError: "Recent health is temporarily unavailable."}
	if err := dashboard.Render(response, slog.New(slog.NewTextHandler(io.Discard, nil)), "", dashboard.Page{Body: "automations", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if !strings.Contains(body, data.RunsError) || strings.Contains(body, "No recent runs are shown") {
		t.Fatal("failed read was presented as empty history")
	}
}
