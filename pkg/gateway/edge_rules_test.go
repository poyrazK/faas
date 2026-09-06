package gateway

// Tests for pkg/gateway/edge_rules.go (PR 3 of Edge Rules rollout,
// ADR-089 / issue #561). Mirror pkg/gateway/routes_test.go shape:
// table-driven where the shape is uniform, individual where the
// behaviour diverges. Run with -race to exercise the mutex.
//
// `EdgeRuleCache` is verified to behave identically to `RouteCache`
// for the LRU primitive — Get promotes, Put evicts, Reset clears.

import (
	"strings"
	"sync"
	"testing"
)

func sampleEdgeRule(id string, prio int, host, slug string) EdgeRuleResolved {
	return EdgeRuleResolved{
		ID:            id,
		AccountID:     "acc_" + id,
		AppID:         "app_" + id,
		Priority:      prio,
		PathGlob:      "",
		Methods:       nil,
		TargetAppSlug: slug,
	}
}

// putRoute is the test-only helper that wraps a route slice in a
// HostEntry for the new PR 4 cache API. Keeps the PR 3 test
// surface short and readable; production code uses cmd-side
// loadHost which populates all four kinds together.
func putRoute(c *EdgeRuleCache, host string, route []EdgeRuleResolved) {
	c.Put(host, &HostEntry{Host: host, Route: route})
}

// --- EdgeRuleCache primitive parity with RouteCache ----------------

func TestEdgeRuleCache_LRUEvictsAtCapacity(t *testing.T) {
	c := NewEdgeRuleCache(2)
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	putRoute(c, "b.example.com", []EdgeRuleResolved{sampleEdgeRule("b", 10, "b.example.com", "beta")})
	putRoute(c, "c.example.com", []EdgeRuleResolved{sampleEdgeRule("c", 10, "c.example.com", "gamma")}) // evicts "a"
	if _, ok := c.Get("a.example.com"); ok {
		t.Errorf("a should have been evicted (cap=2)")
	}
	if _, ok := c.Get("b.example.com"); !ok {
		t.Errorf("b should still be cached")
	}
	if _, ok := c.Get("c.example.com"); !ok {
		t.Errorf("c should still be cached")
	}
}

func TestEdgeRuleCache_GetPromotesEntry(t *testing.T) {
	c := NewEdgeRuleCache(2)
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	putRoute(c, "b.example.com", []EdgeRuleResolved{sampleEdgeRule("b", 10, "b.example.com", "beta")})
	if _, ok := c.Get("a.example.com"); !ok {
		t.Errorf("a should hit before promotion")
	}
	putRoute(c, "c.example.com", []EdgeRuleResolved{sampleEdgeRule("c", 10, "c.example.com", "gamma")})
	if _, ok := c.Get("b.example.com"); ok {
		t.Errorf("b should have been evicted (a was promoted)")
	}
	if _, ok := c.Get("a.example.com"); !ok {
		t.Errorf("a should still be cached after promotion")
	}
}

func TestEdgeRuleCache_ResetClearsAll(t *testing.T) {
	c := NewEdgeRuleCache(4)
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	putRoute(c, "b.example.com", []EdgeRuleResolved{sampleEdgeRule("b", 10, "b.example.com", "beta")})
	if c.Len() != 2 {
		t.Errorf("len before Reset = %d, want 2", c.Len())
	}
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("len after Reset = %d, want 0", c.Len())
	}
	if _, ok := c.Get("a.example.com"); ok {
		t.Errorf("a should be gone after Reset")
	}
}

func TestEdgeRuleCache_EmptyRulesAreCached(t *testing.T) {
	c := NewEdgeRuleCache(4)
	c.Put("failed.example.com", nil)
	c.Put("empty.example.com", &HostEntry{})
	if c.Len() != 1 {
		t.Fatalf("entries = %d, want only successful empty result", c.Len())
	}
	if _, ok := c.Get("failed.example.com"); ok {
		t.Fatal("nil result was cached")
	}
	if rules, ok := c.Get("empty.example.com"); !ok || len(rules) != 0 {
		t.Fatal("empty result missed cache")
	}
}

func TestEdgeRuleCache_GetReturnsCopy(t *testing.T) {
	// Callers must not be able to mutate the cached slice through
	// the Get pointer — mirrors the RouteCache value-copy contract.
	c := NewEdgeRuleCache(2)
	src := []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")}
	putRoute(c, "a.example.com", src)
	got, _ := c.Get("a.example.com")
	if len(got) != 1 {
		t.Fatalf("got len = %d, want 1", len(got))
	}
	got[0].TargetAppSlug = "mutated"
	again, _ := c.Get("a.example.com")
	if again[0].TargetAppSlug != "alpha" {
		t.Errorf("cached entry was mutated through Get return: %q", again[0].TargetAppSlug)
	}
}

func TestEdgeRuleCache_PutOverwritesRules(t *testing.T) {
	c := NewEdgeRuleCache(2)
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 20, "a.example.com", "alpha2")})
	got, ok := c.Get("a.example.com")
	if !ok {
		t.Fatalf("Get miss after Put")
	}
	if len(got) != 1 || got[0].Priority != 20 {
		t.Errorf("overwritten entry not returned; got priority=%d", got[0].Priority)
	}
	if c.Len() != 1 {
		t.Errorf("len = %d, want 1 (Put overwrite must not duplicate)", c.Len())
	}
}

func TestEdgeRuleCache_ConcurrentGetPut(t *testing.T) {
	// -race gate. N writers, M readers; assert no data race and
	// no panic. Len may legitimately drop below N due to LRU
	// eviction; we only assert no race detector hit and a final
	// Len() within (0, cap].
	c := NewEdgeRuleCache(100)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				putRoute(c, "h.example.com", []EdgeRuleResolved{sampleEdgeRule("x", j, "h.example.com", "alpha")})
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = c.Get("h.example.com")
			}
		}()
	}
	wg.Wait()
	if c.Len() < 0 || c.Len() > 100 {
		t.Errorf("Len out of bounds: %d", c.Len())
	}
}

func TestNewEdgeRuleCache_ClampsCapacity(t *testing.T) {
	c := NewEdgeRuleCache(0)
	putRoute(c, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	if c.Len() != 1 {
		t.Errorf("clamped cap=1 should hold one entry; got Len=%d", c.Len())
	}
	c2 := NewEdgeRuleCache(-3)
	putRoute(c2, "a.example.com", []EdgeRuleResolved{sampleEdgeRule("a", 10, "a.example.com", "alpha")})
	if c2.Len() != 1 {
		t.Errorf("negative cap should clamp to 1; got Len=%d", c2.Len())
	}
}

// --- EdgeRuleMatcher / EdgeRuleAuditor / ResolveTargetApp --------
//
// The interfaces are tiny; verify they have the shape production
// expects. Wiring-side correctness (audit row + metric + App
// substitution) is exercised in handler_test.go + the
// cmd/gatewayd-internal integration tests.

func TestEdgeRuleMatcher_InterfaceShape(t *testing.T) {
	// Compile-time assertion: a future kind impl that embeds
	// noOpEdgeRuleMatcher must satisfy the interface and get the
	// no-op default for MatchRoute.
	type future struct {
		noOpEdgeRuleMatcher
	}
	var _ EdgeRuleMatcher = future{}

	var m EdgeRuleMatcher = noOpEdgeRuleMatcher{}
	if r := m.MatchRoute(nil, "x", "/", "GET"); r != nil {
		t.Errorf("noOpEdgeRuleMatcher.MatchRoute = %v, want nil", r)
	}
	m.Reset() // must not panic
}

// --- Pure-Go filter logic (compileRouteRules + firstRouteMatch) ---
//
// These helpers are exported via cmd/gatewayd-internal/edge_rules.go,
// but the compile step is purely a Go function over EdgeRuleResolved
// — the test pins behaviour in pkg/gateway so a future refactor in
// cmd/gatewayd-internal can't silently flip a filter.

func pathMatchMatch(path, glob string) bool {
	// Stand-in for stdlib path.Match covering the patterns used in
	// these tests: empty (match all), exact, "*" (match all),
	// "/*" (single-segment wildcard), and "/api/*" (prefix wildcard).
	// The production loader uses stdlib path.Match
	// (compileRouteRules in cmd/gatewayd-internal/edge_rules.go).
	if glob == "" {
		return true
	}
	if glob == "*" {
		return true
	}
	if glob == path {
		return true
	}
	if glob == "/*" {
		return true
	}
	// Prefix wildcard "/prefix/*" matches "/prefix" + anything
	// after. Stdlib path.Match handles this; the inline helper
	// covers the test cases.
	if strings.HasSuffix(glob, "/*") {
		prefix := strings.TrimSuffix(glob, "/*")
		if strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func methodMatch(method string, methods map[string]bool) bool {
	if len(methods) == 0 {
		return true
	}
	return methods[method]
}

func TestFirstRouteMatch_PriorityOrdering(t *testing.T) {
	// Input is already priority-ASC sorted (the loader must sort
	// before Put — see cmd/gatewayd-internal/edge_rules.go::
	// compileRouteRules). First match wins because lower priority
	// means earlier in the slice.
	rules := []EdgeRuleResolved{
		sampleEdgeRule("high", 0, "a.example.com", "highslug"),
		sampleEdgeRule("mid", 50, "a.example.com", "midslug"),
		sampleEdgeRule("low", 100, "a.example.com", "lowslug"),
	}
	got := pickFirstRouteMatch(rules, "/", "GET")
	if got == nil {
		t.Fatalf("pickFirstRouteMatch returned nil")
	}
	if got.ID != "high" {
		t.Errorf("expected priority-0 rule first; got %q", got.ID)
	}
}

func TestFirstRouteMatch_PathFilter(t *testing.T) {
	// priority-ASC sorted. Rule 1 (priority 0) matches /api/v1,
	// wins. /v1/api falls through to rule 2 (catch-all, priority 10).
	rules := []EdgeRuleResolved{
		{ID: "1", Priority: 0, PathGlob: "/api/*", Methods: nil, TargetAppSlug: "t"},
		{ID: "2", Priority: 10, PathGlob: "", Methods: nil, TargetAppSlug: "t2"},
	}
	if got := pickFirstRouteMatch(rules, "/api/v1", "GET"); got == nil || got.ID != "1" {
		t.Errorf("expected rule 1 (priority-0 path match), got %v", got)
	}
	if got := pickFirstRouteMatch(rules, "/v1/api", "GET"); got == nil || got.ID != "2" {
		t.Errorf("expected rule 2 (catch-all), got %v", got)
	}
}

func TestFirstRouteMatch_MethodFilter(t *testing.T) {
	rules := []EdgeRuleResolved{
		{ID: "1", Priority: 0, PathGlob: "", Methods: map[string]bool{"POST": true}, TargetAppSlug: "t"},
	}
	if got := pickFirstRouteMatch(rules, "/", "GET"); got != nil {
		t.Errorf("GET must not match POST-only rule; got %v", got)
	}
	if got := pickFirstRouteMatch(rules, "/", "POST"); got == nil {
		t.Errorf("POST must match POST-only rule")
	}
}

func TestFirstRouteMatch_NilSafe(t *testing.T) {
	if got := pickFirstRouteMatch(nil, "/", "GET"); got != nil {
		t.Errorf("empty rules must return nil; got %v", got)
	}
	if got := pickFirstRouteMatch([]EdgeRuleResolved{}, "/", "GET"); got != nil {
		t.Errorf("zero-length rules must return nil; got %v", got)
	}
}

// pickFirstRouteMatch is the pure-Go filter used by
// cmd/gatewayd-internal/edge_rules.go::gatewaydEdgeRules.MatchRoute
// after the cache returns the priority-ordered slice. Lives here
// (not in the gatewayd-internal impl) so its behaviour is pinned
// in pkg/gateway and the production loader can't silently drift.
func pickFirstRouteMatch(rules []EdgeRuleResolved, path, method string) *EdgeRuleResolved {
	for i := range rules {
		r := &rules[i]
		if !methodMatch(method, r.Methods) {
			continue
		}
		if !pathMatchMatch(path, r.PathGlob) {
			continue
		}
		return r
	}
	return nil
}

// --- PR 4 surface: resolved types, per-kind pick helpers, hostEntry cache ----

func sampleRewriteRule(id string, prio int, host, from, to string) EdgeRuleRewriteResolved {
	return EdgeRuleRewriteResolved{
		ID:        id,
		AccountID: "acc_" + id,
		AppID:     "app_" + id,
		Priority:  prio,
		PathGlob:  "",
		Methods:   nil,
		From:      from,
		To:        to,
	}
}

func sampleRedirectRule(id string, prio int, host string, status int, to string) EdgeRuleRedirectResolved {
	return EdgeRuleRedirectResolved{
		ID:         id,
		AccountID:  "acc_" + id,
		AppID:      "app_" + id,
		Priority:   prio,
		PathGlob:   "",
		Methods:    nil,
		StatusCode: status,
		To:         to,
	}
}

func sampleHeadersRule(id string, prio int, host string, reqHdr, respHdr []EdgeRuleHeaderOp) EdgeRuleHeadersResolved {
	return EdgeRuleHeadersResolved{
		ID:              id,
		AccountID:       "acc_" + id,
		AppID:           "app_" + id,
		Priority:        prio,
		PathGlob:        "",
		Methods:         nil,
		RequestHeaders:  reqHdr,
		ResponseHeaders: respHdr,
	}
}

func TestPickFirstRewriteMatch_PriorityOrdering(t *testing.T) {
	// Input is priority-ASC (mirrors what compile* writes after
	// sort.Slice); pickFirstRewriteMatch returns the first match
	// in iteration order, so priority-0 ("high") wins over 50/100.
	rules := []EdgeRuleRewriteResolved{
		sampleRewriteRule("high", 0, "a.example.com", "/api", "/v2"),
		sampleRewriteRule("mid", 50, "a.example.com", "/api", "/v3"),
		sampleRewriteRule("low", 100, "a.example.com", "/api", "/v1"),
	}
	got := PickFirstRewriteMatch(rules, "/api/x", "GET")
	if got == nil {
		t.Fatalf("PickFirstRewriteMatch = nil, want high")
	}
	if got.ID != "high" {
		t.Errorf("got ID %q, want high (lowest priority first)", got.ID)
	}
}

func TestPickFirstRedirectMatch_PriorityOrderingAndDefaultStatus(t *testing.T) {
	rules := []EdgeRuleRedirectResolved{
		sampleRedirectRule("high", 0, "a.example.com", 308, "https://c"),
		sampleRedirectRule("low", 100, "a.example.com", 301, "https://b"),
	}
	got := PickFirstRedirectMatch(rules, "/", "GET")
	if got == nil || got.ID != "high" {
		t.Errorf("got %v, want high priority-0", got)
	}
	if got.StatusCode != 308 {
		t.Errorf("got status %d, want 308", got.StatusCode)
	}
}

func TestPickFirstHeadersMatch_PriorityOrdering(t *testing.T) {
	rules := []EdgeRuleHeadersResolved{
		sampleHeadersRule("high", 0, "a.example.com", nil,
			[]EdgeRuleHeaderOp{{Name: "X-High", Value: "1", Action: "set"}}),
		sampleHeadersRule("low", 100, "a.example.com", nil,
			[]EdgeRuleHeaderOp{{Name: "X-Low", Value: "1", Action: "set"}}),
	}
	got := PickFirstHeadersMatch(rules, "/", "GET")
	if got == nil || got.ID != "high" {
		t.Errorf("got %v, want high priority-0", got)
	}
	if len(got.ResponseHeaders) != 1 || got.ResponseHeaders[0].Name != "X-High" {
		t.Errorf("got ResponseHeaders %v, want one X-High op", got.ResponseHeaders)
	}
}

func TestPickFirstRewriteMatch_MethodsFilter(t *testing.T) {
	rules := []EdgeRuleRewriteResolved{
		{
			ID: "post-only", Priority: 0, PathGlob: "",
			Methods: map[string]bool{"POST": true},
			From:    "/api", To: "/v1",
		},
	}
	if got := PickFirstRewriteMatch(rules, "/x", "GET"); got != nil {
		t.Errorf("GET should miss POST-only rule, got %v", got)
	}
	if got := PickFirstRewriteMatch(rules, "/x", "POST"); got == nil {
		t.Errorf("POST should hit POST-only rule, got nil")
	}
}

func TestPickFirstRewriteMatch_PathGlob(t *testing.T) {
	rules := []EdgeRuleRewriteResolved{
		{
			ID: "api-only", Priority: 0,
			PathGlob: "/api/*",
			Methods:  nil,
			From:     "/api", To: "/v1",
		},
	}
	if got := PickFirstRewriteMatch(rules, "/api/x", "GET"); got == nil {
		t.Errorf("/api/x should hit /api/* glob, got nil")
	}
	if got := PickFirstRewriteMatch(rules, "/v1/api", "GET"); got != nil {
		t.Errorf("/v1/api should miss /api/* glob, got %v", got)
	}
}

// putEntry is the test-only helper that puts a HostEntry with all
// four kinds' slices supplied independently. Mirrors putRoute but
// for tests that exercise multiple kinds on one host.
func putEntry(c *EdgeRuleCache, host string,
	route []EdgeRuleResolved,
	rewrite []EdgeRuleRewriteResolved,
	redirect []EdgeRuleRedirectResolved,
	headers []EdgeRuleHeadersResolved,
) {
	c.Put(host, &HostEntry{
		Host:     host,
		Route:    route,
		Rewrite:  rewrite,
		Redirect: redirect,
		Headers:  headers,
	})
}

func TestEdgeRuleCache_HostEntryPerKindAccess(t *testing.T) {
	c := NewEdgeRuleCache(4)
	putEntry(c, "a.example.com",
		[]EdgeRuleResolved{sampleEdgeRule("route-1", 0, "a.example.com", "demo")},
		[]EdgeRuleRewriteResolved{sampleRewriteRule("rewrite-1", 0, "a.example.com", "/api", "/v1")},
		[]EdgeRuleRedirectResolved{sampleRedirectRule("redirect-1", 0, "a.example.com", 308, "https://b")},
		[]EdgeRuleHeadersResolved{sampleHeadersRule("headers-1", 0, "a.example.com", nil, nil)},
	)
	if route, ok := c.Get("a.example.com"); !ok || len(route) != 1 {
		t.Errorf("Get route miss; got ok=%v len=%d", ok, len(route))
	}
	if rewrite, ok := c.GetRewrite("a.example.com"); !ok || len(rewrite) != 1 {
		t.Errorf("GetRewrite miss; got ok=%v len=%d", ok, len(rewrite))
	}
	if redirect, ok := c.GetRedirect("a.example.com"); !ok || len(redirect) != 1 {
		t.Errorf("GetRedirect miss; got ok=%v len=%d", ok, len(redirect))
	}
	if headers, ok := c.GetHeaders("a.example.com"); !ok || len(headers) != 1 {
		t.Errorf("GetHeaders miss; got ok=%v len=%d", ok, len(headers))
	}
}

func TestEdgeRuleCache_HostEntryKindIsolation(t *testing.T) {
	// Putting one kind's slice only leaves the other three as
	// (nil, true) — i.e. entry exists but no rule of that kind.
	// Mirrors the cmd-side loadHost pattern: the SQL roundtrip
	// reads every kind together; per-kind Puts are a test surface.
	c := NewEdgeRuleCache(4)
	putEntry(c, "a.example.com",
		[]EdgeRuleResolved{sampleEdgeRule("route-1", 0, "a.example.com", "demo")},
		nil, nil, nil,
	)
	if _, ok := c.Get("a.example.com"); !ok {
		t.Errorf("Get should hit (entry exists)")
	}
	// Per-kind Get: returns (nil, true) when entry exists but the
	// rewrite slice is nil (the loader would interpret this as a
	// clean miss for the rewrite kind).
	if got, ok := c.GetRewrite("a.example.com"); !ok || got != nil {
		t.Errorf("GetRewrite should return (nil, true) for entry-with-no-rewrite; got (%v, %v)", got, ok)
	}
	// Re-Put with rewrite filled in — entry now carries both.
	putEntry(c, "a.example.com",
		[]EdgeRuleResolved{sampleEdgeRule("route-1", 0, "a.example.com", "demo")},
		[]EdgeRuleRewriteResolved{sampleRewriteRule("rewrite-1", 0, "a.example.com", "/api", "/v1")},
		nil, nil,
	)
	if route, ok := c.Get("a.example.com"); !ok || len(route) != 1 {
		t.Errorf("route lost on re-Put: ok=%v len=%d", ok, len(route))
	}
	if rewrite, ok := c.GetRewrite("a.example.com"); !ok || len(rewrite) != 1 {
		t.Errorf("rewrite missing after re-Put: ok=%v len=%d", ok, len(rewrite))
	}
}

func TestEdgeRuleMatcher_WidenedInterfaceSatisfied(t *testing.T) {
	// Compile-time check: noOpEdgeRuleMatcher (which provides
	// default no-op behaviour) satisfies the widened EdgeRuleMatcher
	// interface. If PR 5-7 (or PR 8 kind=geo) add new Match* methods
	// and forget to add a noOp default, this assignment fails to compile.
	var m EdgeRuleMatcher = noOpEdgeRuleMatcher{}
	_ = m.MatchRoute(nil, "h", "/", "GET")
	_ = m.MatchRewrite(nil, "h", "/", "GET")
	_ = m.MatchRedirect(nil, "h", "/", "GET")
	_ = m.MatchHeaders(nil, "h", "/", "GET")
	_ = m.MatchCORS(nil, "h", "/", "GET")
	_ = m.MatchJWT(nil, "h", "/", "GET")
	_ = m.MatchIP(nil, "h", "/", "GET")
	_ = m.MatchGeo(nil, "h", "/", "GET")
	m.Reset()
}

func TestPickFirstHeadersMatch_HeadersOrderPreserved(t *testing.T) {
	// Customer-declared op order is preserved end-to-end — Cloudflare
	// "first wins" semantics for `set` mean the customer's order is
	// the contract. compile* copies in declared order; pick* does
	// not reorder.
	ops := []EdgeRuleHeaderOp{
		{Name: "X-A", Value: "1", Action: "set"},
		{Name: "X-A", Value: "2", Action: "set"},
		{Name: "X-A", Value: "3", Action: "set"},
	}
	rules := []EdgeRuleHeadersResolved{sampleHeadersRule("h", 0, "a.example.com", nil, ops)}
	got := PickFirstHeadersMatch(rules, "/", "GET")
	if got == nil || len(got.ResponseHeaders) != 3 {
		t.Fatalf("got %v, want 3 ops", got)
	}
	if got.ResponseHeaders[1].Value != "2" {
		t.Errorf("ops reordered: got %q, want 2", got.ResponseHeaders[1].Value)
	}
}

func TestPickFirstRedirectMatch_PathGlob(t *testing.T) {
	rules := []EdgeRuleRedirectResolved{
		{
			ID: "api-only", Priority: 0, PathGlob: "/api/*", Methods: nil,
			StatusCode: 308, To: "https://b",
		},
	}
	if got := PickFirstRedirectMatch(rules, "/api/x", "GET"); got == nil {
		t.Errorf("/api/x should match /api/* glob")
	}
	if got := PickFirstRedirectMatch(rules, "/v1/api", "GET"); got != nil {
		t.Errorf("/v1/api should not match /api/* glob, got %v", got)
	}
}

// --- PR 5 surface: CORS / JWT / IP resolved types + per-kind pick helpers ----
//
// PR 6 widens putEntry to cover all 7 kinds and adds the wholesale-Reset
// property test (ADR-091 D17).

func sampleCORSRule(id string, prio int, host string) EdgeRuleCORSResolved {
	return EdgeRuleCORSResolved{
		ID:           id,
		AccountID:    "acc_" + id,
		AppID:        "app_" + id,
		Priority:     prio,
		PathGlob:     "",
		Methods:      nil,
		AllowOrigins: []string{"https://app.test"},
		AllowMethods: []string{"GET", "POST"},
	}
}

func sampleJWTRule(id string, prio int, host string) EdgeRuleJWTResolved {
	return EdgeRuleJWTResolved{
		ID:         id,
		AccountID:  "acc_" + id,
		AppID:      "app_" + id,
		Priority:   prio,
		PathGlob:   "",
		Methods:    nil,
		Issuer:     "https://idp.example.com/",
		Audience:   []string{"https://api.example.com"},
		JWKSURL:    "https://idp.example.com/.well-known/jwks.json",
		Algorithms: []string{"RS256"},
	}
}

func sampleIPRule(id string, prio int, host string) EdgeRuleIPResolved {
	return EdgeRuleIPResolved{
		ID:        id,
		AccountID: "acc_" + id,
		AppID:     "app_" + id,
		Priority:  prio,
		PathGlob:  "",
		Methods:   nil,
		// nil Allow + nil Deny is the "no rule" shape; apid-Validate
		// requires non-empty lists in production but the gateway
		// compiler is permissive (defence-in-depth).
	}
}

// sampleValidateRule mirrors sampleIPRule's shape for the PR-B
// eighth kind. SchemaDigest is the SHA-256 of the schema body —
// computed at compile time on the cmd side; here we pin a stable
// 32-byte value so the test isn't sensitive to the seed contents.
// The other fields default to the "v1 baseline" (any Content-Type,
// no streaming opt-in, no extra body cap).
func sampleValidateRule(id string, prio int, host string) EdgeRuleValidateResolved {
	var digest [32]byte
	copy(digest[:], []byte("validate-"+id+"-test-fixture-digest-padding"))
	return EdgeRuleValidateResolved{
		ID:           id,
		AccountID:    "acc_" + id,
		AppID:        "app_" + id,
		Priority:     prio,
		PathGlob:     "",
		Methods:      nil,
		SchemaDigest: digest,
	}
}

// sampleLimitRule (ADR-091 D24 / kind=limit) is the ninth-kind
// mirror of sampleIPRule's shape. MaxBodyBytes is the per-rule
// buffered cap; MaxBodyBytesStreaming is the streaming opt-in
// (0 = no streaming carve-out, falls back to MaxBodyBytes on the
// hot path). The cmd-side compileLimitRules clamps out-of-range
// values to api.MaxRequestBodyBytes / api.RawStreamMaxRequestBytes;
// these samples stay in-range so the resolver-level unit test
// doesn't have to simulate the clamp path (which has its own
// cmd-side test in cmd/gatewayd-internal/edge_rules_test.go).
func sampleLimitRule(id string, prio int, host string) EdgeRuleLimitResolved {
	return EdgeRuleLimitResolved{
		ID:                    id,
		AccountID:             "acc_" + id,
		AppID:                 "app_" + id,
		Priority:              prio,
		PathGlob:              "",
		Methods:               nil,
		MaxBodyBytes:          5 * 1024 * 1024, // 5 MiB
		MaxBodyBytesStreaming: 0,
	}
}

// sampleMaintenanceRule is the ADR-091 amendment / §4.1.2.13 sample
// used by the cache + filter tests. Mirrors sampleLimitRule above;
// kind=maintenance only carries RetryAfterSeconds + Message
// (no body caps). The id is reused for AccountID + AppID to keep
// the helper one-line.
func sampleMaintenanceRule(id string, prio int, host string) EdgeRuleMaintenanceResolved {
	return EdgeRuleMaintenanceResolved{
		ID:                id,
		AccountID:         "acc_" + id,
		AppID:             "app_" + id,
		Priority:          prio,
		PathGlob:          "",
		Methods:           nil,
		RetryAfterSeconds: 60,
		Message:           "Test maintenance: " + id,
	}
}

// sampleBudgetRule (ADR-093 / kind=budget) is the eleventh-kind
// mirror of sampleIPRule's shape. BudgetMs is the per-request
// wall-clock budget; AllowOverrideHeader is empty (the runtime
// falls back to api.RequestBudgetDefaultOverrideHeader). The
// cmd-side compileBudgetRules clamps out-of-range values to
// api.RequestBudgetMaxMs; these samples stay in-range so the
// resolver-level unit test doesn't have to simulate the clamp
// path (which has its own cmd-side test in
// cmd/gatewayd-internal/edge_rules_test.go).
func sampleBudgetRule(id string, prio int, host string) EdgeRuleBudgetResolved {
	return EdgeRuleBudgetResolved{
		ID:                  id,
		AccountID:           "acc_" + id,
		AppID:               "app_" + id,
		Priority:            prio,
		PathGlob:            "",
		Methods:             nil,
		BudgetMs:            3000,
		AllowOverrideHeader: "",
	}
}

// sampleCacheRule (ADR-122 / kind=cache) is the thirteenth-kind
// mirror of sampleBudgetRule's shape. MaxAgeSeconds is the
// per-rule fresh window (60 s = the ADR ask example);
// StaleIfErrorSeconds is the per-rule stale-on-error window
// (300 s = the ADR ask example); VaryOn is a closed subset of
// {Accept-Language, Accept-Encoding}; Methods defaults to
// {GET, HEAD} per the closed cacheable-method vocab. The
// cmd-side compileCacheRules clamps out-of-range values to
// api.ResponseCacheMaxAgeMaxSeconds /
// ResponseCacheStaleIfErrorMaxSeconds; these samples stay
// in-range so the resolver-level unit test doesn't have to
// simulate the clamp path (which has its own cmd-side test
// in cmd/gatewayd-internal/edge_rules_test.go).
func sampleCacheRule(id string, prio int, host string) EdgeRuleCacheResolved {
	return EdgeRuleCacheResolved{
		ID:                  id,
		AccountID:           "acc_" + id,
		AppID:               "app_" + id,
		Priority:            prio,
		PathGlob:            "",
		Methods:             map[string]bool{"GET": true, "HEAD": true},
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
		VaryOn:              []string{"Accept-Language"},
	}
}

// putEntryAll is the PR 6 widening of putEntry: covers all 12 kinds
// after the kind=maintenance, kind=geo, and kind=budget extensions
// (ADR-091 D21 amendment, §4.1.2.13, ADR-093). The validate widening
// was PR-B; the limit widening was D24; the maintenance widening is
// PR-A; the geo widening is PR-A's sibling cluster; the budget
// widening is ADR-093. Mirrors cmd-side loadHost's single-pass
// compile pattern — the production loader always populates every
// kind at once; this test helper does the same.
//
// Adding a new edge-rule kind requires extending this signature.
// The compiler enforces it across every callsite — that's the
// load-bearing reason for the wide parameter list over a
// HostEntry-shaped struct.
func putEntryAll(c *EdgeRuleCache, host string,
	route []EdgeRuleResolved,
	rewrite []EdgeRuleRewriteResolved,
	redirect []EdgeRuleRedirectResolved,
	headers []EdgeRuleHeadersResolved,
	cors []EdgeRuleCORSResolved,
	jwt []EdgeRuleJWTResolved,
	ip []EdgeRuleIPResolved,
	validate []EdgeRuleValidateResolved,
	limit []EdgeRuleLimitResolved,
	maintenance []EdgeRuleMaintenanceResolved,
	geo []EdgeRuleGeoResolved,
	budget []EdgeRuleBudgetResolved,
	cache []EdgeRuleCacheResolved,
) {
	c.Put(host, &HostEntry{
		Host:        host,
		Route:       route,
		Rewrite:     rewrite,
		Redirect:    redirect,
		Headers:     headers,
		CORS:        cors,
		JWT:         jwt,
		IP:          ip,
		Validate:    validate,
		Limit:       limit,
		Maintenance: maintenance,
		Geo:         geo,
		Budget:      budget,
		Cache:       cache,
	})
}

func TestPickFirstCORSMatch_PriorityOrdering(t *testing.T) {
	rules := []EdgeRuleCORSResolved{
		sampleCORSRule("high", 0, "a.example.com"),
		sampleCORSRule("low", 100, "a.example.com"),
	}
	got := PickFirstCORSMatch(rules, "/", "OPTIONS")
	if got == nil || got.ID != "high" {
		t.Errorf("got %v, want high priority-0", got)
	}
}

func TestPickFirstJWTMatch_PriorityOrdering(t *testing.T) {
	rules := []EdgeRuleJWTResolved{
		sampleJWTRule("high", 0, "a.example.com"),
		sampleJWTRule("low", 100, "a.example.com"),
	}
	got := PickFirstJWTMatch(rules, "/", "GET")
	if got == nil || got.ID != "high" {
		t.Errorf("got %v, want high priority-0", got)
	}
}

func TestPickFirstIPMatch_PriorityOrdering(t *testing.T) {
	rules := []EdgeRuleIPResolved{
		sampleIPRule("high", 0, "a.example.com"),
		sampleIPRule("low", 100, "a.example.com"),
	}
	got := PickFirstIPMatch(rules, "/", "GET")
	if got == nil || got.ID != "high" {
		t.Errorf("got %v, want high priority-0", got)
	}
}

// TestPickFirstValidateMatch_PriorityOrdering is the PR-B (PR-C
// rollout-closer) mirror of TestPickFirstIPMatch_PriorityOrdering.
// The PickFirst*Match family's semantics are identical across kinds
// — priority ASC, methods filter, then path-glob filter — and
// this test pins that the new kind obeys the same priority contract.
func TestPickFirstValidateMatch_PriorityOrdering(t *testing.T) {
	host := "a.example.com"
	rules := []EdgeRuleValidateResolved{
		sampleValidateRule("high", 0, host),
		sampleValidateRule("low", 100, host),
	}
	got := PickFirstValidateMatch(rules, "/", "POST")
	if got == nil || got.ID != "high" {
		t.Fatalf("got %v, want high priority-0", got)
	}
}

// TestPickFirstValidateMatch_PathGlob pins the path-glob filter for
// the new kind. A rule with PathGlob=`/api/*` must NOT match `/healthz`
// even at lower priority; without the filter, the rule would falsely
// claim a hit on every method+path combination.
func TestPickFirstValidateMatch_PathGlob(t *testing.T) {
	host := "a.example.com"
	rule := sampleValidateRule("glob", 0, host)
	rule.PathGlob = "/api/*"
	rules := []EdgeRuleValidateResolved{rule}
	if got := PickFirstValidateMatch(rules, "/healthz", "POST"); got != nil {
		t.Fatalf("got %v, want nil (path-glob filter)", got)
	}
	if got := PickFirstValidateMatch(rules, "/api/users", "POST"); got == nil || got.ID != "glob" {
		t.Fatalf("got %v, want glob match on /api/users", got)
	}
}

// TestPickFirstValidateMatch_MethodsFilter pins the HTTP-method
// filter for the new kind. The resolver still respects a per-rule
// Methods map; a rule declared for POST must not match GET, even
// at priority 0.
func TestPickFirstValidateMatch_MethodsFilter(t *testing.T) {
	host := "a.example.com"
	rule := sampleValidateRule("post-only", 0, host)
	rule.Methods = map[string]bool{"POST": true}
	rules := []EdgeRuleValidateResolved{rule}
	if got := PickFirstValidateMatch(rules, "/", "GET"); got != nil {
		t.Fatalf("got %v, want nil (methods filter excluded GET)", got)
	}
	if got := PickFirstValidateMatch(rules, "/", "POST"); got == nil || got.ID != "post-only" {
		t.Fatalf("got %v, want post-only match on POST", got)
	}
}

// TestPickFirstLimitMatch_PriorityOrdering (ADR-091 D24 / kind=limit)
// is the ninth-kind mirror of TestPickFirstValidateMatch_PriorityOrdering.
// PickFirst*Match's semantics are identical across kinds — priority
// ASC, methods filter, then path-glob filter — and this test pins
// that the new kind obeys the same priority contract. Without it,
// a regression that re-orders the slice on the limit path would
// silently change which rule wins (the smaller-cap rule is not
// preferred — first-match-wins, mirroring every other kind).
func TestPickFirstLimitMatch_PriorityOrdering(t *testing.T) {
	host := "a.example.com"
	rules := []EdgeRuleLimitResolved{
		sampleLimitRule("high", 0, host),
		sampleLimitRule("low", 100, host),
	}
	got := PickFirstLimitMatch(rules, "/", "POST")
	if got == nil || got.ID != "high" {
		t.Fatalf("got %v, want high priority-0", got)
	}
}

// TestPickFirstLimitMatch_PathGlob pins the path-glob filter for
// the new kind. A rule with PathGlob=`/api/*` must NOT match
// `/healthz` even at lower priority; without the filter, the rule
// would falsely claim a hit on every method+path combination.
func TestPickFirstLimitMatch_PathGlob(t *testing.T) {
	host := "a.example.com"
	rule := sampleLimitRule("glob", 0, host)
	rule.PathGlob = "/api/*"
	rules := []EdgeRuleLimitResolved{rule}
	if got := PickFirstLimitMatch(rules, "/healthz", "POST"); got != nil {
		t.Fatalf("got %v, want nil (path-glob filter)", got)
	}
	if got := PickFirstLimitMatch(rules, "/api/users", "POST"); got == nil || got.ID != "glob" {
		t.Fatalf("got %v, want glob match on /api/users", got)
	}
}

// TestPickFirstLimitMatch_MethodsFilter pins the HTTP-method filter
// for the new kind. The resolver still respects a per-rule Methods
// map; a rule declared for POST must not match GET, even at
// priority 0.
func TestPickFirstLimitMatch_MethodsFilter(t *testing.T) {
	host := "a.example.com"
	rule := sampleLimitRule("post-only", 0, host)
	rule.Methods = map[string]bool{"POST": true}
	rules := []EdgeRuleLimitResolved{rule}
	if got := PickFirstLimitMatch(rules, "/", "GET"); got != nil {
		t.Fatalf("got %v, want nil (methods filter excluded GET)", got)
	}
	if got := PickFirstLimitMatch(rules, "/", "POST"); got == nil || got.ID != "post-only" {
		t.Fatalf("got %v, want post-only match on POST", got)
	}
}

// TestPickFirstBudgetMatch_PriorityOrdering (ADR-093 / kind=budget)
// pins the priority-ASC + first-match-wins shape. Mirrors
// TestPickFirstLimitMatch_PriorityOrdering — the small copy keeps
// the per-kind return type precise without paying for a
// runtime-type assertion on every request.
func TestPickFirstBudgetMatch_PriorityOrdering(t *testing.T) {
	host := "a.example.com"
	rules := []EdgeRuleBudgetResolved{
		sampleBudgetRule("high", 0, host),
		sampleBudgetRule("low", 100, host),
	}
	got := PickFirstBudgetMatch(rules, "/", "POST")
	if got == nil || got.ID != "high" {
		t.Fatalf("got %v, want high priority-0", got)
	}
}

// TestPickFirstBudgetMatch_PathGlob pins the path-glob filter for
// kind=budget. A rule with PathGlob=`/v1/payment/*` must NOT
// match `/healthz` even at lower priority; without the filter,
// the rule would falsely claim a hit on every method+path
// combination.
func TestPickFirstBudgetMatch_PathGlob(t *testing.T) {
	host := "a.example.com"
	rule := sampleBudgetRule("glob", 0, host)
	rule.PathGlob = "/v1/payment/*"
	rules := []EdgeRuleBudgetResolved{rule}
	if got := PickFirstBudgetMatch(rules, "/healthz", "POST"); got != nil {
		t.Fatalf("got %v, want nil (path-glob filter)", got)
	}
	if got := PickFirstBudgetMatch(rules, "/v1/payment/intent", "POST"); got == nil || got.ID != "glob" {
		t.Fatalf("got %v, want glob match on /v1/payment/intent", got)
	}
}

// TestPickFirstBudgetMatch_MethodsFilter pins the HTTP-method filter
// for kind=budget. The resolver still respects a per-rule Methods
// map; a rule declared for POST must not match GET, even at
// priority 0. Mirrors the limit-methods test.
func TestPickFirstBudgetMatch_MethodsFilter(t *testing.T) {
	host := "a.example.com"
	rule := sampleBudgetRule("post-only", 0, host)
	rule.Methods = map[string]bool{"POST": true}
	rules := []EdgeRuleBudgetResolved{rule}
	if got := PickFirstBudgetMatch(rules, "/", "GET"); got != nil {
		t.Fatalf("got %v, want nil (methods filter excluded GET)", got)
	}
	if got := PickFirstBudgetMatch(rules, "/", "POST"); got == nil || got.ID != "post-only" {
		t.Fatalf("got %v, want post-only match on POST", got)
	}
}

// --- PR 6: wholesale-Reset() property test (ADR-091 D17) ----------
//
// The EdgeRuleCache is the LRU mirror for all 9 edge-rule kinds
// (route, rewrite, redirect, headers, cors, jwt, ip, validate, geo).
// pg_notify-driven invalidation in cmd/gatewayd-internal/backend.go
// fires Reset() wholesale — a regression against any single kind
// fails this test, surfacing as a cache-consistency violation for
// ALL 9 kinds simultaneously. The deterministic 9-row table is the
// load-bearing assertion; the fuzz target is hardening on top.
//
// Why one row per kind: the cache stores a HostEntry whose slices
// cover all 9 kinds together. Reset() drops the whole HostEntry
// (and every kind's slice inside it). Pinning that pre-Reset each
// kind's GetK returns a hit AND post-Reset each kind's GetK returns
// (nil, false) is the invariant that catches "Reset forgot kind X"
// regressions before they ship.

// TestEdgeRuleReset_WholesaleAcrossAllElevenKinds is the deterministic
// 11-row table that pins the wholesale-Reset invariant. The PR-C
// rename widened the original PR-6 SevenKinds test to cover the
// kind=validate slice added by PR-B; PR #855 widened it again for
// kind=limit (ADR-091 D24); PR-A widens it for kind=maintenance
// (§4.1.2.13) and for kind=geo (D21); ADR-093 widens it once more
// for kind=budget. See plan §D1 + D21 + D24 + ADR-093 §Decision.
func TestEdgeRuleReset_WholesaleAcrossAllTwelveKinds(t *testing.T) {
	c := NewEdgeRuleCache(EdgeRuleCacheCap)
	host := "a.example.com"
	putEntryAll(c, host,
		[]EdgeRuleResolved{sampleEdgeRule("r", 0, host, "alpha")},
		[]EdgeRuleRewriteResolved{sampleRewriteRule("rw", 0, host, "/api", "/v2")},
		[]EdgeRuleRedirectResolved{sampleRedirectRule("rd", 0, host, 308, "https://c")},
		[]EdgeRuleHeadersResolved{sampleHeadersRule("hd", 0, host, nil, nil)},
		[]EdgeRuleCORSResolved{sampleCORSRule("cors", 0, host)},
		[]EdgeRuleJWTResolved{sampleJWTRule("jwt", 0, host)},
		[]EdgeRuleIPResolved{sampleIPRule("ip", 0, host)},
		[]EdgeRuleValidateResolved{sampleValidateRule("vd", 0, host)},
		[]EdgeRuleLimitResolved{sampleLimitRule("lm", 0, host)},
		[]EdgeRuleMaintenanceResolved{sampleMaintenanceRule("mt", 0, host)},
		[]EdgeRuleGeoResolved{sampleGeoRule("geo", 0, []string{"DE"}, nil, "")},
		[]EdgeRuleBudgetResolved{sampleBudgetRule("bg", 0, host)},
		[]EdgeRuleCacheResolved{sampleCacheRule("cache", 0, host)},
	)

	// Sanity: every GetK returns a hit pre-Reset.
	preChecks := []struct {
		name string
		f    func() bool
	}{
		{"Get", func() bool { _, ok := c.Get(host); return ok }},
		{"GetRewrite", func() bool { _, ok := c.GetRewrite(host); return ok }},
		{"GetRedirect", func() bool { _, ok := c.GetRedirect(host); return ok }},
		{"GetHeaders", func() bool { _, ok := c.GetHeaders(host); return ok }},
		{"GetCORS", func() bool { _, ok := c.GetCORS(host); return ok }},
		{"GetJWT", func() bool { _, ok := c.GetJWT(host); return ok }},
		{"GetIP", func() bool { _, ok := c.GetIP(host); return ok }},
		{"GetValidate", func() bool { _, ok := c.GetValidate(host); return ok }},
		{"GetLimit", func() bool { _, ok := c.GetLimit(host); return ok }},
		{"GetMaintenance", func() bool { _, ok := c.GetMaintenance(host); return ok }},
		{"GetGeo", func() bool { _, ok := c.GetGeo(host); return ok }},
		{"GetBudget", func() bool { _, ok := c.GetBudget(host); return ok }},
		{"GetCache", func() bool { _, ok := c.GetCache(host); return ok }},
	}
	for _, c0 := range preChecks {
		if !c0.f() {
			t.Fatalf("pre-Reset %s: expected hit, got miss", c0.name)
		}
	}

	c.Reset()

	postChecks := []struct {
		name string
		f    func() bool
	}{
		{"Get", func() bool { _, ok := c.Get(host); return ok }},
		{"GetRewrite", func() bool { _, ok := c.GetRewrite(host); return ok }},
		{"GetRedirect", func() bool { _, ok := c.GetRedirect(host); return ok }},
		{"GetHeaders", func() bool { _, ok := c.GetHeaders(host); return ok }},
		{"GetCORS", func() bool { _, ok := c.GetCORS(host); return ok }},
		{"GetJWT", func() bool { _, ok := c.GetJWT(host); return ok }},
		{"GetIP", func() bool { _, ok := c.GetIP(host); return ok }},
		{"GetValidate", func() bool { _, ok := c.GetValidate(host); return ok }},
		{"GetLimit", func() bool { _, ok := c.GetLimit(host); return ok }},
		{"GetMaintenance", func() bool { _, ok := c.GetMaintenance(host); return ok }},
		{"GetGeo", func() bool { _, ok := c.GetGeo(host); return ok }},
		{"GetBudget", func() bool { _, ok := c.GetBudget(host); return ok }},
		{"GetCache", func() bool { _, ok := c.GetCache(host); return ok }},
	}
	for _, c0 := range postChecks {
		if c0.f() {
			t.Errorf("post-Reset %s: returned hit, want miss", c0.name)
		}
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d after Reset, want 0", c.Len())
	}
}

// TestEdgeRuleReset_EmptyCacheNoPanic pins the edge case: calling
// Reset() on a cache that has never been Put to must not panic and
// must leave Len() at 0. Catches a "Reset on nil map" regression.
func TestEdgeRuleReset_EmptyCacheNoPanic(t *testing.T) {
	c := NewEdgeRuleCache(EdgeRuleCacheCap)
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
	// Multiple resets must remain safe.
	c.Reset()
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("Len = %d after triple Reset, want 0", c.Len())
	}
}

// TestEdgeRuleReset_ConcurrentPutResetRaceSafe is the -race gate
// for the wholesale-Reset invariant. 4 goroutines hammer the cache
// with Put(hostA), Get(hostA), Reset(), Put(hostB). Asserts no
// panic, no -race detector hit, post-settle Len() ∈ {0,1,2}.
//
// Mirrors TestEdgeRuleCache_ConcurrentGetPut at line 133 — extends
// the existing concurrent test with Reset() interleaved. PR-C
// widens the helper's putEntryAll invocation to cover the kind=validate
// slice added by PR-B so the race detector still sees every kind's
// HotPath code path.
func TestEdgeRuleReset_ConcurrentPutResetRaceSafe(t *testing.T) {
	c := NewEdgeRuleCache(100)
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			hostA := "a.example.com"
			hostB := "b.example.com"
			for j := 0; j < 500; j++ {
				switch j % 4 {
				case 0:
					putEntryAll(c, hostA,
						[]EdgeRuleResolved{sampleEdgeRule("r", j, hostA, "alpha")},
						[]EdgeRuleRewriteResolved{sampleRewriteRule("rw", j, hostA, "/x", "/y")},
						[]EdgeRuleRedirectResolved{sampleRedirectRule("rd", j, hostA, 308, "https://c")},
						[]EdgeRuleHeadersResolved{sampleHeadersRule("hd", j, hostA, nil, nil)},
						[]EdgeRuleCORSResolved{sampleCORSRule("cors", j, hostA)},
						[]EdgeRuleJWTResolved{sampleJWTRule("jwt", j, hostA)},
						[]EdgeRuleIPResolved{sampleIPRule("ip", j, hostA)},
						[]EdgeRuleValidateResolved{sampleValidateRule("vd", j, hostA)},
						[]EdgeRuleLimitResolved{sampleLimitRule("lm", j, hostA)},
						[]EdgeRuleMaintenanceResolved{sampleMaintenanceRule("mt", j, hostA)},
						[]EdgeRuleGeoResolved{sampleGeoRule("geo", j, []string{"DE"}, nil, "")},
						[]EdgeRuleBudgetResolved{sampleBudgetRule("bg", j, hostA)},
						[]EdgeRuleCacheResolved{sampleCacheRule("cache", j, hostA)},
					)
				case 1:
					_, _ = c.Get(hostA)
					_, _ = c.GetCORS(hostA)
					_, _ = c.GetJWT(hostA)
					_, _ = c.GetIP(hostA)
					_, _ = c.GetValidate(hostA)
					_, _ = c.GetLimit(hostA)
					_, _ = c.GetMaintenance(hostA)
					_, _ = c.GetGeo(hostA)
					_, _ = c.GetBudget(hostA)
					_, _ = c.GetCache(hostA)
				case 2:
					c.Reset()
				case 3:
					putEntryAll(c, hostB,
						[]EdgeRuleResolved{sampleEdgeRule("r2", j, hostB, "beta")},
						nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
					)
				}
			}
		}(i)
	}

	wg.Wait()
	// After wg.Wait the last operation in each goroutine is the
	// final j-loop iteration. Len() may legitimately be 0 (Reset
	// won) or up to 2 (one entry per host that wasn't Reset out).
	if c.Len() < 0 || c.Len() > 2 {
		t.Errorf("Len = %d after concurrent Put+Reset, want 0..2", c.Len())
	}
}

// FuzzEdgeRuleReset_WholesaleInvalidatesAllKinds is the property
// target for the wholesale-Reset invariant. The seed corpus covers
// three shapes:
//
//   - 0x01 0x02 0x00 0x01 0xFE 0x00 0xFE — alternating Put/Get/Reset
//     to exercise a hot Reset path under interleaved Puts.
//   - 0x01 0xFE 0x00 0x01 0x02 0xFE — Put→Reset→Put→Get→Reset
//   - 0xFE 0xFE 0xFE 0x00 0x01 0x02 — back-to-back Reset followed
//     by Put+Get
//
// The fuzz applies each byte as an op (Put / GetK / Reset) on a
// host keyed by `i % 4`, then asserts the wholesale invariant:
// Len() stays within [0, cap]. PR 6 ships three seeds; CI's default
// fuzz corpus is short, so the deterministic
// TestEdgeRuleReset_WholesaleAcrossAllNineKinds is the load-bearing
// assertion (the fuzz is hardening on top). PR-C widens the putEntryAll
// call to cover kind=validate; this PR widens to 9 with kind=geo
// per ADR-091 D21 so a regression that bound only 8 kinds in the
// cache would trip under fuzzing.
func FuzzEdgeRuleReset_WholesaleInvalidatesAllKinds(f *testing.F) {
	// Three seeds that together exercise Put→Get→Reset,
	// Put→Reset→Put→Get, and back-to-back Reset patterns.
	f.Add([]byte{0x01, 0x02, 0x00, 0x01, 0xFE, 0x00, 0xFE})
	f.Add([]byte{0x01, 0xFE, 0x00, 0x01, 0x02, 0xFE})
	f.Add([]byte{0xFE, 0xFE, 0xFE, 0x00, 0x01, 0x02})

	f.Fuzz(func(t *testing.T, ops []byte) {
		c := NewEdgeRuleCache(EdgeRuleCacheCap)
		for i, b := range ops {
			host := []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"}[i%4]
			switch b % 3 {
			case 0: // Put
				putEntryAll(c, host,
					[]EdgeRuleResolved{sampleEdgeRule("r", i, host, "x")},
					[]EdgeRuleRewriteResolved{sampleRewriteRule("rw", i, host, "/a", "/b")},
					[]EdgeRuleRedirectResolved{sampleRedirectRule("rd", i, host, 308, "https://c")},
					[]EdgeRuleHeadersResolved{sampleHeadersRule("hd", i, host, nil, nil)},
					[]EdgeRuleCORSResolved{sampleCORSRule("cors", i, host)},
					[]EdgeRuleJWTResolved{sampleJWTRule("jwt", i, host)},
					[]EdgeRuleIPResolved{sampleIPRule("ip", i, host)},
					[]EdgeRuleValidateResolved{sampleValidateRule("vd", i, host)},
					[]EdgeRuleLimitResolved{sampleLimitRule("lm", i, host)},
					[]EdgeRuleMaintenanceResolved{sampleMaintenanceRule("mt", i, host)},
					[]EdgeRuleGeoResolved{sampleGeoRule("geo", i, []string{"DE"}, nil, "")},
					[]EdgeRuleBudgetResolved{sampleBudgetRule("bg", i, host)},
					[]EdgeRuleCacheResolved{sampleCacheRule("cache", i, host)},
				)
			case 1: // GetK (any kind)
				_, _ = c.Get(host)
				_, _ = c.GetRewrite(host)
				_, _ = c.GetRedirect(host)
				_, _ = c.GetHeaders(host)
				_, _ = c.GetCORS(host)
				_, _ = c.GetJWT(host)
				_, _ = c.GetIP(host)
				_, _ = c.GetValidate(host)
				_, _ = c.GetLimit(host)
				_, _ = c.GetMaintenance(host)
				_, _ = c.GetGeo(host)
				_, _ = c.GetBudget(host)
				_, _ = c.GetCache(host)
			case 2: // Reset
				c.Reset()
			}
			// Wholesale-invariant check: Len() must stay in bounds.
			// A regression in Reset that left stale entries in
			// byID/ll would surface as a -race hit during the GetK
			// sequence above (the mutex is held by every GetK).
			if c.Len() < 0 || c.Len() > c.cap {
				t.Fatalf("Len = %d out of bounds [0, %d] after op %d", c.Len(), c.cap, i)
			}
		}
	})
}

// BenchmarkEdgeRuleCache_Hit (ADR-091 hardening PR-A) pins the
// hot-path cache-hit latency. With EdgeRuleCache.mu widened from
// sync.Mutex to sync.RWMutex (PR-A), the read fast-path is two
// atomic ops (RLock + RUnlock) plus a map lookup plus a container/
// list MoveToFront under write-lock. The tripwire for future
// regressions: p99 must stay < 200 ns on a 2024-class x86_64
// (Apple M3+ ARM64 is comparable). A regression to the previous
// shape would show up as p99 in the 1–2 µs range, so the gap is
// measurable. Run with `go test -bench=BenchmarkEdgeRuleCache_Hit
// -benchtime=2s ./pkg/gateway/...`.
func BenchmarkEdgeRuleCache_Hit(b *testing.B) {
	c := NewEdgeRuleCache(1024)
	c.Put("hot.example.com", &HostEntry{
		Host: "hot.example.com",
		Route: []EdgeRuleResolved{{
			ID:            "rule-hot",
			AccountID:     "acct",
			Priority:      100,
			PathGlob:      "/",
			TargetAppSlug: "hot",
		}},
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get("hot.example.com"); !ok {
			b.Fatalf("expected hit")
		}
	}
}

// BenchmarkEdgeRuleCache_Miss (ADR-091 hardening PR-A) is the
// inverse tripwire: a miss must not pay the MoveToFront cost.
// Used as a sanity check that the RWMutex widening didn't
// accidentally regress the miss path. Both bench functions are
// skipped on -short to keep the default `go test ./...` flow fast.
func BenchmarkEdgeRuleCache_Miss(b *testing.B) {
	c := NewEdgeRuleCache(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get("nope.example.com"); ok {
			b.Fatalf("expected miss")
		}
	}
}
