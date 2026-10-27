package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// objectStorageCapStore is what month close reads to keep an object-storage
// charge under the account's overage cap (ADR-1320).
type objectStorageCapStore interface {
	UsageRangeStore
	GetAccountOverageCapCents(context.Context, string) (int64, bool, error)
}

// objectStorageChargeWithinCap returns the part of a cumulative object-storage
// charge for the month starting at start that the account's overage cap
// allows, reading compute and egress over [start, end) (ADR-1320). Compute
// and live egress keep their place under the cap (ADR-1162); object storage
// takes what remains after their billable cost is reserved. An uncapped
// account is charged in full. The record keeps the full priced usage; only
// the delivered charge is capped, and capped usage is never billed later.
func objectStorageChargeWithinCap(ctx context.Context, store state.ObjectStorageBillingStore, policies MeterUsagePolicyProvider, account state.Account, start, end time.Time, charge int64) (int64, error) {
	if charge == 0 {
		return 0, nil
	}
	caps, ok := store.(objectStorageCapStore)
	if !ok {
		return 0, errors.New("billing: overage cap store is unavailable")
	}
	capCents, capped, err := caps.GetAccountOverageCapCents(ctx, account.ID)
	if err != nil {
		return 0, fmt.Errorf("load overage cap: %w", err)
	}
	if !capped {
		return charge, nil
	}
	// The month's compute overage as meterd delivers it hour by hour, before
	// its own cap clamp.
	compute, err := OverageMBSecondsForRange(ctx, caps, account, start, end)
	if err != nil {
		return 0, err
	}
	egress, egressPolicy, err := monthBillableLiveEgress(ctx, caps, policies, account, start, end)
	if err != nil {
		return 0, err
	}
	remaining, ok := CapRemainingMillicents(capCents, compute, egress, egressPolicy, 0)
	if !ok {
		return 0, errors.New("billing: invalid overage cap reservation")
	}
	return min(charge, FloorCapQuantity(remaining, 1, 1)), nil
}

// monthBillableLiveEgress is the period's egress above the plan's included
// quantity when egress is billed live, with the policy that prices it.
// Shadow or unconfigured egress never consumes the live cap.
func monthBillableLiveEgress(ctx context.Context, caps objectStorageCapStore, policies MeterUsagePolicyProvider, account state.Account, start, end time.Time) (int64, *MeterUsagePolicy, error) {
	if policies == nil {
		return 0, nil, nil
	}
	policy, configured := policies.MeterUsagePolicy(account.Plan, state.BillingMeterEgress)
	if !configured || policy.Mode != MeterDeliveryLive {
		return 0, nil, nil
	}
	if policy.EffectiveFrom.After(start) {
		start = policy.EffectiveFrom
	}
	raw, err := sumEgressBytes(ctx, caps, account.ID, start, end)
	if err != nil {
		return 0, nil, err
	}
	return max(raw-max(policy.IncludedQuantity, 0), 0), &policy, nil
}

func sumEgressBytes(ctx context.Context, store UsageRangeStore, accountID string, start, end time.Time) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	rows, err := store.UsageByHour(ctx, accountID, start, end)
	if err != nil {
		return 0, fmt.Errorf("load egress usage: %w", err)
	}
	var total int64
	for _, row := range rows {
		if row.NetTxBytes < 0 || row.NetTxBytes > math.MaxInt64-total {
			return 0, errors.New("billing: egress usage out of range")
		}
		total += row.NetTxBytes
	}
	return total, nil
}
