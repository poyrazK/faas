package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The authoritative host policy (ADR-570, FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY)
// reads a host's rules in a Postgres snapshot instead of through
// hostStoreRules, and had none of the cached path's later rule filters, so
// turning it on brought back three fixed bugs (ADR-091 amendment 2026-10-31,
// round 7). The snapshot's rule query (ReadPublicHostEdgeRules) now leaves out
// deleted apps' rules and other scopes' rules in the same read, which keeps the
// snapshot within its short budget; a preview's inherited production rules are
// added here.

// publicPreviewInheritedRules returns, for a PR preview whose production app
// is productionAppID (the resolved app's ProductionAppID, "" for any other
// app), the production app's app-wide rules that previews apply (amendment
// 2026-10-30), as previewInheritedRules does on the cached path. Any other app
// costs no read.
func publicPreviewInheritedRules(ctx context.Context, reader state.PublicHostPolicyReader, host, account, productionAppID string) ([]state.EdgeRule, error) {
	if productionAppID == "" {
		return nil, nil
	}
	rules, err := reader.PublicHostAppEdgeRules(ctx, host, account, productionAppID, false)
	if err != nil {
		return nil, err
	}
	inherited := rules[:0:0]
	for _, rule := range rules {
		if rule.AppID == productionAppID && api.IsEdgeRuleAppHosts(rule.MatchHost) && state.EdgeRuleAppliesToPreviews(rule) {
			inherited = append(inherited, rule)
		}
	}
	return inherited, nil
}
