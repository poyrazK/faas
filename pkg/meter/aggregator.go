package meter

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// MonthlyUsageGB is the billable GB-RAM-hours one account has accumulated
// in the current UTC month. Aggregates every per-app Usage row in the
// month — spec §10 quota bands compare against this number.
//
// The aggregator rounds to 6 decimal places so the test surface and the
// financial-model cells line up without float drift. (Spec §4.7 says the
// billable unit is integer mb-seconds; the float only exists at the quota
// gate where the overage rate needs a unit.)
func MonthlyUsageGB(usages []state.Usage) float64 {
	var mbSec int64
	for _, u := range usages {
		mbSec += u.MBSeconds
	}
	return api.InternalMailQuotaWarningUsageGBHours(mbSec)
}

// MonthlyEgressBytes sums the month's application egress: the host-interface
// bytes the subject's instances sent (state.Usage.NetTxBytes), the egress
// meter's source. It saturates rather than wrapping.
func MonthlyEgressBytes(usages []state.Usage) int64 {
	var total int64
	for _, u := range usages {
		if u.NetTxBytes <= 0 {
			continue
		}
		if u.NetTxBytes > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += u.NetTxBytes
	}
	return total
}

// EgressCeilingBytes is the month's application egress ceiling for plan: the
// plan's monthly egress, zero when the plan has none or the ceiling is lifted.
// Live egress billing alone does not lift it (ADR-20261104090000, amending
// ADR-20261103131742): egress above the allowance is billed only for a
// subject whose egress overage election is on, on a plan that allows one,
// while the provider bills that plan's egress live (ADR-20261104204500).
func EgressCeilingBytes(provider billing.Provider, plan api.Plan, election state.EgressOverageElection, now time.Time) int64 {
	limits, ok := api.LimitsFor(plan)
	if !ok || limits.MonthlyEgressGiB <= 0 {
		return 0
	}
	if billing.EgressOverageLifted(provider, plan, election, now) {
		return 0
	}
	return limits.MonthlyEgressGiB << 30
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
//	Free       — included GB-h/month (limits.go).  ≥100 % ⇒ hard stop.
//	Hobby/Pro/Scale — included quotas; overage accrues at api.OverageMillicentsPerGBHour ($0.05/GB-h).
//	                        ≥100 % ⇒ one warning event per UTC day (the
//	                        caller de-duplicates; see EmitQuotaWarning).
//
// We never silently "improve" the rule: Free's hard stop is what the
// financial model prices against, and overage revenue for paid plans
// depends on the warning firing at exactly 100 % (not 95 %, not 105 %).
func CheckQuota(plan api.Plan, usedGB float64) QuotaResult {
	return checkQuotaAgainst(plan, usedGB, plan.PlanIncludedGBHours())
}

// checkQuotaAgainst applies the ladder to an explicit monthly allowance q.
func checkQuotaAgainst(plan api.Plan, usedGB float64, q int) QuotaResult {
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
