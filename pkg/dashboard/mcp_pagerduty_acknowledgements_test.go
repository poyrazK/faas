package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPOperationsPagerDutyAcknowledgements(t *testing.T) {
	data := MCPOperationsData{AppFilter: "demo", AlertsURL: "/dashboard/mcp/apps/demo/alerts", AlertCSRF: "csrf", Alerts: []MCPAlertView{{URL: "/dashboard/mcp/apps/demo/alerts/rule", PagerDuty: &api.MCPReadinessPagerDutyPolicy{Version: 2, Enabled: true, Region: "eu"}, PagerDutyAcknowledgements: &api.MCPReadinessPagerDutyAcknowledgementPolicy{Version: 1, Enabled: true, SyncUnacknowledgements: true, PagerDutyVersion: 2, Region: "eu", ServiceID: "PSVC001", SigningSecretMasked: "***", WebhookPath: "/v1/webhooks/mcp-readiness/pagerduty/rule"}}}}

	data.Alerts[0].Fallback = &api.MCPReadinessFallbackPolicy{Enabled: true, Provider: "pagerduty", Version: 4}
	data.Alerts[0].PagerDutyFallbackAcknowledgements = &api.MCPReadinessPagerDutyAcknowledgementPolicy{Destination: "fallback", Enabled: true, Version: 7, FallbackVersion: 4, ServiceID: "PFALLBACK", Region: "us", SigningSecretMasked: "***", WebhookPath: "/v1/webhooks/mcp-readiness/pagerduty/rule/fallback"}
	data.ReadinessAttention = []MCPReadinessAttentionView{{MCPReadinessIncidentAttention: api.MCPReadinessIncidentAttention{ProviderAcknowledgements: []api.MCPReadinessPagerDutyAcknowledgementHold{{Destination: "fallback", ProviderIncidentID: "PINC_HOLD", ServiceID: "PFALLBACK", Region: "us", ReleaseSyncEnabled: false}}}}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", Page{Title: "MCP", Body: "mcp_operations", Account: &AccountView{ID: "owner"}, Data: data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Save PagerDuty acknowledgement sync", "Disable PagerDuty acknowledgement sync", `type="password" name="signing_secret"`, `name="expected_pagerduty_version" value="2"`, "/v1/webhooks/mcp-readiness/pagerduty/rule", "incident.acknowledged", "incident.unacknowledged", "PINC_HOLD", "local release required", `name="sync_unacknowledgements"`, `value="true" selected`, "Save PagerDuty fallback acknowledgement sync", "Disable PagerDuty fallback acknowledgement sync", `name="destination" value="fallback"`, `name="expected_version" value="7"`, `name="expected_fallback_version" value="4"`, "/v1/webhooks/mcp-readiness/pagerduty/rule/fallback"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing UI", want)
		}
	}
}

func TestMCPOperationsPagerDutyIndependentBindingsReadOnly(t *testing.T) {
	data := MCPOperationsData{AppFilter: "demo", AlertsURL: "/dashboard/mcp/apps/demo/alerts", Alerts: []MCPAlertView{{PagerDutyAcknowledgements: &api.MCPReadinessPagerDutyAcknowledgementPolicy{Version: 1, Enabled: true}, PagerDutyFallbackAcknowledgements: &api.MCPReadinessPagerDutyAcknowledgementPolicy{Destination: "fallback", Version: 7, Enabled: true, ServiceID: "PFALLBACK", WebhookPath: "/v1/webhooks/mcp-readiness/pagerduty/rule/fallback"}}}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", Page{Title: "MCP", Body: "mcp_operations", Account: &AccountView{ID: "owner"}, Data: data}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "PFALLBACK") || strings.Contains(w.Body.String(), "Save PagerDuty fallback acknowledgement sync") {
		t.Fatal("read-only fallback settings missing or writable")
	}
}
