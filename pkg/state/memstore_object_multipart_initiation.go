package state

import "context"

func (m *MemStore) multipartInitiationLocked(u ObjectMultipartUpload) (ObjectMultipartInitiation, error) {
	old, ok := m.objectMultipartUploads[u.ID]
	if !ok || !validMultipartMutationScope(u) || !validMultipartInitiation(u) || !originalMultipartMutationAuthority(old, u, m.clock()) {
		return ObjectMultipartInitiation{}, ErrConflict
	}
	receipt, ok := m.objectMutations[u.ID]
	if !ok || receipt.MultipartUploadID != u.ID {
		return ObjectMultipartInitiation{}, ErrNotFound
	}
	if receipt.Kind != ObjectBucketMutationRequest || receipt.Bucket.ID != old.BucketID || !sameObjectMutationBucket(receipt.Bucket, m.objectBuckets[old.BucketID]) {
		return ObjectMultipartInitiation{}, ErrConflict
	}
	d, ok := m.objectMultipartInitiations[u.ID]
	if !ok {
		// A pre-existing journal cannot be classified as never dispatched.
		d.Dispatched = true
	}
	d.Receipt = receipt
	return d, nil
}

func (m *MemStore) ReadObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload) (ObjectMultipartInitiation, error) {
	if err := ctx.Err(); err != nil {
		return ObjectMultipartInitiation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.multipartInitiationLocked(u)
}

func (m *MemStore) DispatchObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.multipartInitiationLocked(u)
	if err != nil {
		return err
	}
	if d.Dispatched {
		return ErrConflict
	}
	d.Dispatched, d.DispatchToken = true, u.LeaseToken
	m.objectMultipartInitiations[u.ID] = d
	return nil
}

func (m *MemStore) ObserveObjectMultipartInitiation(ctx context.Context, u ObjectMultipartUpload, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.multipartInitiationLocked(u)
	if err != nil {
		return err
	}
	if !validMultipartInitiationResult(id) || !d.Dispatched || d.DispatchToken != u.LeaseToken || d.ProviderUploadID != "" && d.ProviderUploadID != id {
		return ErrConflict
	}
	d.ProviderUploadID = id
	m.objectMultipartInitiations[u.ID] = d
	return nil
}
