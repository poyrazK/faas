package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectTrackedUploadStore = (*MemStore)(nil)

func (m *MemStore) BeginTrackedObjectUpload(_ context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, bool, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedObjectUpload(c) {
		return c, false, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.IdempotencyKey != "" {
		for _, old := range m.objectUploadCompletions {
			if old.RouteID == c.RouteID && old.SubjectID == c.SubjectID && old.IdempotencyKey == c.IdempotencyKey && old.AccountID == c.AccountID && old.AppID == c.AppID {
				return cloneObjectUploadCompletion(old), false, nil
			}
		}
	}
	route, ok := m.objectUploadRoutes[c.RouteID]
	if !ok || route.AccountID != c.AccountID || route.AppID != c.AppID || route.BucketID != c.BucketID || !route.Enabled {
		return c, false, ErrNotFound
	}
	if c.Bytes > route.MaxBytes || !c.Encryption.Equal(route.Encryption) {
		return c, false, ErrConflict
	}
	c.Origin = "route"
	return m.beginTrackedUploadLocked(c, p, true)
}

func (m *MemStore) beginTrackedUploadLocked(c ObjectUploadCompletion, p api.ObjectStoragePolicy, capture bool) (ObjectUploadCompletion, bool, error) {
	if _, held := m.objectWriteFences[c.BucketID]; held {
		return c, false, ErrObjectBucketWriteFenced
	}
	if capture {
		var err error
		c.Encryption, c.EncryptionDefaultRevision, err = m.captureObjectBucketDefaultLocked(c.BucketID, c.Encryption)
		if err != nil {
			return c, false, err
		}
	}
	if capture {
		var protectionErr error
		c.Protection, protectionErr = m.captureObjectWriteProtectionLocked(c.BucketID, c.Protection)
		if protectionErr != nil {
			return c, false, protectionErr
		}
	}
	if !capturedDefaultRouteFits(c) {
		return c, false, capturedDefaultRouteError(c)
	}
	if credential, ok := m.objectS3Credentials[c.SubjectID]; ok && credential.URL != nil && !validObjectURLReceipt(credential, c) {
		return c, false, ErrConflict
	}
	if _, ok := m.objectUploadCompletions[c.ID]; ok {
		return c, false, ErrConflict
	}
	if _, ok := m.objectMutations[c.ID]; ok {
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
	m.objectWriteAdmissions[c.ID] = objectWriteAdmission{BucketID: c.BucketID, KeyHash: objectKeyHash(c.Key), Route: true, NativeVersion: m.objectUsage[c.BucketID].InventoryScope == ObjectInventoryAllVersions, NativeBytes: nativeGrantBytes(m.objectUsage[c.BucketID].InventoryScope == ObjectInventoryAllVersions, c.Bytes)}
	c.VersionID, c.ProviderVersionID = "", ""
	c.ETag = ""
	c.ErrorCode = ""
	c.RecoveryToken = ""
	c.RecoveryCursor = ""
	c.RecoveryVersionsObserved = false
	c.RecoveryLeaseUntil = time.Time{}
	c.RuntimeSinglePutLimit = 0
	c.CreatedAt = m.clock().UTC()
	c.WritePhase = ObjectUploadPrepared
	c.RecoveryRetryAt = c.CreatedAt.Add(api.ObjectUploadPreparationTimeout)
	if credential, ok := m.objectS3Credentials[c.SubjectID]; ok && credential.URL != nil {
		c.RecoveryRetryAt = credential.URL.ExpiresAt
	}
	if m.objectMutations == nil {
		m.objectMutations = map[string]ObjectBucketMutation{}
	}
	m.objectMutations[c.ID] = ObjectBucketMutation{ID: c.ID, UploadID: c.ID, Bucket: m.objectBuckets[c.BucketID], Kind: ObjectBucketMutationRequest, CreatedAt: c.CreatedAt}
	m.objectUploadCompletions[c.ID] = cloneObjectUploadCompletion(c)
	return cloneObjectUploadCompletion(c), true, nil
}
func (m *MemStore) DispatchTrackedObjectUpload(_ context.Context, account, bucket, id string) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	if !ok || c.AccountID != account || c.BucketID != bucket || c.WritePhase != ObjectUploadPrepared {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	if credential, exists := m.objectS3Credentials[c.SubjectID]; exists && credential.URL != nil && (!validObjectURLReceipt(credential, c) || !m.objectURLCredentialLiveLocked(credential)) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	if !m.validCopyReceiptAuthorityLocked(c) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	c.WritePhase = ObjectUploadDispatched
	c.RecoveryRetryAt = m.clock().Add(api.ObjectUploadRecoveryRetry)
	m.objectUploadCompletions[id] = c
	return cloneObjectUploadCompletion(c), nil
}
func (m *MemStore) finishTrackedObjectUploadLocked(old, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !validTrackedEncryptionResult(old, c) {
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	if old.WritePhase == ObjectUploadSettled {
		if old.Status == c.Status {
			return cloneObjectUploadCompletion(old), nil
		}
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	if old.WritePhase != ObjectUploadPrepared && old.WritePhase != ObjectUploadDispatched || c.Status == "completed" && old.WritePhase != ObjectUploadDispatched {
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	w, ok := m.objectWriteAdmissions[old.ID]
	if !ok || !w.Route || w.BucketID != old.BucketID {
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	var version ObjectVersionIdentity
	if c.Status == "completed" && c.ProviderVersionID != "" {
		version = m.prepareObjectVersionLocked(old.BucketID, ObjectVersionIdentity{Key: old.Key, ProviderVersionID: c.ProviderVersionID})
		old.VersionID = publicPreparedVersionID(version)
	}
	old.RecoveryCursor = ""
	old.RecoveryVersionsObserved = old.RecoveryVersionsObserved || c.RecoveryVersionsObserved || c.ProviderVersionID != "" && c.ProviderVersionID != "null"
	old.ETag = c.ETag
	old.Status = c.Status
	old.ErrorCode = c.ErrorCode
	old.WritePhase = ObjectUploadSettled
	old.RecoveryToken = ""
	old.RecoveryLeaseUntil = time.Time{}
	if old.Status == "completed" {
		if err := m.publishObjectEventLocked(old.AccountID, "write:"+old.ID, api.ObjectEventCreated, trackedUploadEvent(old), m.clock()); err != nil {
			return cloneObjectUploadCompletion(old), err
		}
	}
	if version.ProviderVersionID != "" {
		m.commitObjectVersionLocked(old.BucketID, version)
	}
	w.Settled = true
	m.objectWriteAdmissions[old.ID] = w
	m.objectUploadCompletions[old.ID] = old
	if receipt, exists := m.objectMutations[old.ID]; exists && receipt.UploadID == old.ID {
		delete(m.objectMutations, old.ID)
	}
	return cloneObjectUploadCompletion(old), nil
}
func (m *MemStore) FinishTrackedObjectUpload(_ context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return m.finishTrackedObjectUpload(c, false, false)
}
func (m *MemStore) FinishTrackedObjectUploadRecovery(_ context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return m.finishTrackedObjectUpload(c, true, false)
}
func (m *MemStore) FailPreparedObjectUpload(_ context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return m.finishTrackedObjectUpload(c, false, true)
}
func (m *MemStore) finishTrackedObjectUpload(c ObjectUploadCompletion, recovery, preparedOnly bool) (ObjectUploadCompletion, error) {
	if !validTrackedUploadFinish(c) || !validTrackedUploadCursor(c) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectUploadCompletions[c.ID]
	if !ok || old.AccountID != c.AccountID || old.BucketID != c.BucketID {
		return cloneObjectUploadCompletion(old), ErrNotFound
	}
	if preparedOnly && (c.Status != "failed" || old.WritePhase != ObjectUploadPrepared && old.Status != "failed") {
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	if recovery && (!validTrackedUploadRecovery(old, m.clock()) || old.RecoveryToken != c.RecoveryToken || c.Status != "completed") {
		return cloneObjectUploadCompletion(old), ErrConflict
	}
	return m.finishTrackedObjectUploadLocked(old, c)
}
func (m *MemStore) DueTrackedObjectUploads(_ context.Context, limit int32) ([]ObjectUploadCompletion, error) {
	if limit < 1 || limit > api.ObjectUploadRecoveryBatch {
		return nil, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	out := []ObjectUploadCompletion{}
	for _, c := range m.objectUploadCompletions {
		if (c.WritePhase == ObjectUploadPrepared || c.WritePhase == ObjectUploadDispatched) && !c.RecoveryRetryAt.After(now) && !c.RecoveryLeaseUntil.After(now) {
			out = append(out, cloneObjectUploadCompletion(c))
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
	now := m.clock()
	if !ok || c.AccountID != account || c.BucketID != bucket {
		return cloneObjectUploadCompletion(c), ErrNotFound
	}
	if token == "" || c.Status != "pending" || c.WritePhase != "prepared" && c.WritePhase != "dispatched" || c.RecoveryRetryAt.After(now) || c.RecoveryLeaseUntil.After(now) {
		return cloneObjectUploadCompletion(c), ErrConflict
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
	return cloneObjectUploadCompletion(c), nil
}
func (m *MemStore) RetryTrackedObjectUploadRecovery(_ context.Context, c ObjectUploadCompletion, code string) error {
	if !validTrackedUploadRetry(code) || !validTrackedUploadCursor(c) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.objectUploadCompletions[c.ID]
	if !ok || old.AccountID != c.AccountID || old.BucketID != c.BucketID {
		return ErrNotFound
	}
	if !old.Protection.Equal(c.Protection) || !sameCopySourceProvenance(old, c) || old.EncryptionDefaultRevision != c.EncryptionDefaultRevision || !old.Encryption.Equal(c.Encryption) || !validTrackedUploadRecovery(old, m.clock()) || old.RecoveryToken != c.RecoveryToken {
		return ErrConflict
	}
	old.RecoveryToken = ""
	old.RecoveryLeaseUntil = time.Time{}
	old.RecoveryRetryAt = m.clock().Add(api.ObjectUploadRecoveryRetry)
	old.ErrorCode = code
	old.RecoveryCursor = c.RecoveryCursor
	old.RecoveryVersionsObserved = old.RecoveryVersionsObserved || c.RecoveryVersionsObserved
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
	return cloneObjectUploadCompletion(c), nil
}

var _ ObjectTrackedGatewayUploadStore = (*MemStore)(nil)

func (m *MemStore) BeginTrackedGatewayUpload(_ context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedGatewayUpload(c) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	c.Origin = "gateway"
	return m.beginTrackedGatewayWrite(c, p)
}

var _ ObjectTrackedGatewayCopyStore = (*MemStore)(nil)

func (m *MemStore) BeginTrackedGatewayCopy(_ context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedGatewayCopy(c) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	c.Origin = "gateway_copy"
	return m.beginTrackedGatewayWrite(c, p)
}

func (m *MemStore) beginTrackedGatewayWrite(c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[c.BucketID]
	if !ok || b.AccountID != c.AccountID || b.AppID != c.AppID {
		return cloneObjectUploadCompletion(c), ErrNotFound
	}
	if b.State != "ready" {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	if !m.validCopyReceiptAuthorityLocked(c) {
		return cloneObjectUploadCompletion(c), ErrConflict
	}
	out, _, err := m.beginTrackedUploadLocked(c, p, true)
	return out, err
}

func (m *MemStore) validCopyReceiptAuthorityLocked(c ObjectUploadCompletion) bool {
	if emptyCopySourceProvenance(c) {
		return true
	}
	g, _, err := m.resolveObjectS3CopySourceLocked(c.AccountID, c.SubjectID, c.SourceBucketID, c.SourceKey)
	return err == nil && g.ID == c.SourceCopyGrantID && g.BucketID == c.BucketID
}
