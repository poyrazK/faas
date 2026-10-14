package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// The spend cap is a hard ceiling on what a calendar month is billed
// (ADR-1162). An hour that crosses it is billed exactly up to the cap and then
// settled; usage beyond the cap is never billed, even if the cap is raised
// later, and schedd parks the account's workloads while the cap holds. The
// allowances below use the exact cap arithmetic in pkg/billing, which
// month-close object storage shares.

// capComputeAllowance returns the largest cumulative billable compute quantity
// (mb_seconds) the cap allows once egress, the month's live egress overage, is
// reserved. A nil policy reserves no egress. Invalid inputs fail closed (0).
func capComputeAllowance(capCents, egress int64, policy *billing.MeterUsagePolicy) int64 {
	remaining, ok := billing.CapRemainingMillicents(capCents, 0, egress, policy)
	if !ok {
		return 0
	}
	return billing.FloorCapQuantity(remaining, api.SecondsPerGBHour, api.OverageMillicentsPerGBHour)
}

// capEgressAllowance returns the largest cumulative billable egress quantity
// the cap allows once compute, the month's billable compute, is reserved.
func capEgressAllowance(capCents, compute int64, policy billing.MeterUsagePolicy) int64 {
	if policy.UnitQuantity <= 0 || policy.MillicentsPerUnit <= 0 {
		return 0
	}
	remaining, ok := billing.CapRemainingMillicents(capCents, compute, 0, nil)
	if !ok {
		return 0
	}
	return billing.FloorCapQuantity(remaining, policy.UnitQuantity, policy.MillicentsPerUnit)
}

// clampToAllowance bills [before, after) only up to allowance.
func clampToAllowance(before, after, allowance int64) int64 {
	return max(min(after, allowance)-before, 0)
}

// computeWithinCap returns the compute overage of [before, after) that the
// account's cap still allows, reserving the month's live egress overage so an
// earlier egress hour and a later compute hour cannot spend the same cap
// independently. Shadow/off egress never consumes the live cap.
func (p *Pusher) computeWithinCap(ctx context.Context, acct state.Account, hour time.Time, capCents, before, after int64) (int64, error) {
	policies, ok := p.pusher.(billing.MeterUsagePolicyProvider)
	if !ok {
		return clampToAllowance(before, after, capComputeAllowance(capCents, 0, nil)), nil
	}
	policy, configured := policies.MeterUsagePolicy(acct.Plan, state.BillingMeterEgress)
	if !configured || policy.Mode != billing.MeterDeliveryLive {
		return clampToAllowance(before, after, capComputeAllowance(capCents, 0, nil)), nil
	}
	if err := validateMeterUsagePolicy(policy); err != nil {
		return 0, err
	}
	hour = hour.UTC().Truncate(time.Hour)
	start := time.Date(hour.Year(), hour.Month(), 1, 0, 0, 0, 0, time.UTC)
	if policy.EffectiveFrom.After(start) {
		start = policy.EffectiveFrom
	}
	raw, err := sumMeterUsageRows(ctx, p.store, acct.ID, state.BillingMeterEgress, start, hour.Add(time.Hour))
	if err != nil {
		return 0, err
	}
	egress := max(raw-policy.IncludedQuantity, 0)
	return clampToAllowance(before, after, capComputeAllowance(capCents, egress, &policy)), nil
}
