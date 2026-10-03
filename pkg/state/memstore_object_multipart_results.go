package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartCompletionStore = (*MemStore)(nil)

func (m *MemStore) DispatchObjectMultipartCompletion(_ context.Context, u ObjectMultipartUpload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectMultipartUploads[u.ID]
	if !ok || !m.ownedMultipartResultLocked(old, u) {
		return ErrConflict
	}
	old.CompletionDispatched = true
	old.UpdatedAt = m.clock().UTC()
	m.objectMultipartUploads[u.ID] = old
	return nil
}

func (m *MemStore) FinishObjectMultipartCompletion(_ context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult) (ObjectMultipartUpload, error) {
	if !validMultipartFinalResult(u, result) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectMultipartUploads[u.ID]
	b, owned := m.objectBuckets[u.BucketID]
	if !ok || !owned || b.AccountID != u.AccountID || b.State != "ready" || !validMultipartResultOwner(old, u) || !old.CompletionDispatched {
		return ObjectMultipartUpload{}, ErrConflict
	}
	var version ObjectVersionIdentity
	if result.ProviderVersionID != "" {
		version = m.prepareObjectVersionLocked(u.BucketID, ObjectVersionIdentity{Key: u.Key, ProviderVersionID: result.ProviderVersionID})
		old.CompletionVersionID = publicPreparedVersionID(version)
	}
	old.CompletionETag, old.CompletionRecoveryCursor = result.ETag, ""
	old.CompletionVersionsObserved = old.CompletionVersionsObserved || result.VersionsObserved || result.ProviderVersionID != "" && result.ProviderVersionID != "null"
	old.State, old.LeaseToken, old.LeaseUntil = ObjectMultipartCompleted, "", time.Time{}
	old.AttemptCount, old.LastErrorCode = 0, ""
	old.UpdatedAt, old.RetryAt = m.clock().UTC(), m.clock().UTC()
	if err := m.publishObjectEventLocked(old.AccountID, "multipart:"+old.ID, api.ObjectEventCreated, multipartCompletionEvent(old), old.UpdatedAt); err != nil {
		return ObjectMultipartUpload{}, err
	}
	if version.ProviderVersionID != "" {
		m.commitObjectVersionLocked(old.BucketID, version)
	}
	m.objectMultipartUploads[u.ID] = old
	old.Parts, old.Metadata = cloneMultipartParts(old.Parts), cloneObjectMultipartMetadata(old.Metadata)
	return old, nil
}

func (m *MemStore) RetryObjectMultipartCompletion(_ context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult, code string, delay time.Duration) error {
	if !validMultipartRecoveryResult(result) || !validObjectMultipartRetry(code, delay) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectMultipartUploads[u.ID]
	if !ok || !m.ownedMultipartResultLocked(old, u) || !old.CompletionDispatched {
		return ErrConflict
	}
	old.CompletionRecoveryCursor = result.RecoveryCursor
	old.CompletionVersionsObserved = old.CompletionVersionsObserved || result.VersionsObserved
	old.LeaseToken, old.LeaseUntil = "", time.Time{}
	old.LastErrorCode, old.UpdatedAt, old.RetryAt = code, m.clock().UTC(), m.clock().UTC().Add(delay)
	m.objectMultipartUploads[u.ID] = old
	return nil
}

func (m *MemStore) RejectObjectMultipartCompletionResult(_ context.Context, u ObjectMultipartUpload, result ObjectMultipartCompletionResult, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectMultipartUploads[u.ID]
	if !ok || !m.ownedMultipartResultLocked(old, u) || !old.CompletionDispatched || old.State != ObjectMultipartCompletingConditional || !validMultipartCompletionFailure(code) {
		return ErrConflict
	}
	old.CompletionVersionsObserved = old.CompletionVersionsObserved || result.VersionsObserved
	old.CompletionRecoveryCursor = ""
	old.State, old.CompletionErrorCode = ObjectMultipartAborting, code
	old.LeaseToken, old.LeaseUntil = "", time.Time{}
	old.AttemptCount, old.LastErrorCode = 0, code
	old.UpdatedAt, old.RetryAt = m.clock().UTC(), m.clock().UTC()
	m.objectMultipartUploads[u.ID] = old
	return nil
}

func (m *MemStore) ownedMultipartResultLocked(old, u ObjectMultipartUpload) bool {
	b, ok := m.objectBuckets[u.BucketID]
	return ok && b.AccountID == u.AccountID && b.State == "ready" && validMultipartResultOwner(old, u)
}
