package billing

import (
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAPIConsumerUsageRegressed reports that current usage, or the units it
// charges, is below what an earlier finalized statement already covered.
// Finalized revisions are never rewritten, so the period cannot be
// re-planned until usage is reconciled.
var ErrAPIConsumerUsageRegressed = errors.New("API consumer usage is below a finalized statement")

type statementCoverage struct{ units, charged int64 }

// MonthStart returns the start of t's UTC calendar month, where monthly
// allowances begin counting.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// APIConsumerStatementDelta returns the part of a period's current quote
// that no finalized revision has billed yet. Drafts and superseded drafts
// never reserve usage. Per minute, the delta holds the units added since
// finalization and the extra units charged: late usage earlier in a month
// can exhaust the monthly allowance (ADR-844) sooner, so a minute may charge
// units it previously received free without adding any.
func APIConsumerStatementDelta(current APIConsumerUsageQuote, revisions []state.APIConsumerUsageStatement) (APIConsumerUsageQuote, error) {
	covered := map[int64]statementCoverage{}
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			minute := bucket.WindowStart.UTC().Unix()
			prior := covered[minute]
			if prior.units > maxInt64-bucket.BillableUnits || prior.charged > maxInt64-bucket.Charged() {
				return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer statement coverage overflow")
			}
			covered[minute] = statementCoverage{prior.units + bucket.BillableUnits, prior.charged + bucket.Charged()}
		}
	}
	delta := APIConsumerUsageQuote{Currency: current.Currency, Buckets: make([]APIConsumerUsageChargeBucket, 0, len(current.Buckets))}
	for _, bucket := range current.Buckets {
		minute := bucket.WindowStart.UTC().Unix()
		prior := covered[minute]
		delete(covered, minute)
		if bucket.BillableUnits < prior.units || bucket.ChargedUnits < prior.charged {
			return APIConsumerUsageQuote{}, ErrAPIConsumerUsageRegressed
		}
		bucket.BillableUnits -= prior.units
		if bucket.RateCardID != "" {
			bucket.ChargedUnits -= prior.charged
			amount, err := multiplyMillicents(bucket.ChargedUnits, bucket.PriceMillicentsPerUnit)
			if err != nil {
				return APIConsumerUsageQuote{}, err
			}
			bucket.AmountMillicents = amount
		}
		if bucket.BillableUnits == 0 && bucket.ChargedUnits == 0 {
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
	delta.Priced = delta.UnpricedUnits == 0 && delta.Currency != ""
	return delta, nil
}
