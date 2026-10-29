// adr: 20261026120843 — schedd honours meterd's combined spend cap hold.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// holdFixture adds meterd's hold to the compute-only overage fixture.
type holdFixture struct {
	*overageFixture
	hold *state.SpendCapHold
}

func (f *holdFixture) GetSpendCapHold(context.Context, string) (state.SpendCapHold, bool, error) {
	if f.hold == nil {
		return state.SpendCapHold{}, false, nil
	}
	return *f.hold, true, nil
}

func (f *holdFixture) PutSpendCapHold(context.Context, state.SpendCapHold) error { return nil }
func (f *holdFixture) ClearSpendCapHold(context.Context, string) error           { return nil }

func TestOverageCheckerHonoursCombinedSpendCapHold(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		hold *state.SpendCapHold
		want sched.OverageStatus
		obs  int64
	}{
		// Compute overage (40) is under the cap (100), but compute plus live
		// egress reached it.
		{"matching hold", &state.SpendCapHold{Month: october, CapCents: 100, ObservedCents: 130}, sched.OverageReached, 130},
		{"no hold", nil, sched.OverageOK, 40},
		{"hold for an older cap", &state.SpendCapHold{Month: october, CapCents: 50, ObservedCents: 60}, sched.OverageOK, 40},
		{"hold from last month", &state.SpendCapHold{Month: october.AddDate(0, -1, 0), CapCents: 100, ObservedCents: 130}, sched.OverageOK, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &holdFixture{overageFixture: &overageFixture{capCents: ptr(100), observedVal: 40}, hold: tc.hold}
			c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)
			(*c.Now()) = func() time.Time { return now }
			status, obs, capCents, err := c.Check(context.Background(), "acct-1")
			if err != nil || status != tc.want || obs != tc.obs || capCents != 100 {
				t.Fatalf("Check = %v obs=%d cap=%d err=%v; want %v obs=%d cap=100", status, obs, capCents, err, tc.want, tc.obs)
			}
		})
	}
}
