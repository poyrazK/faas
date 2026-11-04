package gateway

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A tenant whose apps fill the gateway's boxes gives up its own boxes: a
// full gateway drops the soonest-ending box of the app holding the most,
// so another app keeps its box and can still box new clients.
func TestPenaltyBoxFullGatewayEvictsTheLargestApp(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	b := penaltyBoxAt(&now)
	p := api.EdgeRulePenalty{Seconds: 3600, After: 1, WithinSeconds: 60}
	victim := net.ParseIP("198.51.100.1")
	if !b.Hit("victim", "r", victim, p) {
		t.Fatal("the victim's first box was refused")
	}
	for n := 1; n < api.EdgePenaltyBoxMaxEntries; n++ {
		app := "flood-" + strconv.Itoa(n/api.EdgePenaltyBoxMaxPerApp)
		b.Hit(app, "r", net.IPv4(10, byte(n>>16), byte(n>>8), byte(n)), p)
	}
	if b.count.Load() != api.EdgePenaltyBoxMaxEntries {
		t.Fatalf("count = %d, want the gateway full", b.count.Load())
	}
	if !b.Hit("victim", "r", net.ParseIP("198.51.100.2"), p) || !b.Hit("flood-new", "r", net.ParseIP("192.0.2.1"), p) {
		t.Fatal("a full gateway refused a box")
	}
	if _, _, ok := b.Boxed("victim", victim); !ok {
		t.Fatal("a full gateway dropped the smallest app's box")
	}
	if b.count.Load() != api.EdgePenaltyBoxMaxEntries {
		t.Fatalf("count = %d, want the cap", b.count.Load())
	}
}

// One app's flood of counting clients cannot crowd out another app's
// counts: past EdgePenaltyHitsMaxPerApp, only the flooded app stops
// counting new clients.
func TestPenaltyBoxCountsArePerAppBounded(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	b := penaltyBoxAt(&now)
	p := api.EdgeRulePenalty{Seconds: 600, After: 2, WithinSeconds: 600}
	for n := range api.EdgePenaltyHitsMaxPerApp {
		b.Hit("flood", "r", net.IPv4(10, byte(n>>16), byte(n>>8), byte(n)), p)
	}
	fresh := net.ParseIP("192.0.2.1")
	if b.Hit("flood", "r", fresh, p) || b.Hit("flood", "r", fresh, p) {
		t.Fatal("the flooded app counted a client past its bound")
	}
	other := net.ParseIP("192.0.2.2")
	if b.Hit("other", "r", other, p) || !b.Hit("other", "r", other, p) {
		t.Fatal("another app's client was not boxed on its second refusal")
	}
	// Counts whose window ended free their app's room.
	now = now.Add(601 * time.Second)
	b.mu.Lock()
	b.sweepHitsLocked(now)
	b.mu.Unlock()
	if b.Hit("flood", "r", fresh, p) || !b.Hit("flood", "r", fresh, p) {
		t.Fatal("the flooded app did not count again once its counts ended")
	}
}

// A browser's cross-site request, such as an <img> a third-party page
// embeds, is still refused by the rule but never counts toward a box, so
// that page cannot get the app's real visitors boxed.
func TestPenaltyBoxIgnoresCrossSiteRefusals(t *testing.T) {
	b := &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "p.example.com", upstream: "127.0.0.1:0", running: true}
	b.targets = append(b.targets, Target{NodeID: b.upstream, InstanceID: "i-fake"})
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.SetWakeGateHook()
	_, denyNet, _ := net.ParseCIDR("203.0.113.0/24")
	h.WithEdgeRules(penaltyMatcher{
		stubEdgeRuleMatcher: stubEdgeRuleMatcher{ip: &EdgeRuleIPResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Deny: []*net.IPNet{denyNet}}},
		penalties:           map[string]api.EdgeRulePenalty{"rule-1": {Seconds: 600, After: 1, WithinSeconds: 60}},
	}, nil, nil)
	box := NewPenaltyBox()
	h.WithPenaltyBox(box)
	get := func(site string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://p.example.com/", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for range 3 {
		if rec := get("cross-site"); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), api.CodePenaltyBox) {
			t.Fatalf("cross-site refusal = %d %s, want the rule's own 403", rec.Code, rec.Body)
		}
	}
	if _, _, ok := box.Boxed("app-1", net.ParseIP("203.0.113.7")); ok {
		t.Fatal("cross-site refusals boxed the client")
	}
	get("same-origin")
	if _, _, ok := box.Boxed("app-1", net.ParseIP("203.0.113.7")); !ok {
		t.Fatal("a same-origin refusal did not box the client")
	}
}
