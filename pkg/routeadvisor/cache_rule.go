package routeadvisor

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cache rule check kinds (ADR-1199).
const (
	// CacheRuleStale: refills found the body had changed before the rule's
	// max-age ran out, so cached copies were served stale.
	CacheRuleStale = "stale"
	// CacheRuleTooShort: the body stayed the same far longer than the rule's
	// max-age; a longer one would answer more requests without the app.
	CacheRuleTooShort = "too_short"
)

// CacheRuleCheck is what refill evidence says about an existing cache rule.
type CacheRuleCheck struct {
	Kind       string
	MaxAge     time.Duration
	Refills    int64
	Stale      int64
	SafeMaxAge time.Duration
}

// CacheRuleFor returns the enabled cache rule the gateway would apply to a
// route: the matching one with the lowest priority.
func CacheRuleFor(rules []ExistingRule, method, route string) (ExistingRule, bool) {
	sample := samplePath(route)
	var found []ExistingRule
	for _, rule := range rules {
		if rule.Enabled && matches(rule, api.RouteAdviceKindCache, method, sample) {
			found = append(found, rule)
		}
	}
	if len(found) == 0 {
		return ExistingRule{}, false
	}
	return slices.MinFunc(found, func(a, b ExistingRule) int { return a.Priority - b.Priority }), true
}

// CheckCacheRule judges a GET route's refills under rule. A rule is stale when
// enough refills were observed and more than RouteCacheRuleMaxStaleShare of
// them found a changed body; it is too short when none did and the body stayed
// the same for at least RouteCacheRuleTooShortFactor times the max-age.
func CheckCacheRule(r RouteStats, rule ExistingRule) (CacheRuleCheck, bool) {
	maxAge := time.Duration(rule.MaxAgeSeconds) * time.Second
	if r.Method != "GET" || maxAge <= 0 {
		return CacheRuleCheck{}, false
	}
	v := r.Content.Verdict()
	check := CacheRuleCheck{MaxAge: maxAge, Refills: v.Repeats + v.Changes + r.Content.Stale, Stale: r.Content.Stale, SafeMaxAge: v.SafeMaxAge}
	switch {
	case check.Refills >= api.RouteCacheRuleMinRefills && share(check.Stale, check.Refills) > api.RouteCacheRuleMaxStaleShare:
		check.Kind = CacheRuleStale
	case check.Stale == 0 && v.Unsafe == "" && v.SafeMaxAge >= maxAge*api.RouteCacheRuleTooShortFactor:
		check.Kind = CacheRuleTooShort
	default:
		return CacheRuleCheck{}, false
	}
	return check, true
}
