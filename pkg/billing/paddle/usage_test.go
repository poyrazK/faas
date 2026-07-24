package paddle

// usage_test covers the pure helpers in usage.go + products.go's
// money conversion functions — primitives that PR #3's
// integration test will exercise end-to-end but should also be
// pinned at the unit level so a regression is caught at the
// cheapest layer.
//
// Driving accumulateOverage end-to-end requires substituting the
// SDK's CreateTransaction call; PR #3 introduces the state-store-
// backed dedupe that makes a stub-mode of the provider worth
// adding. Today we pin the primitives the executor depends on.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCalendarMonthStart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "mid-month floors to the 1st",
			in:   time.Date(2025, 6, 17, 12, 34, 56, 789_000_000, time.UTC),
			want: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "first-of-month is unchanged",
			in:   time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Feb leap year (Feb 29 23:59 lands in March bucket)",
			in:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Feb non-leap (Feb 28 23:59 lands in Feb bucket, NOT Jan 30)",
			in:   time.Date(2025, 2, 28, 23, 59, 0, 0, time.UTC),
			want: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Dec → Jan year boundary",
			in:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC input is normalized",
			in:   time.Date(2025, 6, 17, 1, 0, 0, 0, time.FixedZone("CET", 3600)),
			want: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calendarMonthStart(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("calendarMonthStart(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlanMonthlyMillicents + TestPlanOverageMillicents removed: the
// price-table coverage moved to pkg/billing/plans_test.go in package
// billing_test. The per-provider copies were package-private and have
// been deleted with their helpers; the shared wrappers in plans.go now
// own the contract.

func TestMillicentsToPaddleAmount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mc   int64
		want string
	}{
		{"€9 = 900 cents", 900_000, "900"},
		{"€29 = 2900 cents", 2_900_000, "2900"},
		{"€99 = 9900 cents", 9_900_000, "9900"},
		{"overage €0.01 = 1 cent", 1_000, "1"},
		{"zero (free)", 0, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := millicentsToPaddleAmount(tc.mc); got != tc.want {
				t.Errorf("millicentsToPaddleAmount(%d) = %q, want %q", tc.mc, got, tc.want)
			}
		})
	}
}

func TestPlanToProductName(t *testing.T) {
	t.Parallel()

	if got := planToProductName(api.PlanHobby); got != "faas-plan-hobby" {
		t.Errorf("planToProductName(hobby) = %q, want faas-plan-hobby", got)
	}
	if got := planToProductName(api.PlanScale); got != "faas-plan-scale" {
		t.Errorf("planToProductName(scale) = %q, want faas-plan-scale", got)
	}
}

func TestPlanProducts_ExcludesFree(t *testing.T) {
	t.Parallel()

	ps := planProducts()
	for _, p := range ps {
		if p == api.PlanFree {
			t.Errorf("planProducts() contains free (it has no recurring line item)")
		}
	}
	// 3 paid tiers — pinned so an accidental addition lands in the
	// review queue.
	if len(ps) != 3 {
		t.Errorf("planProducts() len = %d, want 3 (hobby/pro/scale)", len(ps))
	}
}

// --- accumulator end-to-end via the FlushFn test seam ---

// flushFnCounter is a FlushFn stub that records every call. The
// locking around `acc.flushed` is exercised by the production
// code; the stub only counts. Production default is defaultFlushLocked
// (real SDK POST); tests inject this counter.
func flushFnCounter(counter *int, flushErr error) FlushFn {
	return func(_ context.Context, _ *Provider, acc *overageAccumulator) error {
		*counter++
		return flushErr
	}
}

// seedOverageProvider builds a Provider whose catalog has the
// overage price for `plan` primed, so accumulateOverage reaches
// the flush step without EnsurePlanProducts needing the live SDK.
// Also swaps in a counting flushFn so tests can assert call counts.
func seedOverageProvider(t *testing.T, plan api.Plan, priceID string, flush FlushFn) *Provider {
	t.Helper()
	p := &Provider{
		client: nil, // unused — accumulator never reaches CreateTransaction via stubbed flushFn
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{plan: priceID},
		},
		flushFn: flush,
	}
	return p
}

// TestAccumulateOverage_CrossMonthFlush is the boundary-case pin
// for the calendarMonthStart fix. Two pushes on either side of a
// Feb → Mar boundary must bucket separately — one flush per
// month, in the right order.
func TestAccumulateOverage_CrossMonthFlush(t *testing.T) {
	t.Parallel()

	var calls int
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(&calls, nil))

	acct := acctWithPlan(api.PlanHobby)

	// Jan 31 23:59 UTC.
	jan31 := time.Date(2025, 1, 31, 23, 59, 0, 0, time.UTC)
	if err := p.accumulateOverage(context.Background(), acct, jan31, 1000); err != nil {
		t.Fatalf("Jan push: %v", err)
	}
	if calls != 0 {
		t.Errorf("after first push: calls=%d, want 0", calls)
	}

	// Mar 1 00:01 UTC (skips Feb entirely; exercises the calendar
	// math rather than adjacent-month drift).
	mar1 := time.Date(2025, 3, 1, 0, 1, 0, 0, time.UTC)
	if err := p.accumulateOverage(context.Background(), acct, mar1, 2000); err != nil {
		t.Fatalf("Mar push: %v", err)
	}

	// Crossing Jan → Mar should produce exactly 1 flush: Jan's
	// bucket drains when March's hour is observed. Feb never has
	// any pushes, so it doesn't flush (the bucket for Feb doesn't
	// exist).
	if calls != 1 {
		t.Errorf("after crossing Jan → Mar: calls=%d, want 1", calls)
	}
}

// TestAccumulateOverage_AdjacentMonthBoundary pins the simpler
// Jan → Feb case (every-month-has-30-day-shaped data) so a regression
// in the calendar math is loud.
func TestAccumulateOverage_AdjacentMonthBoundary(t *testing.T) {
	t.Parallel()

	var calls int
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(&calls, nil))

	acct := acctWithPlan(api.PlanHobby)

	jan15 := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	feb1 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	if err := p.accumulateOverage(context.Background(), acct, jan15, 500); err != nil {
		t.Fatalf("Jan push: %v", err)
	}
	if calls != 0 {
		t.Errorf("after Jan push: calls=%d, want 0", calls)
	}
	if err := p.accumulateOverage(context.Background(), acct, feb1, 700); err != nil {
		t.Fatalf("Feb push: %v", err)
	}
	if calls != 1 {
		t.Errorf("after Feb push: calls=%d, want 1 (Jan's bucket flushed)", calls)
	}
}

// TestAccumulateOverage_WithinMonthDedupe confirms the second push
// in the same calendar month does NOT cause an additional flush —
// the `flushed` flag prevents double-billing within the same month.
// (Cross-process dedupe is documented in usage.go as a PR #3
// follow-up; this test pins the within-process contract only.)
func TestAccumulateOverage_WithinMonthDedupe(t *testing.T) {
	t.Parallel()

	var calls int
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(&calls, nil))

	acct := acctWithPlan(api.PlanHobby)

	// Three pushes in the same month with hour-precision spacing.
	if err := p.accumulateOverage(context.Background(), acct, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), 100); err != nil {
		t.Fatalf("push1: %v", err)
	}
	if err := p.accumulateOverage(context.Background(), acct, time.Date(2025, 6, 1, 0, 30, 0, 0, time.UTC), 200); err != nil {
		t.Fatalf("push2: %v", err)
	}
	if err := p.accumulateOverage(context.Background(), acct, time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC), 300); err != nil {
		t.Fatalf("push3: %v", err)
	}
	if calls != 0 {
		t.Errorf("within-month pushes: calls=%d, want 0 (no flush yet)", calls)
	}

	// Crossing into July triggers the June flush.
	if err := p.accumulateOverage(context.Background(), acct, time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), 50); err != nil {
		t.Fatalf("July push: %v", err)
	}
	if calls != 1 {
		t.Errorf("after month-rollover: calls=%d, want 1", calls)
	}

	// Another July push: same bucket, should not flush again.
	if err := p.accumulateOverage(context.Background(), acct, time.Date(2025, 7, 15, 12, 0, 0, 0, time.UTC), 80); err != nil {
		t.Fatalf("second July push: %v", err)
	}
	if calls != 1 {
		t.Errorf("second July push should not flush: calls=%d, want 1", calls)
	}
}

// TestAccumulateOverage_FlushErrorPropagates pins the error
// contract: a failed flush must surface to the caller so meterd
// can decide whether to retry, escalate, or skip.
func TestAccumulateOverage_FlushErrorPropagates(t *testing.T) {
	t.Parallel()

	stubErr := errors.New("paddle: simulated flush failure")
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(new(int), stubErr))

	acct := acctWithPlan(api.PlanHobby)

	jan15 := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	feb1 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	if err := p.accumulateOverage(context.Background(), acct, jan15, 100); err != nil {
		t.Fatalf("Jan push should succeed: %v", err)
	}
	err := p.accumulateOverage(context.Background(), acct, feb1, 200)
	if err == nil {
		t.Fatal("Feb push should surface flush failure")
	}
	if !strings.Contains(err.Error(), "simulated flush failure") {
		t.Errorf("err = %v, want it to wrap the stub error", err)
	}
}

// acctWithPlan builds a state.Account with a Plan stamped for the
// overage accumulator's price-key lookup (priceIDForPlan) and a
// non-empty StripeCustomerID (column name stale per ADR-025 — the
// stub flush doesn't post, but the production flushFn DOES pass
// it to CreateTransaction).
func acctWithPlan(plan api.Plan) state.Account {
	return state.Account{
		ID:               "acct_test_" + string(plan),
		Email:            "test@example.test",
		Plan:             plan,
		StripeCustomerID: "ctm_test_dummy",
	}
}
