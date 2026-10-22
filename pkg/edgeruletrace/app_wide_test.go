package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// adr: 1191 — an app-wide rule (match_host "@app") applies to whatever host
// the per-app trace simulates, custom domains included.
func TestSimulateAppWideRule(t *testing.T) {
	priority := 10
	proposal := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/old/*", Priority: &priority, Kind: "maintenance",
		Action: json.RawMessage(`{"maintenance":{"message":"app-wide"}}`),
	}}}
	for _, host := range []string{"example.com", "shop.customer.test"} {
		input := edgeruletrace.Input{App: "demo", Host: host, Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true, Headers: http.Header{}}
		cmp, err := edgeruletrace.SimulateProposal(input, proposalTestRules(), proposal, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if cmp.Proposed.Simulation.Outcome != "maintenance" {
			t.Fatalf("%s: proposed outcome = %q, want maintenance", host, cmp.Proposed.Simulation.Outcome)
		}
	}
}
