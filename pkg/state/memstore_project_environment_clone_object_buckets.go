package state

import (
	"context"
	"time"
)

func (m *MemStore) ReserveProjectEnvironmentCloneObjectBucket(_ context.Context, lease ProjectEnvironmentCloneLease, appID, sourceID string, limit int) (ObjectBucket, bool, error) {
	if !validCloneLeaseIdentity(lease) || appID == "" || sourceID == "" || limit < 1 {
		return ObjectBucket{}, false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.cloneLeaseOwnedLocked(lease, time.Now().UTC())
	if err != nil {
		return ObjectBucket{}, false, err
	}
	if op.Status != CloneOperationCapturing {
		return ObjectBucket{}, false, ErrConflict
	}
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[op.ID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, sourceID)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	// Validate existing placement before a mutation, including a malformed ID
	// collision, so a failed reservation does not leave a new target behind.
	for _, row := range m.objectBuckets {
		if row.AccountID == want.AccountID && row.AppID == appID && row.Name == want.Name && row.Scope == want.Scope && row.State != "deleted" {
			if err := validateCloneBucketReservation(want, row, source); err != nil {
				return ObjectBucket{}, false, err
			}
		}
	}
	reserved, created, err := m.reserveObjectBucketLocked(want, limit)
	if err != nil {
		return ObjectBucket{}, false, err
	}
	return reserved, created, validateCloneBucketReservation(want, reserved, source)
}

func (m *MemStore) ProjectEnvironmentCloneObjectBucketForLease(_ context.Context, lease ProjectEnvironmentCloneLease, appID, sourceID, targetID string) (ObjectBucket, error) {
	if !validCloneLeaseIdentity(lease) || appID == "" || sourceID == "" || targetID == "" {
		return ObjectBucket{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, _, err := m.cloneLeaseOwnedLocked(lease, time.Now().UTC())
	if err != nil {
		return ObjectBucket{}, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCopying && op.Status != CloneOperationPublishing {
		return ObjectBucket{}, ErrConflict
	}
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[op.ID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucket{}, err
	}
	want, source, err := capturedCloneBucketReservation(op, views, appID, sourceID)
	if err != nil {
		return ObjectBucket{}, err
	}
	actual, exists := m.objectBuckets[targetID]
	if !exists || actual.AccountID != op.AccountID || actual.EnvironmentCloneOperationID != op.ID {
		return ObjectBucket{}, ErrNotFound
	}
	return actual, validateCloneBucketReservation(want, actual, source)
}

func (m *MemStore) cloneBucketAccessibleLocked(bucket ObjectBucket) bool {
	if bucket.EnvironmentCloneOperationID == "" {
		return true
	}
	op, ok := m.projectEnvironmentCloneOperations[bucket.EnvironmentCloneOperationID]
	return ok && op.AccountID == bucket.AccountID && op.TargetEnvironment == bucket.Scope && op.Status == CloneOperationReady
}
