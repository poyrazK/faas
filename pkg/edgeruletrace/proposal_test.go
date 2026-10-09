package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

func proposalTestRules() []api.EdgeRuleResponse {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return []api.EdgeRuleResponse{
		{ID: "redirect-rule", AppID: "app", Enabled: true, Kind: "redirect", MatchHost: "*", MatchPath: "/old/*", Priority: 50,
			Action: json.RawMessage(`{"redirect":{"to":"/new"}}`), CreatedAt: created},
	}
}

// A proposed maintenance rule ahead of today's redirect turns the request
// into a 503 — the trace reports the new outcome and the new rule, without
// any server call.
func TestSimulateProposalReportsChangedOutcome(t *testing.T) {
	input := edgeruletrace.Input{App: "demo", Host: "Example.COM", Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true}
	priority := 10
	proposal := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: "example.com", MatchPath: "/old/*", Priority: &priority, Kind: "maintenance",
		Action: json.RawMessage(`{"maintenance":{"message":"migrating","retry_after_seconds":60}}`),
	}}}
	cmp, err := edgeruletrace.SimulateProposal(input, proposalTestRules(), proposal, time.Now())
	if err != nil {
		t.Fatalf("SimulateProposal: %v", err)
	}
	if cmp.Current.Simulation.Outcome != "redirect" {
		t.Fatalf("current outcome = %q, want redirect", cmp.Current.Simulation.Outcome)
	}
	if cmp.Proposed.Simulation.Outcome != "maintenance" || !cmp.Changed {
		t.Fatalf("proposed = %#v changed=%v", cmp.Proposed.Simulation, cmp.Changed)
	}
	joined := strings.Join(cmp.Differences, "\n")
	if !strings.Contains(joined, `outcome: "redirect" → "maintenance"`) || !strings.Contains(joined, "rule proposed-1 (maintenance, new)") {
		t.Fatalf("differences = %v", cmp.Differences)
	}
}

// Removing or disabling the only matching rule, or a change that does not
// touch the request, is reported precisely.
func TestSimulateProposalUpdateRemoveAndNoEffect(t *testing.T) {
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true}
	rules := proposalTestRules()

	disabled := false
	cmp, err := edgeruletrace.SimulateProposal(input, rules, edgeruletrace.Proposal{
		Update: map[string]api.UpdateEdgeRuleRequest{"redirect-rule": {Enabled: &disabled}},
	}, time.Now())
	if err != nil || !cmp.Changed || cmp.Proposed.Simulation.Outcome == "redirect" {
		t.Fatalf("disable: changed=%v outcome=%q err=%v", cmp.Changed, cmp.Proposed.Simulation.Outcome, err)
	}

	cmp, err = edgeruletrace.SimulateProposal(input, rules, edgeruletrace.Proposal{Remove: []string{"redirect-rule"}}, time.Now())
	if err != nil || !strings.Contains(strings.Join(cmp.Differences, "\n"), "rule redirect-rule (redirect, removed)") {
		t.Fatalf("remove: differences=%v err=%v", cmp.Differences, err)
	}

	other := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: "other.example.com", Kind: "maintenance",
		Action: json.RawMessage(`{"maintenance":{"message":"x"}}`),
	}}}
	cmp, err = edgeruletrace.SimulateProposal(input, rules, other, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Proposed.Simulation.Outcome != "redirect" || strings.Contains(strings.Join(cmp.Differences, "\n"), "outcome:") {
		t.Fatalf("unrelated host changed the outcome: %v", cmp.Differences)
	}
}

func TestApplyProposalRejectsInvalidDrafts(t *testing.T) {
	rules := proposalTestRules()
	cases := map[string]edgeruletrace.Proposal{
		"unknown remove":     {Remove: []string{"nope"}},
		"unknown update":     {Update: map[string]api.UpdateEdgeRuleRequest{"nope": {}}},
		"update and remove":  {Remove: []string{"redirect-rule"}, Update: map[string]api.UpdateEdgeRuleRequest{"redirect-rule": {}}},
		"unknown kind":       {Add: []api.CreateEdgeRuleRequest{{MatchHost: "a", Kind: "teleport", Action: json.RawMessage(`{}`)}}},
		"non-object action":  {Add: []api.CreateEdgeRuleRequest{{MatchHost: "a", Kind: "redirect", Action: json.RawMessage(`[1]`)}}},
		"missing match_host": {Add: []api.CreateEdgeRuleRequest{{Kind: "redirect", Action: json.RawMessage(`{}`)}}},
	}
	for name, p := range cases {
		if _, err := edgeruletrace.ApplyProposal(rules, p, time.Now()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := edgeruletrace.ParseProposal([]byte(`{"add":[],"delete":["x"]}`)); err == nil {
		t.Error("ParseProposal accepted an unknown field")
	}
}

// adr: 906 — the simulator evaluates match conditions with the gateway's
// evaluator: a cookie-gated maintenance rule applies only to beta testers,
// and a proposal can add such a rule and show who it affects.
func TestSimulateEvaluatesMatchConditions(t *testing.T) {
	var cond api.EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(`{"field":"cookie:beta","op":"eq","value":"1"}`), &cond); err != nil {
		t.Fatal(err)
	}
	priority := 10
	proposal := edgeruletrace.Proposal{Add: []api.CreateEdgeRuleRequest{{
		MatchHost: "example.com", MatchPath: "/old/*", Priority: &priority, Kind: "maintenance", Match: &cond,
		Action: json.RawMessage(`{"maintenance":{"message":"beta only"}}`),
	}}}
	beta := http.Header{}
	beta.Set("Cookie", "beta=1")
	for name, tc := range map[string]struct {
		headers http.Header
		want    string
	}{"beta tester": {beta, "maintenance"}, "everyone else": {http.Header{}, "redirect"}} {
		input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true, Headers: tc.headers}
		cmp, err := edgeruletrace.SimulateProposal(input, proposalTestRules(), proposal, time.Now())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cmp.Proposed.Simulation.Outcome != tc.want {
			t.Fatalf("%s: proposed outcome = %q, want %q", name, cmp.Proposed.Simulation.Outcome, tc.want)
		}
	}
}

// adr: 907 — in_list conditions evaluate against the lists the caller
// supplies; a list that was not supplied never matches, as on the gateway.
func TestSimulateEvaluatesListConditions(t *testing.T) {
	rules := append(proposalTestRules(), api.EdgeRuleResponse{
		ID: "office-maint", AppID: "app", Enabled: true, Kind: "maintenance",
		MatchHost: "*", MatchPath: "/old/*", Priority: 1,
		Match:  &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"},
		Action: json.RawMessage(`{"maintenance":{"message":"office only"}}`),
	})
	if got := edgeruletrace.ReferencedEdgeRuleLists(rules); len(got) != 1 || got[0] != "office" {
		t.Fatalf("referenced lists = %v", got)
	}
	office := []api.EdgeRuleListResponse{{Name: "office", Kind: "ip", Items: []string{"203.0.113.0/24"}}}
	for name, tc := range map[string]struct {
		ip    string
		lists []api.EdgeRuleListResponse
		want  string
	}{
		"office ip":       {"203.0.113.9", office, "maintenance"},
		"other ip":        {"198.51.100.9", office, "redirect"},
		"list not loaded": {"203.0.113.9", nil, "redirect"},
	} {
		input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/page", Method: http.MethodGet,
			AppMaintenanceLoaded: true, ClientIP: tc.ip, EdgeRuleLists: tc.lists}
		result, err := edgeruletrace.Simulate(input, rules)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.Simulation.Outcome != tc.want {
			t.Fatalf("%s: outcome = %q, want %q", name, result.Simulation.Outcome, tc.want)
		}
	}
}

// adr: 904 — a log-mode rule is reported as logged and leaves the outcome to
// the enforced rules, exactly as the gateway counts it without acting.
func TestSimulateLogModeRuleDoesNotChangeOutcome(t *testing.T) {
	rules := append(proposalTestRules(), api.EdgeRuleResponse{
		ID: "shadow-maint", AppID: "app", Enabled: true, Kind: "maintenance", Mode: api.EdgeRuleModeLog,
		MatchHost: "*", MatchPath: "/old/*", Priority: 1,
		Action: json.RawMessage(`{"maintenance":{"message":"trial"}}`),
	})
	input := edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/old/page", Method: http.MethodGet, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.Outcome != "redirect" {
		t.Fatalf("outcome = %q, want redirect (log-mode maintenance must not apply)", result.Simulation.Outcome)
	}
	for _, row := range result.Rules {
		if row.ID == "shadow-maint" && row.Status != "logged" {
			t.Fatalf("log-mode row status = %q, want logged", row.Status)
		}
	}
}

// adr: 091 — the simulator now shares the gateway's matchers: host patterns
// compare case-insensitively and protective kinds match normalized and
// case-folded path variants, exactly as pkg/gateway does.
func TestSimulateUsesGatewayMatchers(t *testing.T) {
	rules := []api.EdgeRuleResponse{
		{ID: "maint", Enabled: true, Kind: "maintenance", MatchHost: "API.example.com", MatchPath: "/admin/*", Priority: 10,
			Action: json.RawMessage(`{"maintenance":{"message":"closed"}}`)},
	}
	input := edgeruletrace.Input{App: "demo", Host: "api.example.com", Path: "/ADMIN/users", Method: http.MethodGet, AppMaintenanceLoaded: true}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.Outcome != "maintenance" {
		t.Fatalf("outcome = %q, want maintenance (gateway would match host case and /ADMIN/ path variant)", result.Simulation.Outcome)
	}
}
