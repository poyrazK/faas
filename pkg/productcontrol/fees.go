package productcontrol

import (
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanHeld is a catalog revision and plan a customer held during a month:
// Since is when they were first assigned it in the month, or earlier when
// they held it as the month started; First marks the customer's first
// assignment ever (ADR-20261021154511).
type PlanHeld struct {
	Revision, Plan string
	Since          time.Time
	First          bool
}

// MonthlyFee is the fee a customer owes for a UTC month: the highest monthly
// fee among the plans held, once, prorated by the minute from the customer's
// first assignment when it falls in the month. fee looks up a plan's monthly
// fee in its revision. nil means no fee.
func MonthlyFee(held []PlanHeld, fee func(revision, plan string) int64, period string) *api.ProductBillingFee {
	start, err := time.Parse("2006-01", period)
	if err != nil {
		return nil
	}
	end := start.AddDate(0, 1, 0)
	var best *api.ProductBillingFee
	var bestSince time.Time
	var joined *time.Time
	for _, h := range held {
		if h.First && !h.Since.Before(start) && h.Since.Before(end) {
			at := h.Since.UTC().Truncate(time.Minute)
			joined = &at
		}
		amount := fee(h.Revision, h.Plan)
		if amount <= 0 || best != nil && (amount < best.MonthlyFeeMillicents || amount == best.MonthlyFeeMillicents && !h.Since.Before(bestSince)) {
			continue
		}
		best, bestSince = &api.ProductBillingFee{Plan: h.Plan, Revision: h.Revision, MonthlyFeeMillicents: amount, AmountMillicents: amount}, h.Since
	}
	if best != nil && joined != nil && joined.After(start) {
		best.AmountMillicents, best.ProratedFrom = prorate(best.MonthlyFeeMillicents, *joined, start, end), joined
	}
	return best
}

// prorate scales a monthly amount to the whole minutes of [from, end) over
// the month's minutes, rounded half up to the millicent, as ADR-1325 does
// for platform fees.
func prorate(amount int64, from, start, end time.Time) int64 {
	total := int64(end.Sub(start) / time.Minute)
	share := int64(end.Sub(from) / time.Minute)
	if share >= total {
		return amount
	}
	scaled := new(big.Int).Mul(big.NewInt(amount), big.NewInt(share))
	scaled.Add(scaled, big.NewInt(total/2))
	scaled.Quo(scaled, big.NewInt(total))
	return scaled.Int64()
}
