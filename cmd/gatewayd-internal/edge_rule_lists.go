package main

import (
	"context"
	"log/slog"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRuleListResolver is the ADR-833 read the host loader needs.
type edgeRuleListResolver interface {
	EdgeRuleListsByName(ctx context.Context, accountID string, names []string) ([]state.EdgeRuleList, error)
}

// resolveEdgeRuleLists attaches the account lists each rule's condition
// references, compiled once per host load. A list missing from the store
// (or a store without lists) leaves the reference unresolved, so the
// condition fails to compile and the rule never matches. A store error
// fails the load, keeping the host's last-known-good rules.
func (g *gatewaydEdgeRules) resolveEdgeRuleLists(ctx context.Context, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	wanted := map[string]map[string]struct{}{}
	for _, r := range rules {
		for _, name := range api.EdgeRuleMatchListRefs(r.Match) {
			if wanted[r.AccountID] == nil {
				wanted[r.AccountID] = map[string]struct{}{}
			}
			wanted[r.AccountID][name] = struct{}{}
		}
	}
	resolver, ok := g.store.(edgeRuleListResolver)
	if len(wanted) == 0 || !ok {
		return rules, nil
	}
	resolved := make(map[string]api.EdgeRuleLists, len(wanted))
	for accountID, set := range wanted {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		lists, err := resolver.EdgeRuleListsByName(ctx, accountID, names)
		if err != nil {
			return nil, err
		}
		compiled := make(api.EdgeRuleLists, len(lists))
		for _, l := range lists {
			c, err := api.CompileEdgeRuleList(l.Kind, l.Items)
			if err != nil {
				slog.Warn("edge rule list does not compile; referencing rules disabled", "list", l.Name, "account", accountID, "err", err)
				continue
			}
			compiled[l.Name] = c
		}
		resolved[accountID] = compiled
	}
	out := make([]state.EdgeRule, len(rules))
	copy(out, rules)
	for i := range out {
		if out[i].Match != nil {
			out[i].MatchLists = resolved[out[i].AccountID]
		}
	}
	return out, nil
}
