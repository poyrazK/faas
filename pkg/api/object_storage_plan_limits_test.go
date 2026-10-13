// adr: 1200

package api

import "testing"

func TestObjectStoragePlanLimitsZeroChangesNothing(t *testing.T) {
	operator := ObjectStoragePolicy{MaxAccountBytes: 100, MaxMonthlyRequests: 200, MaxMonthlyEgressBytes: 300, MaxBucketBytes: 50}
	if got := (ObjectStoragePlanLimits{}).Policy(operator); got != operator {
		t.Fatalf("zero plan ceilings changed the operator policy: %+v", got)
	}
	if got := (ObjectStoragePlanLimits{}).Allowance(); got != (ObjectStorageAllowance{}) {
		t.Fatalf("zero plan allowance = %+v, want none", got)
	}
	for _, plan := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		if MustLimitsFor(plan).ObjectStorage != (ObjectStoragePlanLimits{}) {
			t.Fatalf("%s object-storage limits are set; ADR-1200 ships the mechanism with every plan at zero", plan)
		}
	}
}

func TestObjectStoragePlanLimitsTightenOperatorPolicy(t *testing.T) {
	operator := ObjectStoragePolicy{MaxAccountBytes: 100, MaxMonthlyRequests: 200, MaxMonthlyEgressBytes: 300, MaxBucketBytes: 50}
	plan := ObjectStoragePlanLimits{MaxAccountBytes: 10, MaxMonthlyRequests: 500, MaxMonthlyEgressBytes: 30}
	got := plan.Policy(operator)
	if got.MaxAccountBytes != 10 || got.MaxMonthlyRequests != 200 || got.MaxMonthlyEgressBytes != 30 || got.MaxBucketBytes != 50 {
		t.Fatalf("policy = %+v, want the tighter positive value per ceiling and other fields untouched", got)
	}
}

func TestObjectStoragePlanLimitsAllowanceUnits(t *testing.T) {
	got := ObjectStoragePlanLimits{IncludedBytes: 1 << 30, IncludedRequests: 1_000, IncludedEgressBytes: 2 << 30}.Allowance()
	want := ObjectStorageAllowance{StoredByteHours: (1 << 30) * ObjectStorageBillingHoursPerMonth, Requests: 1_000, EgressBytes: 2 << 30}
	if got != want {
		t.Fatalf("allowance = %+v, want %+v", got, want)
	}
	if got := (ObjectStoragePlanLimits{IncludedBytes: MaxObjectStoragePolicyValue}).Allowance(); got.StoredByteHours != MaxObjectStoragePolicyValue || !got.Valid() {
		t.Fatalf("saturated allowance = %+v, want a valid ceiling value", got)
	}
}
