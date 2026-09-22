package state

// Per-kind edge-rule quotas for the ADR-201 traffic primitives.
//
// These live in one place so MemStore and PgStore cannot drift — the class of
// bug that produced the always-zero uppercase-state-literal queries, where
// MemStore was right and PgStore's SQL was wrong and no test ran both.
//
// IMPORTANT — the zero-quota semantics here differ from the older per-kind
// branches (throttle, geo, cache) on purpose. Those are written as:
//
//	if in.Kind == K && limits.XPerApp > 0 { ...count and compare... }
//
// which means a plan whose quota is ZERO skips the check entirely and the
// rule is created. ADR-122 says "Free cannot cache", but with
// EdgeRulesCachePerApp = 0 that is not actually enforced at the store layer —
// a Free app can create unlimited kind=cache rules today.
//
// ADR-201 needs Free = 0 to genuinely mean "no retry rules", so these
// helpers treat 0 as DENY. The older branches are deliberately left alone:
// tightening them would revoke rules customers may already have created, and
// that is a product decision, not a refactor.

import "github.com/onebox-faas/faas/pkg/api"

// edgeRuleKindQuota returns the per-app quota for an ADR-201 kind, and
// whether the kind is one this helper governs.
func edgeRuleKindQuota(kind EdgeRuleKind, limits api.Limits) (int, bool) {
	switch kind {
	case EdgeRuleKindRetry:
		return limits.EdgeRulesRetryPerApp, true
	case EdgeRuleKindCircuitBreaker:
		return limits.EdgeRulesCircuitBreakerPerApp, true
	default:
		return 0, false
	}
}

// edgeRuleKindQuotaDenied reports the quota error for a plan whose quota for
// this kind is zero — the "not available on your plan" case. Returns nil when
// the kind is not governed here or the plan allows at least one rule.
func edgeRuleKindQuotaDenied(kind EdgeRuleKind, limits api.Limits) *EdgeRuleQuotaError {
	limit, governed := edgeRuleKindQuota(kind, limits)
	if !governed || limit > 0 {
		return nil
	}
	return &EdgeRuleQuotaError{
		Limit:      0,
		Observed:   0,
		Kind:       string(kind),
		PerAppOnly: true,
		PerKind:    true,
	}
}

// edgeRuleKindQuotaExceeded reports the quota error when an app already holds
// `observed` rules of this kind. Returns nil when the kind is not governed
// here or the app is under its quota.
func edgeRuleKindQuotaExceeded(kind EdgeRuleKind, limits api.Limits, observed int) *EdgeRuleQuotaError {
	limit, governed := edgeRuleKindQuota(kind, limits)
	if !governed || observed < limit {
		return nil
	}
	return &EdgeRuleQuotaError{
		Limit:      limit,
		Observed:   observed,
		Kind:       string(kind),
		PerAppOnly: true,
		PerKind:    true,
	}
}
