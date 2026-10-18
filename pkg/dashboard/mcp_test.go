package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOperationsPartialAndEscapedEvidence(t *testing.T) {
	data := MCPOperationsData{ObservedAt: "2026-10-14T12:00:00Z", HealthComplete: false, TotalIssues: 1, IssuanceUnknown: 1, Issues: []MCPConnectionIssueView{{App: "deleted-probe", Deleted: true, Kind: "issuance_unknown", Guidance: "Confirm issuance at the provider before reconnecting."}}, Evidence: &MCPAdmissionView{Outcome: "rejected", Reason: "<script>private-code</script>"}}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce-test", Page{Title: "MCP operations", Body: "mcp_operations", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"Partial inventory", "deleted application", "Confirm issuance at the provider", "&lt;script&gt;private-code&lt;/script&gt;", "nonce=\"nonce-test\""} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "method=\"post\"") {
		t.Fatal("unsafe or mutating output")
	}
}

func TestMCPOperationsUnavailableHealthDoesNotShowZeroIssues(t *testing.T) {
	rec := httptest.NewRecorder()
	err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", Page{Title: "MCP operations", Body: "mcp_operations", Data: MCPOperationsData{HealthError: "Connection health unavailable"}})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "0 issues in the observed inventory") || strings.Contains(body, "No connection issues on this page") {
		t.Fatal("unavailable source presented as healthy")
	}
}

func TestMCPOperationsUnknownIssuanceRequiresProviderConfirmation(t *testing.T) {
	rec := httptest.NewRecorder()
	data := MCPOperationsData{ActionCSRF: "test-token", Retirements: []MCPRetirementView{{ID: "unknown", State: "manual_required", Reason: "issuance_unknown", Recoverable: true}}}
	if err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", Page{Title: "MCP operations", Body: "mcp_operations", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Queue retirement retry") {
		t.Fatal("unknown issuance offered retry without retained grant")
	}
	for _, want := range []string{"provider_confirmed", "test-token", "Record manual resolution", "does not contact the provider"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestMCPOperationsContinuousReadiness(t *testing.T) {
	data := MCPOperationsData{AppFilter: "monitor-app", ReadinessMonitoringState: "Enabled", ReadinessMonitoring: []MCPReadinessMonitorView{{Deployment: "deployment-id", Role: "reader", State: "firing", Failures: 3, Threshold: 3, Reason: "catalog_drift", ConnectionRole: "reader", RecoveryAction: "Reconnect", RecoveryURL: "/dashboard/mcp?app=monitor-app#connections", LastSuccess: "2026-10-15T12:00:00Z", NextProbe: "2026-10-15T12:02:00Z", History: []string{"2026-10-15T12:01:00Z · catalog_drift"}}}}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", Page{Title: "MCP", Body: "mcp_operations", Data: data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Continuous readiness monitoring", "3 / 3", "Recent checks", "OAuth connection: reader", "Reconnect in OAuth connections", "2026-10-15T12:01:00Z", "catalog_drift", "does not automatically move traffic"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
