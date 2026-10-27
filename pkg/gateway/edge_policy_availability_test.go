package gateway

// adr: 091 — amendment 2026-10-26: a mutation drops only the cached rule sets
// that can hold its account's rules, and a host whose rules cannot be read or
// served from a last-known set is refused once, before any rule kind runs.

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

func TestEdgeRuleCacheInvalidateAccountHosts(t *testing.T) {
	a := EdgeRuleCacheKey("a.example.com", "app-a")
	a2 := EdgeRuleCacheKey("a2.example.com", "app-a2")
	b := EdgeRuleCacheKey("b.example.com", "app-b")
	unscoped := EdgeRuleCacheKey("u.example.com", "app-u")
	fill := func() *EdgeRuleCache {
		c := NewEdgeRuleCache(10)
		for key, owner := range map[string]string{a: "acct-a", a2: "acct-a", b: "acct-b", unscoped: ""} {
			e := &HostEntry{}
			e.SetOwner(owner)
			c.Put(key, e)
		}
		return c
	}
	cached := func(c *EdgeRuleCache) map[string]bool {
		return map[string]bool{"a": c.HasHost(a), "a2": c.HasHost(a2), "b": c.HasHost(b), "u": c.HasHost(unscoped)}
	}
	for _, tc := range []struct {
		name     string
		account  string
		app      string
		patterns []string
		want     map[string]bool
	}{
		{name: `"*" by account A keeps account B`, account: "acct-a", app: "app-a", patterns: []string{"*"},
			want: map[string]bool{"a": false, "a2": false, "b": true, "u": false}},
		{name: `"@app" drops only the rule's app`, account: "acct-a", app: "app-a", patterns: []string{api.EdgeRuleAppHosts},
			want: map[string]bool{"a": false, "a2": true, "b": true, "u": true}},
		{name: "an exact host drops only that host", account: "acct-b", app: "app-b", patterns: []string{"b.example.com"},
			want: map[string]bool{"a": true, "a2": true, "b": false, "u": true}},
		{name: "no account falls back to every owner", patterns: []string{"*"},
			want: map[string]bool{"a": false, "a2": false, "b": false, "u": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fill()
			c.InvalidateAccountHosts(tc.account, tc.app, tc.patterns)
			got := cached(c)
			for host, want := range tc.want {
				if got[host] != want {
					t.Errorf("%s cached = %v, want %v (all: %v)", host, got[host], want, got)
				}
			}
		})
	}
}

// unavailablePolicyMatcher is a stub matcher that also reports whether the
// host's rules could be read.
type unavailablePolicyMatcher struct {
	stubEdgeRuleMatcher
	available bool
}

func (m unavailablePolicyMatcher) EdgePolicyAvailable(context.Context, string) bool {
	return m.available
}

func TestEdgePolicyUnavailableRefusesBeforeAnyGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available bool
		wantCode  int
	}{
		{name: "unreadable rules are refused before the ip gate", available: false, wantCode: http.StatusServiceUnavailable},
		{name: "readable rules reach the ip gate", available: true, wantCode: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "p.example.com", upstream: "127.0.0.1:0", running: true}
			b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			h.SetWakeGateHook()
			_, denyNet, _ := net.ParseCIDR("203.0.113.0/24")
			h.WithEdgeRules(unavailablePolicyMatcher{available: tc.available, stubEdgeRuleMatcher: stubEdgeRuleMatcher{
				ip: &EdgeRuleIPResolved{ID: "rule-deny", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denyNet}},
			}}, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "http://p.example.com/admin", nil)
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body, tc.wantCode)
			}
			if tc.available {
				return
			}
			if !strings.Contains(rec.Body.String(), "Edge security unavailable") {
				t.Fatalf("body = %s", rec.Body)
			}
			body := bodyForCounter(t, h.metrics)
			for _, want := range []string{
				`gateway_edge_rejections_total{app="app-1",kind="policy_unavailable",status="503"} 1`,
				`gateway_edge_rule_apply_total{kind="policy",result="error"} 1`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("metrics missing %s", want)
				}
			}
		})
	}
}
