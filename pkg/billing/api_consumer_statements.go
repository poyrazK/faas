package billing

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAPIConsumerUsageRegressed reports that current usage is below what an
// earlier finalized statement already covered. Finalized revisions are never
// rewritten, so the period cannot be re-planned until usage is reconciled.
var ErrAPIConsumerUsageRegressed = errors.New("API consumer usage is below a finalized statement")

// APIConsumerUsageDelta returns the usage not yet covered by any finalized
// revision of one period. Drafts and superseded drafts never reserve usage.
// Each finalized revision's per-minute buckets are its coverage, so usage
// that arrives after finalization becomes an additive adjustment instead of
// being billed twice or never.
func APIConsumerUsageDelta(usage []state.APIConsumerUsageBucket, revisions []state.APIConsumerUsageStatement) ([]state.APIConsumerUsageBucket, error) {
	covered := map[int64]int64{}
	for _, statement := range revisions {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue
		}
		for _, bucket := range statement.Buckets {
			minute := bucket.WindowStart.UTC().Unix()
			if covered[minute] > maxInt64-bucket.BillableUnits {
				return nil, fmt.Errorf("billing: API consumer statement coverage overflow")
			}
			covered[minute] += bucket.BillableUnits
		}
	}
	delta := make([]state.APIConsumerUsageBucket, 0, len(usage))
	for _, bucket := range usage {
		minute := bucket.WindowStart.UTC().Unix()
		prior := covered[minute]
		if bucket.BillableUnits < prior {
			return nil, ErrAPIConsumerUsageRegressed
		}
		delete(covered, minute)
		bucket.BillableUnits -= prior
		if bucket.BillableUnits > 0 {
			delta = append(delta, bucket)
		}
	}
	for _, remaining := range covered {
		if remaining > 0 {
			return nil, ErrAPIConsumerUsageRegressed
		}
	}
	return delta, nil
}
