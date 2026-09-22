package meter

import (
	"context"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type billableQuantities struct {
	compute, egress int64
}

// combinedOverageCapReached compares exact costs before rounding to cents.
// Flooring compute first permits over-cap egress; rounding each meter up
// independently would reject fractional quantities that fit the cap together.
// Rational arithmetic also avoids overflow for large but valid quantities.
func combinedOverageCapReached(capCents int64, before, after billableQuantities, policy billing.MeterUsagePolicy) bool {
	if capCents < 0 || before.compute < 0 || before.egress < 0 || after.compute < 0 || after.egress < 0 || policy.UnitQuantity <= 0 || policy.MillicentsPerUnit <= 0 {
		return true
	}
	limit := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(capCents), big.NewInt(api.MillicentsPerCent)))
	cost := func(q billableQuantities) *big.Rat {
		compute := new(big.Rat).SetFrac(
			new(big.Int).Mul(big.NewInt(q.compute), big.NewInt(api.OverageMillicentsPerGBHour)),
			big.NewInt(api.SecondsPerGBHour))
		egress := new(big.Rat).SetFrac(
			new(big.Int).Mul(big.NewInt(q.egress), big.NewInt(policy.MillicentsPerUnit)),
			big.NewInt(policy.UnitQuantity))
		return compute.Add(compute, egress)
	}
	return cost(before).Cmp(limit) >= 0 || cost(after).Cmp(limit) > 0
}

// computeCapReached reserves the same live-egress cost that the secondary
// meter checks against compute. Otherwise processing all compute before all
// egress lets an earlier egress hour and a later compute hour spend the same
// account cap independently. Shadow/off usage never consumes the live cap.
func (p *Pusher) computeCapReached(ctx context.Context, acct state.Account, hour time.Time, capCents, before, after int64) (bool, error) {
	if overageCapReached(capCents, before, after) {
		return true, nil
	}
	policies, ok := p.pusher.(billing.MeterUsagePolicyProvider)
	if !ok {
		return false, nil
	}
	policy, configured := policies.MeterUsagePolicy(acct.Plan, state.BillingMeterEgress)
	if !configured || policy.Mode != billing.MeterDeliveryLive {
		return false, nil
	}
	if err := validateMeterUsagePolicy(policy); err != nil {
		return false, err
	}
	hour = hour.UTC().Truncate(time.Hour)
	start := time.Date(hour.Year(), hour.Month(), 1, 0, 0, 0, 0, time.UTC)
	if policy.EffectiveFrom.After(start) {
		start = policy.EffectiveFrom
	}
	raw, err := sumMeterUsageRows(ctx, p.store, acct.ID, state.BillingMeterEgress, start, hour.Add(time.Hour))
	if err != nil {
		return false, err
	}
	egress := max(raw-policy.IncludedQuantity, 0)
	return combinedOverageCapReached(capCents,
		billableQuantities{compute: before, egress: egress},
		billableQuantities{compute: after, egress: egress}, policy), nil
}
