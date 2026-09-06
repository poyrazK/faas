package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartUploadStore = (*MemStore)(nil)

func objectMultipartLive(state string) bool {
	return state == ObjectMultipartInitiating || state == ObjectMultipartActive || state == ObjectMultipartCompleting || state == ObjectMultipartAborting
}

func (m *MemStore) ReserveObjectMultipartUpload(_ context.Context, upload ObjectMultipartUpload, limit int) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.objectBuckets[upload.BucketID]
	if !ok || bucket.AccountID != upload.AccountID || bucket.AppID != upload.AppID || bucket.State != "ready" || upload.ID == "" || upload.Key == "" || upload.SizeBytes <= 0 || upload.PartSizeBytes <= 0 || upload.PartCount < 1 || upload.ExpiresAt.IsZero() || limit < 1 {
		return ObjectMultipartUpload{}, ErrConflict
	}
	count := 0
	for _, old := range m.objectMultipartUploads {
		if old.BucketID != upload.BucketID || !objectMultipartLive(old.State) {
			continue
		}
		if old.Key == upload.Key {
			if old.SizeBytes != upload.SizeBytes || old.ContentType != upload.ContentType {
				return ObjectMultipartUpload{}, ErrConflict
			}
			old.Parts = cloneMultipartParts(old.Parts)
			return old, nil
		}
		count++
	}
	if count >= limit {
		return ObjectMultipartUpload{}, ErrConflict
	}
	now := time.Now().UTC()
	upload.State, upload.CreatedAt, upload.UpdatedAt, upload.RetryAt = ObjectMultipartInitiating, now, now, now
	upload.Parts = []api.ObjectMultipartCompletedPart{}
	if m.objectMultipartUploads == nil {
		m.objectMultipartUploads = map[string]ObjectMultipartUpload{}
	}
	m.objectMultipartUploads[upload.ID] = upload
	return upload, nil
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
		rows = append(rows, upload)
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
	return upload, nil
}

func (m *MemStore) ClaimObjectMultipartUpload(_ context.Context, account, app, bucket, id, token, operation string, parts []api.ObjectMultipartCompletedPart, recovery bool) (ObjectMultipartUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	now := time.Now()
	if !ok || upload.AccountID != account || upload.AppID != app || upload.BucketID != bucket {
		return ObjectMultipartUpload{}, ErrNotFound
	}
	if token == "" || !validObjectMultipartOperation(operation) || upload.LeaseUntil.After(now) || upload.State == operation && upload.RetryAt.After(now) {
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
	case ObjectMultipartCompleting:
		if oldState != ObjectMultipartActive && oldState != ObjectMultipartCompleting || oldState == ObjectMultipartActive && !upload.ExpiresAt.After(now) {
			return ObjectMultipartUpload{}, ErrConflict
		}
		if oldState == ObjectMultipartActive {
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
	return upload, nil
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
	upload.UpdatedAt, upload.RetryAt = time.Now().UTC(), time.Now().UTC()
	m.objectMultipartUploads[id] = upload
	return nil
}

func (m *MemStore) FinishObjectMultipartUpload(_ context.Context, id, token, next string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	upload, ok := m.objectMultipartUploads[id]
	valid := upload.State == ObjectMultipartCompleting && next == ObjectMultipartCompleted || upload.State == ObjectMultipartAborting && next == ObjectMultipartAborted
	if !ok || token == "" || upload.LeaseToken != token || !valid {
		return ErrConflict
	}
	upload.State, upload.LeaseToken, upload.LeaseUntil = next, "", time.Time{}
	upload.AttemptCount, upload.LastErrorCode = 0, ""
	upload.UpdatedAt, upload.RetryAt = time.Now().UTC(), time.Now().UTC()
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
	now := time.Now().UTC()
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
	now := time.Now()
	rows := make([]ObjectMultipartUpload, 0)
	for _, upload := range m.objectMultipartUploads {
		dueOperation := upload.State == ObjectMultipartInitiating || upload.State == ObjectMultipartCompleting || upload.State == ObjectMultipartAborting
		if (dueOperation && !upload.RetryAt.After(now) || upload.State == ObjectMultipartActive && !upload.ExpiresAt.After(now)) && !upload.LeaseUntil.After(now) {
			upload.Parts = cloneMultipartParts(upload.Parts)
			rows = append(rows, upload)
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
