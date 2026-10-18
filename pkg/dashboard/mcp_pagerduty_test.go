package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOperationsPagerDuty(t *testing.T) {
	data := MCPOperationsData{AppFilter: "demo", AlertsURL: "/dashboard/mcp/apps/demo/alerts", AlertCSRF: "csrf", Alerts: []MCPAlertView{{PagerDuty: &api.MCPReadinessPagerDutyPolicy{Version: 2, Enabled: true, Region: "eu", IntegrationKeyMasked: "***"}, URL: "/dashboard/mcp/apps/demo/alerts/rule"}}, ReadinessIncidents: []api.MCPReadinessIncidentEvent{{DestinationDeliveries: []api.MCPReadinessDestinationDelivery{{Provider: "pagerduty", ProviderStatus: "accepted", Destination: "primary", Status: "delivered"}}}}}
	w := httptest.NewRecorder()
	if e := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", Page{Title: "MCP", Body: "mcp_operations", Account: &AccountView{ID: "owner"}, Data: data}); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"Save PagerDuty integration", `name="region"`, `value="eu" selected`, "Events API v2 · eu", "Use webhook primary for new deliveries", `type="password" name="integration_key"`, `name="expected_version" value="2"`, "primary · pagerduty: accepted"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
}
