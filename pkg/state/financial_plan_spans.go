package state

import (
	"math"
	"math/big"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanSpan is a stretch of a billing subject's month under one plan.
type PlanSpan struct {
	Plan       api.Plan
	Start, End time.Time
}

// MonthPlanSpans splits [monthStart, through) at the subject's recorded
// compute plan activations, oldest first (ADR-20261103131742 amendment 2).
// Each span runs under the plan activated at its start; the earliest
// activation also covers the month before it, as MonthComputeIncludedMBSeconds
// has it. With no activation recorded the current plan holds the whole
// stretch. Adjacent spans on the same plan are merged.
func MonthPlanSpans(current api.Plan, snapshots []FinancialPriceSnapshot, monthStart, through time.Time) []PlanSpan {
	monthStart, through = monthStart.UTC(), through.UTC()
	if !through.After(monthStart) {
		return nil
	}
	var activations []FinancialPriceSnapshot
	for _, s := range snapshots {
		if s.Price.Meter == "compute" && s.Plan.Valid() && s.EffectiveFrom.Before(through) {
			activations = append(activations, s)
		}
	}
	if len(activations) == 0 {
		return []PlanSpan{{Plan: current, Start: monthStart, End: through}}
	}
	slices.SortStableFunc(activations, func(a, b FinancialPriceSnapshot) int {
		if c := a.EffectiveFrom.Compare(b.EffectiveFrom); c != 0 {
			return c
		}
		return a.RecordedAt.Compare(b.RecordedAt)
	})
	var spans []PlanSpan
	for i, s := range activations {
		start := monthStart
		if i > 0 && s.EffectiveFrom.After(monthStart) {
			start = s.EffectiveFrom.UTC()
		}
		end := through
		if i+1 < len(activations) && activations[i+1].EffectiveFrom.After(start) {
			end = activations[i+1].EffectiveFrom.UTC()
		} else if i+1 < len(activations) {
			end = start
		}
		if !end.After(start) {
			continue
		}
		if n := len(spans); n > 0 && spans[n-1].Plan == s.Plan {
			spans[n-1].End = end
			continue
		}
		spans = append(spans, PlanSpan{Plan: s.Plan, Start: start, End: end})
	}
	return spans
}

// WeightedMonthAllowance is a monthly allowance weighted by the share of the
// month each span's plan held it: allowance(plan) × span ÷ month, summed and
// rounded down. The spans are taken to cover the month; a plan's allowance
// counts only for the time it held (ADR-20261030093512).
func WeightedMonthAllowance(spans []PlanSpan, monthStart time.Time, allowance func(api.Plan) int64) int64 {
	monthStart = monthStart.UTC()
	month := big.NewInt(int64(monthStart.AddDate(0, 1, 0).Sub(monthStart) / time.Second))
	total := new(big.Int)
	for _, span := range spans {
		seconds := int64(span.End.Sub(span.Start) / time.Second)
		if seconds <= 0 {
			continue
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(allowance(span.Plan)), big.NewInt(seconds)))
	}
	total.Quo(total, month)
	if !total.IsInt64() {
		return math.MaxInt64
	}
	return total.Int64()
}
