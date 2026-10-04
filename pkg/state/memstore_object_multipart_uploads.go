package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartUploadStore = (*MemStore)(nil)
var _ ObjectFixedMultipartAdmissionStore = (*MemStore)(nil)

func objectMultipartLive(state string) bool {
	return state == ObjectMultipartInitiating || state == ObjectMultipartActive || ObjectMultipartIsCompleting(state) || state == ObjectMultipartAborting
}

func (m *MemStore) ReserveObjectMultipartUpload(_ context.Context, upload ObjectMultipartUpload, limit int) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserveObjectMultipartLocked(upload, limit, nil)
}

func (m *MemStore) ReserveAdmittedObjectMultipartUpload(_ context.Context, upload ObjectMultipartUpload, limit int, policy api.ObjectStoragePolicy) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserveObjectMultipartLocked(upload, limit, &policy)
}

func (m *MemStore) reserveObjectMultipartLocked(upload ObjectMultipartUpload, limit int, policy *api.ObjectStoragePolicy) (ObjectMultipartUpload, error) {
	if m.objectCapacityFencedLocked(upload.BucketID) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	bucket, ok := m.objectBuckets[upload.BucketID]
	unknownSize := upload.SizeBytes == 0 && upload.PartSizeBytes == 0 && upload.PartCount == 0
	knownSize := upload.SizeBytes > 0 && upload.PartSizeBytes > 0 && upload.PartCount > 0
	if upload.EncryptionDefaultRevision != 0 || upload.FixedAdmission || policy != nil && !validFixedMultipartLayout(upload) || !ok || bucket.AccountID != upload.AccountID || bucket.AppID != upload.AppID || bucket.State != "ready" || !m.cloneBucketAccessibleLocked(bucket) || upload.ID == "" || upload.Key == "" || !unknownSize && !knownSize || upload.SizeBytes < 0 || upload.SizeBytes > api.MaxObjectUploadBytes || upload.PartSizeBytes < 0 || upload.PartSizeBytes > api.MaxObjectSinglePutBytes || upload.PartCount < 0 || upload.PartCount > api.MaxMultipartParts || upload.ExpiresAt.IsZero() || limit < 1 || !emptyInitialMultipartResult(upload) || !upload.Encryption.ValidFor(upload.AccountID) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	count := 0
	for _, old := range m.objectMultipartUploads {
		if old.BucketID != upload.BucketID || !objectMultipartLive(old.State) {
			continue
		}
		if old.Key == upload.Key {
			if old.SizeBytes != upload.SizeBytes || old.ContentType != upload.ContentType || !equalObjectMultipartMetadata(old.Metadata, upload.Metadata) || !sameMultipartEncryptionRequest(old, upload.Encryption) {
				return ObjectMultipartUpload{}, ErrConflict
			}
			old.Parts = cloneMultipartParts(old.Parts)
			old.Metadata = cloneObjectMultipartMetadata(old.Metadata)
			return cloneObjectMultipartUpload(old), nil
		}
		count++
	}
	if _, exists := m.objectMultipartUploads[upload.ID]; exists || count >= limit {
		return ObjectMultipartUpload{}, ErrConflict
	}
	var captureErr error
	upload.Encryption, upload.EncryptionDefaultRevision, captureErr = m.captureObjectBucketDefaultLocked(upload.BucketID, upload.Encryption)
	if captureErr != nil {
		return ObjectMultipartUpload{}, captureErr
	}
	if policy != nil {
		if err := m.admitObjectURLLocked(upload.AccountID, upload.BucketID, upload.Key, upload.SizeBytes, true, *policy, upload.ID); err != nil {
			return ObjectMultipartUpload{}, err
		}
		if m.objectWriteAdmissions == nil {
			m.objectWriteAdmissions = map[string]objectWriteAdmission{}
		}
		all := m.objectUsage[upload.BucketID].InventoryScope == ObjectInventoryAllVersions
		m.objectWriteAdmissions[upload.ID] = objectWriteAdmission{BucketID: upload.BucketID, KeyHash: objectKeyHash(upload.Key), MultipartID: upload.ID, NativeVersion: all, NativeBytes: nativeGrantBytes(all, upload.SizeBytes)}
		upload.FixedAdmission = true
	}
	now := m.clock().UTC()
	upload.State, upload.CreatedAt, upload.UpdatedAt, upload.RetryAt = ObjectMultipartInitiating, now, now, now
	upload.Parts = []api.ObjectMultipartCompletedPart{}
	upload.Metadata = cloneObjectMultipartMetadata(upload.Metadata)
	if m.objectMultipartUploads == nil {
		m.objectMultipartUploads = map[string]ObjectMultipartUpload{}
	}
	m.objectMultipartUploads[upload.ID] = cloneObjectMultipartUpload(upload)
	return cloneObjectMultipartUpload(upload), nil
}

func (m *MemStore) ListObjectMultipartUploads(_ context.Context, account, app, bucket string, limit int32, cursor string) ([]ObjectMultipartUpload, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ObjectMultipartUpload, 0)
	for _, upload := range m.objectMultipartUploads {
		if upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket || cursor != "" && upload.ID <= cursor {
			continue
		}
		upload.Parts = cloneMultipartParts(upload.Parts)
		upload.Metadata = cloneObjectMultipartMetadata(upload.Metadata)
		rows = append(rows, cloneObjectMultipartUpload(upload))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	next := ""
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = rows[len(rows)-1].ID
	}
	return rows, next, nil
}

func (m *MemStore) GetObjectMultipartUpload(_ context.Context, account, app, bucket, id string) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	if !ok || upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket {
		return ObjectMultipartUpload{}, ErrNotFound
	}
	upload.Parts = cloneMultipartParts(upload.Parts)
	upload.Metadata = cloneObjectMultipartMetadata(upload.Metadata)
	return cloneObjectMultipartUpload(upload), nil
}

func (m *MemStore) ClaimObjectMultipartUpload(_ context.Context, account, app, bucket, id, token, operation string, parts []api.ObjectMultipartCompletedPart, recovery bool) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claimObjectMultipartLocked(account, app, bucket, id, token, operation, parts, recovery, api.ObjectWriteConditions{})
}

func (m *MemStore) claimObjectMultipartLocked(account, app, bucket, id, token, operation string, parts []api.ObjectMultipartCompletedPart, recovery bool, conditions api.ObjectWriteConditions) (ObjectMultipartUpload, error) {
	upload, ok := m.objectMultipartUploads[id]
	now := m.clock()
	if !ok || upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket {
		return ObjectMultipartUpload{}, ErrNotFound
	}
	if token == "" || !upload.Encryption.Empty() && len(token) > api.MaxObjectEncryptionLeaseTokenBytes || !validObjectMultipartOperation(operation) || upload.LeaseUntil.After(now) || upload.State == operation && upload.RetryAt.After(now) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	if recovery && upload.State != operation && (upload.State != ObjectMultipartActive || operation != ObjectMultipartAborting) {
		return ObjectMultipartUpload{}, ErrConflict
	}
	oldState := upload.State
	switch operation {
	case ObjectMultipartInitiating:
		if oldState != ObjectMultipartInitiating || upload.ProviderUploadID != "" {
			return ObjectMultipartUpload{}, ErrConflict
		}
	case ObjectMultipartCompleting, ObjectMultipartCompletingConditional:
		if m.multipartTransfersPendingLocked(id) {
			return ObjectMultipartUpload{}, ErrConflict
		}
		if oldState != ObjectMultipartActive && oldState != operation || oldState == ObjectMultipartActive && !upload.ExpiresAt.After(now) {
			return ObjectMultipartUpload{}, ErrConflict
		}
		if oldState == ObjectMultipartActive {
			if !conditions.Valid() || operation != multipartCompletionOperation(conditions) || !conditions.Empty() && upload.PartCount != 0 {
				return ObjectMultipartUpload{}, ErrConflict
			}
			upload.CompletionConditions = conditions
			if len(parts) == 0 {
				return ObjectMultipartUpload{}, ErrConflict
			}
			upload.Parts = cloneMultipartParts(parts)
		} else if len(parts) != 0 && !slices.Equal(upload.Parts, parts) {
			return ObjectMultipartUpload{}, ErrConflict
		}
	case ObjectMultipartAborting:
		if oldState != ObjectMultipartActive && oldState != ObjectMultipartAborting || upload.ProviderUploadID == "" {
			return ObjectMultipartUpload{}, ErrConflict
		}
	}
	if oldState != operation {
		upload.AttemptCount, upload.LastErrorCode = 0, ""
	}
	if upload.AttemptCount < 30 {
		upload.AttemptCount++
	}
	upload.State, upload.LeaseToken = operation, token
	upload.UpdatedAt, upload.RetryAt = now.UTC(), now.UTC()
	upload.LeaseUntil = now.Add(ObjectMultipartLeaseDuration)
	m.objectMultipartUploads[id] = upload
	upload.Parts = cloneMultipartParts(upload.Parts)
	upload.Metadata = cloneObjectMultipartMetadata(upload.Metadata)
	return cloneObjectMultipartUpload(upload), nil
}

func (m *MemStore) ActivateObjectMultipartUpload(_ context.Context, id, token, providerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	if !ok || upload.State != ObjectMultipartInitiating || token == "" || upload.LeaseToken != token || providerID == "" {
		return ErrConflict
	}
	upload.State, upload.ProviderUploadID = ObjectMultipartActive, providerID
	upload.LeaseToken, upload.LeaseUntil = "", time.Time{}
	upload.AttemptCount, upload.LastErrorCode = 0, ""
	upload.UpdatedAt, upload.RetryAt = m.clock().UTC(), m.clock().UTC()
	m.objectMultipartUploads[id] = upload
	return nil
}

func (m *MemStore) SetObjectMultipartUploadSize(_ context.Context, id, token string, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	if !ok || !ObjectMultipartIsCompleting(upload.State) || upload.LeaseToken != token || size < 1 || size > api.MaxObjectUploadBytes || upload.FixedAdmission && size != upload.SizeBytes {
		return ErrConflict
	}
	upload.SizeBytes = size
	upload.UpdatedAt = m.clock().UTC()
	m.objectMultipartUploads[id] = upload
	return nil
}

func (m *MemStore) FinishObjectMultipartUpload(_ context.Context, id, token, next string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	valid := ObjectMultipartIsCompleting(upload.State) && next == ObjectMultipartCompleted
	if !ok || token == "" || upload.LeaseToken != token || !valid || next == ObjectMultipartCompleted && (upload.CompletionDispatched || !upload.Encryption.Empty()) {
		return ErrConflict
	}
	upload.State, upload.LeaseToken, upload.LeaseUntil = next, "", time.Time{}
	upload.AttemptCount, upload.LastErrorCode = 0, ""
	upload.UpdatedAt, upload.RetryAt = m.clock().UTC(), m.clock().UTC()
	m.objectMultipartUploads[id] = upload
	return nil
}

func (m *MemStore) RetryObjectMultipartUpload(_ context.Context, id, token, code string, delay time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	if !ok || token == "" || upload.LeaseToken != token || !validObjectMultipartOperation(upload.State) || !validObjectMultipartRetry(code, delay) {
		return ErrConflict
	}
	now := m.clock().UTC()
	upload.LeaseToken, upload.LeaseUntil = "", time.Time{}
	upload.LastErrorCode, upload.UpdatedAt, upload.RetryAt = code, now, now.Add(delay)
	m.objectMultipartUploads[id] = upload
	return nil
}

func (m *MemStore) DueObjectMultipartUploads(_ context.Context, limit int32) ([]ObjectMultipartUpload, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	rows := make([]ObjectMultipartUpload, 0)
	for _, upload := range m.objectMultipartUploads {
		dueOperation := upload.State == ObjectMultipartInitiating || ObjectMultipartIsCompleting(upload.State) || upload.State == ObjectMultipartAborting
		if (dueOperation && !upload.RetryAt.After(now) || upload.State == ObjectMultipartActive && !upload.ExpiresAt.After(now)) && !upload.LeaseUntil.After(now) {
			upload.Parts = cloneMultipartParts(upload.Parts)
			upload.Metadata = cloneObjectMultipartMetadata(upload.Metadata)
			rows = append(rows, cloneObjectMultipartUpload(upload))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RetryAt.Before(rows[j].RetryAt) || rows[i].RetryAt.Equal(rows[j].RetryAt) && rows[i].ID < rows[j].ID
	})
	if len(rows) > int(limit) {
		rows = rows[:limit]
	}
	return rows, nil
}
