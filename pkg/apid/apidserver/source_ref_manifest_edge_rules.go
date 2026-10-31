package apidserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// manifestEdgeRuleSet is one manifest section's desired edge rules on an app
// (async_routes, routes). The section owns every rule whose ManifestKey
// starts with prefix; it never adopts or deletes an unmanaged rule.
type manifestEdgeRuleSet struct {
	prefix string
	// label names the section in customer messages ("async route", "route").
	label   string
	kinds   map[state.EdgeRuleKind]bool
	desired []state.CreateEdgeRuleParams
	// matches reports whether current already equals desired.
	matches func(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool
	// collides reports whether an unmanaged rule would compete with desired.
	collides func(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool
}

// reconcileManifestEdgeRules converges an app's manifest-owned rules for one
// section: existing identities are updated in place, stale ones removed and
// new ones created, recording each change in staged for rollback.
func (s *server) reconcileManifestEdgeRules(ctx context.Context, acct state.Account, app state.App, set manifestEdgeRuleSet, staged *sourceRefManifestStaged) *api.Problem {
	existing, err := s.store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity(fmt.Sprintf("could not list app edge rules for %ss", set.label))
	}
	existingByKey := make(map[string]state.EdgeRule)
	managedCount := 0
	for _, rule := range existing {
		if !strings.HasPrefix(rule.ManifestKey, set.prefix) {
			continue
		}
		if !set.kinds[rule.Kind] {
			return api.ErrCapacity(fmt.Sprintf("manifest-owned %s has an unexpected edge-rule kind", set.label))
		}
		if _, duplicate := existingByKey[rule.ManifestKey]; duplicate {
			return api.ErrCapacity(fmt.Sprintf("duplicate manifest-owned %s identity", set.label))
		}
		existingByKey[rule.ManifestKey] = cloneSourceRefManifestEdgeRule(rule)
		managedCount++
	}

	desiredByKey := make(map[string]state.CreateEdgeRuleParams, len(set.desired))
	desiredOrder := make([]string, 0, len(set.desired))
	for _, params := range set.desired {
		desiredByKey[params.ManifestKey] = params
		desiredOrder = append(desiredOrder, params.ManifestKey)
	}

	// A manifest rule never adopts an unowned edge rule implicitly. This
	// preflight catches the common exact-match collision before any mutation.
	for _, key := range desiredOrder {
		desired := desiredByKey[key]
		for _, current := range existing {
			if current.ManifestKey == "" && set.collides(current, desired) {
				return api.ErrEdgeRuleConflict(fmt.Sprintf("%s %q overlaps unmanaged edge rule %s; remove or change the existing rule first", set.label, strings.TrimPrefix(key, set.prefix), current.ID))
			}
		}
	}

	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return api.ErrCapacity("could not resolve account plan limits")
	}
	finalRuleCount := len(existing) - managedCount + len(set.desired)
	if finalRuleCount > limits.EdgeRulesPerApp {
		return api.ErrPlanLimitEdgeRules(acct.Plan, limits.EdgeRulesPerApp, finalRuleCount)
	}

	// Existing identities are updated in place, preserving the edge-rule ID.
	for _, key := range desiredOrder {
		current, exists := existingByKey[key]
		if !exists {
			continue
		}
		desired := desiredByKey[key]
		if set.matches(current, desired) {
			continue
		}
		updated, problem := s.updateSourceRefManifestEdgeRule(ctx, set.label, app, current, desired, staged)
		if problem != nil {
			return problem
		}
		existingByKey[key] = updated
	}

	// Remove stale manifest-owned rules before creating replacements so a
	// deploy at the plan's exact edge-rule cap has room to make progress.
	staleKeys := make([]string, 0)
	for key := range existingByKey {
		if _, keep := desiredByKey[key]; !keep {
			staleKeys = append(staleKeys, key)
		}
	}
	sort.Strings(staleKeys)
	for _, key := range staleKeys {
		if problem := s.deleteSourceRefManifestEdgeRule(ctx, set.label, app, existingByKey[key], staged); problem != nil {
			return problem
		}
	}

	for _, key := range desiredOrder {
		if _, exists := existingByKey[key]; exists {
			continue
		}
		if problem := s.createSourceRefManifestEdgeRule(ctx, set.label, acct, app, desiredByKey[key], staged); problem != nil {
			return problem
		}
	}
	return nil
}
