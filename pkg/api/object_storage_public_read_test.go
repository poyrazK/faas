// adr: 20261023223000

package api

import "testing"

func TestAnonymousReadPolicyKeepsPrivateHeadroom(t *testing.T) {
	p := ObjectStoragePolicy{MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 4, MaxMonthlyAuthorizations: 1, MaxMonthlyCostMillicents: 0}
	for _, tc := range []struct {
		name                          string
		plan                          ObjectStoragePlanLimits
		requests, egress, auth, costs int64
	}{
		{name: "operator ceilings", requests: 750, egress: 3, auth: 1},
		{name: "tighter plan ceilings scale too", plan: ObjectStoragePlanLimits{MaxMonthlyRequests: 100}, requests: 75, egress: 3, auth: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.plan.Policy(p.AnonymousReadPolicy())
			if got.MaxMonthlyRequests != tc.requests || got.MaxMonthlyEgressBytes != tc.egress || got.MaxMonthlyAuthorizations != tc.auth || got.MaxMonthlyCostMillicents != tc.costs {
				t.Fatalf("anonymous policy = %+v", got)
			}
			if again := tc.plan.Policy(got); again != got {
				t.Fatalf("policy scaled twice: %+v", again)
			}
			if private := tc.plan.Policy(p); private.MaxMonthlyEgressBytes != 4 {
				t.Fatalf("private policy scaled: %+v", private)
			}
		})
	}
}
