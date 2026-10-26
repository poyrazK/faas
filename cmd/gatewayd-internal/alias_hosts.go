package main

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026100000: gatewayd-public rewrites a request for an alias apps
// domain host to its canonical host, so gatewayd-internal only routes
// canonical hosts. Edge rules a customer stored with the alias name (rules
// written before the apps domain moved) must keep applying to that app, and a
// mutation of such a rule must fence and invalidate the canonical host. With
// no alias configured both helpers are no-ops.

// withAliasHostRules merges into rules the rules read for each alias name of
// host. read is the same store read the caller made for host.
func withAliasHostRules(rules []state.EdgeRule, host string, read func(string) ([]state.EdgeRule, error)) ([]state.EdgeRule, error) {
	for _, alias := range hostidentity.Configured().AliasHosts(host) {
		more, err := read(alias)
		if err != nil {
			return nil, err
		}
		rules = state.MergeEdgeRulesInOrder(rules, more)
	}
	return rules, nil
}

// withCanonicalHostPatterns returns patterns plus, for each pattern under an
// alias apps domain, the same pattern under the primary apps domain.
func withCanonicalHostPatterns(patterns []string) []string {
	domains := hostidentity.Configured()
	if len(domains.AppAliases) == 0 {
		return patterns
	}
	out := slices.Clone(patterns)
	for _, pattern := range patterns {
		if canonical := domains.CanonicalHostPattern(pattern); !slices.Contains(out, canonical) {
			out = append(out, canonical)
		}
	}
	return out
}
