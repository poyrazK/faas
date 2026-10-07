package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartTransferStore = (*MemStore)(nil)

func (m *MemStore) BeginObjectMultipartPart(_ context.Context, account, bucket, id, token string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	if token == "" || len(token) > 128 {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.admitMultipartPartLocked(account, bucket, id, token, part, size, maxObject, p)
}

func (m *MemStore) SettleObjectMultipartPart(_ context.Context, account, id string, part int32, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	transfer := m.objectMultipartTransfers[id][part]
	if u.AccountID != account || token == "" || transfer.token != token || m.multipartPartWriterPendingLocked(id, part) {
		return ErrConflict
	}
	m.settleMultipartPartLocked(id, part)
	return nil
}

func (m *MemStore) settleMultipartPartLocked(id string, part int32) {
	transfer := m.objectMultipartTransfers[id][part]
	key := multipartPartWriterKey{id, part, transfer.token}
	if d, ok := m.objectMultipartPartWriters[key]; ok {
		d.settled = true
		m.objectMultipartPartWriters[key] = d
	}
	transfer.token, transfer.unsafeUntil = "", time.Time{}
	m.objectMultipartTransfers[id][part] = transfer
}

func (m *MemStore) multipartTransfersPendingLocked(id string) bool {
	if m.multipartPartWriterPendingLocked(id, 0) {
		return true
	}
	for _, transfer := range m.objectMultipartTransfers[id] {
		if transfer.token != "" && transfer.unsafeUntil.After(m.clock()) {
			return true
		}
	}
	return false
}

func (m *MemStore) PrepareObjectMultipartCompletion(_ context.Context, u ObjectMultipartUpload, token string, size int64, parts []api.ObjectMultipartCompletedPart, p api.ObjectStoragePolicy) (ObjectMultipartUpload, error) {
	if !u.CompletionConditions.Valid() || token == "" || len(token) > 128 || size < 1 || size > api.MaxObjectUploadBytes || len(parts) < 1 || len(parts) > api.MaxMultipartParts {
		return ObjectMultipartUpload{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.objectMultipartUploads[u.ID]
	if !ok || !current.Protection.Equal(u.Protection) || current.EncryptionDefaultRevision != u.EncryptionDefaultRevision || !current.Encryption.Equal(u.Encryption) || current.PartRevision != u.PartRevision || m.multipartTransfersPendingLocked(u.ID) || ObjectMultipartIsCompleting(current.State) && (current.CompletionConditions != u.CompletionConditions || current.SizeBytes != size) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	// Validate the claim before changing the capacity ledger.
	claimed, err := m.claimObjectMultipartLocked(u.AccountID, u.AppID, u.BucketID, u.ID, token, multipartCompletionOperation(u.CompletionConditions), parts, false, u.CompletionConditions)
	if err != nil {
		return ObjectMultipartUpload{}, err
	}
	if err = m.admitMultipartCompletionLocked(u.AccountID, u.BucketID, u.ID, u.Key, size, p, uuid.NewString()); err != nil {
		m.objectMultipartUploads[u.ID] = current
		return ObjectMultipartUpload{}, err
	}
	claimed.SizeBytes = size
	m.objectMultipartUploads[u.ID] = claimed
	return cloneObjectMultipartUpload(claimed), nil
}

func (m *MemStore) ObjectMultipartAbortReady(_ context.Context, id, token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	if token == "" || u.LeaseToken != token || u.State != ObjectMultipartAborting {
		return false, ErrConflict
	}
	return !u.PartURLUnsafeUntil.After(m.clock()) && !m.multipartTransfersPendingLocked(id), nil
}

func (m *MemStore) FinishVerifiedObjectMultipartAbort(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	if token == "" || u.LeaseToken != token || u.State != ObjectMultipartAborting || u.PartURLUnsafeUntil.After(m.clock()) || m.multipartTransfersPendingLocked(id) {
		return ErrConflict
	}
	u.State, u.LeaseToken, u.LeaseUntil = ObjectMultipartAborted, "", time.Time{}
	u.AttemptCount, u.LastErrorCode = 0, ""
	u.UpdatedAt, u.RetryAt = m.clock().UTC(), m.clock().UTC()
	m.objectMultipartUploads[id] = u
	m.retireMultipartMutationLocked(id)
	for part, transfer := range m.objectMultipartTransfers[id] {
		m.settleMultipartPartLocked(id, part)
		if transfer.tracked {
			delete(m.objectMultipartPartGrants[id], part)
			delete(m.objectMultipartTransfers[id], part)
		} else {
			transfer.token, transfer.unsafeUntil = "", time.Time{}
			m.objectMultipartTransfers[id][part] = transfer
		}
	}
	return nil
}

func (m *MemStore) RejectObjectMultipartCompletion(_ context.Context, id, token, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	if token == "" || u.LeaseToken != token || u.State != ObjectMultipartCompletingConditional || !validMultipartCompletionFailure(code) {
		return ErrConflict
	}
	u.State, u.CompletionErrorCode = ObjectMultipartAborting, code
	u.LeaseToken, u.LeaseUntil = "", time.Time{}
	u.AttemptCount, u.LastErrorCode = 0, code
	u.UpdatedAt, u.RetryAt = m.clock().UTC(), m.clock().UTC()
	m.objectMultipartUploads[id] = u
	return nil
}

var _ ObjectCrossBucketMultipartStore = (*MemStore)(nil)

func (m *MemStore) BeginObjectCrossBucketMultipartPart(_ context.Context, account, bucket, id, token string, part int32, size, maxObject int64, p api.ObjectStoragePolicy, source ObjectMultipartCopySource) error {
	if token == "" || len(token) > 128 || !validMultipartCopyAuthority(source, bucket) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, _, err := m.resolveObjectS3CopySourceLocked(account, source.SubjectID, source.BucketID, source.Key)
	if err != nil || g.ID != source.GrantID || g.BucketID != bucket {
		return ErrConflict
	}
	if err = m.admitMultipartPartLocked(account, bucket, id, token, part, size, maxObject, p); err != nil {
		return err
	}
	transfer := m.objectMultipartTransfers[id][part]
	transfer.copySource = source
	m.objectMultipartTransfers[id][part] = transfer
	return nil
}
