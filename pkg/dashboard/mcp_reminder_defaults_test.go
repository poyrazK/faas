package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPOperationsReminderDefaults(t *testing.T) {
	zero := int64(0)
	data := MCPOperationsData{AppFilter: "demo", AlertsURL: "/dashboard/mcp/apps/demo/alerts", ReadinessScope: "staging", AlertCSRF: "csrf", Alerts: []MCPAlertView{{Name: "Readiness", URL: "/dashboard/mcp/apps/demo/alerts/rule", ReminderDefaults: &api.MCPReadinessReminderDefaults{Version: 2}}}, ReadinessAttention: []MCPReadinessAttentionView{{MCPReadinessIncidentAttention: api.MCPReadinessIncidentAttention{IncidentID: "incident", RuleName: "Readiness", ReminderPolicySource: "rule_default", ReminderDefaultsVersion: &zero}}}}
	w := httptest.NewRecorder()
	if e := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "MCP", Body: "mcp_operations", Account: &AccountView{ID: "owner"}, Data: data}); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"Save reminder defaults", "Changes apply when a new incident opens", `name="expected_version" value="2"`, "Inherited rule policy", "version 0"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
}
