package meter

import (
	"context"
	"errors"
	"fmt"
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
// object storage's daily steps share.

// capComputeAllowance returns the largest cumulative billable compute quantity
// (mb_seconds) the cap allows once egress, the month's live egress overage,
// and objectStorage, its live object-storage charge in millicents, are
// reserved. A nil policy reserves no egress. Invalid inputs fail closed (0).
func capComputeAllowance(capCents, egress int64, policy *billing.MeterUsagePolicy, objectStorage int64) int64 {
	remaining, ok := billing.CapRemainingMillicents(capCents, 0, egress, policy, objectStorage)
	if !ok {
		return 0
	}
	return billing.FloorCapQuantity(remaining, api.SecondsPerGBHour, api.OverageMillicentsPerGBHour)
}

// capEgressAllowance returns the largest cumulative billable egress quantity
// the cap allows once compute, the month's billable compute, and
// objectStorage, its live object-storage charge in millicents, are reserved.
func capEgressAllowance(capCents, compute, objectStorage int64, policy billing.MeterUsagePolicy) int64 {
	if policy.UnitQuantity <= 0 || policy.MillicentsPerUnit <= 0 {
		return 0
	}
	remaining, ok := billing.CapRemainingMillicents(capCents, compute, 0, nil, objectStorage)
	if !ok {
		return 0
	}
	return billing.FloorCapQuantity(remaining, policy.UnitQuantity, policy.MillicentsPerUnit)
}

// liveObjectStorageCharge is the account's live object-storage charge for the
// month of at: delivered, and pending in steps apid has not finished
// (ADR-20261027093220). The cap reserves both before compute and egress; a
// store without the progress ledger reserves nothing.
func liveObjectStorageCharge(ctx context.Context, store state.Store, accountID string, at time.Time) (delivered, pending int64, err error) {
	progress, ok := store.(state.ObjectStorageBillingProgressStore)
	if !ok {
		return 0, 0, nil
	}
	delivered, pending, err = progress.LiveObjectStorageMillicents(ctx, accountID, state.ObjectStoragePeriod(at))
	if err != nil {
		return 0, 0, fmt.Errorf("load live object storage charge: %w", err)
	}
	return delivered, pending, nil
}

// SpendCapObjectStorageCents is the object-storage charge the spend-cap hold
// counts for the month of at: the delivered charge, a part cent counting as a
// whole one (ADR-20261027093220). A pending step's reservation is an upper
// bound, not a charge, so it is not counted. Every view that compares the
// month's spending with the cap uses it, so a page never shows headroom the
// hold has already spent.
func SpendCapObjectStorageCents(ctx context.Context, store state.Store, accountID string, at time.Time) (int64, error) {
	delivered, _, err := liveObjectStorageCharge(ctx, store, accountID, at)
	if err != nil {
		return 0, err
	}
	return delivered/api.MillicentsPerCent + min(delivered%api.MillicentsPerCent, 1), nil
}

// liveObjectStorageReserved is the delivered and pending live object-storage
// charge the cap reserves.
func liveObjectStorageReserved(ctx context.Context, store state.Store, accountID string, at time.Time) (int64, error) {
	delivered, pending, err := liveObjectStorageCharge(ctx, store, accountID, at)
	return delivered + pending, err
}

// clampToAllowance bills [before, after) only up to allowance.
func clampToAllowance(before, after, allowance int64) int64 {
	return max(min(after, allowance)-before, 0)
}

// computeWithinCap returns the compute overage of [before, after) that the
// account's cap still allows, reserving the month's live egress overage and
// live object-storage charge so an earlier hour of another meter and a later
// compute hour cannot spend the same cap independently. Shadow/off egress
// never consumes the live cap.
func (p *Pusher) computeWithinCap(ctx context.Context, acct state.Account, hour time.Time, capCents, before, after int64) (int64, error) {
	objectStorage, err := liveObjectStorageReserved(ctx, p.store, acct.ID, hour)
	if err != nil {
		return 0, err
	}
	policies, ok := p.pusher.(billing.MeterUsagePolicyProvider)
	if !ok {
		return clampToAllowance(before, after, capComputeAllowance(capCents, 0, nil, objectStorage)), nil
	}
	policy, configured := policies.MeterUsagePolicy(acct.Plan, state.BillingMeterEgress)
	if !configured || policy.Mode != billing.MeterDeliveryLive {
		return clampToAllowance(before, after, capComputeAllowance(capCents, 0, nil, objectStorage)), nil
	}
	if err := validateMeterUsagePolicy(policy); err != nil {
		return 0, err
	}
	hour = hour.UTC().Truncate(time.Hour)
	egress, err := p.liveEgressReserved(ctx, acct, hour, hour.Add(time.Hour), policy)
	if err != nil {
		return 0, err
	}
	return clampToAllowance(before, after, capComputeAllowance(capCents, egress, &policy, objectStorage)), nil
}

// liveEgressReserved is the egress the cap reserves for acct's month of hour,
// before through: what the month is billed in live delivery by the egress
// hour rules, so egress the subject is not billed for (overage off, Free
// hours) never spends the cap. A store that cannot read usage by hour
// reserves all usage above policy's allowance, the older bound.
func (p *Pusher) liveEgressReserved(ctx context.Context, acct state.Account, hour, through time.Time, policy billing.MeterUsagePolicy) (int64, error) {
	billed, err := monthLiveEgressBilled(ctx, p.usage(), p.pusher, acct, hour, through)
	if !errors.Is(err, errNoHourlyUsage) {
		return billed, err
	}
	hour = hour.UTC()
	start := time.Date(hour.Year(), hour.Month(), 1, 0, 0, 0, 0, time.UTC)
	if policy.EffectiveFrom.After(start) {
		start = policy.EffectiveFrom
	}
	// Only egress in hours the election covers is billed, so only that keeps
	// a place under the cap (ADR-20261104204500).
	election, err := billing.EgressOverageElectionFor(ctx, p.store, acct.ID)
	if err != nil {
		return 0, err
	}
	if election.EnabledAt == nil || !start.Before(through) {
		return 0, nil
	}
	rows, err := p.store.UsageByHour(ctx, acct.ID, start, through)
	if err != nil {
		return 0, err
	}
	return billing.ElectedEgressOverage(rows, policy.IncludedQuantity, election), nil
}
