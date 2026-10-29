package main

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRuleScopedInvalidator is the host- and app-scoped invalidation surface
// *gateway.PGBackend provides. Invalidators without it (legacy wiring, small
// test fakes) keep the wholesale flush.
type edgeRuleScopedInvalidator interface {
	InvalidateEdgeRuleHosts([]string)
	InvalidateResponseCacheByApp(string)
	Lookup(context.Context, string) (gateway.App, bool)
}

// edgeRuleAccountScopedInvalidator narrows a mutation to the sets that hold
// its account's rules (ADR-091 amendment 2026-10-26).
type edgeRuleAccountScopedInvalidator interface {
	InvalidateEdgeRuleAccountHosts(accountID, appID string, patterns []string)
}

// responseCacheAccountInvalidator drops one account's cached responses; false
// means the account's apps could not be listed (ADR-091 amendment
// 2026-10-27, GA hardening).
type responseCacheAccountInvalidator interface {
	InvalidateResponseCacheByAccount(ctx context.Context, accountID string) bool
}

// invalidateEdgeRuleScope applies one edge-rule mutation to the gateway's
// caches. A rule only applies to hosts its match_host pattern covers, so only
// those hosts' compiled rule sets are dropped, and only the apps that can
// have cached responses under those rules (the rule's own app plus the apps
// exact hosts resolve to) lose their response cache. Previously every
// mutation by any tenant flushed every node's rule and response caches, so
// one account editing rules in a loop forced fleet-wide cold reloads and
// cache misses for everyone.
//
// With the mutating account known (the live notification carries it; the
// durable repair ledger does not), only sets holding that account's rules are
// dropped, so a "*" or "@app" edit no longer reloads every tenant's hosts.
//
// An unknown scope (no hosts, or no scoped invalidator) still flushes
// everything. A wildcard pattern covers hosts that cannot be enumerated, but
// only the mutating account's (owned reads, ADR-091 amendment 2026-10-26), so
// it drops that account's apps' cached responses. Without the account (the
// repair ledger) or when its apps cannot be listed, it flushes the response
// cache.
func invalidateEdgeRuleScope(ctx context.Context, inv edgeRuleRepairInvalidator, accountID, appID string, hosts []string) {
	hosts = withCanonicalHostPatterns(hosts)
	scoped, ok := inv.(edgeRuleScopedInvalidator)
	if !ok || len(hosts) == 0 {
		inv.ResetEdgeRules()
		inv.InvalidateResponseCacheAll()
		return
	}
	if byAccount, ok := inv.(edgeRuleAccountScopedInvalidator); ok && accountID != "" {
		byAccount.InvalidateEdgeRuleAccountHosts(accountID, appID, hosts)
	} else {
		scoped.InvalidateEdgeRuleHosts(hosts)
	}
	apps := map[string]struct{}{}
	if appID != "" {
		apps[appID] = struct{}{}
	}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if strings.ContainsAny(host, "*?[") {
			byAccount, ok := inv.(responseCacheAccountInvalidator)
			if !ok || accountID == "" || !byAccount.InvalidateResponseCacheByAccount(ctx, accountID) {
				inv.InvalidateResponseCacheAll()
			}
			return
		}
		if app, found := scoped.Lookup(ctx, host); found && app.ID != "" {
			apps[app.ID] = struct{}{}
		}
	}
	for id := range apps {
		scoped.InvalidateResponseCacheByApp(id)
	}
}

// accountAppStore is the read accountAppIDLister needs; *state.PgStore has it.
type accountAppStore interface {
	ListApps(ctx context.Context, accountID string) ([]state.App, error)
}

// accountAppIDLister lists an account's app IDs for account-scoped response
// cache invalidation. The read runs on the notification loop, so it is bounded.
func accountAppIDLister(store accountAppStore) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, accountID string) ([]string, error) {
		listCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		apps, err := store.ListApps(listCtx, accountID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(apps))
		for _, app := range apps {
			ids = append(ids, app.ID)
		}
		return ids, nil
	}
}
