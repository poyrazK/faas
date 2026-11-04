package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// The trace mirrors the gateway: a gate scoped to DELETE covers a POST the
// app runs as DELETE (ADR-091 amendment 2026-11-02, later).
func TestSimulateProtectiveRuleCoversMethodOverride(t *testing.T) {
	rules := []api.EdgeRuleResponse{{
		ID: "delete-maintenance", Enabled: true, Kind: "maintenance", MatchHost: "*", MatchPath: "/admin/**",
		MatchMethods: []string{http.MethodDelete},
		Action:       json.RawMessage(`{"maintenance":{"message":"down","retry_after_seconds":30}}`),
	}}
	for _, tc := range []struct {
		name, query string
		headers     http.Header
		want        string
	}{
		{name: "plain POST", want: "continue"},
		{name: "header override", headers: http.Header{"X-Http-Method-Override": {"DELETE"}}, want: "maintenance"},
		{name: "query override", query: "_method=delete", want: "maintenance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := edgeruletrace.Input{
				App: "demo", Host: "example.com", Path: "/admin/users", Method: http.MethodPost,
				Query: tc.query, Headers: tc.headers, AppMaintenanceLoaded: true,
			}
			result, err := edgeruletrace.Simulate(input, rules)
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if result.Simulation.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q (%#v)", result.Simulation.Outcome, tc.want, result.Simulation)
			}
			row := result.Rules[0]
			if matched := row.Status != "skipped"; matched != (tc.want == "maintenance") {
				t.Fatalf("rule row = %#v", row)
			}
			if tc.want == "maintenance" && !strings.Contains(row.Reason, "method override") {
				t.Fatalf("rule row reason %q does not name the override", row.Reason)
			}
		})
	}
}
