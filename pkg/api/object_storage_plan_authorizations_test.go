package api

import "testing"

// Every admitted object read and write spends one authorization. A plan's
// monthly authorizations follow its monthly requests, and the operator value
// only bounds them from above: production-us's 1,000,000 held Pro (2,500,000
// requests) and Scale (10,000,000) to 1,000,000 operations a month (object
// storage audit OS-1, 2026-11-10).
func TestObjectStoragePolicyAuthorizationsFollowPlanRequests(t *testing.T) {
	operator := ObjectStoragePolicy{MaxMonthlyRequests: 10_000_000, MaxMonthlyAuthorizations: 10_000_000}
	for _, plan := range Plans {
		t.Run(string(plan), func(t *testing.T) {
			l := MustLimitsFor(plan).ObjectStorage
			got := l.Policy(operator)
			if got.MaxMonthlyAuthorizations != l.MaxMonthlyRequests {
				t.Fatalf("authorizations = %d, want the plan's %d requests", got.MaxMonthlyAuthorizations, l.MaxMonthlyRequests)
			}
			if got.MaxMonthlyAuthorizations < got.MaxMonthlyRequests {
				t.Fatalf("authorizations %d stop before requests %d", got.MaxMonthlyAuthorizations, got.MaxMonthlyRequests)
			}
		})
	}
	tight := ObjectStoragePolicy{MaxMonthlyRequests: 10_000_000, MaxMonthlyAuthorizations: 1_000}
	scale := MustLimitsFor(PlanScale).ObjectStorage
	if got := scale.Policy(tight).MaxMonthlyAuthorizations; got != 1_000 {
		t.Fatalf("a tighter operator bound must still apply: got %d", got)
	}
	if most := ObjectStoragePlanLimitsMax(); most.MaxMonthlyRequests != scale.MaxMonthlyRequests {
		t.Fatalf("ObjectStoragePlanLimitsMax requests = %d, want Scale's %d", most.MaxMonthlyRequests, scale.MaxMonthlyRequests)
	}
}
