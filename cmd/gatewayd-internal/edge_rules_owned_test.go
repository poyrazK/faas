package main

// adr: 091 — amendment 2026-10-26: a host's rules are read for the account
// that owns it, a mutation drops only its account's cached sets, and a host
// whose rules can be neither read nor served from a last-known set reports
// its policy unavailable, with each read counted by outcome.

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownedCountingStore counts which read the loader used.
type ownedCountingStore struct {
	mem                   *state.MemStore
	hostReads, ownedReads atomic.Int32
}

func (s *ownedCountingStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error) {
	s.hostReads.Add(1)
	return s.mem.MatchEdgeRulesForHost(ctx, host)
}

func (s *ownedCountingStore) GetCorsPresetByID(ctx context.Context, accountID, id string) (state.CorsPreset, error) {
	return s.mem.GetCorsPresetByID(ctx, accountID, id)
}

func (s *ownedCountingStore) OwnedEdgeRulesForHost(ctx context.Context, host, accountID, appID string) ([]state.EdgeRule, error) {
	s.ownedReads.Add(1)
	return s.mem.OwnedEdgeRulesForHost(ctx, host, accountID, appID)
}

func TestEdgeRuleLoadReadsOnlyTheOwnersRules(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	owner, _ := mem.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	other, _ := mem.CreateAccount(ctx, "other@example.test", api.PlanScale)
	shop, _ := mem.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "shop", Status: state.AppActive})
	blog, _ := mem.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "blog", Status: state.AppActive})
	own, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: owner.ID, AppID: shop.ID,
		MatchHost: "shop.example.com", MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Owner")})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: other.ID, AppID: blog.ID,
		MatchHost: "*", MatchPath: "/**", Priority: 0, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Foreign")})
	if err != nil {
		t.Fatal(err)
	}
	store := &ownedCountingStore{mem: mem}
	g := newGatewaydEdgeRules(store, testLogger(), nil, nil)

	serving := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, owner.ID), shop.ID)
	if got := g.MatchHeaders(serving, "shop.example.com", "/", "GET"); got == nil || got.ID != own.ID {
		t.Fatalf("MatchHeaders = %v, want the owner's rule", got)
	}
	entry, ok := g.cache.GetHost(gateway.EdgeRuleCacheKey("shop.example.com", shop.ID))
	if !ok || len(entry.Headers) != 1 || entry.Headers[0].ID != own.ID {
		t.Fatalf("cached set = %+v, want only the owner's rule (not %s)", entry, foreign.ID)
	}
	if store.ownedReads.Load() != 1 || store.hostReads.Load() != 0 {
		t.Fatalf("reads: owned %d, every-account %d; want the owned read only", store.ownedReads.Load(), store.hostReads.Load())
	}

	// A read before the host's owner is known still reads every account's
	// rules for the host, as before.
	g.MatchHeaders(ctx, "unresolved.example.com", "/", "GET")
	if store.hostReads.Load() != 1 {
		t.Fatalf("every-account reads = %d, want 1 for an unowned context", store.hostReads.Load())
	}
}

func TestEdgePolicyAvailableAndLoadOutcomes(t *testing.T) {
	var down atomic.Bool
	s := &concurrentEdgeStore{load: func(context.Context, string, int32) ([]state.EdgeRule, error) {
		if down.Load() {
			return nil, errors.New("postgres unavailable")
		}
		return nil, nil
	}}
	m := gateway.NewMetrics()
	g := newGatewaydEdgeRules(s, testLogger(), nil, m)
	now := time.Unix(1_000, 0)
	clock := func() time.Time { return now }
	g.clock = clock
	g.cache.SetClock(clock)
	ctx := context.Background()

	down.Store(true)
	if g.EdgePolicyAvailable(ctx, "a.example.com") {
		t.Fatal("a host never loaded, with reads failing, reported its policy available")
	}
	down.Store(false)
	now = now.Add(edgeRuleLoadRetryBackoff)
	if !g.EdgePolicyAvailable(ctx, "a.example.com") {
		t.Fatal("a host whose read succeeded reported its policy unavailable")
	}
	reads := s.calls.Load()
	if !g.EdgePolicyAvailable(ctx, "a.example.com") || s.calls.Load() != reads {
		t.Fatal("a cached host should answer without another read")
	}
	down.Store(true)
	now = now.Add(time.Hour) // past the cache TTL: the set is last-known only
	if !g.EdgePolicyAvailable(ctx, "a.example.com") {
		t.Fatal("a host with a last-known set reported its policy unavailable during the outage")
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{
		`gateway_edge_rule_load_total{outcome="failed"} 1`,
		`gateway_edge_rule_load_total{outcome="loaded"} 1`,
		`gateway_edge_rule_load_total{outcome="last_known"} 1`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// scopeRecordingInvalidator records which invalidation a mutation used.
type scopeRecordingInvalidator struct {
	calls []string
}

func (f *scopeRecordingInvalidator) ResetEdgeRules() { f.calls = append(f.calls, "reset") }
func (f *scopeRecordingInvalidator) InvalidateResponseCacheAll() {
	f.calls = append(f.calls, "response-all")
}
func (f *scopeRecordingInvalidator) InvalidateEdgeRuleHosts(hosts []string) {
	f.calls = append(f.calls, "hosts:"+strings.Join(hosts, ","))
}
func (f *scopeRecordingInvalidator) InvalidateResponseCacheByApp(app string) {
	f.calls = append(f.calls, "response-app:"+app)
}
func (f *scopeRecordingInvalidator) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{}, false
}
func (f *scopeRecordingInvalidator) InvalidateEdgeRuleAccountHosts(account, app string, hosts []string) {
	f.calls = append(f.calls, "account:"+account+"/"+app+":"+strings.Join(hosts, ","))
}

func TestInvalidateEdgeRuleScopeByAccount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		hosts   []string
		want    []string
	}{
		{name: "the live notification narrows by account", account: "acct-a", hosts: []string{api.EdgeRuleAppHosts},
			want: []string{"account:acct-a/app-a:@app", "response-app:app-a"}},
		{name: "a wildcard flushes responses without an account invalidator", account: "acct-a", hosts: []string{"*"},
			want: []string{"account:acct-a/app-a:*", "response-all"}},
		{name: "the repair ledger has no account", hosts: []string{"a.example.com"},
			want: []string{"hosts:a.example.com", "response-app:app-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &scopeRecordingInvalidator{}
			invalidateEdgeRuleScope(context.Background(), f, tc.account, "app-a", tc.hosts)
			if !slices.Equal(f.calls, tc.want) {
				t.Fatalf("calls = %v, want %v", f.calls, tc.want)
			}
		})
	}
}

// accountCacheInvalidator adds account-scoped response cache invalidation;
// listed is what InvalidateResponseCacheByAccount reports.
type accountCacheInvalidator struct {
	scopeRecordingInvalidator
	listed bool
}

func (f *accountCacheInvalidator) InvalidateResponseCacheByAccount(_ context.Context, account string) bool {
	f.calls = append(f.calls, "response-account:"+account)
	return f.listed
}

// adr: 091 — amendment 2026-10-27 (GA hardening): a wildcard change drops
// only the mutating account's cached responses; without the account, or when
// its apps cannot be listed, the whole response cache goes.
func TestInvalidateEdgeRuleScopeWildcardResponseCacheByAccount(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		listed  bool
		want    []string
	}{
		{name: "the account's apps are listed", account: "acct-a", listed: true,
			want: []string{"account:acct-a/app-a:*.example.com", "response-account:acct-a"}},
		{name: "listing the apps failed", account: "acct-a", listed: false,
			want: []string{"account:acct-a/app-a:*.example.com", "response-account:acct-a", "response-all"}},
		{name: "the repair ledger has no account", listed: true,
			want: []string{"hosts:*.example.com", "response-all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &accountCacheInvalidator{listed: tc.listed}
			invalidateEdgeRuleScope(context.Background(), f, tc.account, "app-a", []string{"*.example.com"})
			if !slices.Equal(f.calls, tc.want) {
				t.Fatalf("calls = %v, want %v", f.calls, tc.want)
			}
		})
	}
}

// adr: 091 — amendment 2026-10-27 (GA hardening): the backend drops the
// listed apps' entries and keeps every other account's.
func TestPGBackendInvalidateResponseCacheByAccount(t *testing.T) {
	cache := gateway.NewResponseCache()
	put := func(app string) gateway.CacheKey {
		t.Helper()
		key := gateway.CacheKey{AppID: app, RuleID: "rule-" + app, Method: "GET", NormalizedPath: "/"}
		if !cache.Put(key, 200, nil, []byte("ok"), time.Now().Add(time.Minute), time.Now().Add(time.Minute), nil) {
			t.Fatalf("put %s", app)
		}
		return key
	}
	mine, theirs := put("app-mine"), put("app-theirs")
	backend := gateway.NewPGBackend(nil, nil, testLogger()).WithResponseCache(cache).
		WithAccountAppLister(func(_ context.Context, account string) ([]string, error) {
			if account != "acct-mine" {
				return nil, errors.New("unexpected account " + account)
			}
			return []string{"app-mine"}, nil
		})
	if !backend.InvalidateResponseCacheByAccount(context.Background(), "acct-mine") {
		t.Fatal("listing the account's apps reported failure")
	}
	if got, _ := cache.Get(mine); got == "fresh" {
		t.Fatal("the account's entry survived")
	}
	if got, _ := cache.Get(theirs); got != "fresh" {
		t.Fatalf("another account's entry = %q, want fresh", got)
	}
	if backend.InvalidateResponseCacheByAccount(context.Background(), "acct-other") {
		t.Fatal("a failed listing reported success")
	}
	if gateway.NewPGBackend(nil, nil, testLogger()).WithResponseCache(cache).InvalidateResponseCacheByAccount(context.Background(), "acct-mine") {
		t.Fatal("a backend without a lister reported success")
	}
}
