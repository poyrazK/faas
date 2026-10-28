package gateway

import (
	"context"
	"strings"
)

type edgeRuleOwnerKey struct{}

// WithEdgeRuleOwner records the account that owns the app a request resolved
// to. Edge-rule matchers use it to ignore rules other accounts wrote.
func WithEdgeRuleOwner(ctx context.Context, accountID string) context.Context {
	if accountID == "" {
		return ctx
	}
	return context.WithValue(ctx, edgeRuleOwnerKey{}, accountID)
}

type edgeRuleResourceOwnerKey struct{}

// WithEdgeRuleResourceOwner records the resource owner (the org when the app
// belongs to one, otherwise the account) of the app a request resolved to.
// The store reads scope a host's rule set by this, so a rule written on an app
// of another tenant — another org, or the same creator's personal account —
// is never read for this host (Hunt 24 H24-1). The account-level
// WithEdgeRuleOwner stays the gateway's second, narrower filter.
func WithEdgeRuleResourceOwner(ctx context.Context, resourceOwnerID string) context.Context {
	if resourceOwnerID == "" {
		return ctx
	}
	return context.WithValue(ctx, edgeRuleResourceOwnerKey{}, resourceOwnerID)
}

// EdgeRuleResourceOwner returns the resource owner recorded by
// WithEdgeRuleResourceOwner, or "".
func EdgeRuleResourceOwner(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	owner, _ := ctx.Value(edgeRuleResourceOwnerKey{}).(string)
	return owner
}

type edgeRuleAppKey struct{}

// WithEdgeRuleApp records the app a request resolved to, so app-wide
// edge rules (match_host "@app", ADR-1191) of that app apply to the host.
func WithEdgeRuleApp(ctx context.Context, appID string) context.Context {
	if appID == "" {
		return ctx
	}
	return context.WithValue(ctx, edgeRuleAppKey{}, appID)
}

// EdgeRuleApp returns the app recorded by WithEdgeRuleApp, or "".
func EdgeRuleApp(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	app, _ := ctx.Value(edgeRuleAppKey{}).(string)
	return app
}

// EdgeRuleCacheKey is the edge-rule cache key for host as served by appID.
// A host's compiled rules include its app's app-wide rules, so the key
// carries the app: a host moved to another app never reuses the old app's
// set. With no app the key is the host itself.
func EdgeRuleCacheKey(host, appID string) string {
	if appID == "" {
		return host
	}
	return host + "\x00" + appID
}

// EdgeRuleCacheKeyHost returns the host part of an EdgeRuleCacheKey.
func EdgeRuleCacheKeyHost(key string) string {
	host, _, _ := strings.Cut(key, "\x00")
	return host
}

// edgeRuleCacheKeyApp returns the app part of an EdgeRuleCacheKey, or "".
func edgeRuleCacheKeyApp(key string) string {
	_, app, _ := strings.Cut(key, "\x00")
	return app
}

// EdgeRuleOwner returns the account recorded by WithEdgeRuleOwner, or "".
func EdgeRuleOwner(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	owner, _ := ctx.Value(edgeRuleOwnerKey{}).(string)
	return owner
}

// OwnedEdgeRules drops rules written by an account other than the request's
// owner. match_host is free-form ("*", "*.gregale.dev", another tenant's
// host), and rules are loaded per host, so another account's rule can sit
// ahead of the owner's in priority order. The appliers already refuse to act
// on a foreign rule, but they stopped there: first-match picking meant a
// foreign "*" rule at priority 0 hid the owner's own kind=jwt / ip / geo /
// maintenance gate, so the request went through ungated. Filtering before
// the pick removes both the shadowing and the reliance on every applier's
// check. With no recorded owner (before app resolution) rules pass through.
func OwnedEdgeRules[T any](ctx context.Context, rules []T, account func(*T) string) []T {
	owner := EdgeRuleOwner(ctx)
	if owner == "" {
		return rules
	}
	foreign := false
	for i := range rules {
		if account(&rules[i]) != owner {
			foreign = true
			break
		}
	}
	if !foreign {
		return rules
	}
	out := make([]T, 0, len(rules))
	for i := range rules {
		if account(&rules[i]) == owner {
			out = append(out, rules[i])
		}
	}
	return out
}
