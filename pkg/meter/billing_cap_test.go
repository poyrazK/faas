// spec: §10 — integer money and a shared, exact overage cap (ADR-1162).
package meter

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestCapAllowancesExactArithmetic(t *testing.T) {
	const gib = int64(1 << 30)
	policy := billing.MeterUsagePolicy{UnitQuantity: gib, MillicentsPerUnit: 2000}
	halfGBHour := api.SecondsPerGBHour / 2
	for _, tc := range []struct {
		name            string
		cap             int64
		compute, egress int64
		wantCompute     int64 // compute allowance after reserving egress
		wantEgress      int64 // egress allowance after reserving compute
	}{
		// 1 cent = 1,000 millicents: half a GB-hour (500) plus a quarter GiB
		// at 2,000 millicents per GiB (500) exactly fill it.
		{"fractional meters exactly fit", 1, halfGBHour, gib / 4, halfGBHour, gib / 4},
		{"no egress leaves the whole cap to compute", 1, 0, 0, api.SecondsPerGBHour, gib / 2},
		{"egress alone fills the cap", 1, 0, gib / 2, 0, gib / 2},
		{"usage beyond the cap leaves nothing", 1, 2 * api.SecondsPerGBHour, gib, 0, 0},
		{"zero cap", 0, 0, 0, 0, 0},
		{"negative cap fails closed", -1, 0, 0, 0, 0},
		{"negative usage fails closed", 1, -1, -1, 0, 0},
		{"large caps saturate", math.MaxInt64, 0, 0, math.MaxInt64, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := capComputeAllowance(tc.cap, tc.egress, &policy); got != tc.wantCompute {
				t.Fatalf("compute allowance = %d, want %d", got, tc.wantCompute)
			}
			if got := capEgressAllowance(tc.cap, tc.compute, policy); got != tc.wantEgress {
				t.Fatalf("egress allowance = %d, want %d", got, tc.wantEgress)
			}
		})
	}
	if got := capEgressAllowance(1, 0, billing.MeterUsagePolicy{}); got != 0 {
		t.Fatalf("invalid pricing policy egress allowance = %d, want 0 (fail closed)", got)
	}
	if got := capComputeAllowance(1, 1, &billing.MeterUsagePolicy{}); got != 0 {
		t.Fatalf("invalid pricing policy compute allowance = %d, want 0 (fail closed)", got)
	}
}

func TestClampToAllowanceBillsOnlyUpToTheCap(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		before, after, allowance int64
		want                     int64
	}{
		{"under the cap", 0, 10, 100, 10},
		{"crosses the cap", 90, 110, 100, 10},
		{"already at the cap", 100, 110, 100, 0},
		{"already past the cap", 120, 130, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampToAllowance(tc.before, tc.after, tc.allowance); got != tc.want {
				t.Fatalf("clamp = %d, want %d", got, tc.want)
			}
		})
	}
}
