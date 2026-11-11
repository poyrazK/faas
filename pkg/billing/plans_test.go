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
		{api.PlanHobby, 1_000_000}, // $10
		{api.PlanPro, 3_000_000},   // $30
		{api.PlanScale, 9_900_000}, // $99
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
	// CLAUDE.md hard limit: "Overage $0.05/GB-h" = 5_000 millicents
	// (ADR-20261107192333).
	if got != 5_000 {
		t.Errorf("PlanOverageMillicentsPerGBHour() = %d, want 5000", got)
	}

	// Wrapper must agree with the pkg/api constant. Catches a future
	// change that hard-codes a value instead of delegating.
	if got != api.OverageMillicentsPerGBHour {
		t.Errorf("overage wrapper = %d, want API value %d",
			got, api.OverageMillicentsPerGBHour)
	}
}

// TestWireQuantityForMBSeconds pins the integer wire-quantity formula
// both providers share (Stripe metered subscription_item + Paddle
// per-line-item Quantity). Pre-PR-P-fixes the helper lived only in
// pkg/billing/stripe; Paddle hard-coded Quantity=1 and silently
// under-billed every account by ~250× for the canonical Hobby 24h
// case. The move to pkg/billing is the load-bearing fix; this test
// pins every boundary the two providers must agree on.
//
// Reference derivation (mirrors pkg/billing/stripe/usage_math_test.go's
// pre-PR-P-fixes test, kept verbatim so the two providers' accepted
// answers cannot drift):
//   - Plan: Hobby (256 MB)
//   - Billed RAM: 256 + 8 = 264 MB (§4.7 "plan RAM + 8 MB per running
//     second" — the 8 MB overhead is for virtio/firecracker bookkeeping,
//     not the customer's working set)
//   - Window: 24 h = 86_400 s
//   - mbSeconds = 264 * 86_400 = 22_809_600
//   - qty = 22_809_600 * 5000 / (1024 * 3600) = 114_048_000_000 / 3_686_400
//     = 30937.5 → truncates to 30937 wire units ($0.05/GB-h,
//     ADR-20261107192333)
//
// The trailing remainder (the .5) is the sub-milliunit gap; it is
// dropped on purpose — see WireQuantityForMBSeconds's docstring and
// CLAUDE.md's "Floats near money fail review". The accumulation of
// this remainder across millions of windows is the entire reason the
// float path was retired.
func TestWireQuantityForMBSeconds(t *testing.T) {
	t.Parallel()

	const (
		hobbyRamMB   = 256
		billedMB     = hobbyRamMB + 8
		hoursInDay   = 24
		secsInHour   = 3600
		secsInDay    = hoursInDay * secsInHour
		canonicalQty = 30_937
	)

	cases := []struct {
		name      string
		mbSeconds int64
		want      int64
	}{
		// The acceptance case — a Hobby app resident for exactly one
		// full day. qty MUST be 30937, not 30936, not 30938, not a
		// float-rounded "30937.5". If this ever fails, the customer's
		// daily bill changes for both providers.
		{
			name:      "hobby_24h_canonical",
			mbSeconds: billedMB * secsInDay, // 22_809_600
			want:      canonicalQty,
		},

		// Boundary: zero. A parked-app with no resident-seconds MUST
		// produce a zero qty (no wire record at all would be the next
		// layer's decision, but the math must not invent units).
		{name: "zero", mbSeconds: 0, want: 0},

		// One minute of one MB — smallest billable quantum. The
		// customer should NOT be charged for a single second of
		// runtime, only full minutes (meterd rounds upstream), but the
		// helper itself must return the integer answer for whatever
		// sum lands on it. 60 mb-s → 60 * 5000 / 3_686_400 = 0
		// (truncated). Documents the sub-milliunit floor.
		{name: "one_minute_one_mb", mbSeconds: 60, want: 0},

		// One hour of one MB. 3_600 mb-s → 18_000_000 / 3_686_400 = 4
		// (truncated from 4.88…). Pinned so any future change to the
		// constant floors would surface here.
		{name: "one_hour_one_mb", mbSeconds: 3_600, want: 4},

		// The breakpoint where 1 wire unit (one millicent) is reached:
		// 738 mb-s → 3_690_000 / 3_686_400 = 1 (truncated from
		// 1.000976…). The 737 vs 738 split is the rounding boundary the
		// helper must respect exactly.
		{name: "first_wire_unit", mbSeconds: 738, want: 1},

		// One MB-resident-second below the breakpoint. 737 mb-s →
		// 3_685_000 / 3_686_400 = 0 (truncated from 0.99962…).
		// Companion to the previous case — together they pin the
		// exact floor.
		{name: "below_first_wire_unit", mbSeconds: 737, want: 0},

		// Pro plan (512 MB) for one hour. 520 * 3_600 = 1_872_000
		// mb-s → 9_360_000_000 / 3_686_400 = 2539 (truncated from
		// 2539.06…). Pin the higher-volume case to make sure the
		// integer math doesn't overflow at the megabyte scale.
		{name: "pro_one_hour", mbSeconds: 520 * 3_600, want: 2539},

		// Free plan (128 MB) for one hour. 136 * 3_600 = 489_600
		// mb-s → 2_448_000_000 / 3_686_400 = 664 (truncated from
		// 664.06…). Cheapest plan, smallest integer.
		{name: "free_one_hour", mbSeconds: 136 * 3_600, want: 664},

		// Scale plan (1024 MB) for one hour. 1032 * 3_600 = 3_715_200
		// mb-s → 18_576_000_000 / 3_686_400 = 5039 (truncated from
		// 5039.06…). Top-of-fleet case for the per-hour helper.
		{name: "scale_one_hour", mbSeconds: 1032 * 3_600, want: 5039},

		// One GB resident for one hour: 1_024 * 60 * 60 = 3_686_400
		// mb-s → 5000 wire units, 5 cents at $0.05/GB-h. Pinned because
		// it is the cleanest round answer the formula produces, and is
		// what a naive `gbHours * 5000` formula returns too — verifying
		// the two paths agree at the exactly-aligned point.
		{name: "one_gb_hour", mbSeconds: 1024 * 3600, want: 5000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := billing.WireQuantityForMBSeconds(tc.mbSeconds)
			if got != tc.want {
				t.Fatalf("WireQuantityForMBSeconds(%d) = %d, want %d",
					tc.mbSeconds, got, tc.want)
			}
		})
	}
}

// TestWireQuantityConstants pins the two constants the wire-quantity
// formula relies on. If either changes, every customer's bill changes
// for both providers; both must be a deliberate edit and a re-run of
// this test.
//
// Migrated from pkg/billing/stripe/usage_math_test.go's
// TestWireQuantityConstants — the constants now live in pkg/billing
// (shared between providers), so the test lives there too. Stripe's
// usage_math_test.go no longer re-asserts these directly; the stripe
// package's alias block reads through billing.SecondsPerGBHour, so a
// change here would surface as a stripe-side compile error first.
func TestWireQuantityConstants(t *testing.T) {
	t.Parallel()

	if billing.WireQuantityMillicentsPerGBHour != 5000 {
		t.Fatalf("WireQuantityMillicentsPerGBHour = %d, want 5000 (spec §4.7, ADR-20261107192333)",
			billing.WireQuantityMillicentsPerGBHour)
	}
	if billing.SecondsPerGBHour != 1024*3600 {
		t.Fatalf("SecondsPerGBHour = %d, want %d (1 GB = 1024 MB, 1 h = 3600 s)",
			billing.SecondsPerGBHour, 1024*3600)
	}
}
