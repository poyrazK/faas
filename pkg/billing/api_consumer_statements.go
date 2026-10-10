package billing

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAPIConsumerUsageRegressed reports that current usage, or the units it
// charges, is below what an earlier finalized statement already covered.
// Finalized revisions are never rewritten, so the period cannot be
// re-planned until usage is reconciled.
var ErrAPIConsumerUsageRegressed = errors.New("API consumer usage is below a finalized statement")

// ErrAPIConsumerChargeDecreased reports that re-rating a tiered period would
// lower its total below what finalized revisions billed (ADR-845). Gregale
// never issues credits, so the period cannot take an adjustment.
var ErrAPIConsumerChargeDecreased = errors.New("API consumer charges would fall below a finalized statement")

type statementCoverage struct {
	units, charged, amount int64
	tierUnits              []int64
}

// MonthStart returns the start of t's UTC calendar month, where monthly
// allowances begin counting.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// WeightAPIConsumerUsage converts each minute's billable requests into
// weighted units (ADR-846): a request on a route the effective card weights
// counts that many units, every other request counts one. Weighted units are
// what allowances, tiers, and statements then measure. Route rows never add
// more requests than the minute's total; an unattributed remainder counts at
// weight 1. Callers pass usage ascending by minute.
func WeightAPIConsumerUsage(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, routes []state.APIConsumerRouteUsageBucket) ([]state.APIConsumerUsageBucket, error) {
	ordered := append([]state.APIConsumerRateCard(nil), cards...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom) })
	byMinute := map[int64][]state.APIConsumerRouteUsageBucket{}
	for _, route := range routes {
		minute := route.WindowStart.UTC().Unix()
		byMinute[minute] = append(byMinute[minute], route)
	}
	out := make([]state.APIConsumerUsageBucket, 0, len(usage))
	cardIndex := -1
	for _, bucket := range usage {
		minute := bucket.WindowStart.UTC()
		for cardIndex+1 < len(ordered) && !ordered[cardIndex+1].EffectiveFrom.After(minute) {
			cardIndex++
		}
		if cardIndex >= 0 && len(ordered[cardIndex].RouteWeights) > 0 {
			weighted, err := weightedUnits(ordered[cardIndex].RouteWeights, bucket.BillableUnits, byMinute[minute.Unix()])
			if err != nil {
				return nil, err
			}
			bucket.BillableUnits = weighted
		}
		out = append(out, bucket)
	}
	return out, nil
}

func weightedUnits(weights map[string]int64, units int64, routes []state.APIConsumerRouteUsageBucket) (int64, error) {
	total, remaining := units, units
	for _, route := range routes {
		weight, ok := weights[route.Route]
		if !ok || weight <= 1 || remaining <= 0 {
			continue
		}
		counted := min(route.BillableUnits, remaining)
		remaining -= counted
		extra, err := multiplyMillicents(counted, weight-1)
		if err != nil || total > maxInt64-extra {
			return 0, fmt.Errorf("billing: API consumer weighted units overflow")
		}
		total += extra
	}
	return total, nil
}

// IsCalendarMonth reports whether [start, end) is exactly one UTC month.
func IsCalendarMonth(start, end time.Time) bool {
	return start.Equal(MonthStart(start)) && end.Equal(MonthStart(start).AddDate(0, 1, 0))
}

// APIConsumerStatementDelta returns the part of a period's current quote
// that no finalized revision has billed yet. Drafts and superseded drafts
// never reserve usage. Per minute, the delta holds the units added since
// finalization and the extra units charged: late usage earlier in a month
// can exhaust the monthly allowance (ADR-844) sooner, so a minute may charge
// units it previously received free without adding any. A tiered minute
// (ADR-845) is re-rated: its delta is the difference in each ladder step's
// units and in amount, which can be negative when late usage pushed billed
// units into a cheaper step. The revision as a whole never credits.
func APIConsumerStatementDelta(current APIConsumerUsageQuote, revisions []state.APIConsumerUsageStatement) (APIConsumerUsageQuote, error) {
	covered, err := finalizedCoverage(revisions)
	if err != nil {
		return APIConsumerUsageQuote{}, err
	}
	delta := APIConsumerUsageQuote{Currency: current.Currency, Buckets: make([]APIConsumerUsageChargeBucket, 0, len(current.Buckets))}
	for _, bucket := range current.Buckets {
		minute := bucket.WindowStart.UTC().Unix()
		prior := covered[minute]
		delete(covered, minute)
		if bucket.BillableUnits < prior.units || bucket.ChargedUnits < prior.charged {
			return APIConsumerUsageQuote{}, ErrAPIConsumerUsageRegressed
		}
		bucket, changed, err := subtractCoverage(bucket, prior)
		if err != nil {
			return APIConsumerUsageQuote{}, err
		}
		if !changed {
			continue
		}
		if err := delta.add(bucket); err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}
	for _, remaining := range covered {
		if remaining.units > 0 {
			return APIConsumerUsageQuote{}, ErrAPIConsumerUsageRegressed
		}
	}
	if delta.AmountMillicents < 0 {
		return APIConsumerUsageQuote{}, ErrAPIConsumerChargeDecreased
	}
	delta.Priced = delta.UnpricedUnits == 0 && delta.Currency != ""
	return delta, nil
}

func finalizedCoverage(revisions []state.APIConsumerUsageStatement) (map[int64]statementCoverage, error) {
	covered := map[int64]statementCoverage{}
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			minute := bucket.WindowStart.UTC().Unix()
			prior := covered[minute]
			if prior.units > maxInt64-bucket.BillableUnits || prior.charged > maxInt64-bucket.Charged() {
				return nil, fmt.Errorf("billing: API consumer statement coverage overflow")
			}
			next := statementCoverage{units: prior.units + bucket.BillableUnits, charged: prior.charged + bucket.Charged(),
				amount: prior.amount + bucket.AmountMillicents, tierUnits: prior.tierUnits}
			if bucket.TierUnits != nil {
				next.tierUnits = addTierUnits(prior.tierUnits, bucket.TierUnits)
			}
			covered[minute] = next
		}
	}
	return covered, nil
}

// subtractCoverage leaves only what finalized revisions have not billed.
// Flat minutes price new charged units at the current price, as before;
// tiered minutes take the exact difference in step units and amount.
func subtractCoverage(bucket APIConsumerUsageChargeBucket, prior statementCoverage) (APIConsumerUsageChargeBucket, bool, error) {
	bucket.BillableUnits -= prior.units
	if bucket.RateCardID == "" {
		return bucket, bucket.BillableUnits != 0, nil
	}
	bucket.ChargedUnits -= prior.charged
	if bucket.TierUnits == nil {
		amount, err := multiplyMillicents(bucket.ChargedUnits, bucket.PriceMillicentsPerUnit)
		bucket.AmountMillicents = amount
		return bucket, bucket.BillableUnits != 0 || bucket.ChargedUnits != 0, err
	}
	bucket.AmountMillicents -= prior.amount
	moved := false
	for i := range bucket.TierUnits {
		if i < len(prior.tierUnits) {
			bucket.TierUnits[i] -= prior.tierUnits[i]
		}
		moved = moved || bucket.TierUnits[i] != 0
	}
	return bucket, bucket.BillableUnits != 0 || bucket.ChargedUnits != 0 || bucket.AmountMillicents != 0 || moved, nil
}

func addTierUnits(total, more []int64) []int64 {
	out := make([]int64, max(len(total), len(more)))
	copy(out, total)
	for i, units := range more {
		out[i] += units
	}
	return out
}
