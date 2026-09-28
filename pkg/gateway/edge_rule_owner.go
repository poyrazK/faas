package gateway

import "context"

type edgeRuleOwnerKey struct{}

// WithEdgeRuleOwner records the account that owns the app a request resolved
// to. Edge-rule matchers use it to ignore rules other accounts wrote.
func WithEdgeRuleOwner(ctx context.Context, accountID string) context.Context {
	if accountID == "" {
		return ctx
	}
	return context.WithValue(ctx, edgeRuleOwnerKey{}, accountID)
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
