package productcontrol

import (
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanHeld is one assignment of a customer to a catalog revision and plan
// that overlaps a month: held from Since until Until, the next assignment,
// or still held when Until is zero. First marks the customer's first
// assignment ever (ADR-20261021154511).
type PlanHeld struct {
	Revision, Plan string
	Since, Until   time.Time
	First          bool
}

// Trial is a customer's free trial, [Start, End) (ADR-20261021195901).
type Trial struct{ Start, End time.Time }

type span struct{ from, until time.Time }

// MonthlyFee is the fee a customer owes for a UTC month. Its billable
// minutes run from the customer's first assignment, when that falls in the
// month, to the month's end, less the trial. It charges the highest monthly
// fee among the plans held in those minutes, once, prorated by their share
// of the month, rounded half up to the millicent. fee looks up a plan's
// monthly fee in its revision; nil means no fee.
func MonthlyFee(held []PlanHeld, fee func(revision, plan string) int64, period string, trial *Trial) *api.ProductBillingFee {
	start, err := time.Parse("2006-01", period)
	if err != nil {
		return nil
	}
	end := start.AddDate(0, 1, 0)
	from := start
	for _, h := range held {
		if joined := h.Since.UTC().Truncate(time.Minute); h.First && joined.After(from) && joined.Before(end) {
			from = joined
		}
	}
	billable := []span{{from, end}}
	if trial != nil {
		billable = without(billable, span{trial.Start.UTC().Truncate(time.Minute), trial.End.UTC().Truncate(time.Minute)})
	}
	var best *api.ProductBillingFee
	var bestSince time.Time
	for _, h := range held {
		amount := fee(h.Revision, h.Plan)
		if amount <= 0 || !overlapsAny(h, billable) || best != nil && (amount < best.MonthlyFeeMillicents || amount == best.MonthlyFeeMillicents && !h.Since.Before(bestSince)) {
			continue
		}
		best, bestSince = &api.ProductBillingFee{Plan: h.Plan, Revision: h.Revision, MonthlyFeeMillicents: amount}, h.Since
	}
	if best == nil {
		return nil
	}
	var minutes int64
	for _, s := range billable {
		minutes += int64(s.until.Sub(s.from) / time.Minute)
	}
	best.AmountMillicents = prorate(best.MonthlyFeeMillicents, minutes, int64(end.Sub(start)/time.Minute))
	if from.After(start) {
		best.ProratedFrom = &from
	}
	if trial != nil && trial.Start.Before(end) && trial.End.After(start) {
		ts, te := maxTime(trial.Start.UTC(), start), minTime(trial.End.UTC(), end)
		best.TrialStartsAt, best.TrialEndsAt = &ts, &te
	}
	return best
}

// without removes cut from spans, dropping empty pieces.
func without(spans []span, cut span) []span {
	var out []span
	for _, s := range spans {
		if !cut.from.Before(s.until) || !cut.until.After(s.from) {
			out = append(out, s)
			continue
		}
		if cut.from.After(s.from) {
			out = append(out, span{s.from, cut.from})
		}
		if cut.until.Before(s.until) {
			out = append(out, span{cut.until, s.until})
		}
	}
	return out
}

func overlapsAny(h PlanHeld, spans []span) bool {
	for _, s := range spans {
		if h.Since.Before(s.until) && (h.Until.IsZero() || h.Until.After(s.from)) {
			return true
		}
	}
	return false
}

// prorate scales a monthly amount to minutes of the month's total, rounded
// half up to the millicent, as ADR-1325 does for platform fees.
func prorate(amount, minutes, total int64) int64 {
	if minutes >= total {
		return amount
	}
	scaled := new(big.Int).Mul(big.NewInt(amount), big.NewInt(minutes))
	scaled.Add(scaled, big.NewInt(total/2))
	scaled.Quo(scaled, big.NewInt(total))
	return scaled.Int64()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
