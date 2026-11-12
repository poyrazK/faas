package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// objectBucketActivity mirrors a row of object_bucket_activity
// (ADR-20261110151628 §2).
type objectBucketActivity struct {
	mutatedAt, activeSince time.Time
}

// markObjectBucketMutatedLocked is object_bucket_mark_mutated: Postgres runs
// it from triggers on the source-writer journal, the deletion journal and
// versioning, Object Lock and capacity reconciliation changes. The MemStore
// calls it wherever it changes the same state.
func (m *MemStore) markObjectBucketMutatedLocked(bucket string) {
	if bucket == "" {
		return
	}
	now := m.clock()
	if m.objectBucketActivity == nil {
		m.objectBucketActivity = map[string]objectBucketActivity{}
	}
	a, ok := m.objectBucketActivity[bucket]
	if !ok {
		m.objectBucketActivity[bucket] = objectBucketActivity{mutatedAt: now, activeSince: now}
		return
	}
	granularity := api.ObjectInventoryMutationStampGranularity
	if !a.mutatedAt.Before(now.Add(-granularity)) {
		return
	}
	if observed := m.objectUsage[bucket].ObservedAt; !observed.IsZero() && a.mutatedAt.Before(observed.Add(-granularity)) {
		a.activeSince = now
	}
	a.mutatedAt = now
	m.objectBucketActivity[bucket] = a
}

// markOutstandingObjectMutationsLocked is ObjectBucketMarkOutstandingMutations:
// a listing published while a writer or deletion of its bucket is
// outstanding leaves the bucket active.
func (m *MemStore) markOutstandingObjectMutationsLocked(bucket string) {
	for _, r := range m.objectMutations {
		if r.Bucket.ID == bucket {
			m.markObjectBucketMutatedLocked(bucket)
			return
		}
	}
	for _, j := range m.objectDeletions {
		if j.BucketID == bucket && (j.State == "prepared" || j.State == "dispatched") {
			m.markObjectBucketMutatedLocked(bucket)
			return
		}
	}
}

// putObjectMutationLocked and dropObjectMutationLocked record and clear a
// source writer; like the object_bucket_mutations trigger, both stamp the
// bucket's activity.
func (m *MemStore) putObjectMutationLocked(r ObjectBucketMutation) {
	if m.objectMutations == nil {
		m.objectMutations = map[string]ObjectBucketMutation{}
	}
	m.objectMutations[r.ID] = r
	m.markObjectBucketMutatedLocked(r.Bucket.ID)
}

func (m *MemStore) dropObjectMutationLocked(id string) {
	r, ok := m.objectMutations[id]
	if !ok {
		return
	}
	delete(m.objectMutations, id)
	m.markObjectBucketMutatedLocked(r.Bucket.ID)
}

// objectInventoryQuietModeLocked is object_inventory_quiet_mode(): the
// applied object_inventory_quiet value, off when none is applied or valid.
func (m *MemStore) objectInventoryQuietModeLocked() string {
	row, ok := m.runtimeConfigs[runtimeConfigMapKey(api.ObjectInventoryQuietConfigKey, RuntimeConfigScopeGlobal, "")]
	var mode string
	if !ok || row.Status != RuntimeConfigApplied || json.Unmarshal(row.EffectiveValue, &mode) != nil || !api.ValidObjectInventoryQuiet(mode) {
		return api.ObjectInventoryQuietOff
	}
	return mode
}

// objectUsageWithActivityLocked is a bucket's usage with its activity stamps,
// as the Postgres queries join them.
func (m *MemStore) objectUsageWithActivityLocked(bucket string) ObjectBucketUsage {
	u := m.objectUsage[bucket]
	if a, ok := m.objectBucketActivity[bucket]; ok {
		u.MutatedAt, u.ActiveSince = a.mutatedAt, a.activeSince
	}
	return u
}
