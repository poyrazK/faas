package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

func (g *gatewaydEdgeRules) environmentCacheRules(ctx context.Context, host string, app state.App, environment state.ProjectEnvironment, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	store, ok := g.store.(state.EnvironmentCachePolicyReader)
	if !ok {
		return nil, errors.New("environment cache policy reader unavailable")
	}
	policy, err := store.GetEnvironmentCachePolicy(ctx, app.AccountID, app.ID, environment.Slug)
	if err != nil {
		return nil, err
	}
	if policy.AppID != app.ID || policy.ProjectID != app.ProjectID || policy.EnvironmentID != environment.ID || policy.Scope != environment.Slug {
		return nil, state.ErrNotFound
	}
	out, err := state.ApplyEnvironmentCachePolicy(rules, policy, host)
	if err != nil {
		return nil, err
	}
	if capture, ok := ctx.Value(environmentCacheCaptureKey{}).(*environmentCacheCapture); ok {
		capture.AppID = policy.AppID
		capture.EnvironmentID = policy.EnvironmentID
		capture.Revision = policy.Revision
		capture.RuleCount = 0
		for _, rule := range out {
			if rule.Enabled && rule.Kind == state.EdgeRuleKindCache {
				capture.RuleCount++
			}
		}
	}
	for i := range out {
		if out[i].ManifestKey == "environment_cache_policy" {
			out[i].AccountID = app.AccountID
		}
	}
	return out, nil
}
