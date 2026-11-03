package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type edgeGateTestRule struct{ id string }

func (r *edgeGateTestRule) edgeGateRuleID() string { return r.id }

// adr: 091 — an access gate enforces the rule for the path the client sent
// and the rule for the rewritten path (amendment 2026-11-02, review round 8;
// amendment 2026-11-03, review round 9).
func TestMatchEdgeGatesReturnsTheClientAndTheRewrittenPathRules(t *testing.T) {
	rules := map[string]*edgeGateTestRule{
		"/api/users":      {"er_api"},
		"/internal/users": {"er_internal"},
		"/legacy/users":   {"er_legacy"},
		"/both":           {"er_both"},
		"/both-rewritten": {"er_both"},
	}
	match := func(_ context.Context, path string) *edgeGateTestRule { return rules[path] }
	ids := func(got []*edgeGateTestRule) []string {
		out := make([]string, 0, len(got))
		for _, rule := range got {
			out = append(out, rule.id)
		}
		return out
	}
	tests := []struct {
		name, client, rewritten string
		want                    []string
	}{
		{"rewrite took the matched prefix away", "/api/users", "/users", []string{"er_api"}},
		{"only the rewritten path has a rule", "/old/users", "/internal/users", []string{"er_internal"}},
		{"both paths have a rule: both apply, client first", "/legacy/users", "/internal/users", []string{"er_legacy", "er_internal"}},
		{"one rule matching both paths applies once", "/both", "/both-rewritten", []string{"er_both"}},
		{"no rewrite", "/api/users", "", []string{"er_api"}},
		{"no rule", "/other", "/elsewhere", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.client, nil)
			r = withLifecycleRequestRoute(r, r.URL.Path, r.Method)
			if tc.rewritten != "" {
				r.URL.Path = tc.rewritten
			}
			got := ids(matchEdgeGates(r, match))
			if len(got) != len(tc.want) {
				t.Fatalf("rules = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("rules = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestMergeJWTClaimsKeepsEarlierRulesClaims(t *testing.T) {
	got := mergeJWTClaims(map[string]string{"tenant": "a"}, map[string]string{"plan": "pro"})
	if got["tenant"] != "a" || got["plan"] != "pro" || len(got) != 2 {
		t.Fatalf("merged = %v", got)
	}
	if got := mergeJWTClaims(nil, map[string]string{"plan": "pro"}); got["plan"] != "pro" {
		t.Fatalf("merged = %v", got)
	}
}

type pathIPMatcher struct {
	stubEdgeRuleMatcher
	byPrefix map[string]*EdgeRuleIPResolved
}

func (m pathIPMatcher) MatchIP(_ context.Context, _, path, _ string) *EdgeRuleIPResolved {
	for prefix, rule := range m.byPrefix {
		if strings.HasPrefix(path, prefix) {
			return rule
		}
	}
	return nil
}

// adr: 091 — after a rewrite, an ip rule written for the rewritten path still
// applies when another ip rule matches the client's path (amendment
// 2026-11-03, review round 9): an allowlist on /legacy/** no longer shadows a
// deny on /internal/**.
func TestIPGateEnforcesBothPathsRulesAfterRewrite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	_, client, _ := net.ParseCIDR("198.51.100.0/24")
	b := &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithEdgeRules(pathIPMatcher{
		stubEdgeRuleMatcher: stubEdgeRuleMatcher{rewrite: &EdgeRuleRewriteResolved{
			ID: "rewrite", AccountID: "acct-1", AppID: "app-1", PathGlob: "/legacy/**", From: "/legacy", To: "/internal",
		}},
		byPrefix: map[string]*EdgeRuleIPResolved{
			"/legacy/":   {ID: "legacy-allow", AccountID: "acct-1", AppID: "app-1", Allow: []*net.IPNet{client}},
			"/internal/": {ID: "internal-deny", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{client}},
		},
	}, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/legacy/users", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body %q, want 403 from the /internal/** deny", rec.Code, rec.Body)
	}
}
