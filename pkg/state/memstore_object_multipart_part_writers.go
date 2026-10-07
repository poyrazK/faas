package state

import (
	"context"
	"github.com/google/uuid"
)

var _ ObjectMultipartPartCopyMutationStore = (*MemStore)(nil)

var _ ObjectMultipartPartMutationStore = (*MemStore)(nil)

func (m *MemStore) multipartPartWriterPendingLocked(id string, part int32) bool {
	for key, d := range m.objectMultipartPartWriters {
		if key.upload == id && (part == 0 || key.part == part) && d.dispatched && !d.settled {
			return true
		}
	}
	return false
}

func (m *MemStore) reserveMultipartPartWriterLocked(id string, part int32, token string) {
	for key, d := range m.objectMultipartPartWriters {
		if key.upload == id && key.part == part && !d.dispatched {
			d.settled = true
			m.objectMultipartPartWriters[key] = d
		}
	}
	if m.objectMultipartPartWriters == nil {
		m.objectMultipartPartWriters = map[multipartPartWriterKey]multipartPartWriter{}
	}
	writerID := uuid.NewString()
	m.objectMultipartPartWriters[multipartPartWriterKey{id, part, token}] = multipartPartWriter{receipt: ObjectBucketMutation{ID: writerID, MultipartPartWriterID: writerID, Bucket: m.objectBuckets[m.objectMultipartUploads[id].BucketID], Kind: ObjectBucketMutationRequest, CreatedAt: m.clock().UTC()}}
}

func (m *MemStore) DispatchObjectMultipartPartMutation(ctx context.Context, b ObjectBucket, id string, part int32, token string) (ObjectBucketMutation, error) {
	return m.dispatchMultipartPart(ctx, b, id, part, token, nil)
}

func (m *MemStore) DispatchObjectMultipartPartCopyMutation(ctx context.Context, b ObjectBucket, id string, part int32, token string, intent ObjectMultipartPartCopyIntent) (ObjectBucketMutation, error) {
	if !validMultipartPartCopyIntent(intent) {
		return ObjectBucketMutation{}, ErrConflict
	}
	return m.dispatchMultipartPart(ctx, b, id, part, token, &intent)
}

func (m *MemStore) dispatchMultipartPart(ctx context.Context, b ObjectBucket, id string, part int32, token string, intent *ObjectMultipartPartCopyIntent) (ObjectBucketMutation, error) {
	if err := ctx.Err(); err != nil {
		return ObjectBucketMutation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := multipartPartWriterKey{id, part, token}
	d, ok := m.objectMultipartPartWriters[key]
	u := m.objectMultipartUploads[id]
	t := m.objectMultipartTransfers[id][part]
	if !ok || d.dispatched || d.settled || !sameObjectMutationBucket(b, d.receipt.Bucket) || !sameObjectMutationBucket(b, m.objectBuckets[b.ID]) || u.AccountID != b.AccountID || u.AppID != b.AppID || u.BucketID != b.ID || u.State != ObjectMultipartActive || !u.ExpiresAt.After(m.clock()) || t.token != token || !t.unsafeUntil.After(m.clock()) {
		return ObjectBucketMutation{}, ErrConflict
	}
	if intent != nil {
		source := m.objectBuckets[intent.SourceBucketID]
		if source.AccountID != b.AccountID || source.State != "ready" || source.BackendID != intent.SourceBackendID || source.BackendFingerprint != intent.SourceBackendFingerprint || source.PhysicalName != intent.SourcePhysicalName || u.Key != intent.DestinationKey || u.ProviderUploadID != intent.ProviderUploadID || intent.ExpectedSize > m.objectMultipartPartGrants[id][part] || (t.copySource.BucketID == "" && intent.SourceBucketID != b.ID) || (t.copySource.BucketID != "" && (intent.SourceBucketID != t.copySource.BucketID || intent.SourceKey != t.copySource.Key)) {
			return ObjectBucketMutation{}, ErrConflict
		}
		value := *intent
		d.copyIntent = &value
	}
	d.dispatched = true
	m.objectMultipartPartWriters[key] = d
	if m.objectMutations == nil {
		m.objectMutations = map[string]ObjectBucketMutation{}
	}
	m.objectMutations[d.receipt.ID] = d.receipt
	return d.receipt, nil
}

func (m *MemStore) FinishObjectMultipartPartMutation(ctx context.Context, r ObjectBucketMutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	actual, ok := m.objectMutations[r.ID]
	if !ok || r.ID == "" || r.MultipartPartWriterID != r.ID || r.UploadID != "" || r.MultipartUploadID != "" || actual.MultipartPartWriterID != r.ID || r.Kind != ObjectBucketMutationRequest || !sameObjectMutationBucket(r.Bucket, actual.Bucket) || !sameObjectMutationBucket(r.Bucket, m.objectBuckets[r.Bucket.ID]) {
		return ErrConflict
	}
	for key, d := range m.objectMultipartPartWriters {
		if d.receipt.ID != r.ID {
			continue
		}
		t := m.objectMultipartTransfers[key.upload][key.part]
		if !d.dispatched || d.settled || t.token != key.token {
			return ErrConflict
		}
		d.settled = true
		m.objectMultipartPartWriters[key] = d
		m.settleMultipartPartLocked(key.upload, key.part)
		delete(m.objectMutations, r.ID)
		return nil
	}
	return ErrConflict
}

func (m *MemStore) ReadObjectMultipartPartCopyIntent(ctx context.Context, r ObjectBucketMutation) (ObjectMultipartPartCopyIntent, error) {
	if err := ctx.Err(); err != nil {
		return ObjectMultipartPartCopyIntent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == "" || r.ID != r.MultipartPartWriterID || r.Kind != ObjectBucketMutationRequest || r.UploadID != "" || r.MultipartUploadID != "" || !sameObjectMutationBucket(r.Bucket, m.objectBuckets[r.Bucket.ID]) {
		return ObjectMultipartPartCopyIntent{}, ErrConflict
	}
	for _, d := range m.objectMultipartPartWriters {
		if d.receipt.ID == r.ID && sameObjectMutationBucket(r.Bucket, d.receipt.Bucket) && d.dispatched && d.copyIntent != nil {
			return *d.copyIntent, nil
		}
	}
	return ObjectMultipartPartCopyIntent{}, ErrConflict
}
