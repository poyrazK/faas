package state

import "context"

var _ ObjectMultipartPartPutMutationStore = (*MemStore)(nil)

func (m *MemStore) DispatchObjectMultipartPartPutMutation(ctx context.Context, b ObjectBucket, id string, part int32, token string, i ObjectMultipartPartPutIntent) (ObjectBucketMutation, error) {
	if !validMultipartPartPutIntent(i) || i.BodySHA256 != "" {
		return ObjectBucketMutation{}, ErrConflict
	}
	return m.dispatchMultipartPart(ctx, b, id, part, token, nil, &i)
}
func (m *MemStore) ObserveObjectMultipartPartBody(ctx context.Context, r ObjectBucketMutation, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validMultipartPartReceipt(r) || !validMultipartPartSHA256(digest) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !sameObjectMutationBucket(r.Bucket, m.objectBuckets[r.Bucket.ID]) {
		return ErrConflict
	}
	for key, d := range m.objectMultipartPartWriters {
		if d.receipt.ID != r.ID {
			continue
		}
		if !sameObjectMutationBucket(r.Bucket, d.receipt.Bucket) || !d.dispatched || d.settled || d.putIntent == nil || m.objectMultipartTransfers[key.upload][key.part].token != key.token || d.putIntent.ExpectedSHA256 != "" && d.putIntent.ExpectedSHA256 != digest || d.putIntent.BodySHA256 != "" && d.putIntent.BodySHA256 != digest {
			return ErrConflict
		}
		i := *d.putIntent
		i.BodySHA256 = digest
		d.putIntent = &i
		m.objectMultipartPartWriters[key] = d
		return nil
	}
	return ErrConflict
}
func (m *MemStore) ReadObjectMultipartPartPutIntent(ctx context.Context, r ObjectBucketMutation) (ObjectMultipartPartPutIntent, error) {
	if err := ctx.Err(); err != nil {
		return ObjectMultipartPartPutIntent{}, err
	}
	if !validMultipartPartReceipt(r) {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !sameObjectMutationBucket(r.Bucket, m.objectBuckets[r.Bucket.ID]) {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	for _, d := range m.objectMultipartPartWriters {
		if d.receipt.ID == r.ID && sameObjectMutationBucket(r.Bucket, d.receipt.Bucket) && d.dispatched && d.putIntent != nil {
			return *d.putIntent, nil
		}
	}
	return ObjectMultipartPartPutIntent{}, ErrConflict
}
