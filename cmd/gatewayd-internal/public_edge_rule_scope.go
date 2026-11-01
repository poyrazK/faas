package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The authoritative host policy (ADR-570, FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY)
// reads a host's rules in a Postgres snapshot instead of through
// hostStoreRules, and had none of the cached path's later rule filters, so
// turning it on brought back three fixed bugs (ADR-091 amendment 2026-10-31,
// round 7). These give it the same filters, reading through the snapshot.

// scopePublicHostRules drops the rules of deleted apps (amendment
// 2026-10-28) and, with a serving app, the rules of apps outside its
// edge-rule scope (amendment 2026-10-31, round 6). The serving app's own rules
// always stay.
func scopePublicHostRules(ctx context.Context, reader state.PublicHostPolicyReader, appID string, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	// The snapshot read runs under a short budget: a host whose rules are
	// all the serving app's needs no extra read.
	if !slices.ContainsFunc(rules, func(rule state.EdgeRule) bool { return appID == "" || rule.AppID != appID }) {
		return rules, nil
	}
	type ruleApp struct {
		live  bool
		scope string
	}
	apps := map[string]ruleApp{}
	load := func(id string) (ruleApp, error) {
		if known, ok := apps[id]; ok {
			return known, nil
		}
		app, err := reader.AppByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			apps[id] = ruleApp{}
			return ruleApp{}, nil
		}
		if err != nil {
			return ruleApp{}, fmt.Errorf("edge rule app %s: %w", id, err)
		}
		scope, err := state.AppEdgeRuleScope(ctx, reader, app)
		if err != nil {
			return ruleApp{}, err
		}
		apps[id] = ruleApp{live: app.Status != state.AppDeleted, scope: scope}
		return apps[id], nil
	}
	var want ruleApp
	if appID != "" {
		serving, err := load(appID)
		if err != nil {
			return nil, err
		}
		want = serving
	}
	out := make([]state.EdgeRule, 0, len(rules))
	for _, rule := range rules {
		if rule.AppID == appID && appID != "" {
			out = append(out, rule)
			continue
		}
		owner, err := load(rule.AppID)
		if err != nil {
			return nil, err
		}
		if owner.live && (appID == "" || owner.scope == want.scope) {
			out = append(out, rule)
		}
	}
	return out, nil
}

// publicPreviewInheritedRules returns, for a PR preview served by appID, its
// production app's app-wide rules that previews apply (amendment 2026-10-30),
// as previewInheritedRules does on the cached path.
func publicPreviewInheritedRules(ctx context.Context, reader state.PublicHostPolicyReader, host, account, appID string) ([]state.EdgeRule, error) {
	if appID == "" {
		return nil, nil
	}
	app, err := reader.AppByID(ctx, appID)
	if err != nil || app.PreviewOfSlug == "" {
		return nil, ignoreNotFound(err)
	}
	production, err := reader.AppBySlug(ctx, app.PreviewOfSlug)
	if err != nil {
		return nil, ignoreNotFound(err)
	}
	if production.AccountID != app.AccountID || production.Status == state.AppDeleted {
		return nil, nil
	}
	rules, err := reader.PublicHostAppEdgeRules(ctx, host, account, production.ID, false)
	if err != nil {
		return nil, err
	}
	inherited := rules[:0:0]
	for _, rule := range rules {
		if rule.AppID == production.ID && api.IsEdgeRuleAppHosts(rule.MatchHost) && state.EdgeRuleAppliesToPreviews(rule) {
			inherited = append(inherited, rule)
		}
	}
	return inherited, nil
}

func ignoreNotFound(err error) error {
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return err
}
