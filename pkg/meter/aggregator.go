package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MonthlyUsageGB is the billable GB-RAM-hours one account has accumulated
// in the current UTC month. Aggregates every per-app Usage row in the
// month — spec §10 quota bands compare against this number.
//
// The aggregator rounds to 6 decimal places so the test surface and the
// financial-model cells line up without float drift. (Spec §4.7 says the
// billable unit is integer mb-seconds; the float only exists at the quota
// gate where €0.01/GB-h needs a unit.)
func MonthlyUsageGB(usages []state.Usage) float64 {
	var mbSec int64
	for _, u := range usages {
		mbSec += u.MBSeconds
	}
	gb := GBHours(mbSec)
	// round to 6 dp — well below the 0.1 % acceptance delta.
	return float64(int64(gb*1e6+0.5)) / 1e6
}

// QuotaResult is the verdict the quota loop hands to the caller. Negative
// margins (under quota) and zero are both non-actionable; the loop only
// acts on Reached or Exceeded.
type QuotaResult struct {
	AccountID string
	Plan      api.Plan
	UsedGB    float64
	QuotaGB   int
	// Percent in the [0, ∞) range; capped callers don't need it past 100.
	Percent int
	// Action is one of "", "warn", "stop". Empty = no event.
	Action string
}

// CheckQuota applies the per-plan quota ladder. The rule (spec §4.7,
// financial-model §1):
//
//	Free       — included 5 GB-h/month.  ≥100 % ⇒ hard stop.
//	Hobby/Pro/Scale — included quotas; overage accrues at €0.01/GB-h.
//	                        ≥100 % ⇒ one warning event per UTC day (the
//	                        caller de-duplicates; see EmitQuotaWarning).
//
// We never silently "improve" the rule: Free's hard stop is what the
// financial model prices against, and overage revenue for paid plans
// depends on the warning firing at exactly 100 % (not 95 %, not 105 %).
func CheckQuota(plan api.Plan, usedGB float64) QuotaResult {
	q := plan.PlanIncludedGBHours()
	res := QuotaResult{Plan: plan, UsedGB: usedGB, QuotaGB: q}
	if q <= 0 {
		// No quota band — never reached. Reserved for an internal/dev plan.
		return res
	}
	pct := int(usedGB*100.0/float64(q) + 0.5)
	if pct > 999 {
		pct = 999
	}
	res.Percent = pct
	switch {
	case plan == api.PlanFree && usedGB >= float64(q):
		res.Action = "stop"
	case plan != api.PlanFree && usedGB >= float64(q):
		res.Action = "warn"
	}
	return res
}

// MonthUsageForAccount fetches the current-month rows for one account.
// Helper used by the quota loop and the Stripe pusher. Minute-grain rows
// stay behind the Store; the meter only ever sees the rolled-up shape.
func MonthUsageForAccount(ctx context.Context, store state.Store, accountID string, at time.Time) ([]state.Usage, error) {
	month := AccountMonthKey(at)
	return store.UsageByMonth(ctx, accountID, month)
}
