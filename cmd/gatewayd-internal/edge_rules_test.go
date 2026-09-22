// Tests for cmd/gatewayd-internal/edge_rules.go (PR 3 of the Edge
// Rules rollout, ADR-089 / issue #561). Pins the production wiring:
//   - compileRouteRules filters store rules to kind=route,
//     compiles methods into a lookup map, and sorts priority-ASC.
//   - gatewaydEdgeRules.MatchRoute goes cache → store (on miss) →
//     compile → Put → PickFirstRouteMatch.
//   - gatewaydEdgeRules.Reset drops the cache.
//   - gatewaydEdgeRulesAud.Emit is a thin wrapper over *gatewaydAuditor.
//
// Mirrors cmd/gatewayd-internal/backend_test.go shape (small fakeStore
// + sync.Mutex for race). The pg-backed integration (live invalidation
// via pg_notify, cross-account blocked) lives in pkg/state tests; this
// file stays cmd-side only.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeEdgeRuleStore is the test double for edgeRuleStore. It returns
// canned rule slices keyed by host; an optional `err` returns a
// synthetic error so loader failures are exercised.
type fakeEdgeRuleStore struct {
	mu       sync.Mutex
	rules    map[string][]state.EdgeRule
	presetBy map[string]state.CorsPreset // accountID+":"+id → CorsPreset, keyed by composite
	err      error
	calls    map[string]int // host -> call count, for loader-frequency assertions
}

func (f *fakeEdgeRuleStore) MatchEdgeRulesForHost(_ context.Context, host string) ([]state.EdgeRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	return f.rules[host], nil
}

// GetCorsPresetByID mirrors state.Store.GetCorsPresetByID for tests:
// cross-tenant lookups collapse to ErrNotFound (account_id + id
// composite key) so PR-B IDOR regressions surface in tests. Issue
// #975 #4 PR-B / ADR-129 D3.
func (f *fakeEdgeRuleStore) GetCorsPresetByID(_ context.Context, accountID, id string) (state.CorsPreset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if preset, ok := f.presetBy[accountID+":"+id]; ok {
		return preset, nil
	}
	return state.CorsPreset{}, state.ErrNotFound
}

// sampleRouteRule builds a fully-populated state.EdgeRule with the
// given id/priority/host/path/methods and a kind=route action whose
// TargetAppSlug is the supplied slug.
func sampleRouteRule(id string, priority int, host, path string, methods []string, targetSlug string) state.EdgeRule {
	return state.EdgeRule{
		ID:           id,
		AccountID:    "acc_test",
		AppID:        "app_test",
		MatchHost:    host,
		MatchPath:    path,
		MatchMethods: methods,
		Priority:     priority,
		Enabled:      true,
		Kind:         state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{
			Kind:  state.EdgeRuleKindRoute,
			Route: &state.EdgeRuleRouteAction{TargetAppSlug: targetSlug},
		},
	}
}

// --- compileRouteRules ---------------------------------------------------

func TestCompileRouteRules_KeepsOnlyKindRoute(t *testing.T) {
	// PR 3 loader filters out non-route kinds. PR 4-7 widen the
	// filter to compile their own kinds; this test pins PR 3.
	in := []state.EdgeRule{
		sampleRouteRule("route-1", 100, "a.example.com", "/", nil, "demo"),
		{
			ID: "rewrite-1", AccountID: "acc_test", AppID: "app_test",
			MatchHost: "a.example.com", Priority: 0, Enabled: true,
			Kind: state.EdgeRuleKindRewrite,
			Action: state.EdgeRuleAction{
				Kind:    state.EdgeRuleKindRewrite,
				Rewrite: &state.EdgeRuleRewriteAction{From: "/api", To: "/v1"},
			},
		},
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rules, want 1 (kind filter)", len(got))
	}
	if got[0].ID != "route-1" {
		t.Errorf("got %q, want route-1", got[0].ID)
	}
}

func TestCompileRouteRules_SkipsDisabled(t *testing.T) {
	// Disabled rules must NEVER steer traffic. apid toggles
	// Enable=false to deactivate a rule without losing the row.
	in := []state.EdgeRule{
		sampleRouteRule("route-on", 100, "a.example.com", "/", nil, "demo"),
		func() state.EdgeRule {
			r := sampleRouteRule("route-off", 0, "a.example.com", "/", nil, "demo")
			r.Enabled = false
			return r
		}(),
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (disabled filtered)", len(got))
	}
	if got[0].ID != "route-on" {
		t.Errorf("got %q, want route-on (priority 0 disabled rule dropped)", got[0].ID)
	}
}

func TestCompileRouteRules_SortsPriorityAscending(t *testing.T) {
	// Loader MUST sort before Put so PickFirstRouteMatch returns
	// the lowest-priority (earliest) match. The store returns
	// rows in arbitrary order; the compile step is the only
	// place this invariant lives.
	in := []state.EdgeRule{
		sampleRouteRule("low", 100, "a.example.com", "/", nil, "demo"),
		sampleRouteRule("high", 0, "a.example.com", "/", nil, "demo"),
		sampleRouteRule("mid", 50, "a.example.com", "/", nil, "demo"),
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0].Priority != 0 || got[1].Priority != 50 || got[2].Priority != 100 {
		t.Errorf("priority order = [%d %d %d], want [0 50 100]",
			got[0].Priority, got[1].Priority, got[2].Priority)
	}
}

func TestCompileRouteRules_CompilesMethodsLookupTable(t *testing.T) {
	// The per-request methods filter is O(1) via the map; the
	// loader is the only place the slice → map conversion happens.
	in := []state.EdgeRule{
		sampleRouteRule("r", 0, "a.example.com", "/", []string{"get", "POST"}, "demo"),
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if !got[0].Methods["GET"] {
		t.Errorf("GET not in methods (must be upper-cased)")
	}
	if !got[0].Methods["POST"] {
		t.Errorf("POST not in methods")
	}
	if got[0].Methods["DELETE"] {
		t.Errorf("DELETE in methods, must NOT be present")
	}
}

func TestCompileRouteRules_EmptyInputProducesEmptyOutput(t *testing.T) {
	// Edge case: store returns no rules → Put is a no-op (the
	// gateway cache drops empty slices); the loader must not
	// allocate a zero-length slice header that gets Put.
	if got, parseErrs := compileRouteRules(nil); got != nil || parseErrs != nil {
		t.Errorf("compileRouteRules(nil) = %v, %v, want nil, nil", got, parseErrs)
	}
	if got, parseErrs := compileRouteRules([]state.EdgeRule{}); got != nil || parseErrs != nil {
		t.Errorf("compileRouteRules([]) = %v, %v, want nil, nil", got, parseErrs)
	}
}

func TestCompileThrottleRules_PreservesDimensionalPolicy(t *testing.T) {
	in := []state.EdgeRule{{
		ID: "rule-throttle", AccountID: "acct-1", AppID: "app-1",
		MatchHost: "api.example.com", MatchPath: "/v1/*", Priority: 10, Enabled: true,
		Kind: state.EdgeRuleKindThrottle,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindThrottle,
			Throttle: &state.EdgeRuleThrottleAction{
				RequestsPerSecond: 10,
				Burst:             20,
				KeyBy:             api.ThrottleKeyByCountry,
				MaxKeysPerRule:    250,
				MissingKeyPolicy:  api.ThrottleMissingKeyReject,
			},
		},
	}}

	got, parseErrs := compileThrottleRules(in)
	if len(parseErrs) != 0 {
		t.Fatalf("compileThrottleRules parse errors = %v", parseErrs)
	}
	if len(got) != 1 {
		t.Fatalf("compileThrottleRules length = %d, want 1", len(got))
	}
	if got[0].KeyBy != api.ThrottleKeyByCountry || got[0].MaxKeysPerRule != 250 {
		t.Errorf("dimension = (%q, %d), want (%q, 250)", got[0].KeyBy, got[0].MaxKeysPerRule, api.ThrottleKeyByCountry)
	}
	if got[0].MissingKeyPolicy != api.ThrottleMissingKeyReject {
		t.Errorf("missing_key_policy = %q, want %q", got[0].MissingKeyPolicy, api.ThrottleMissingKeyReject)
	}
}

// TestCompileRouteRules_MalformedGlobDroppedAndReported (review fix R3)
// pins the new behaviour: a rule whose MatchPath fails stdlib
// path.Match validation (unmatched bracket, etc.) is dropped
// from the compiled slice AND returned via the parseErrs slice
// so the loader can log it. Prior behaviour was silent — the
// rule made it into the cache and silently never matched any
// request, with no operator-visible signal.
func TestCompileRouteRules_MalformedGlobDroppedAndReported(t *testing.T) {
	in := []state.EdgeRule{
		sampleRouteRule("good", 100, "a.example.com", "/api/*", nil, "demo"),
		func() state.EdgeRule {
			r := sampleRouteRule("bad", 50, "a.example.com", "[unmatched", nil, "demo")
			return r
		}(),
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 1 {
		t.Fatalf("parseErrs = %v, want 1 entry", parseErrs)
	}
	if parseErrs[0].RuleID != "bad" {
		t.Errorf("parseErrs[0].RuleID = %q, want bad", parseErrs[0].RuleID)
	}
	if parseErrs[0].Glob != "[unmatched" {
		t.Errorf("parseErrs[0].Glob = %q, want [unmatched", parseErrs[0].Glob)
	}
	if parseErrs[0].Err == nil {
		t.Errorf("parseErrs[0].Err = nil, want non-nil")
	}
	if len(got) != 1 {
		t.Fatalf("got len = %d, want 1 (malformed rule dropped)", len(got))
	}
	if got[0].ID != "good" {
		t.Errorf("got[0].ID = %q, want good", got[0].ID)
	}
}

// TestCompileRouteRules_EmptyAndStarGlobsAccepted (review fix R3)
// pins the match-all sentinels: MatchPath="" and MatchPath="*"
// must NOT trip the parser (stdlib path.Match rejects both as
// errors on the empty-input probe).
func TestCompileRouteRules_EmptyAndStarGlobsAccepted(t *testing.T) {
	in := []state.EdgeRule{
		sampleRouteRule("empty", 100, "a.example.com", "", nil, "demo"),
		sampleRouteRule("star", 50, "a.example.com", "*", nil, "demo"),
	}
	got, parseErrs := compileRouteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty (match-all sentinels)", parseErrs)
	}
	if len(got) != 2 {
		t.Fatalf("got len = %d, want 2", len(got))
	}
}

// --- gatewaydEdgeRules (cache + loader) ---------------------------------

func newQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGatewaydEdgeRules_MatchRoute_CacheMissHitsStore(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-1", 100, "a.example.com", "/", nil, "demo"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET")
	if got == nil {
		t.Fatalf("MatchRoute = nil, want rule")
	}
	if got.ID != "route-1" {
		t.Errorf("got %q, want route-1", got.ID)
	}
	if got.TargetAppSlug != "demo" {
		t.Errorf("got target %q, want demo", got.TargetAppSlug)
	}
	if store.calls["a.example.com"] != 1 {
		t.Errorf("store calls = %d, want 1", store.calls["a.example.com"])
	}
}

func TestGatewaydEdgeRules_MatchRoute_CacheHitSkipsStore(t *testing.T) {
	// Second MatchRoute on the same host must NOT re-hit the
	// store — that's the whole point of the LRU. Pin the
	// invariant so a future refactor (e.g. eagerly revalidating
	// on every request) doesn't silently widen the hit window.
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-1", 100, "a.example.com", "/", nil, "demo"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	for i := 0; i < 5; i++ {
		_ = g.MatchRoute(context.Background(), "a.example.com", "/", "GET")
	}
	if store.calls["a.example.com"] != 1 {
		t.Errorf("store calls = %d, want 1 (cache hit should skip store)", store.calls["a.example.com"])
	}
}

func TestGatewaydEdgeRules_MatchRoute_StoreErrorIsMiss(t *testing.T) {
	// A loader failure is a transient miss — return nil and
	// log. The handler then falls through to Backend.Lookup;
	// the customer sees a 404, not a 500. (A future PR could
	// add a last-error gauge; PR 3 stays silent.)
	store := &fakeEdgeRuleStore{err: errors.New("pg unreachable")}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	if got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET"); got != nil {
		t.Errorf("MatchRoute on store error = %v, want nil", got)
	}
}

func TestGatewaydEdgeRules_ResetDropsCache(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-1", 100, "a.example.com", "/", nil, "demo"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	_ = g.MatchRoute(context.Background(), "a.example.com", "/", "GET")
	if g.cache.Len() != 1 {
		t.Fatalf("len before Reset = %d, want 1", g.cache.Len())
	}
	g.Reset()
	if g.cache.Len() != 0 {
		t.Errorf("len after Reset = %d, want 0", g.cache.Len())
	}
	if store.calls["a.example.com"] != 1 {
		t.Errorf("Reset called = %d, want 1 (Reset must not re-hit store)", store.calls["a.example.com"])
	}
	// Next MatchRoute re-hits store.
	_ = g.MatchRoute(context.Background(), "a.example.com", "/", "GET")
	if store.calls["a.example.com"] != 2 {
		t.Errorf("store calls post-Reset = %d, want 2", store.calls["a.example.com"])
	}
}

// --- PR 6: cmd-side Reset forward across all 7 kinds (ADR-091 D17) ----
//
// Mirrors TestGatewaydEdgeRules_ResetDropsCache at line 300 but
// seeds all 7 edge-rule kinds on a single host. Pins the
// production contract that g.Reset() is a one-line forward to
// g.cache.Reset() (cmd/gatewayd-internal/edge_rules.go) —
// store.calls must stay at 1 across the Reset (no re-hit) AND
// every kind's Match* must miss until the next loadHost.

// sampleAllSevenKindsRules builds a single-host state.EdgeRule
// slice covering all 7 kinds (route / rewrite / redirect / headers
// / cors / jwt / ip). Mirrors the cmd-side loadHost shape: one
// SQL roundtrip returns every kind together; this helper exercises
// that single-pass contract.
func sampleAllSevenKindsRules(host string) []state.EdgeRule {
	return []state.EdgeRule{
		sampleRouteRule("route-1", 0, host, "", nil, "demo"),
		sampleRewriteActionRule("rewrite-1", 0, host, "", nil, "/api", "/v2"),
		sampleRedirectActionRule("redirect-1", 0, host, "", nil, 308, "https://b", nil),
		sampleHeadersActionRule("headers-1", 0, host, "", nil, nil, nil),
		{
			ID: "cors-1", AccountID: "acc_test", AppID: "app_test",
			MatchHost: host, MatchPath: "", Priority: 0, Enabled: true,
			Kind: state.EdgeRuleKindCORSA,
			Action: state.EdgeRuleAction{
				Kind: state.EdgeRuleKindCORSA,
				CORS: &state.EdgeRuleCORSAction{
					AllowOrigins: []string{"https://app.test"},
					AllowMethods: []string{"GET", "POST"},
				},
			},
		},
		{
			ID: "jwt-1", AccountID: "acc_test", AppID: "app_test",
			MatchHost: host, MatchPath: "", Priority: 0, Enabled: true,
			Kind: state.EdgeRuleKindJWT,
			Action: state.EdgeRuleAction{
				Kind: state.EdgeRuleKindJWT,
				JWT: &state.EdgeRuleJWTAction{
					Issuer:     "https://idp.example.com/",
					JWKSURL:    "https://idp.example.com/.well-known/jwks.json",
					Algorithms: []string{"RS256"},
				},
			},
		},
		{
			ID: "ip-1", AccountID: "acc_test", AppID: "app_test",
			MatchHost: host, MatchPath: "", Priority: 0, Enabled: true,
			Kind: state.EdgeRuleKindIP,
			Action: state.EdgeRuleAction{
				Kind: state.EdgeRuleKindIP,
				IP: &state.EdgeRuleIPAction{
					Allow: []string{"10.0.0.0/8"},
				},
			},
		},
	}
}

func TestGatewaydEdgeRules_ResetForwardsToCache_AcrossAllSevenKinds(t *testing.T) {
	// PR 6 D17: production gatewaydEdgeRules.Reset() must forward
	// to cache.Reset() without re-hitting the store. The test
	// populates the cache with all 7 kinds on a single host (one
	// Match* triggers one loadHost which compiles all 7 kinds at
	// once), calls Reset(), and asserts:
	//
	//  1. cache.Len() == 0 (Reset dropped the entry)
	//  2. store.calls[host] == 1 (Reset did NOT re-hit the store)
	//  3. the cache stays at Len() == 0 immediately after Reset
	//     (the wholesale invariant — the property test in
	//     pkg/gateway/edge_rules_test.go pins the cache primitive)
	//  4. the next Match* re-hits the store exactly once and the
	//     single loadHost repopulates all 7 kinds at once — every
	//     per-kind Match* after that is cache-served (store.calls
	//     stays at 2).
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": sampleAllSevenKindsRules("a.example.com"),
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)

	// Prime the cache via MatchRoute (one loadHost compiles all 7
	// kinds together into one HostEntry).
	if got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Fatalf("MatchRoute pre-Reset returned nil; want route-1")
	}
	if g.cache.Len() != 1 {
		t.Fatalf("len before Reset = %d, want 1 (one HostEntry covering 7 kinds)", g.cache.Len())
	}
	if store.calls["a.example.com"] != 1 {
		t.Fatalf("store calls pre-Reset = %d, want 1", store.calls["a.example.com"])
	}

	// The Reset contract under test.
	g.Reset()
	if g.cache.Len() != 0 {
		t.Errorf("len after Reset = %d, want 0 (Reset must wipe the whole HostEntry)", g.cache.Len())
	}
	if store.calls["a.example.com"] != 1 {
		t.Errorf("Reset re-hit store: calls = %d, want 1 (Reset is a pure forward)", store.calls["a.example.com"])
	}

	// The next Match* triggers a fresh loadHost (one SQL roundtrip
	// + compile every kind). All 7 kinds' Match* must now return a
	// hit because the single loadHost populated all of them.
	if got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Errorf("post-Reset MatchRoute returned nil; want route-1 (cache should repopulate)")
	}
	if store.calls["a.example.com"] != 2 {
		t.Errorf("store calls post-Reset MatchRoute = %d, want 2 (re-populate should re-hit)", store.calls["a.example.com"])
	}
	if g.cache.Len() != 1 {
		t.Errorf("len after post-Reset MatchRoute = %d, want 1 (cache repopulated)", g.cache.Len())
	}
	// Every kind's Match* must now hit because one loadHost
	// populated all 7 kinds.
	if got := g.MatchRewrite(context.Background(), "a.example.com", "/api", "GET"); got == nil {
		t.Errorf("post-repopulate MatchRewrite = nil, want hit (single loadHost compiles all 7)")
	}
	if got := g.MatchRedirect(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Errorf("post-repopulate MatchRedirect = nil, want hit")
	}
	if got := g.MatchHeaders(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Errorf("post-repopulate MatchHeaders = nil, want hit")
	}
	if got := g.MatchCORS(context.Background(), "a.example.com", "/", "OPTIONS"); got == nil {
		t.Errorf("post-repopulate MatchCORS = nil, want hit")
	}
	if got := g.MatchJWT(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Errorf("post-repopulate MatchJWT = nil, want hit")
	}
	if got := g.MatchIP(context.Background(), "a.example.com", "/", "GET"); got == nil {
		t.Errorf("post-repopulate MatchIP = nil, want hit")
	}
	// store.calls should NOT have grown — the per-kind Match* hits
	// are all served from the cache the prior MatchRoute populated.
	if store.calls["a.example.com"] != 2 {
		t.Errorf("per-kind Match* after repopulate triggered store re-hit: calls = %d, want 2", store.calls["a.example.com"])
	}
}

// --- gatewaydEdgeRulesAud (audit thin wrapper) ---------------------------

func TestGatewaydEdgeRulesAud_NilReceiverIsNoOp(t *testing.T) {
	// PR 3 production wires a real *gatewaydAuditor; unit tests
	// that don't exercise the audit path can pass nil. The
	// receiver is a pointer so a nil receiver must NOT panic.
	var aud *gatewaydEdgeRulesAud
	aud.Emit(context.Background(), "edge_rule.route_matched", nil, map[string]any{
		"rule_id": "r1",
	}) // must not panic
}

func TestGatewaydEdgeRulesAud_NilInnerIsNoOp(t *testing.T) {
	// Defence in depth: a wrapper whose inner auditor is nil
	// must drop the row instead of panicking. Same shape as
	// gatewaydAuditor.Emit's nil-self guard.
	aud := &gatewaydEdgeRulesAud{inner: nil}
	aud.Emit(context.Background(), "edge_rule.route_matched", nil, map[string]any{}) // must not panic
}

// --- PR 4 surface: kind=rewrite / kind=redirect / kind=headers -----

func sampleRewriteActionRule(id string, priority int, host, path string, methods []string, from, to string) state.EdgeRule {
	return state.EdgeRule{
		ID:           id,
		AccountID:    "acc_test",
		AppID:        "app_test",
		MatchHost:    host,
		MatchPath:    path,
		MatchMethods: methods,
		Priority:     priority,
		Enabled:      true,
		Kind:         state.EdgeRuleKindRewrite,
		Action: state.EdgeRuleAction{
			Kind:    state.EdgeRuleKindRewrite,
			Rewrite: &state.EdgeRuleRewriteAction{From: from, To: to},
		},
	}
}

func sampleRedirectActionRule(id string, priority int, host, path string, methods []string, status int, to string, hdrs map[string]string) state.EdgeRule {
	return state.EdgeRule{
		ID:           id,
		AccountID:    "acc_test",
		AppID:        "app_test",
		MatchHost:    host,
		MatchPath:    path,
		MatchMethods: methods,
		Priority:     priority,
		Enabled:      true,
		Kind:         state.EdgeRuleKindRedirect,
		Action: state.EdgeRuleAction{
			Kind:     state.EdgeRuleKindRedirect,
			Redirect: &state.EdgeRuleRedirectAction{StatusCode: status, To: to, Headers: hdrs},
		},
	}
}

func sampleHeadersActionRule(id string, priority int, host, path string, methods []string, reqHdrs, respHdrs []state.EdgeRuleHeaderOp) state.EdgeRule {
	return state.EdgeRule{
		ID:           id,
		AccountID:    "acc_test",
		AppID:        "app_test",
		MatchHost:    host,
		MatchPath:    path,
		MatchMethods: methods,
		Priority:     priority,
		Enabled:      true,
		Kind:         state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{
			Kind:    state.EdgeRuleKindHeaders,
			Headers: &state.EdgeRuleHeadersAction{RequestHeaders: reqHdrs, ResponseHeaders: respHdrs},
		},
	}
}

func TestCompileRewriteRules_KeepsOnlyKindRewrite(t *testing.T) {
	in := []state.EdgeRule{
		sampleRewriteActionRule("rewrite-1", 0, "a.example.com", "/", nil, "/api", "/v1"),
		{
			ID: "route-1", AccountID: "acc_test", AppID: "app_test",
			MatchHost: "a.example.com", Priority: 0, Enabled: true,
			Kind: state.EdgeRuleKindRoute,
			Action: state.EdgeRuleAction{
				Kind:  state.EdgeRuleKindRoute,
				Route: &state.EdgeRuleRouteAction{TargetAppSlug: "demo"},
			},
		},
	}
	got, parseErrs := compileRewriteRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 1 || got[0].ID != "rewrite-1" {
		t.Errorf("got %d rules, want 1 (kind filter)", len(got))
	}
	if got[0].From != "/api" || got[0].To != "/v1" {
		t.Errorf("From/To not copied: got %q/%q", got[0].From, got[0].To)
	}
}

func TestCompileRedirectRules_DefaultsStatusCode(t *testing.T) {
	in := []state.EdgeRule{
		sampleRedirectActionRule("redirect-1", 0, "a.example.com", "/", nil, 0, "https://b", nil),
	}
	got, parseErrs := compileRedirectRules(in)
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs = %v, want empty", parseErrs)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].StatusCode != 302 {
		t.Errorf("StatusCode = %d, want 302 (default)", got[0].StatusCode)
	}
}

func TestCompileRedirectRules_CopiesHeaders(t *testing.T) {
	hdrs := map[string]string{"X-Frame-Options": "DENY"}
	in := []state.EdgeRule{
		sampleRedirectActionRule("redirect-1", 0, "a.example.com", "/", nil, 308, "https://b", hdrs),
	}
	got, _ := compileRedirectRules(in)
	if len(got) != 1 || got[0].Headers["X-Frame-Options"] != "DENY" {
		t.Errorf("Headers not copied: got %v", got[0].Headers)
	}
}

func TestCompileHeadersRules_CopiesOpsInOrder(t *testing.T) {
	ops := []state.EdgeRuleHeaderOp{
		{Name: "X-A", Value: "1", Action: "set"},
		{Name: "X-A", Value: "2", Action: "set"},
		{Name: "X-B", Value: "3", Action: "remove"},
	}
	in := []state.EdgeRule{
		sampleHeadersActionRule("headers-1", 0, "a.example.com", "/", nil, ops, ops),
	}
	got, _ := compileHeadersRules(in)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if len(got[0].RequestHeaders) != 3 || got[0].RequestHeaders[1].Value != "2" {
		t.Errorf("RequestHeaders order not preserved: %v", got[0].RequestHeaders)
	}
	if len(got[0].ResponseHeaders) != 3 || got[0].ResponseHeaders[2].Action != "remove" {
		t.Errorf("ResponseHeaders order not preserved: %v", got[0].ResponseHeaders)
	}
}

func TestCompileRewriteRules_EmptyInputProducesEmptyOutput(t *testing.T) {
	if got, parseErrs := compileRewriteRules(nil); got != nil || parseErrs != nil {
		t.Errorf("compileRewriteRules(nil) = %v, %v", got, parseErrs)
	}
}

func TestCompileRedirectRules_EmptyInputProducesEmptyOutput(t *testing.T) {
	if got, parseErrs := compileRedirectRules(nil); got != nil || parseErrs != nil {
		t.Errorf("compileRedirectRules(nil) = %v, %v", got, parseErrs)
	}
}

func TestCompileHeadersRules_EmptyInputProducesEmptyOutput(t *testing.T) {
	if got, parseErrs := compileHeadersRules(nil); got != nil || parseErrs != nil {
		t.Errorf("compileHeadersRules(nil) = %v, %v", got, parseErrs)
	}
}

func TestGatewaydEdgeRules_MatchRewrite_CacheMissHitsStore(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRewriteActionRule("rewrite-1", 0, "a.example.com", "/api/*", nil, "/api", "/v1"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	got := g.MatchRewrite(context.Background(), "a.example.com", "/api/x", "GET")
	if got == nil {
		t.Fatalf("MatchRewrite = nil, want rule")
	}
	if got.From != "/api" || got.To != "/v1" {
		t.Errorf("got From/To %q/%q, want /api/v1", got.From, got.To)
	}
	if store.calls["a.example.com"] != 1 {
		t.Errorf("store calls = %d, want 1", store.calls["a.example.com"])
	}
}

func TestGatewaydEdgeRules_MatchRedirect_CacheMissHitsStore(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRedirectActionRule("redirect-1", 0, "a.example.com", "/", nil, 308, "https://b", nil),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	got := g.MatchRedirect(context.Background(), "a.example.com", "/", "GET")
	if got == nil {
		t.Fatalf("MatchRedirect = nil, want rule")
	}
	if got.StatusCode != 308 || got.To != "https://b" {
		t.Errorf("got status %d to %q, want 308 https://b", got.StatusCode, got.To)
	}
}

func TestGatewaydEdgeRules_MatchHeaders_CacheMissHitsStore(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleHeadersActionRule("headers-1", 0, "a.example.com", "/", nil,
					[]state.EdgeRuleHeaderOp{{Name: "X-Test", Value: "1", Action: "set"}},
					nil,
				),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	got := g.MatchHeaders(context.Background(), "a.example.com", "/", "GET")
	if got == nil {
		t.Fatalf("MatchHeaders = nil, want rule")
	}
	if len(got.RequestHeaders) != 1 || got.RequestHeaders[0].Name != "X-Test" {
		t.Errorf("got RequestHeaders %v, want one X-Test op", got.RequestHeaders)
	}
}

func TestGatewaydEdgeRules_SharedCacheAcrossKinds(t *testing.T) {
	// PR 4 plan D5: a cache miss for any kind recompiles all four
	// kinds together (the SQL roundtrip dominates). Verify by
	// hitting Match* for two kinds on the same host and asserting
	// the store is called exactly once across both hits.
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-1", 100, "a.example.com", "/", nil, "demo"),
				sampleRewriteActionRule("rewrite-1", 50, "a.example.com", "/", nil, "/api", "/v1"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	_ = g.MatchRoute(context.Background(), "a.example.com", "/", "GET")
	_ = g.MatchRewrite(context.Background(), "a.example.com", "/", "GET")
	if store.calls["a.example.com"] != 1 {
		t.Errorf("store calls = %d, want 1 (shared cache recompiles all kinds)", store.calls["a.example.com"])
	}
}

// counterBody scrapes the Prometheus /metrics endpoint of m and returns
// the body as a string. Mirrors pkg/gateway/handler_test.go::bodyForCounter
// but lives cmd-side so the cmd-side compile-error tests don't have to
// import httptest from the handler test package.
func counterBody(t *testing.T, m *gateway.Metrics) string {
	t.Helper()
	if m == nil {
		t.Fatalf("counterBody: nil metrics")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d", rec.Code)
	}
	return rec.Body.String()
}

// TestLoadHost_EmitsCompileErrorCounter is the load-bearing pin for the
// PR-B observability surface. PR-A registered and pre-instantiated
// gateway_edge_rule_compile_error_total{kind} but did not wire call sites;
// PR-B's contract is "one tick per dropped rule, not per host". Drive the
// loader with a malformed-glob route rule and assert the counter shows 1
// for kind=route (and 0 for the other six kinds — pre-instantiated rows
// must always be present in the scrape).
func TestLoadHost_EmitsCompileErrorCounter(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				// Malformed path glob (unmatched bracket). compileRouteRules
				// drops this rule and appends one PathGlobError to routeErrs.
				sampleRouteRule("route-bad", 100, "a.example.com", "[unmatched", nil, "demo"),
			},
		},
	}
	metrics := gateway.NewMetrics()
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, metrics)

	// Cache miss → MatchRoute calls loadHost → compileRouteRules drops
	// route-bad → ObserveEdgeRuleCompileError("route") fires once.
	if got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET"); got != nil {
		t.Fatalf("MatchRoute = %v, want nil (the only route rule was malformed and dropped)", got)
	}

	body := counterBody(t, metrics)
	want := `gateway_edge_rule_compile_error_total{kind="route"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("/metrics body missing %q\n--- body ---\n%s", want, body)
	}
	// Spot-check the other six pre-instantiated rows are at 0 (PR-A
	// pre-instantiates all seven; PR-B only ticked one).
	for _, kind := range []string{"rewrite", "redirect", "headers", "cors", "jwt", "ip"} {
		row := fmt.Sprintf(`gateway_edge_rule_compile_error_total{kind=%q} 0`, kind)
		if !strings.Contains(body, row) {
			t.Errorf("/metrics body missing pre-instantiated row %q\n--- body ---\n%s", row, body)
		}
	}
}

// TestLoadHost_NilMetrics_DoesNotPanic is the nil-safety tripwire. The
// production daemon always wires metrics via run.go; cmd-side tests
// historically pass nil. PR-B's emit must guard before touching the
// registry — a nil *gateway.Metrics deref would crash the loader and
// take every cache miss with it.
func TestLoadHost_NilMetrics_DoesNotPanic(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-bad", 100, "a.example.com", "[unmatched", nil, "demo"),
			},
		},
	}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil) // nil metrics
	// Must NOT panic. The matcher returns nil because the only rule was dropped.
	if got := g.MatchRoute(context.Background(), "a.example.com", "/", "GET"); got != nil {
		t.Errorf("MatchRoute = %v, want nil", got)
	}
}

// TestLoadHost_EmitsOncePerRuleNotOncePerHost pins the cardinality
// contract. Two malformed route rules under one host must tick the
// counter twice (one per rule), not once (one per host). A bug that
// incremented at the host level would under-report fleet breakage.
func TestLoadHost_EmitsOncePerRuleNotOncePerHost(t *testing.T) {
	store := &fakeEdgeRuleStore{
		rules: map[string][]state.EdgeRule{
			"a.example.com": {
				sampleRouteRule("route-bad-1", 100, "a.example.com", "[unmatched", nil, "demo"),
				sampleRouteRule("route-bad-2", 200, "a.example.com", "[also-bad", nil, "demo"),
			},
		},
	}
	metrics := gateway.NewMetrics()
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, metrics)
	_ = g.MatchRoute(context.Background(), "a.example.com", "/", "GET")

	body := counterBody(t, metrics)
	want := `gateway_edge_rule_compile_error_total{kind="route"} 2`
	if !strings.Contains(body, want) {
		t.Errorf("/metrics body missing %q (want one tick per dropped rule, not per host)\n--- body ---\n%s", want, body)
	}
}

// TestLoadHost_AllKindsCovered pins the kind-switch. If a future
// PR adds a new kind but forgets to add the counter loop, this
// test (extended with the new kind) fails. Today it pins the ten:
// route, rewrite, redirect, headers, cors, jwt, ip, validate,
// limit, budget (the validate widening was PR-B, the limit
// widening was PR-D24, the budget widening is ADR-093).
func TestLoadHost_AllKindsCovered(t *testing.T) {
	// We only need the counter contract; the actual malformed-glob /
	// malformed-CIDR trigger lives in compile* helpers, which already
	// have their own tests. Here we drive loadHost with a malformed
	// route rule to prove the loop for kind="route" fires; the other
	// nine loops are byte-identical (range over each err slice; kind
	// string literal differs) so we verify the ten kind literals
	// are present in the source rather than re-driving nine malformed
	// fixtures (which would couple this test to compileIPRules et al).
	srcBytes, err := os.ReadFile("edge_rules.go")
	if err != nil {
		// Tests run from cmd/gatewayd-internal/ — try the package dir.
		// Fall back is best-effort; the test is a lint guard, not a
		// behavioural pin.
		t.Skipf("could not read edge_rules.go: %v", err)
	}
	src := string(srcBytes)
	for _, kind := range []string{
		`"route"`, `"rewrite"`, `"redirect"`, `"headers"`,
		`"cors"`, `"jwt"`, `"ip"`,
		`"validate"`, `"limit"`, `"budget"`,
	} {
		pattern := fmt.Sprintf(`ObserveEdgeRuleCompileError(%s)`, kind)
		if !strings.Contains(src, pattern) {
			t.Errorf("edge_rules.go missing compile-error loop for kind=%s (pattern %q)", kind, pattern)
		}
	}
}

// TestCompileBudgetRules_ClampsOutOfRangeValues pins the
// defence-in-depth posture of compileBudgetRules (ADR-093). The
// cmd-side compile step is the second line of defence against a
// direct-DB write that bypassed apid-Validate; a non-positive or
// >-ceiling BudgetMs must NOT 504 every request, but degrade to
// the platform ceiling. Mirrors the compileLimitRules clamping
// posture.
func TestCompileBudgetRules_ClampsOutOfRangeValues(t *testing.T) {
	maxMs := int(api.RequestBudgetMax.Milliseconds())
	cases := []struct {
		name     string
		budgetMs int
		want     int
	}{
		{"zero_clamps_to_ceiling", 0, maxMs},
		{"negative_clamps_to_ceiling", -1, maxMs},
		{"over_ceiling_clamps_to_ceiling", maxMs + 1, maxMs},
		{"in_range_passes_through", 3000, 3000},
		{"at_ceiling_passes_through", maxMs, maxMs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := []state.EdgeRule{{
				ID:        "rule_" + tc.name,
				AccountID: "acc_test",
				AppID:     "app_test",
				MatchHost: "a.example.com",
				MatchPath: "/v1/payment",
				Priority:  0,
				Enabled:   true,
				Kind:      state.EdgeRuleKindBudget,
				Action: state.EdgeRuleAction{
					Kind: state.EdgeRuleKindBudget,
					Budget: &state.EdgeRuleBudgetAction{
						BudgetMs: tc.budgetMs,
					},
				},
			}}
			out, errs := compileBudgetRules(rules)
			if len(errs) != 0 {
				t.Fatalf("compileBudgetRules returned errs: %v", errs)
			}
			if len(out) != 1 {
				t.Fatalf("compileBudgetRules returned %d rules, want 1", len(out))
			}
			if out[0].BudgetMs != tc.want {
				t.Errorf("BudgetMs = %d, want %d", out[0].BudgetMs, tc.want)
			}
		})
	}
}

// TestCompileBudgetRules_OnlyKindBudget pins the kind-filter arm.
// Non-budget kinds must be dropped silently (no rule, no error).
func TestCompileBudgetRules_OnlyKindBudget(t *testing.T) {
	rules := []state.EdgeRule{
		sampleRouteRule("route", 0, "a.example.com", "/", nil, "demo"),
	}
	out, errs := compileBudgetRules(rules)
	if len(errs) != 0 {
		t.Fatalf("compileBudgetRules returned errs: %v", errs)
	}
	if len(out) != 0 {
		t.Fatalf("compileBudgetRules returned %d rules, want 0 (only kind=budget should compile)", len(out))
	}
}

// TestCompileBudgetRules_SkipsDisabled pins the enabled-flag filter.
// A disabled budget rule must not be in the compiled slice — the
// apid-side create/update guards this, but the compile step is the
// second line of defence (defence-in-depth posture mirrors
// compileLimitRules).
func TestCompileBudgetRules_SkipsDisabled(t *testing.T) {
	rules := []state.EdgeRule{
		{
			ID:        "budget_off",
			AccountID: "acc_test",
			AppID:     "app_test",
			MatchHost: "a.example.com",
			MatchPath: "/v1/payment",
			Priority:  0,
			Enabled:   false,
			Kind:      state.EdgeRuleKindBudget,
			Action: state.EdgeRuleAction{
				Kind:   state.EdgeRuleKindBudget,
				Budget: &state.EdgeRuleBudgetAction{BudgetMs: 3000},
			},
		},
	}
	out, errs := compileBudgetRules(rules)
	if len(errs) != 0 {
		t.Fatalf("compileBudgetRules returned errs: %v", errs)
	}
	if len(out) != 0 {
		t.Fatalf("compileBudgetRules returned %d rules, want 0 (disabled rule must be skipped)", len(out))
	}
}

// TestCompileBudgetRules_CopiesAllowOverrideHeader pins the
// per-customer-tunable knob plumbing. The compiled slice must
// carry AllowOverrideHeader verbatim so the runtime can read the
// per-request header (default api.RequestBudgetDefaultOverrideHeader).
func TestCompileBudgetRules_CopiesAllowOverrideHeader(t *testing.T) {
	rules := []state.EdgeRule{
		{
			ID:        "budget_hdr",
			AccountID: "acc_test",
			AppID:     "app_test",
			MatchHost: "a.example.com",
			MatchPath: "/v1/payment",
			Priority:  0,
			Enabled:   true,
			Kind:      state.EdgeRuleKindBudget,
			Action: state.EdgeRuleAction{
				Kind: state.EdgeRuleKindBudget,
				Budget: &state.EdgeRuleBudgetAction{
					BudgetMs:            3000,
					AllowOverrideHeader: "x-custom-budget-ms",
				},
			},
		},
	}
	out, errs := compileBudgetRules(rules)
	if len(errs) != 0 {
		t.Fatalf("compileBudgetRules returned errs: %v", errs)
	}
	if len(out) != 1 {
		t.Fatalf("compileBudgetRules returned %d rules, want 1", len(out))
	}
	if out[0].AllowOverrideHeader != "x-custom-budget-ms" {
		t.Errorf("AllowOverrideHeader = %q, want x-custom-budget-ms", out[0].AllowOverrideHeader)
	}
}

// TestCompileBudgetRules_SortsPriorityAscending pins the
// priority-ASC + first-match-wins contract. The compiled slice's
// order is what PickFirstBudgetMatch iterates over; a regression
// in the sort would silently change which rule fires.
func TestCompileBudgetRules_SortsPriorityAscending(t *testing.T) {
	budgetAction := func(ms int) state.EdgeRuleAction {
		return state.EdgeRuleAction{
			Kind:   state.EdgeRuleKindBudget,
			Budget: &state.EdgeRuleBudgetAction{BudgetMs: ms},
		}
	}
	rules := []state.EdgeRule{
		{
			ID: "low", AccountID: "acc", AppID: "app",
			MatchHost: "a.example.com", MatchPath: "/",
			Priority: 100, Enabled: true, Kind: state.EdgeRuleKindBudget,
			Action: budgetAction(5000),
		},
		{
			ID: "high", AccountID: "acc", AppID: "app",
			MatchHost: "a.example.com", MatchPath: "/",
			Priority: 0, Enabled: true, Kind: state.EdgeRuleKindBudget,
			Action: budgetAction(3000),
		},
		{
			ID: "mid", AccountID: "acc", AppID: "app",
			MatchHost: "a.example.com", MatchPath: "/",
			Priority: 50, Enabled: true, Kind: state.EdgeRuleKindBudget,
			Action: budgetAction(7000),
		},
	}
	out, errs := compileBudgetRules(rules)
	if len(errs) != 0 {
		t.Fatalf("compileBudgetRules returned errs: %v", errs)
	}
	if len(out) != 3 {
		t.Fatalf("compileBudgetRules returned %d rules, want 3", len(out))
	}
	wantOrder := []string{"high", "mid", "low"}
	for i, want := range wantOrder {
		if out[i].ID != want {
			t.Errorf("out[%d].ID = %q, want %q (compileBudgetRules must sort priority-ASC + first-match-wins)", i, out[i].ID, want)
		}
	}
}
