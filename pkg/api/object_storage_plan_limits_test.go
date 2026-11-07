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
// Free also stops at its allowance. ADR-20261031122757 caps each plan's
// monthly egress at ten times its included storage. ADR-20261101214152 stops
// Hobby at twice its included storage, Pro and Scale at five times.
func TestObjectStoragePlanLimitsFollowCOGSReport(t *testing.T) {
	const gib = int64(1 << 30)
	want := map[Plan]ObjectStoragePlanLimits{
		PlanFree:  {MaxAccountBytes: gib, MaxMonthlyEgressBytes: 10 * gib, IncludedBytes: gib},
		PlanHobby: {MaxAccountBytes: 20 * gib, MaxMonthlyEgressBytes: 100 * gib, IncludedBytes: 10 * gib},
		PlanPro:   {MaxAccountBytes: 250 * gib, MaxMonthlyEgressBytes: 500 * gib, IncludedBytes: 50 * gib},
		PlanScale: {MaxAccountBytes: 1_000 * gib, MaxMonthlyEgressBytes: 2_000 * gib, IncludedBytes: 200 * gib},
	}
	for plan, limits := range want {
		if got := MustLimitsFor(plan).ObjectStorage; got != limits {
			t.Fatalf("%s object-storage limits = %+v, want %+v", plan, got, limits)
		}
	}
}

// adr: 20261031122757
func TestObjectStoragePlanEgressFollowsIncludedStorage(t *testing.T) {
	for _, plan := range Plans {
		l := MustLimitsFor(plan).ObjectStorage
		if want := ObjectStorageEgressPerIncludedByte * l.IncludedBytes; l.MaxMonthlyEgressBytes != want {
			t.Fatalf("%s monthly egress ceiling = %d, want %d× its %d included bytes", plan, l.MaxMonthlyEgressBytes, ObjectStorageEgressPerIncludedByte, l.IncludedBytes)
		}
		if l.IncludedEgressBytes != 0 {
			t.Fatalf("%s includes %d egress bytes; egress is a limit, not a priced allowance", plan, l.IncludedEgressBytes)
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

// adr: 20261101214152 — a paid account's storage ceiling is its included
// storage plus what its spend cap buys at the published price; no cap keeps
// the operator's ceiling, and Free keeps its own.
// ADR-20261101214152: each paid plan has its own storage ceiling, support can
// grant an account another, and the spend cap tightens either.
func TestObjectStorageCeilingFollowsThePlanGrantAndSpendCap(t *testing.T) {
	const gib = int64(1 << 30)
	bytesOf := func(n int64) *int64 { return &n }
	defaultCap := func(plan Plan) *int64 {
		cents, ok := plan.DefaultOverageCapCents()
		if !ok {
			t.Fatalf("%s has no default spend cap", plan)
		}
		return &cents
	}
	for _, tc := range []struct {
		name  string
		plan  Plan
		grant *int64
		cap   *int64
		want  int64
	}{
		{"hobby at its default cap", PlanHobby, nil, defaultCap(PlanHobby), 20 * gib},
		{"pro at its default cap", PlanPro, nil, defaultCap(PlanPro), 250 * gib},
		{"scale at its default cap", PlanScale, nil, defaultCap(PlanScale), 1_000 * gib},
		{"no cap keeps the plan's ceiling", PlanScale, nil, nil, 1_000 * gib},
		{"a raised cap does not raise the plan's ceiling", PlanHobby, nil, bytesOf(30_000), 20 * gib},
		{"a zero cap stops at the included storage", PlanPro, nil, bytesOf(0), 50 * gib},
		{"a low cap binds below the plan's ceiling", PlanHobby, nil, bytesOf(15), 15 * gib},
		{"a support grant replaces the plan's ceiling", PlanPro, bytesOf(2_000 * gib), nil, 2_000 * gib},
		{"the cap still tightens a grant", PlanPro, bytesOf(2_000 * gib), defaultCap(PlanPro), 1_716 * gib},
		{"free keeps its own ceiling", PlanFree, nil, nil, gib},
		{"free ignores a grant", PlanFree, bytesOf(100 * gib), nil, gib},
		{"free's ceiling is tighter than any cap", PlanFree, nil, bytesOf(1_000_000), gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ObjectStorageLimitsFor(tc.plan, tc.grant, tc.cap)
			if got.MaxAccountBytes != tc.want {
				t.Fatalf("ceiling = %d bytes (%d GiB), want %d (%d GiB)", got.MaxAccountBytes, got.MaxAccountBytes/gib, tc.want, tc.want/gib)
			}
			if got.IncludedBytes != MustLimitsFor(tc.plan).ObjectStorage.IncludedBytes || got.MaxMonthlyEgressBytes != MustLimitsFor(tc.plan).ObjectStorage.MaxMonthlyEgressBytes {
				t.Fatalf("the grant or cap changed more than the ceiling: %+v", got)
			}
		})
	}
	if got := (ObjectStoragePlanLimits{IncludedBytes: 200 * gib}).WithSpendCap(bytesOf(1 << 62)); got.MaxAccountBytes != MaxObjectStoragePolicyValue {
		t.Fatalf("an absurd cap must saturate: %+v", got)
	}
	operator := ObjectStoragePolicy{MaxAccountBytes: 100 * gib, MaxBucketBytes: 100 * gib}
	if got := ObjectStorageLimitsFor(PlanPro, nil, defaultCap(PlanPro)).Policy(operator); got.MaxAccountBytes != 100*gib {
		t.Fatalf("a lower operator ceiling must still win: %+v", got)
	}
}
