// adr: 962
package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func conditionFromJSON(t *testing.T, raw string) EdgeRuleCondition {
	t.Helper()
	var expr api.EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(raw), &expr); err != nil {
		t.Fatal(err)
	}
	p, err := api.CompileEdgeRuleMatch(&expr)
	if err != nil {
		t.Fatal(err)
	}
	return EdgeRuleCondition{Match: p}
}

// adr: 962 — a rule's condition acts as one more selector on every kind: a
// cookie-gated route applies only to beta testers, an unconditioned rule
// still applies to everyone, and owner scoping still runs first.
func TestApplicableEdgeRulesFiltersByCondition(t *testing.T) {
	rules := []EdgeRuleResolved{
		{ID: "beta", AccountID: "acct", TargetAppSlug: "canary",
			EdgeRuleCondition: conditionFromJSON(t, `{"field":"cookie:beta","op":"eq","value":"1"}`)},
		{ID: "default", AccountID: "acct", TargetAppSlug: "stable"},
		{ID: "foreign", AccountID: "other", TargetAppSlug: "x"},
	}
	account := func(r *EdgeRuleResolved) string { return r.AccountID }
	pick := func(cookie string) []string {
		req := httptest.NewRequest(http.MethodGet, "http://api.example.com/v1?x=1", nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		ctx := WithEdgeRuleOwner(context.Background(), "acct")
		ctx = WithEdgeRuleMatchContext(ctx, NewEdgeRuleMatchContext(req, nil, nil, nil))
		var ids []string
		for _, r := range ApplicableEdgeRules(ctx, rules, account, "/v1", http.MethodGet) {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := pick("beta=1"); len(got) != 2 || got[0] != "beta" || got[1] != "default" {
		t.Fatalf("beta tester rules = %v, want [beta default]", got)
	}
	if got := pick(""); len(got) != 1 || got[0] != "default" {
		t.Fatalf("other visitor rules = %v, want [default]", got)
	}
}

// adr: 091 — a condition on path reads the path the rule is being matched
// on, not the request's current path: an access gate matching the client's
// path after a rewrite sees the client's path in its condition, and the
// rewritten-path match sees the rewritten one (amendment 2026-11-03, review
// round 9).
func TestEdgeRuleConditionPathIsThePathTheRuleMatchesOn(t *testing.T) {
	rules := []EdgeRuleResolved{{ID: "api", AccountID: "acct",
		EdgeRuleCondition: conditionFromJSON(t, `{"field":"path","op":"regex","value":"^/api/"}`)}}
	account := func(r *EdgeRuleResolved) string { return r.AccountID }
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/api/users", nil)
	ctx := WithEdgeRuleOwner(context.Background(), "acct")
	ctx = WithEdgeRuleMatchContext(ctx, NewEdgeRuleMatchContext(req, nil, nil, nil))
	req.URL.Path = "/users" // a rewrite runs after the snapshot is taken
	if got := ApplicableEdgeRules(ctx, rules, account, "/api/users", http.MethodGet); len(got) != 1 {
		t.Fatalf("client-path match = %v, want the /api condition to hold", got)
	}
	if got := ApplicableEdgeRules(ctx, rules, account, "/users", http.MethodGet); len(got) != 0 {
		t.Fatalf("rewritten-path match = %v, want the /api condition to fail", got)
	}
}

// adr: 962 — the country lookup runs at most once per request, and an
// untrusted client IP leaves both client_ip and country absent.
func TestEdgeRuleMatchContextCountryLookupIsLazyAndOnce(t *testing.T) {
	calls := 0
	lookup := func(net.IP) string { calls++; return "DE" }
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	rules := []EdgeRuleResolved{
		{ID: "eu", AccountID: "acct", EdgeRuleCondition: conditionFromJSON(t, `{"field":"country","op":"eq","value":"DE"}`)},
		{ID: "eu2", AccountID: "acct", EdgeRuleCondition: conditionFromJSON(t, `{"field":"country","op":"in","values":["DE","FR"]}`)},
	}
	account := func(r *EdgeRuleResolved) string { return r.AccountID }

	m := NewEdgeRuleMatchContext(req, net.ParseIP("203.0.113.9"), lookup, nil)
	ctx := WithEdgeRuleMatchContext(context.Background(), m)
	if got := ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet); len(got) != 2 {
		t.Fatalf("trusted DE request matched %d rules, want 2", len(got))
	}
	ApplicableEdgeRules(ctx, rules, account, "/", http.MethodGet)
	if calls != 1 {
		t.Fatalf("country lookups = %d, want 1 per request", calls)
	}

	untrusted := WithEdgeRuleMatchContext(context.Background(), NewEdgeRuleMatchContext(req, nil, lookup, nil))
	if got := ApplicableEdgeRules(untrusted, rules, account, "/", http.MethodGet); len(got) != 0 {
		t.Fatalf("untrusted client matched country rules: %v", got)
	}
}

// adr: 962 — a stored condition that no longer compiles disables its rule.
func TestNeverMatchingConditionDisablesRule(t *testing.T) {
	rules := []EdgeRuleResolved{{ID: "broken", AccountID: "acct", EdgeRuleCondition: EdgeRuleCondition{Match: api.NeverMatchingEdgeRuleProgram()}}}
	got := ApplicableEdgeRules(context.Background(), rules, func(r *EdgeRuleResolved) string { return r.AccountID }, "/", http.MethodGet)
	if len(got) != 0 {
		t.Fatalf("rule with a never-matching condition applied: %v", got)
	}
}
