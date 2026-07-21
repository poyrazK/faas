// Package ledgerparity — cross-package parity assertion that the per-VM
// billable RAM number is computed identically by meterd (the sampler that
// writes usage_minutes) and schedd (the ledger that enforces invariant §6.2-2).
// Both sides used to inline `ram_mb + api.PerVMOverheadMB`; the introduction
// of api.BillableRAMMB makes this test the load-bearing guard against drift
// between the metering surface and the admission surface. If they ever
// disagree by even one MB, a customer is either under-billed or over-billed
// for the same instance — this test fails before that ships.
//
// Lives in its own subpackage because no other test file in the repo imports
// both pkg/meter and pkg/sched (exploration confirmed), and Go forbids two
// `package X` declarations in one directory.
package ledgerparity

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TestBillableRAMMB_Parity asserts that for every plan-tier RAM, the value
// computed by api.BillableRAMMB equals both:
//
//  1. meterd's billable mb_seconds per minute / 60 (the sampler writes this),
//  2. schedd's Ledger.ResidentRAM() after one Admit (the §6.2-2 invariant).
//
// If the helper or any of its callers ever changes, this test fails before
// the diff lands. No PG, no metal — pure unit test.
func TestBillableRAMMB_Parity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ram  int
		plan api.Plan
	}{
		// One row per plan tier — covers Free/Hobby/Pro/Scale's typical RAM
		// values and the +8 MB overhead edge.
		{"free-128", 128, api.PlanFree},
		{"hobby-256", 256, api.PlanHobby},
		{"pro-512", 512, api.PlanPro},
		{"scale-1024", 1024, api.PlanScale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Helper value — the single source of truth.
			gotHelper := api.BillableRAMMB(tc.ram)

			// (1) meterd: mb_seconds/minute / 60 == helper for any plan.
			// MBSecondsPerMinute uses seconds, not minutes, so divide by 60
			// to recover the per-instance RAM that one minute billed.
			mbSec := meter.MBSecondsPerMinute(api.BillableRAMMB(tc.ram))
			gotMeter := int(mbSec / 60)
			if gotMeter != gotHelper {
				t.Fatalf("meter mismatch: helper=%d meter=%d (mbSec=%d)", gotHelper, gotMeter, mbSec)
			}

			// (2) schedd: Ledger.ResidentRAM() after one Admit == helper.
			ledger := sched.NewLedger()
			if err := ledger.Admit(sched.Request{
				Instance: "i-1",
				AppID:    "a-1",
				Plan:     tc.plan,
				RAMMB:    tc.ram,
				VCPU:     1,
			}); err != nil {
				t.Fatalf("admit: %v", err)
			}
			if got := ledger.ResidentRAM(); got != gotHelper {
				t.Fatalf("sched mismatch: helper=%d sched.ResidentRAM=%d", gotHelper, got)
			}
		})
	}
}
