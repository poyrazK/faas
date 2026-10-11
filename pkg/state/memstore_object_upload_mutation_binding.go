package state

import "context"

func (m *MemStore) ReadTrackedObjectUploadMutation(ctx context.Context, c ObjectUploadCompletion) (ObjectBucketMutation, error) {
	if !validUploadMutationScope(c) {
		return ObjectBucketMutation{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketMutation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, exists := m.objectUploadCompletions[c.ID]
	if !exists || !originalUploadMutationAuthority(old, c, m.clock()) {
		return ObjectBucketMutation{}, ErrConflict
	}
	receipt, exists := m.objectMutations[old.ID]
	if !exists || receipt.UploadID != old.ID {
		return ObjectBucketMutation{}, ErrNotFound
	}
	if receipt.Bucket.ID != old.BucketID || receipt.Kind != ObjectBucketMutationRequest || !sameObjectMutationBucket(receipt.Bucket, m.objectBuckets[old.BucketID]) {
		return ObjectBucketMutation{}, ErrConflict
	}
	return receipt, nil
}
