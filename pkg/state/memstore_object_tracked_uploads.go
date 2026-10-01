package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectTrackedUploadStore = (*MemStore)(nil)

func (m *MemStore) BeginTrackedObjectUpload(_ context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, bool, error) {
	if !validTrackedObjectUpload(c) {
		return c, false, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.IdempotencyKey != "" {
		for _, old := range m.objectUploadCompletions {
			if old.RouteID == c.RouteID && old.SubjectID == c.SubjectID && old.IdempotencyKey == c.IdempotencyKey && old.AccountID == c.AccountID && old.AppID == c.AppID {
				return old, false, nil
			}
		}
	}
	route, ok := m.objectUploadRoutes[c.RouteID]
	if !ok || route.AccountID != c.AccountID || route.AppID != c.AppID || route.BucketID != c.BucketID || !route.Enabled {
		return c, false, ErrNotFound
	}
	if c.Bytes > route.MaxBytes {
		return c, false, ErrConflict
	}
	c.Origin = "route"
	return m.beginTrackedUploadLocked(c, p)
}

func (m *MemStore) beginTrackedUploadLocked(c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, bool, error) {
	if _, ok := m.objectUploadCompletions[c.ID]; ok {
		return c, false, ErrConflict
	}
	if _, ok := m.objectWriteAdmissions[c.ID]; ok {
		return c, false, ErrConflict
	}
	if err := m.admitObjectURLLocked(c.AccountID, c.BucketID, c.Key, c.Bytes, true, p, c.ID); err != nil {
		return c, false, err
	}
	if m.objectWriteAdmissions == nil {
		m.objectWriteAdmissions = map[string]objectWriteAdmission{}
	}
	m.objectWriteAdmissions[c.ID] = objectWriteAdmission{BucketID: c.BucketID, KeyHash: objectKeyHash(c.Key), Route: true}
	c.ETag = ""
	c.ErrorCode = ""
	c.RecoveryToken = ""
	c.RecoveryLeaseUntil = time.Time{}
	c.CreatedAt = time.Now().UTC()
	c.WritePhase = ObjectUploadPrepared
	c.RecoveryRetryAt = c.CreatedAt.Add(api.ObjectUploadPreparationTimeout)
	m.objectUploadCompletions[c.ID] = c
	return c, true, nil
}
func (m *MemStore) DispatchTrackedObjectUpload(_ context.Context, account, bucket, id string) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	if !ok || c.AccountID != account || c.BucketID != bucket || c.WritePhase != ObjectUploadPrepared {
		return c, ErrConflict
	}
	c.WritePhase = ObjectUploadDispatched
	c.RecoveryRetryAt = time.Now().Add(api.ObjectUploadRecoveryRetry)
	m.objectUploadCompletions[id] = c
	return c, nil
}
func (m *MemStore) finishTrackedObjectUploadLocked(old, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if old.WritePhase == ObjectUploadSettled {
		if old.Status == c.Status {
			return old, nil
		}
		return old, ErrConflict
	}
	if old.WritePhase != ObjectUploadPrepared && old.WritePhase != ObjectUploadDispatched || c.Status == "completed" && old.WritePhase != ObjectUploadDispatched {
		return old, ErrConflict
	}
	w, ok := m.objectWriteAdmissions[old.ID]
	if !ok || !w.Route || w.BucketID != old.BucketID {
		return old, ErrConflict
	}
	w.Settled = true
	m.objectWriteAdmissions[old.ID] = w
	old.ETag = c.ETag
	old.Status = c.Status
	old.ErrorCode = c.ErrorCode
	old.WritePhase = ObjectUploadSettled
	old.RecoveryToken = ""
	old.RecoveryLeaseUntil = time.Time{}
	m.objectUploadCompletions[old.ID] = old
	return old, nil
}
func (m *MemStore) FinishTrackedObjectUpload(_ context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return m.finishTrackedObjectUpload(c, false)
}
func (m *MemStore) FinishTrackedObjectUploadRecovery(_ context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return m.finishTrackedObjectUpload(c, true)
}
func (m *MemStore) finishTrackedObjectUpload(c ObjectUploadCompletion, recovery bool) (ObjectUploadCompletion, error) {
	if !validTrackedUploadFinish(c) {
		return c, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectUploadCompletions[c.ID]
	if !ok || old.AccountID != c.AccountID || old.BucketID != c.BucketID {
		return old, ErrNotFound
	}
	if recovery && (!validTrackedUploadRecovery(old, time.Now()) || old.RecoveryToken != c.RecoveryToken || c.Status != "completed") {
		return old, ErrConflict
	}
	return m.finishTrackedObjectUploadLocked(old, c)
}
func (m *MemStore) DueTrackedObjectUploads(_ context.Context, limit int32) ([]ObjectUploadCompletion, error) {
	if limit < 1 || limit > api.ObjectUploadRecoveryBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := []ObjectUploadCompletion{}
	for _, c := range m.objectUploadCompletions {
		if (c.WritePhase == ObjectUploadPrepared || c.WritePhase == ObjectUploadDispatched) && !c.RecoveryRetryAt.After(now) && !c.RecoveryLeaseUntil.After(now) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RecoveryRetryAt.Before(out[j].RecoveryRetryAt) || out[i].RecoveryRetryAt.Equal(out[j].RecoveryRetryAt) && out[i].ID < out[j].ID
	})
	return out[:min(int(limit), len(out))], nil
}
func (m *MemStore) ClaimTrackedObjectUploadRecovery(_ context.Context, account, bucket, id, token string) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	now := time.Now()
	if !ok || c.AccountID != account || c.BucketID != bucket {
		return c, ErrNotFound
	}
	if token == "" || c.Status != "pending" || c.WritePhase != "prepared" && c.WritePhase != "dispatched" || c.RecoveryRetryAt.After(now) || c.RecoveryLeaseUntil.After(now) {
		return c, ErrConflict
	}
	if c.WritePhase == ObjectUploadPrepared {
		done := c
		done.Status = "failed"
		done.ErrorCode = "preparation_expired"
		return m.finishTrackedObjectUploadLocked(c, done)
	}
	c.RecoveryToken = token
	c.RecoveryLeaseUntil = now.Add(api.ObjectUploadRecoveryLease)
	m.objectUploadCompletions[id] = c
	return c, nil
}
func (m *MemStore) RetryTrackedObjectUploadRecovery(_ context.Context, c ObjectUploadCompletion, code string) error {
	if !validTrackedUploadRetry(code) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectUploadCompletions[c.ID]
	if !ok || old.AccountID != c.AccountID || old.BucketID != c.BucketID {
		return ErrNotFound
	}
	if !validTrackedUploadRecovery(old, time.Now()) || old.RecoveryToken != c.RecoveryToken {
		return ErrConflict
	}
	old.RecoveryToken = ""
	old.RecoveryLeaseUntil = time.Time{}
	old.RecoveryRetryAt = time.Now().Add(api.ObjectUploadRecoveryRetry)
	old.ErrorCode = code
	m.objectUploadCompletions[old.ID] = old
	return nil
}

func (m *MemStore) GetObjectUploadReceipt(_ context.Context, account, app, route, subject, id string) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	if !ok || c.AccountID != account || c.AppID != app || c.RouteID != route || c.SubjectID != subject {
		return ObjectUploadCompletion{}, ErrNotFound
	}
	return c, nil
}

var _ ObjectTrackedGatewayUploadStore = (*MemStore)(nil)

func (m *MemStore) BeginTrackedGatewayUpload(_ context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	if !validTrackedGatewayUpload(c) {
		return c, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[c.BucketID]
	if !ok || b.AccountID != c.AccountID || b.AppID != c.AppID {
		return c, ErrNotFound
	}
	if b.State != "ready" {
		return c, ErrConflict
	}
	c.Origin = "gateway"
	out, _, err := m.beginTrackedUploadLocked(c, p)
	return out, err
}
