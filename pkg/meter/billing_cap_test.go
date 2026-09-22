// spec: §10 — integer money and a shared, exact overage cap.
package meter

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

func TestCombinedOverageCapExactArithmetic(t *testing.T) {
	const gib = int64(1 << 30)
	policy := billing.MeterUsagePolicy{UnitQuantity: gib, MillicentsPerUnit: 2000}
	for _, tc := range []struct {
		name          string
		cap           int64
		before, after billableQuantities
		want          bool
	}{
		{"fractional meters exactly fit", 1, billableQuantities{}, billableQuantities{compute: api.SecondsPerGBHour / 2, egress: gib / 4}, false},
		{"one MB-second over", 1, billableQuantities{}, billableQuantities{compute: api.SecondsPerGBHour/2 + 1, egress: gib / 4}, true},
		{"one byte over", 1, billableQuantities{}, billableQuantities{compute: api.SecondsPerGBHour / 2, egress: gib/4 + 1}, true},
		{"already at cap", 1, billableQuantities{egress: gib / 2}, billableQuantities{egress: gib / 2}, true},
		{"zero cap", 0, billableQuantities{}, billableQuantities{compute: 1}, true},
		{"negative cap", -1, billableQuantities{}, billableQuantities{}, true},
		{"negative usage", 1, billableQuantities{}, billableQuantities{egress: -1}, true},
		{"large exact intermediates", math.MaxInt64, billableQuantities{}, billableQuantities{compute: math.MaxInt64, egress: math.MaxInt64}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := combinedOverageCapReached(tc.cap, tc.before, tc.after, policy); got != tc.want {
				t.Fatalf("cap reached = %v, want %v", got, tc.want)
			}
		})
	}
	if !combinedOverageCapReached(1, billableQuantities{}, billableQuantities{egress: 1}, billing.MeterUsagePolicy{}) {
		t.Fatal("invalid pricing policy did not fail closed")
	}
}
