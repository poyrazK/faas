// adr: 570 — immutable request policy and readiness/deadline ownership.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficPolicySealIsIndependentAndAppDigestIncludesEffectiveInputs(t *testing.T) {
	entry := &HostEntry{Host: "app.test", Headers: []EdgeRuleHeadersResolved{{
		ID: "headers", MatchHeaders: map[string]string{"X-Mode": "original"},
		ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Policy", Value: "old", Action: "set"}},
	}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(t.Context(), "app.test", entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.Headers[0].MatchHeaders["X-Mode"] = "changed"
	entry.Headers[0].ResponseHeaders[0].Value = "new"
	pinned, _ := PinnedHostPolicy(ctx, "app.test")
	if pinned.Headers[0].MatchHeaders["X-Mode"] != "original" || pinned.Headers[0].ResponseHeaders[0].Value != "old" {
		t.Fatal("source mutation changed a pinned policy")
	}
	app := App{ID: "app", AccountID: "owner", Plan: api.PlanPro, CORSDefaultOrigins: []string{"https://old.test"}}
	frozen, before, err := freezeTrafficApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	app.CORSDefaultOrigins[0] = "https://new.test"
	if frozen.CORSDefaultOrigins[0] != "https://old.test" {
		t.Fatal("app snapshot retained mutable source origins")
	}
	if _, same, err := freezeTrafficApp(ctx, frozen); err != nil || same != before {
		t.Fatalf("unchanged effective policy digest changed: %s %s %v", before, same, err)
	}
	for _, changed := range []App{app, {ID: frozen.ID, AccountID: frozen.AccountID, Plan: api.PlanScale},
		{ID: frozen.ID, AccountID: frozen.AccountID, Plan: frozen.Plan, RequireAuthn: true}} {
		if _, after, err := freezeTrafficApp(ctx, changed); err != nil || after == before {
			t.Fatalf("changed app policy kept digest: %s %v", after, err)
		}
	}
}

type routeSnapshotMatcherStub struct {
	declaredRouteMatcherStub
	revision string
	fail     bool
	slow     bool
}

func (m *routeSnapshotMatcherStub) PinDeclaredRoutePolicy(ctx context.Context, app App) (context.Context, App, string, error) {
	if m.slow {
		<-ctx.Done()
		return nil, App{}, "", ctx.Err()
	}
	if m.fail {
		return nil, App{}, "", errors.New("contract unavailable")
	}
	app.OnlyAllowDeclaredRoutes = true
	app.DeclaredRoutes = []DeclaredRoute{{Path: "/allowed", Methods: []string{"GET"}}}
	return ctx, app, m.revision, nil
}

func TestTrafficPolicyIncludesPinnedDeclaredContractBeforeGuestWork(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	entry := &HostEntry{Host: backend.host}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	h.WithEdgeRules(snapshotTestMatcher{entry: entry}, nil, nil)
	matcher := &routeSnapshotMatcherStub{declaredRouteMatcherStub: declaredRouteMatcherStub{allowed: true}, revision: "contract-old"}
	h.WithDeclaredRouteMatcher(matcher)
	forwarded := 0
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++; w.WriteHeader(http.StatusNoContent) })
	})
	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/allowed", nil))
		return rec
	}
	first := request()
	if first.Code != http.StatusNoContent || matcher.called.Load() != 1 {
		t.Fatalf("scoped declaration was not applied before guest: %d/%d", first.Code, matcher.called.Load())
	}
	matcher.revision = "contract-new"
	second := request()
	if first.Header().Get(TrafficPolicyRevisionHeader) == second.Header().Get(TrafficPolicyRevisionHeader) {
		t.Fatal("changed imported contract missing from effective fingerprint")
	}
	matcher.fail = true
	refused := request()
	if refused.Code != http.StatusServiceUnavailable || !strings.Contains(refused.Body.String(), api.CodeTrafficPolicyUnavailable) || forwarded != 2 {
		t.Fatalf("unverified contract reached guest: %d/%s forwards=%d", refused.Code, refused.Body, forwarded)
	}
}

func TestTotalDeadlineBoundsDeclaredRouteSnapshotLoad(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	setTotalBudget(h, backend.app, 50)
	entry := &HostEntry{Host: backend.host, Budget: []EdgeRuleBudgetResolved{{ID: "total", AccountID: backend.app.AccountID, AppID: backend.app.ID, BudgetMs: 1000, TotalDeadlineMs: 50}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	h.WithEdgeRules(snapshotTestMatcher{entry: entry}, nil, nil)
	h.WithDeclaredRouteMatcher(&routeSnapshotMatcherStub{slow: true})
	rec := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	assertTotalTimeout(t, rec)
	if time.Since(started) > 200*time.Millisecond || backend.admits != 0 {
		t.Fatal("document pin escaped total deadline or reached wake")
	}
}

type snapshotTestMatcher struct {
	noOpEdgeRuleMatcher
	entry *HostEntry
	err   error
}

func (m snapshotTestMatcher) PinHostPolicy(ctx context.Context, host string) (context.Context, error) {
	if m.err != nil {
		return nil, m.err
	}
	return WithPinnedHostPolicy(ctx, host, m.entry)
}

func (m snapshotTestMatcher) MatchBudget(ctx context.Context, host, path, method string) *EdgeRuleBudgetResolved {
	entry, ok := PinnedHostPolicy(ctx, host)
	if !ok {
		return nil
	}
	return PickFirstBudgetMatch(entry.Budget, path, method, EdgeRuleRequestHeaders(ctx))
}

type updatingPolicyBackend struct {
	*fakeBackend
	onAdmit func()
}

func (b *updatingPolicyBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	b.onAdmit()
	return b.fakeBackend.Admit(ctx, app, deployment, scope, trigger, maxConcurrency)
}

func TestTrafficPolicyPinsHostAndAppAcrossActualHandlerWake(t *testing.T) {
	fixture, backend, _ := newTestHandler(t)
	backend.app.CORSDefaultOrigins = []string{"https://old.test"}
	entry := &HostEntry{Host: backend.host, Budget: []EdgeRuleBudgetResolved{{
		ID: "old-budget", AccountID: backend.app.AccountID, AppID: backend.app.ID, BudgetMs: 1000,
	}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	expectedCtx, err := WithPinnedHostPolicy(t.Context(), backend.host, entry)
	if err != nil {
		t.Fatal(err)
	}
	expectedApp, _ := backend.Lookup(t.Context(), backend.host)
	_, expectedRevision, err := freezeTrafficApp(expectedCtx, expectedApp)
	if err != nil {
		t.Fatal(err)
	}
	updating := &updatingPolicyBackend{fakeBackend: backend, onAdmit: func() {
		entry.Budget[0].ID = "changed-during-wake"
		backend.mu.Lock()
		backend.app.CORSDefaultOrigins[0] = "https://changed.test"
		backend.mu.Unlock()
	}}
	h := NewHandlerWith(updating, NewMetrics(), fixture.log)
	h.WithEdgeRules(snapshotTestMatcher{entry: entry}, nil, nil)
	var forwardedRevision string
	h.proxyByNode = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwardedRevision = TrafficPolicyRevision(r.Context())
			pinned, _ := PinnedHostPolicy(r.Context(), backend.host)
			if pinned.Budget[0].ID != "old-budget" {
				t.Error("wake replaced the request's budget policy")
			}
			w.Header().Set(TrafficPolicyRevisionHeader, "guest override")
			w.WriteHeader(http.StatusOK)
		})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusOK || backend.admits != 1 || forwardedRevision != expectedRevision || rec.Header().Get(TrafficPolicyRevisionHeader) != forwardedRevision {
		t.Fatalf("wake policy evidence: %d admits=%d forwarded=%s response=%s", rec.Code, backend.admits,
			forwardedRevision, rec.Header().Get(TrafficPolicyRevisionHeader))
	}
}

func TestTrafficPolicyUnavailableRefusesBeforeWakeAndProtectsResponseEvidence(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	h.WithEdgeRules(snapshotTestMatcher{err: errors.New("store unavailable")}, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), api.CodeTrafficPolicyUnavailable) || backend.admits != 0 {
		t.Fatalf("unverified policy reached admission: %d %s admits=%d", rec.Code, rec.Body, backend.admits)
	}
	for _, revision := range []string{"traffic-v1:verified", ""} {
		rec = httptest.NewRecorder()
		writer := &statusRecorder{ResponseWriter: rec, trafficPolicyRevision: revision}
		writer.Header().Set(TrafficPolicyRevisionHeader, "guest forgery")
		writer.installHeaderOps([]EdgeRuleHeaderOp{{Name: TrafficPolicyRevisionHeader, Value: "rule forgery", Action: "set"}})
		writer.WriteHeader(http.StatusOK)
		if got := rec.Header().Get(TrafficPolicyRevisionHeader); got != revision {
			t.Fatalf("response evidence overwritten: %q, want %q", got, revision)
		}
	}
}

func BenchmarkTrafficPolicyFreeze(b *testing.B) {
	entry := &HostEntry{Host: "app.test", Budget: []EdgeRuleBudgetResolved{{ID: "budget", BudgetMs: 1000}}}
	if err := entry.SealPolicy(); err != nil {
		b.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(b.Context(), entry.Host, entry)
	if err != nil {
		b.Fatal(err)
	}
	app := App{ID: "app", AccountID: "owner", Plan: api.PlanScale, PublicAuth: PublicAuthConfig{Mode: "open"},
		CORSDefaultOrigins: []string{"https://app.test"}, DeclaredRoutes: []DeclaredRoute{{Path: "/orders/{id}", Methods: []string{"GET", "POST"}}},
		Favicon: bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 2048)}
	for _, bench := range []struct {
		name    string
		freezes *appFreezes
	}{{"unmemoized", nil}, {"memoized", newAppFreezes()}} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := bench.freezes.freeze(ctx, app); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// legacyFreezeTrafficApp is the freeze as it was before memoization. The
// memoized one must produce the same copy and byte-for-byte the same digest:
// the revision is response and span evidence, and gateways on both sides of a
// release must name one policy the same way.
func legacyFreezeTrafficApp(ctx context.Context, app App) (App, string, error) {
	encoded, err := json.Marshal(app)
	if err != nil {
		return App{}, "", err
	}
	var frozen App
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return App{}, "", err
	}
	pinned, _ := ctx.Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	hosts := make(map[string]string, len(pinned))
	for host, entry := range pinned {
		hosts[host] = entry.revision
	}
	limits, _ := api.LimitsFor(app.Plan)
	effective, err := json.Marshal(struct {
		App            json.RawMessage
		Hosts          map[string]string
		Limits         api.Limits
		DeclaredRoutes string `json:",omitempty"`
	}{encoded, hosts, limits, declaredRoutePolicyRevision(ctx)})
	if err != nil {
		return App{}, "", err
	}
	return frozen, policyDigest(effective), nil
}

func TestAppFreezesMatchTheUnmemoizedFreeze(t *testing.T) {
	enabled := true
	apps := map[string]App{
		"empty": {},
		"minimal": {ID: "app", AccountID: "owner", Plan: api.PlanFree},
		"unknown plan": {ID: "app", AccountID: "owner", Plan: "legacy-plan"},
		"escaped strings": {ID: "app", AccountID: "owner", Plan: api.PlanPro,
			RobotsTxt: "User-agent: *\nDisallow: /<admin>&\u2028\u2029\"quoted\"", CanonicalHost: "ünïcode.test"},
		"nested values": {ID: "app", AccountID: "owner", Plan: api.PlanScale, Favicon: bytes.Repeat([]byte{0, 0xff, '<'}, 700),
			CORSDefaultEnabled: &enabled, CORSDefaultOrigins: []string{"https://a.test", "https://b.test"},
			DeclaredRoutes: []DeclaredRoute{{Path: "/orders/{id}", Methods: []string{"GET", "POST"}}, {Path: "/empty", Methods: []string{}}},
			Sidecars: []AppSidecar{{Name: "metrics", Port: 9100}}, Ports: []AppPort{{Name: "http", Port: 8080, Protocol: "http"}},
			PreAuthRateLimit: &api.PreAuthRateLimitConfig{Mode: "enforce", RequestsPerSecond: 10, Burst: 20},
			PublicAuth: PublicAuthConfig{Mode: "basic", BasicSealed: []byte("sealed"), IPAllowlist: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
			ImportedRoutePolicy: &ImportedRoutePolicy{Document: []byte("not encoded"), Found: true}},
		"empty slices": {ID: "app", AccountID: "owner", Plan: api.PlanHobby, CORSDefaultOrigins: []string{}, DeclaredRoutes: []DeclaredRoute{}},
	}
	hostA := &HostEntry{Host: "a.test", Budget: []EdgeRuleBudgetResolved{{ID: "budget", BudgetMs: 1000}}}
	hostB := &HostEntry{Host: "b.test"}
	for _, entry := range []*HostEntry{hostA, hostB} {
		if err := entry.SealPolicy(); err != nil {
			t.Fatal(err)
		}
	}
	pinnedOne, err := WithPinnedHostPolicy(t.Context(), hostA.Host, hostA)
	if err != nil {
		t.Fatal(err)
	}
	pinnedTwo, err := WithPinnedHostPolicy(pinnedOne, hostB.Host, hostB)
	if err != nil {
		t.Fatal(err)
	}
	contexts := map[string]context.Context{
		"no pins":         t.Context(),
		"one pinned host": pinnedOne,
		"two pinned hosts and declared routes": context.WithValue(pinnedTwo, declaredRoutePolicyRevisionKey{},
			"declared-routes-v1:<&>"),
	}
	freezes := newAppFreezes()
	for appName, app := range apps {
		for ctxName, ctx := range contexts {
			wantApp, wantRevision, err := legacyFreezeTrafficApp(ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			for _, pass := range []string{"miss", "hit"} {
				gotApp, gotRevision, err := freezes.freeze(ctx, app)
				if err != nil {
					t.Fatalf("%s/%s/%s: %v", appName, ctxName, pass, err)
				}
				if gotRevision != wantRevision || !reflect.DeepEqual(gotApp, wantApp) {
					t.Fatalf("%s/%s/%s: memoized freeze diverged: %s, want %s", appName, ctxName, pass, gotRevision, wantRevision)
				}
			}
		}
	}
}

func TestAppFreezesShareOnlyAnIdenticalEncoding(t *testing.T) {
	freezes := newAppFreezes()
	app := App{ID: "app", AccountID: "owner", Plan: api.PlanPro, CORSDefaultOrigins: []string{"https://old.test"}}
	first, revision, err := freezes.freeze(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	again, same, err := freezes.freeze(t.Context(), app)
	if err != nil || same != revision || &again.CORSDefaultOrigins[0] != &first.CORSDefaultOrigins[0] {
		t.Fatalf("identical encoding was not shared: %s %s %v", revision, same, err)
	}
	if &first.CORSDefaultOrigins[0] == &app.CORSDefaultOrigins[0] {
		t.Fatal("the shared copy aliases the source app")
	}
	app.CORSDefaultOrigins[0] = "https://new.test"
	changed, changedRevision, err := freezes.freeze(t.Context(), app)
	if err != nil || changedRevision == revision || changed.CORSDefaultOrigins[0] != "https://new.test" || first.CORSDefaultOrigins[0] != "https://old.test" {
		t.Fatalf("a changed app reused the old policy: %s %v", changedRevision, err)
	}

	// Two encodings that share a hash key: the cached one is not returned.
	other := App{ID: "other", AccountID: "owner", Plan: api.PlanFree}
	encoded, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	impostor, err := sealAppFreeze(0, []byte(`{"ID":"impostor"}`))
	if err != nil {
		t.Fatal(err)
	}
	impostor.key = maphash.Bytes(freezes.seed, encoded)
	freezes.store(impostor)
	frozen, otherRevision, err := freezes.freeze(t.Context(), other)
	if err != nil || frozen.ID != "other" {
		t.Fatalf("a colliding entry answered for another app: %+v %v", frozen.ID, err)
	}
	if _, want, _ := legacyFreezeTrafficApp(t.Context(), other); otherRevision != want {
		t.Fatalf("collision changed the digest: %s, want %s", otherRevision, want)
	}
}

func TestAppFreezesStayWithinTheirByteBudget(t *testing.T) {
	freezes := newAppFreezes()
	oversized := App{ID: "oversized", Favicon: make([]byte, appFreezeCacheBytes/16)}
	if _, _, err := freezes.freeze(t.Context(), oversized); err != nil {
		t.Fatal(err)
	}
	if len(freezes.index) != 0 {
		t.Fatal("an encoding above the per-entry bound was kept")
	}
	favicon := make([]byte, appFreezeCacheBytes/64)
	for i := range 64 {
		if _, _, err := freezes.freeze(t.Context(), App{ID: fmt.Sprintf("app-%d", i), Favicon: favicon}); err != nil {
			t.Fatal(err)
		}
		if freezes.bytes > appFreezeCacheBytes || freezes.order.Len() != len(freezes.index) {
			t.Fatalf("cache over budget after %d apps: %d bytes, %d/%d entries", i+1, freezes.bytes, freezes.order.Len(), len(freezes.index))
		}
	}
	if freezes.order.Len() == 64 {
		t.Fatal("no entry was evicted")
	}
	newest := freezes.order.Front().Value.(*appFreeze)
	if newest.frozen.ID != "app-63" {
		t.Fatalf("most recent entry = %s, want app-63", newest.frozen.ID)
	}
}

func TestAppFreezesAreSafeForConcurrentRequests(t *testing.T) {
	freezes := newAppFreezes()
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				app := App{ID: fmt.Sprintf("app-%d", (worker+i)%5), AccountID: "owner", Plan: api.PlanPro}
				got, revision, err := freezes.freeze(context.Background(), app)
				_, want, _ := legacyFreezeTrafficApp(context.Background(), app)
				if err != nil || got.ID != app.ID || revision != want {
					t.Errorf("concurrent freeze of %s: %s, want %s (%v)", app.ID, revision, want, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A pinned policy is a JSON copy of the cached entry. ADR-962 conditions must
// survive it: an empty decoded program would silently never match, so a
// conditional deny rule would stop applying on every pinned request.
func TestTrafficPolicySealPreservesEdgeRuleConditions(t *testing.T) {
	match, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Field: "method", Op: "eq", Value: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	entry := &HostEntry{Host: "app.test", IP: []EdgeRuleIPResolved{{
		EdgeRuleCondition: EdgeRuleCondition{Match: match, RuleID: "ip", AppID: "app"},
		ID:                "ip", PathGlob: "/*",
	}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(t.Context(), "app.test", entry)
	if err != nil {
		t.Fatal(err)
	}
	pinned, _ := PinnedHostPolicy(ctx, "app.test")
	condition := pinned.IP[0].Match
	if condition == nil {
		t.Fatal("pinned rule lost its condition and would match every request")
	}
	if !condition.Matches(api.EdgeRuleMatchInput{Method: "POST", Headers: http.Header{}}) ||
		condition.Matches(api.EdgeRuleMatchInput{Method: "GET", Headers: http.Header{}}) {
		t.Fatal("pinned condition does not evaluate like the cached rule")
	}
}
