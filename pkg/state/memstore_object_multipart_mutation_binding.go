package state

import "context"

func (m *MemStore) ReadObjectMultipartMutation(ctx context.Context, u ObjectMultipartUpload) (ObjectBucketMutation, error) {
	if !validMultipartMutationScope(u) {
		return ObjectBucketMutation{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketMutation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectMultipartUploads[u.ID]
	if !ok || !originalMultipartMutationAuthority(old, u, m.clock()) {
		return ObjectBucketMutation{}, ErrConflict
	}
	receipt, ok := m.objectMutations[u.ID]
	if !ok || receipt.MultipartUploadID != u.ID {
		return ObjectBucketMutation{}, ErrNotFound
	}
	if receipt.Bucket.ID != old.BucketID || receipt.Kind != ObjectBucketMutationRequest || !sameObjectMutationBucket(receipt.Bucket, m.objectBuckets[old.BucketID]) {
		return ObjectBucketMutation{}, ErrConflict
	}
	return receipt, nil
}

func (m *MemStore) retireMultipartMutationLocked(id string) {
	if receipt, ok := m.objectMutations[id]; ok && receipt.MultipartUploadID == id && !objectMultipartLive(m.objectMultipartUploads[id].State) {
		delete(m.objectMutations, id)
	}
}
