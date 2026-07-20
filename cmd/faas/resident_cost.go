// resident_cost.go — `--min N` always-resident GB-h echo (UX §6.5,
// issue #65 D3).
//
// When a customer pins an app at `faas app <slug> --min 1` on Pro or
// Scale, N instances stay resident 24/7. The CLI prints an honest
// cost echo so the customer sees the bill shape *before* committing.
// Free and Hobby cannot set a positive min (apid rejects with
// CodePlanMinInstancesNotAllowed); the helper returns 0 for those
// plans because the echo must not appear at all in the rejected path.
//
// Formula:
//
//	GB-h/mo = (RAMMB + PerVMOverheadMB) × min × 30 days / 1024
//
// The 30-day month matches pkg/meter/math.go GBHours rounding and is
// close enough to a calendar month (< 2 %) for budget planning. The
// actual monthly bill uses real resident seconds; this is a
// planning estimate, not an invoice.

package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ResidentGBHoursPerMonth estimates the always-resident GB-h/month
// bill for keeping `min` instances of an app with `ramMB` always warm
// on `plan`. Returns 0 when the plan does not permit min_instances > 0,
// when min <= 0, or when the plan string is empty/unknown — a missing
// plan from the auth path must not crash the CLI.
func ResidentGBHoursPerMonth(plan api.Plan, ramMB, min int) float64 {
	if min <= 0 {
		return 0
	}
	limits, ok := limitsFor(plan)
	if !ok || !limits.MinInstancesAllowed {
		return 0
	}
	return float64((ramMB+api.PerVMOverheadMB)*min*30) / 1024.0
}

// limitsFor is the lookup half of api.LimitsFor without the ok-bool
// ceremony; we centralise the empty-plan guard here so production
// callers don't panic on stale or unknown plan strings.
func limitsFor(p api.Plan) (api.Limits, bool) {
	if p == "" {
		return api.Limits{}, false
	}
	return api.LimitsFor(p)
}

// formatGBHours renders f as "~<n.n> GB-h/mo". Used by the --min
// echo and the manual smoke check.
func formatGBHours(f float64) string {
	return fmt.Sprintf("~%.1f GB-h/mo", f)
}

// printResidentCostEcho writes the "kept warm" cost line to osStdout.
// Caller must pass min > 0 AND a Pro/Scale plan; the function does
// not gate on those conditions itself — the caller already validated
// via `MinInstancesAllowed` upstream.
func printResidentCostEcho(plan api.Plan, ramMB, min int) {
	cost := ResidentGBHoursPerMonth(plan, ramMB, min)
	_, _ = fmt.Fprintf(osStdout,
		"  %d instance%s of %d MB kept warm ≈ %s (billed against your included quota, then %d millicent/GB-h overage).\n",
		min, pluralOne(min), ramMB, formatGBHours(cost), api.OverageMillicentsPerGBHour)
}

func pluralOne(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
