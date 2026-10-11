package state

import (
	"context"
	"sort"
	"time"
)

var _ ProjectEnvironmentCloneObjectWriteFenceStore = (*MemStore)(nil)

func (m *MemStore) cloneObjectWriteFenceLeaseLocked(lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error) {
	op, _, err := m.cloneLeaseOwnedLocked(lease, time.Now().UTC())
	if err != nil {
		return op, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCompensating {
		return op, ErrConflict
	}
	return op, nil
}

func (m *MemStore) AcquireProjectEnvironmentCloneObjectWriteFence(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ObjectBucketWriteFence, error) {
	if !validCloneObjectFenceLease(lease) || !validCloneCredentialSourceID(sourceID) {
		return ObjectBucketWriteFence{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, err := m.cloneObjectWriteFenceLeaseLocked(lease)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if op.Status != CloneOperationCapturing {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[op.ID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	b, err := capturedCloneWriteFenceBucket(op, views, sourceID)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if !sameObjectMutationBucket(b, m.objectBuckets[b.ID]) {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	if _, exists := m.objectWriteFences[b.ID]; exists {
		return m.ownedObjectWriteFenceLocked(b, cloneObjectWriteFenceToken(op.ID), op.ID)
	}
	if _, err := m.cloneObjectWriteFenceLeaseLocked(lease); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if m.objectWriteFences == nil {
		m.objectWriteFences = map[string]ObjectBucketWriteFence{}
	}
	m.objectWriteFences[b.ID] = ObjectBucketWriteFence{Bucket: m.objectBuckets[b.ID], BucketID: b.ID, Token: cloneObjectWriteFenceToken(op.ID), CloneOperationID: op.ID}
	return m.ownedObjectWriteFenceLocked(b, cloneObjectWriteFenceToken(op.ID), op.ID)
}

func (m *MemStore) cloneObjectWriteFencesLocked(op ProjectEnvironmentCloneOperation) ([]ObjectBucketWriteFence, error) {
	out := []ObjectBucketWriteFence{}
	for _, stored := range m.objectWriteFences {
		if stored.CloneOperationID != op.ID {
			continue
		}
		fence, err := m.ownedObjectWriteFenceLocked(stored.Bucket, cloneObjectWriteFenceToken(op.ID), op.ID)
		if err != nil || fence.Bucket.AccountID != op.AccountID {
			if err == nil {
				err = ErrConflict
			}
			return nil, err
		}
		out = append(out, fence)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketID < out[j].BucketID })
	return out, nil
}

func (m *MemStore) ProjectEnvironmentCloneObjectWriteFencesForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) ([]ObjectBucketWriteFence, error) {
	if !validCloneObjectFenceLease(lease) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, err := m.cloneObjectWriteFenceLeaseLocked(lease)
	if err != nil {
		return nil, err
	}
	return m.cloneObjectWriteFencesLocked(op)
}

func (m *MemStore) AbandonProjectEnvironmentCloneObjectWriteFences(ctx context.Context, lease ProjectEnvironmentCloneLease) error {
	if !validCloneObjectFenceLease(lease) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, err := m.cloneObjectWriteFenceLeaseLocked(lease)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	fences, err := m.cloneObjectWriteFencesLocked(op)
	if err != nil {
		return err
	}
	if _, err := m.cloneObjectWriteFenceLeaseLocked(lease); err != nil {
		return err
	}
	for _, fence := range fences {
		delete(m.objectWriteFences, fence.BucketID)
	}
	return nil
}

func (m *MemStore) cloneHasObjectWriteFencesLocked(operationID string) bool {
	for _, fence := range m.objectWriteFences {
		if fence.CloneOperationID == operationID {
			return true
		}
	}
	return false
}
