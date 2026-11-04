package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingOwnerSetStore counts the gateway's owner-set and per-host rule reads.
type countingOwnerSetStore struct {
	mem        *state.MemStore
	ownerReads atomic.Int32
	hostReads  atomic.Int32
}

func (s *countingOwnerSetStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error) {
	s.hostReads.Add(1)
	return s.mem.MatchEdgeRulesForHost(ctx, host)
}

func (s *countingOwnerSetStore) GetCorsPresetByID(ctx context.Context, accountID, id string) (state.CorsPreset, error) {
	return s.mem.GetCorsPresetByID(ctx, accountID, id)
}

func (s *countingOwnerSetStore) OwnedEdgeRulesForHost(ctx context.Context, host, accountID, appID string) ([]state.EdgeRule, error) {
	s.hostReads.Add(1)
	return s.mem.OwnedEdgeRulesForHost(ctx, host, accountID, appID)
}

func (s *countingOwnerSetStore) OwnerEdgeRules(ctx context.Context, accountID string, limit int) ([]state.EdgeRule, bool, error) {
	s.ownerReads.Add(1)
	return s.mem.OwnerEdgeRules(ctx, accountID, limit)
}

// adr: 091 — amendment 2026-10-27: every subdomain under an owner's wildcard
// rule is served from one read of the owner's rule set; the owner's own
// mutation and the TTL reload it, another account's mutation does not, and
// an owner past the set bound keeps the per-host read.
func TestEdgeRuleOwnerSetServesWildcardSubdomains(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acctA, _ := mem.CreateAccount(ctx, "owner-set-a@example.test", api.PlanPro)
	acctB, _ := mem.CreateAccount(ctx, "owner-set-b@example.test", api.PlanPro)
	shop, _ := mem.CreateApp(ctx, state.App{AccountID: acctA.ID, Slug: "owner-set-shop", Status: state.AppActive})
	other, _ := mem.CreateApp(ctx, state.App{AccountID: acctB.ID, Slug: "owner-set-other", Status: state.AppActive})
	mk := func(acct, app, host string, priority int) string {
		t.Helper()
		r, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct, AppID: app, MatchHost: host, MatchPath: "/**",
			Priority: priority, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Owner-Set")})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	wildcard := mk(acctA.ID, shop.ID, "*.tenants.example.com", 10)
	mk(acctB.ID, other.ID, "*", 1) // another account's catch-all never applies

	store := &countingOwnerSetStore{mem: mem}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	now := time.Now()
	matcher.clock = func() time.Time { return now }
	owned := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, acctA.ID), shop.ID)
	match := func(host string) string {
		t.Helper()
		if got := matcher.MatchHeaders(owned, host, "/", "GET"); got != nil {
			return got.ID
		}
		return ""
	}
	for i := range 20 {
		if got := match(fmt.Sprintf("t%d.tenants.example.com", i)); got != wildcard {
			t.Fatalf("t%d = %q, want the wildcard rule", i, got)
		}
	}
	if got := match("unrelated.example.org"); got != "" {
		t.Fatalf("uncovered host matched %q", got)
	}
	if store.ownerReads.Load() != 1 || store.hostReads.Load() != 0 {
		t.Fatalf("reads: owner=%d host=%d, want 1 owner-set read and no per-host read", store.ownerReads.Load(), store.hostReads.Load())
	}

	matcher.InvalidateAccountHosts(acctB.ID, other.ID, []string{"*"})
	match("t20.tenants.example.com")
	if store.ownerReads.Load() != 1 {
		t.Fatal("another account's mutation dropped this owner's set")
	}

	exact := mk(acctA.ID, shop.ID, "t21.tenants.example.com", 1)
	matcher.InvalidateAccountHosts(acctA.ID, shop.ID, []string{"t21.tenants.example.com"})
	if got := match("t21.tenants.example.com"); got != exact || store.ownerReads.Load() != 2 {
		t.Fatalf("after the owner's mutation: match=%q reads=%d, want the new rule from a fresh set", got, store.ownerReads.Load())
	}

	now = now.Add(edgeRuleOwnerSetTTL + time.Second)
	match("t22.tenants.example.com")
	if store.ownerReads.Load() != 3 {
		t.Fatalf("expired set reused: owner reads=%d", store.ownerReads.Load())
	}

	big, _ := mem.CreateAccount(ctx, "owner-set-big@example.test", api.PlanScale)
	bigApp, _ := mem.CreateApp(ctx, state.App{AccountID: big.ID, Slug: "owner-set-big", Status: state.AppActive})
	for i := range edgeRuleOwnerSetMaxRules + 1 {
		mk(big.ID, bigApp.ID, fmt.Sprintf("h%d.big.example.com", i), i)
	}
	bigCtx := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, big.ID), bigApp.ID)
	for _, host := range []string{"h1.big.example.com", "h2.big.example.com"} {
		if got := matcher.MatchHeaders(bigCtx, host, "/", "GET"); got == nil {
			t.Fatalf("%s: no rule from the per-host read", host)
		}
	}
	if store.ownerReads.Load() != 4 || store.hostReads.Load() != 2 {
		t.Fatalf("oversized owner: owner reads=%d host reads=%d, want one set read then per-host reads", store.ownerReads.Load(), store.hostReads.Load())
	}
}
