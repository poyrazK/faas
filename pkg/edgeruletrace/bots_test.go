package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// adr: 968 — the simulator classifies the simulated User-Agent and treats
// verified_bot as the claimed crawler only when VerifiedBot is set.
func TestSimulateEvaluatesBotConditions(t *testing.T) {
	var cond api.EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(`{"all":[{"field":"ua_family","op":"eq","value":"bot"},{"field":"verified_bot","op":"missing"}]}`), &cond); err != nil {
		t.Fatal(err)
	}
	priority := 10
	proposal := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: "example.com", MatchPath: "/old/*", Priority: &priority, Kind: "maintenance", Match: &cond,
		Action: json.RawMessage(`{"maintenance":{"message":"no fake bots"}}`),
	}}}
	googlebot := http.Header{}
	googlebot.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	browser := http.Header{}
	browser.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/129.0 Safari/537.36")
	for name, tc := range map[string]struct {
		headers  http.Header
		verified bool
		want     string
	}{
		"unverified googlebot": {googlebot, false, "maintenance"},
		"verified googlebot":   {googlebot, true, "redirect"},
		"browser":              {browser, false, "redirect"},
	} {
		input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true, Headers: tc.headers, VerifiedBot: tc.verified}
		cmp, err := edgeruletrace.SimulateProposal(input, proposalTestRules(), proposal, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cmp.Proposed.Simulation.Outcome != tc.want {
			t.Fatalf("%s: proposed outcome = %q, want %q", name, cmp.Proposed.Simulation.Outcome, tc.want)
		}
	}
}
