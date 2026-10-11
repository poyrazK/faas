package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// edgeRuleScopedInvalidator is the host- and app-scoped invalidation surface
// *gateway.PGBackend provides. Invalidators without it (legacy wiring, small
// test fakes) keep the wholesale flush.
type edgeRuleScopedInvalidator interface {
	InvalidateEdgeRuleHosts([]string)
	InvalidateResponseCacheByApp(string)
	Lookup(context.Context, string) (gateway.App, bool)
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
// An unknown scope (no hosts, or no scoped invalidator) still flushes
// everything. A wildcard pattern scopes the rule cache but flushes the
// response cache, whose entries are keyed by app and cannot be enumerated by
// host pattern.
func invalidateEdgeRuleScope(ctx context.Context, inv edgeRuleRepairInvalidator, appID string, hosts []string) {
	scoped, ok := inv.(edgeRuleScopedInvalidator)
	if !ok || len(hosts) == 0 {
		inv.ResetEdgeRules()
		inv.InvalidateResponseCacheAll()
		return
	}
	scoped.InvalidateEdgeRuleHosts(hosts)
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
			inv.InvalidateResponseCacheAll()
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
