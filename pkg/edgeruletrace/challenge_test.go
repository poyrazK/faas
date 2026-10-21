package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

// adr: 831 — amendment 7: the simulator shows a kind=challenge rule as the
// challenge page for a browser navigation and 403 challenge_required for
// anything else, both incomplete because a pass or verified credential is
// not collected; a verified crawler is not challenged.
func TestSimulateChallengeRule(t *testing.T) {
	var cond api.EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(`{"field":"verified_bot","op":"missing"}`), &cond); err != nil {
		t.Fatal(err)
	}
	priority := 10
	proposal := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: "example.com", MatchPath: "/login", Priority: &priority, Kind: "challenge", Match: &cond,
		Action: json.RawMessage(`{"challenge":{"difficulty":12,"pass_ttl_seconds":600}}`),
	}}}
	page := http.Header{}
	page.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/129.0 Safari/537.36")
	page.Set("Sec-Fetch-Mode", "navigate")
	fetch := http.Header{}
	fetch.Set("User-Agent", "curl/8.4")
	fetch.Set("Accept", "application/json")
	crawler := http.Header{}
	crawler.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	crawler.Set("Accept", "text/html")
	for name, tc := range map[string]struct {
		method   string
		headers  http.Header
		verified bool
		outcome  string
		problem  string
	}{
		"browser navigation": {http.MethodGet, page, false, "challenge_page", ""},
		"script post":        {http.MethodPost, fetch, false, "challenge_required", api.CodeChallengeRequired},
		"verified crawler":   {http.MethodGet, crawler, true, "", ""},
	} {
		input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/login", Method: tc.method, AppMaintenanceLoaded: true, Headers: tc.headers, VerifiedBot: tc.verified}
		cmp, err := edgeruletrace.SimulateProposal(input, nil, proposal, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sim := cmp.Proposed.Simulation
		if tc.outcome == "" {
			if sim.StoppedAt == "challenge" {
				t.Fatalf("%s: challenged a verified crawler: %+v", name, sim)
			}
			continue
		}
		if sim.Outcome != tc.outcome || sim.Status != "incomplete" || sim.StatusCode != http.StatusForbidden || sim.ProblemCode != tc.problem || sim.StoppedAt != "challenge" {
			t.Fatalf("%s: simulation = %+v", name, sim)
		}
	}
}
