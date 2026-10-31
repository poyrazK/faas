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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func penaltyBoxAt(now *time.Time) *PenaltyBox {
	b := NewPenaltyBox()
	b.now = func() time.Time { return *now }
	return b
}

// adr: 20261022105846 — a client is boxed once the rule refused it After
// times within the window; an older refusal starts a new window; the box
// ends on time; an IPv6 client is its /64.
func TestPenaltyBoxThresholdWindowAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	b := penaltyBoxAt(&now)
	p := api.EdgeRulePenalty{Seconds: 600, After: 3, WithinSeconds: 60}
	ip := net.ParseIP("203.0.113.9")

	if b.Hit("app", "r1", ip, p) || b.Hit("app", "r1", ip, p) {
		t.Fatal("boxed before the threshold")
	}
	now = now.Add(61 * time.Second) // the window ran out; counting restarts
	if b.Hit("app", "r1", ip, p) || b.Hit("app", "r1", ip, p) {
		t.Fatal("boxed on refusals from an expired window")
	}
	if !b.Hit("app", "r1", ip, p) {
		t.Fatal("third refusal within the window did not box")
	}
	if rule, left, ok := b.Boxed("app", ip); !ok || rule != "r1" || left != 600*time.Second {
		t.Fatalf("Boxed = %q %v %v", rule, left, ok)
	}
	if _, _, ok := b.Boxed("other-app", ip); ok {
		t.Fatal("box leaked to another app")
	}
	now = now.Add(600 * time.Second)
	if _, _, ok := b.Boxed("app", ip); ok || b.count.Load() != 0 {
		t.Fatalf("box outlived its penalty (count %d)", b.count.Load())
	}

	v6 := api.EdgeRulePenalty{Seconds: 60, After: 1, WithinSeconds: 60}
	if !b.Hit("app", "r1", net.ParseIP("2001:db8:1:2::1"), v6) {
		t.Fatal("ipv6 refusal did not box")
	}
	if _, _, ok := b.Boxed("app", net.ParseIP("2001:db8:1:2:ffff::9")); !ok {
		t.Fatal("another address in the same /64 is not boxed")
	}
	if _, _, ok := b.Boxed("app", net.ParseIP("2001:db8:1:3::1")); ok {
		t.Fatal("a different /64 is boxed")
	}
	if _, _, ok := b.Boxed("app", nil); ok || b.Hit("app", "r1", nil, v6) {
		t.Fatal("a request without a client IP was boxed")
	}
}

// adr: 20261022105846 — a full app drops its soonest-ending box rather than
// refusing new ones, and boxing a held client keeps the later end.
func TestPenaltyBoxPerAppCap(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	b := penaltyBoxAt(&now)
	short := api.EdgeRulePenalty{Seconds: 60, After: 1, WithinSeconds: 60}
	long := api.EdgeRulePenalty{Seconds: 3600, After: 1, WithinSeconds: 60}
	first := net.ParseIP("10.0.0.0")
	b.Hit("app", "r", first, short)
	for i := 1; i < api.EdgePenaltyBoxMaxPerApp; i++ {
		b.Hit("app", "r", net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)), long)
	}
	if b.count.Load() != api.EdgePenaltyBoxMaxPerApp {
		t.Fatalf("count = %d", b.count.Load())
	}
	if !b.Hit("app", "r", net.ParseIP("192.0.2.1"), long) {
		t.Fatal("a full app refused a new box")
	}
	if _, _, ok := b.Boxed("app", first); ok {
		t.Fatal("the soonest-ending box was not the one dropped")
	}
	if b.count.Load() != api.EdgePenaltyBoxMaxPerApp {
		t.Fatalf("count after drop = %d, want the cap", b.count.Load())
	}
	b.Hit("app", "r", net.ParseIP("192.0.2.1"), short)
	if _, left, _ := b.Boxed("app", net.ParseIP("192.0.2.1")); left != time.Hour {
		t.Fatalf("re-boxing shortened a box to %v", left)
	}
}

// penaltyMatcher is the stub matcher plus rule penalties.
type penaltyMatcher struct {
	stubEdgeRuleMatcher
	penalties map[string]api.EdgeRulePenalty
}

func (m penaltyMatcher) RulePenalty(_ context.Context, _, ruleID string) (api.EdgeRulePenalty, bool) {
	p, ok := m.penalties[ruleID]
	return p, ok
}

// adr: 20261022105846 — an IP rule with a penalty boxes a client after two
// refusals; the box then refuses that client on every path with 403
// penalty_box and Retry-After, leaves other clients alone, and lets go once
// the rule loses its penalty.
func TestPenaltyBoxRefusesBoxedClientAcrossPaths(t *testing.T) {
	b := &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "p.example.com",
		upstream: "127.0.0.1:0",
		running:  true,
	}
	b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.SetWakeGateHook()
	_, denyNet, _ := net.ParseCIDR("203.0.113.0/24")
	matcher := penaltyMatcher{
		stubEdgeRuleMatcher: stubEdgeRuleMatcher{ip: &EdgeRuleIPResolved{
			ID: "rule-scanners", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denyNet},
		}},
		penalties: map[string]api.EdgeRulePenalty{"rule-scanners": {Seconds: 600, After: 2, WithinSeconds: 60}},
	}
	h.WithEdgeRules(matcher, nil, nil)
	box := NewPenaltyBox()
	h.WithPenaltyBox(box)

	get := func(path, ip string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://p.example.com"+path, nil)
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 2; i++ {
		if rec := get("/wp-login.php", "203.0.113.7"); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), api.CodePenaltyBox) {
			t.Fatalf("refusal %d: %d %s, want the rule's own 403", i+1, rec.Code, rec.Body)
		}
	}
	rec := get("/", "203.0.113.7")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodePenaltyBox) ||
		!strings.Contains(rec.Body.String(), "rule-scanners") || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("boxed client: %d %s (Retry-After %q)", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	if rec := get("/", "198.51.100.1"); strings.Contains(rec.Body.String(), api.CodePenaltyBox) {
		t.Fatal("another client was refused by the box")
	}
	body := bodyForCounter(t, h.metrics)
	for _, want := range []string{
		`gateway_edge_penalty_total{app="app-1",outcome="boxed"} 1`,
		`gateway_edge_penalty_total{app="app-1",outcome="rejected"} 1`,
		`gateway_edge_rejections_total{app="app-1",kind="penalty",status="403"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}

	// The rule's penalty is removed: the client is no longer boxed, and is
	// judged by the rule alone again.
	matcher.penalties = nil
	h.WithEdgeRules(matcher, nil, nil)
	if rec := get("/", "203.0.113.7"); strings.Contains(rec.Body.String(), api.CodePenaltyBox) {
		t.Fatalf("box outlived the rule's penalty: %s", rec.Body)
	}
	// Deployment smoke checks are never boxed.
	if h.applyEdgePenaltyBox(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://p.example.com/", nil), b.app, true) {
		t.Fatal("a deployment smoke check was refused")
	}
}
