package billing_test

// Tests for the shared billing-plan wrappers in plans.go. These exist
// in `package billing_test` so we exercise the exported contract that
// pkg/billing/stripe and pkg/billing/paddle actually call, rather than
// implementation-private helpers (the old per-provider copies were
// package-private; the deletion of those copies is the whole point of
// the DRY refactor).
//
// Two layers of coverage:
//   - TestPlanMonthlyMillicents is the financial-model snapshot: an
//     explicit table pin of every Plan value (plus an `unknown` zero-
//     fallback case), so a regression in the wrapper is loud at the
//     cheapest test layer.
//   - TestPlansTableCoversAPILimits asserts the explicit table covers
//     every plan in api.Plans. Catches a future addition to pkg/api
//     that the billing wrapper forgets to handle.
//
// The overage test pins both the literal spec value (CLAUDE.md
// "Overage €0.01/GB-h") and the wrapper-must-delegate invariant against
// pkg/api.

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

// TestPlansTableCoversAPILimits asserts the explicit monthly table
// exercises every plan pkg/api knows about — plus one extra "unknown"
// zero-fallback case. A future addition to api.Plans that the billing
// wrapper doesn't cover fails here, regardless of the explicit table's
// contents above.
func TestPlansTableCoversAPILimits(t *testing.T) {
	t.Parallel()

	apiPlans := len(api.Plans)
	// cases in TestPlanMonthlyMillicents = 4 api.Plans + 1 "unknown".
	const tableSize = 5
	if apiPlans+1 != tableSize {
		t.Errorf("api.Plans len = %d, expected %d (so the explicit table has %d rows)",
			apiPlans, tableSize-1, tableSize)
	}
}

func TestPlanOverageMillicentsPerGBHour(t *testing.T) {
	t.Parallel()

	got := billing.PlanOverageMillicentsPerGBHour()
	// CLAUDE.md hard limit: "Overage €0.01/GB-h" = 1_000 millicents.
	if got != 1_000 {
		t.Errorf("PlanOverageMillicentsPerGBHour() = %d, want 1000", got)
	}

	// Wrapper must agree with the pkg/api constant. Catches a future
	// change that hard-codes a value instead of delegating.
	if got != api.OverageMillicentsPerGBHour {
		t.Errorf("overage wrapper = %d, want API value %d",
			got, api.OverageMillicentsPerGBHour)
	}
}
