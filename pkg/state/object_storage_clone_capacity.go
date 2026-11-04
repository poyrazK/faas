package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectCloneSourceCapacity is the conservative capacity of the named live
// buckets in a usage snapshot: what an environment clone may copy out of them
// (ADR-20261103160000). A bucket missing from the snapshot is reported as
// unknown, since its size cannot be bounded.
func ObjectCloneSourceCapacity(s ObjectUsageSnapshot, bucketIDs []string) (bytes, keys int64, known bool) {
	want := make(map[string]bool, len(bucketIDs))
	for _, id := range bucketIDs {
		want[id] = true
	}
	for _, b := range s.Buckets {
		if !want[b.Bucket.ID] || b.Bucket.State == "deleted" {
			continue
		}
		delete(want, b.Bucket.ID)
		capacityBytes, capacityKeys := objectBucketCapacity(b)
		bytes, keys = boundedObjectAdd(bytes, capacityBytes), boundedObjectAdd(keys, capacityKeys)
	}
	return bytes, keys, len(want) == 0
}

// CheckObjectCloneCapacity refuses an environment clone whose copied objects
// would take the account past its stored bytes or keys under the effective
// policy: the operator safety policy tightened by the plan's ceilings
// (ADR-1320, ADR-20261103160000). Admission checks the source buckets'
// conservative capacity; the worker checks again with the exact totals of the
// manifests it is about to copy.
func CheckObjectCloneCapacity(s ObjectUsageSnapshot, p api.ObjectStoragePolicy, now time.Time, bytes, keys int64) error {
	if bytes < 0 || keys < 0 {
		return ErrInvalidArgument
	}
	p = s.planPolicy(p)
	u, _ := summarizeObjectUsage(s, p, now)
	if !u.Fresh {
		return ErrObjectUsageStale
	}
	for _, limit := range []struct {
		name          string
		observed, max int64
	}{
		{"account_bytes", boundedObjectAdd(u.CapacityBytes, bytes), p.MaxAccountBytes},
		{"account_keys", boundedObjectAdd(u.CapacityKeys, keys), p.MaxAccountKeys},
	} {
		if limit.observed > limit.max {
			return &ObjectStorageLimitError{Kind: limit.name, Observed: limit.observed, Limit: limit.max, Cause: ErrObjectCapacity}
		}
	}
	return nil
}
