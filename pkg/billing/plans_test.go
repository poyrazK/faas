package billing_test

// Tests for the shared billing-plan wrappers in plans.go. These exist
// in `package billing_test` so we exercise the exported contract that
// pkg/billing/stripe and pkg/billing/paddle actually call, rather than
// implementation-private helpers (the old per-provider copies were
// package-private; the deletion of those copies is the whole point of
// the DRY refactor).
//
// Three layers of coverage:
//   - TestPlanMonthlyMillicents pins the financial-model values per plan
//     (including the unknown-plan zero fallback) so a regression in the
//     wrapper is loud at the cheapest test layer.
//   - TestPlanMonthlyMillicentsMatchesAPILimits loops api.Plans and
//     asserts the wrapper agrees with pkg/api's authoritative table.
//     Catches a future change that bypasses LimitsFor.
//   - TestPlanOverageMillicentsPerGBHour pins the per-GB-hour rate and
//     asserts the wrapper agrees with pkg/api.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestPlanMonthlyMillicents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		plan api.Plan
		want int64
	}{
		{api.PlanFree, 0},          // Free has no recurring line item
		{api.PlanHobby, 900_000},   // €9
		{api.PlanPro, 2_900_000},   // €29
		{api.PlanScale, 9_900_000}, // €99
		{api.Plan("unknown"), 0},   // zero fallback, matches api.LimitsFor
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := billing.PlanMonthlyMillicents(tc.plan); got != tc.want {
				t.Errorf("PlanMonthlyMillicents(%s) = %d, want %d",
					tc.plan, got, tc.want)
			}
		})
	}
}

// TestPlanMonthlyMillicentsMatchesAPILimits is the wrapper-must-delegate
// invariant. Loops every plan in api.Plans (the canonical list) and
// asserts the billing wrapper agrees with pkg/api's authoritative
// PriceMillicents value. Any future change that bypasses api.LimitsFor
// fails here, regardless of what the explicit table above returns.
func TestPlanMonthlyMillicentsMatchesAPILimits(t *testing.T) {
	t.Parallel()

	for _, plan := range api.Plans {
		want := api.MustLimitsFor(plan).PriceMillicents
		if got := billing.PlanMonthlyMillicents(plan); got != want {
			t.Errorf("PlanMonthlyMillicents(%s) = %d, want API limit %d",
				plan, got, want)
		}
	}
}

func TestPlanOverageMillicentsPerGBHour(t *testing.T) {
	t.Parallel()

	// CLAUDE.md hard limit: "Overage €0.01/GB-h" = 1_000 millicents.
	if got := billing.PlanOverageMillicentsPerGBHour(); got != 1_000 {
		t.Errorf("PlanOverageMillicentsPerGBHour() = %d, want 1000", got)
	}

	// Wrapper must agree with the pkg/api constant. Catches a future
	// change that hard-codes a value instead of delegating.
	if got := billing.PlanOverageMillicentsPerGBHour(); got != api.OverageMillicentsPerGBHour {
		t.Errorf("overage wrapper = %d, want API value %d",
			got, api.OverageMillicentsPerGBHour)
	}
}
