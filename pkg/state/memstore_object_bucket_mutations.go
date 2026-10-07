package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

var _ ObjectBucketWriteFenceStore = (*MemStore)(nil)

func (m *MemStore) BeginObjectBucketMutation(ctx context.Context, b ObjectBucket, kind string) (ObjectBucketMutation, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationKind(kind) {
		return ObjectBucketMutation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketMutation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !sameObjectMutationBucket(b, m.objectBuckets[b.ID]) {
		return ObjectBucketMutation{}, ErrConflict
	}
	if _, fenced := m.objectWriteFences[b.ID]; fenced {
		return ObjectBucketMutation{}, ErrObjectBucketWriteFenced
	}
	if m.objectMutations == nil {
		m.objectMutations = map[string]ObjectBucketMutation{}
	}
	out := ObjectBucketMutation{ID: uuid.NewString(), Bucket: m.objectBuckets[b.ID], Kind: kind, CreatedAt: time.Now().UTC()}
	m.objectMutations[out.ID] = out
	return out, nil
}

func (m *MemStore) FinishObjectBucketMutation(ctx context.Context, receipt ObjectBucketMutation) error {
	if !validObjectMutationBucket(receipt.Bucket) || !validObjectMutationToken(receipt.ID) || receipt.Kind != ObjectBucketMutationRequest {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	actual, exists := m.objectMutations[receipt.ID]
	if !exists || actual.UploadID != "" || actual.MultipartUploadID != "" || actual.MultipartPartWriterID != "" || actual.Kind != receipt.Kind || !sameObjectMutationBucket(receipt.Bucket, actual.Bucket) || !sameObjectMutationBucket(receipt.Bucket, m.objectBuckets[receipt.Bucket.ID]) {
		return ErrConflict
	}
	delete(m.objectMutations, receipt.ID)
	return nil
}

func (m *MemStore) objectWriteFenceLocked(b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	return m.ownedObjectWriteFenceLocked(b, token, "")
}

func (m *MemStore) ownedObjectWriteFenceLocked(b ObjectBucket, token, operationID string) (ObjectBucketWriteFence, error) {
	if !sameObjectMutationBucket(b, m.objectBuckets[b.ID]) {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	fence, exists := m.objectWriteFences[b.ID]
	if !exists || fence.Token != token || fence.CloneOperationID != operationID || !sameObjectMutationBucket(b, fence.Bucket) {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	fence.Requests, fence.NativeGrants, fence.Deletions, fence.Protections, fence.Uploads, fence.Multipart = 0, 0, 0, 0, 0, 0
	for _, receipt := range m.objectMutations {
		if receipt.Bucket.ID != b.ID {
			continue
		}
		if receipt.Kind == ObjectBucketMutationNativeGrant {
			fence.NativeGrants++
		} else {
			fence.Requests++
		}
	}
	for _, d := range m.objectMultipartPartWriters {
		if d.receipt.Bucket.ID == b.ID && d.dispatched && !d.settled {
			if _, bound := m.objectMutations[d.receipt.ID]; !bound {
				fence.Requests++
			}
		}
	}
	for _, deletion := range m.objectDeletions {
		if deletion.BucketID == b.ID && deletionActive(deletion) {
			fence.Deletions++
		}
	}
	for _, protection := range m.objectVersionProtection {
		if protection.BucketID == b.ID && protectionActive(protection) {
			fence.Protections++
		}
	}
	for _, upload := range m.objectUploadCompletions {
		if upload.BucketID == b.ID && (upload.Status == "pending" || (upload.WritePhase == "" || upload.WritePhase == "untracked") && upload.Status == "failed") {
			fence.Uploads++
		}
	}
	for _, upload := range m.objectMultipartUploads {
		if upload.BucketID == b.ID && objectMultipartLive(upload.State) {
			fence.Multipart++
		}
	}
	return fence, nil
}

func (m *MemStore) AcquireObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(token) {
		return ObjectBucketWriteFence{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !sameObjectMutationBucket(b, m.objectBuckets[b.ID]) {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	if existing, ok := m.objectWriteFences[b.ID]; ok && existing.Token != token {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	if m.objectWriteFences == nil {
		m.objectWriteFences = map[string]ObjectBucketWriteFence{}
	}
	if _, exists := m.objectWriteFences[b.ID]; !exists {
		m.objectWriteFences[b.ID] = ObjectBucketWriteFence{Bucket: m.objectBuckets[b.ID], BucketID: b.ID, Token: token}
	}
	return m.objectWriteFenceLocked(b, token)
}

func (m *MemStore) ReadObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(token) {
		return ObjectBucketWriteFence{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objectWriteFenceLocked(b, token)
}

func (m *MemStore) ReleaseObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) error {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(token) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.objectWriteFenceLocked(b, token); err != nil {
		return err
	}
	delete(m.objectWriteFences, b.ID)
	return nil
}
