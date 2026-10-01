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
	if u.AccountID != account || token == "" || transfer.token != token {
		return ErrConflict
	}
	transfer.token, transfer.unsafeUntil = "", time.Time{}
	m.objectMultipartTransfers[id][part] = transfer
	return nil
}

func (m *MemStore) multipartTransfersPendingLocked(id string) bool {
	for _, transfer := range m.objectMultipartTransfers[id] {
		if transfer.token != "" && transfer.unsafeUntil.After(time.Now()) {
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
	if !ok || current.PartRevision != u.PartRevision || m.multipartTransfersPendingLocked(u.ID) || ObjectMultipartIsCompleting(current.State) && (current.CompletionConditions != u.CompletionConditions || current.SizeBytes != size) {
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
	return claimed, nil
}

func (m *MemStore) ObjectMultipartAbortReady(_ context.Context, id, token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	if token == "" || u.LeaseToken != token || u.State != ObjectMultipartAborting {
		return false, ErrConflict
	}
	return !m.multipartTransfersPendingLocked(id), nil
}

func (m *MemStore) FinishVerifiedObjectMultipartAbort(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[id]
	if token == "" || u.LeaseToken != token || u.State != ObjectMultipartAborting || m.multipartTransfersPendingLocked(id) {
		return ErrConflict
	}
	u.State, u.LeaseToken, u.LeaseUntil = ObjectMultipartAborted, "", time.Time{}
	u.AttemptCount, u.LastErrorCode = 0, ""
	u.UpdatedAt, u.RetryAt = time.Now().UTC(), time.Now().UTC()
	m.objectMultipartUploads[id] = u
	for part, transfer := range m.objectMultipartTransfers[id] {
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
	u.UpdatedAt, u.RetryAt = time.Now().UTC(), time.Now().UTC()
	m.objectMultipartUploads[id] = u
	return nil
}
