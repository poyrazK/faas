package routeadvisor

// adr: 1199

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCacheRuleFor(t *testing.T) {
	rules := []ExistingRule{
		{Kind: api.RouteAdviceKindCache, Path: "/catalog/*", Enabled: false, Priority: 1, MaxAgeSeconds: 10},
		{Kind: api.RouteAdviceKindCache, Path: "/catalog/*", Enabled: true, Priority: 20, MaxAgeSeconds: 600},
		{Kind: api.RouteAdviceKindCache, Path: "/*", Enabled: true, Priority: 5, MaxAgeSeconds: 60},
		{Kind: api.RouteAdviceKindAsync, Path: "/catalog/*", Enabled: true, Priority: 0},
		{Kind: api.RouteAdviceKindCache, Path: "/catalog/*", Methods: []string{"POST"}, Enabled: true, Priority: 0, MaxAgeSeconds: 5},
	}
	rule, ok := CacheRuleFor(rules, "GET", "/catalog/{id}")
	if !ok || rule.MaxAgeSeconds != 60 {
		t.Fatalf("the lowest-priority enabled cache rule applies: %+v %v", rule, ok)
	}
	if _, ok := CacheRuleFor(rules[:1], "GET", "/catalog/{id}"); ok {
		t.Fatal("a disabled rule caches nothing")
	}
}

func TestCheckCacheRule(t *testing.T) {
	rule := ExistingRule{Kind: api.RouteAdviceKindCache, Path: "/catalog", Enabled: true, MaxAgeSeconds: 60}
	route := func(stale int64, same []int64) RouteStats {
		return RouteStats{Method: "GET", Path: "/catalog", Content: ContentStats{Same: same, Changed: []int64{0, 0, 0}, Stale: stale}}
	}
	check, ok := CheckCacheRule(route(10, []int64{0, 40, 0}), rule)
	if !ok || check.Kind != CacheRuleStale || check.Refills != 50 || check.Stale != 10 {
		t.Fatalf("20%% stale refills: %+v %v", check, ok)
	}
	if _, ok := CheckCacheRule(route(2, []int64{0, 60, 0}), rule); ok {
		t.Fatal("3% stale refills is within the tolerance")
	}
	if _, ok := CheckCacheRule(route(10, []int64{0, 5, 0}), rule); ok {
		t.Fatal("too few refills to call a rule stale")
	}
	// Unchanged for up to an hour across 70 refills: a 60s rule is too short.
	check, ok = CheckCacheRule(route(0, []int64{0, 30, 40}), rule)
	if !ok || check.Kind != CacheRuleTooShort || check.SafeMaxAge != time.Hour {
		t.Fatalf("too short: %+v %v", check, ok)
	}
	long := rule
	long.MaxAgeSeconds = 3600
	if _, ok := CheckCacheRule(route(0, []int64{0, 30, 40}), long); ok {
		t.Fatal("an hour-long rule is not too short when an hour is all that was measured")
	}
	if _, ok := CheckCacheRule(RouteStats{Method: "HEAD", Content: ContentStats{Stale: 50}}, rule); ok {
		t.Fatal("only GET bodies are observed")
	}
}
