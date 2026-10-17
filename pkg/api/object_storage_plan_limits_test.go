// adr: 1320

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
}

// adr: 1350 — plans include stored bytes from the October 2026 COGS report;
// Free also stops at its allowance.
func TestObjectStoragePlanLimitsFollowCOGSReport(t *testing.T) {
	const gib = int64(1 << 30)
	want := map[Plan]ObjectStoragePlanLimits{
		PlanFree:  {MaxAccountBytes: gib, IncludedBytes: gib},
		PlanHobby: {IncludedBytes: 10 * gib},
		PlanPro:   {IncludedBytes: 50 * gib},
		PlanScale: {IncludedBytes: 200 * gib},
	}
	for plan, limits := range want {
		if got := MustLimitsFor(plan).ObjectStorage; got != limits {
			t.Fatalf("%s object-storage limits = %+v, want %+v", plan, got, limits)
		}
	}
}

func TestObjectStoragePlanLimitsTightenOperatorPolicy(t *testing.T) {
	operator := ObjectStoragePolicy{MaxAccountBytes: 100, MaxMonthlyRequests: 200, MaxMonthlyEgressBytes: 300, MaxBucketBytes: 50}
	plan := ObjectStoragePlanLimits{MaxAccountBytes: 10, MaxMonthlyRequests: 500, MaxMonthlyEgressBytes: 30}
	got := plan.Policy(operator)
	if got.MaxAccountBytes != 10 || got.MaxMonthlyRequests != 200 || got.MaxMonthlyEgressBytes != 30 || got.MaxBucketBytes != 10 {
		t.Fatalf("policy = %+v, want the tighter positive value per ceiling and the bucket cap held under the account", got)
	}
	if got := (ObjectStoragePlanLimits{MaxAccountBytes: 80}).Policy(operator); got.MaxBucketBytes != 50 {
		t.Fatalf("bucket cap = %d, want the operator's 50 while the account ceiling stays above it", got.MaxBucketBytes)
	}
}

func TestObjectStoragePlanLimitsKeepPolicyValid(t *testing.T) {
	operator := ObjectStoragePolicy{
		MaxAccountBytes: 1 << 40, MaxBucketBytes: 1 << 39, MaxAccountKeys: 1_000, MaxMonthlyRequests: 1 << 20,
		MaxMonthlyEgressBytes: 1 << 40, MaxMonthlyAuthorizations: 1_000, MaxMonthlyCostMillicents: 1_000, MaxReportAgeSeconds: 3600,
	}
	if !operator.Valid() {
		t.Fatalf("test operator policy is invalid: %+v", operator)
	}
	for _, plan := range []ObjectStoragePlanLimits{
		{MaxAccountBytes: 1 << 30},
		{MaxAccountBytes: 1, MaxMonthlyRequests: 1, MaxMonthlyEgressBytes: 1},
		{MaxAccountBytes: MaxObjectStoragePolicyValue, MaxMonthlyRequests: MaxObjectStoragePolicyValue},
	} {
		if got := plan.Policy(operator); !got.Valid() {
			t.Fatalf("plan %+v made a valid operator policy invalid: %+v", plan, got)
		}
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
