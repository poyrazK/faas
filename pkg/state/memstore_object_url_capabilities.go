package state

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectURLCapabilityStore = (*MemStore)(nil)

func (m *MemStore) DispatchObjectURLUpload(_ context.Context, account, bucket, id string) (ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectUploadCompletions[id]
	credential, exists := m.objectS3Credentials[c.SubjectID]
	if !ok || !exists || credential.URL == nil || c.AccountID != account || c.BucketID != bucket || c.WritePhase != ObjectUploadPrepared || !validObjectURLReceipt(credential, c) || !m.objectURLCredentialLiveLocked(credential) {
		return ObjectUploadCompletion{}, ErrConflict
	}
	key := objectProviderRequestKey(bucket, time.Now().UTC())
	if m.objectProviderRequests[key] >= api.MaxObjectStoragePolicyValue {
		return ObjectUploadCompletion{}, ErrConflict
	}
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	m.objectProviderRequests[key]++
	c.WritePhase = ObjectUploadDispatched
	c.RecoveryRetryAt = m.clock().Add(api.ObjectUploadRecoveryRetry)
	m.objectUploadCompletions[id] = c
	return cloneObjectUploadCompletion(c), nil
}

func (m *MemStore) objectURLCredentialLiveLocked(c ObjectS3Credential) bool {
	if c.URL == nil {
		return true
	}
	b, ok := m.objectBuckets[c.BucketID]
	if !ok || b.AccountID != c.AccountID || b.State != "ready" || !m.cloneBucketAccessibleLocked(b) {
		return false
	}
	if c.Status != ObjectS3CredentialStatusActive || !c.URL.ExpiresAt.After(m.clock()) {
		return false
	}
	if c.URL.APIKeyID == "" {
		return true
	}
	k, ok := m.keys[c.URL.APIKeyID]
	g, granted := m.objectAccessGrants[objectBucketAccessGrantKey(c.BucketID, c.URL.APIKeyID)]
	return ok && k.AccountID == c.AccountID && (k.Status == string(APIKeyStatusActive) || k.Status == string(APIKeyStatusGrace)) && (k.ExpiresAt == nil || k.ExpiresAt.After(m.clock())) && (apiKeyHasScope(k, "admin") || granted && g.AccountID == c.AccountID && apiKeyHasScope(k, "storage:"+c.Permission) && (g.Permission == c.Permission || g.Permission == ObjectBucketPermissionReadWrite))
}

func (m *MemStore) IssueObjectURLCredential(_ context.Context, c ObjectS3Credential, receipt ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectS3Credential, ObjectUploadCompletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if receipt.EncryptionDefaultRevision != 0 || !validObjectURLCredential(c, receipt, m.clock()) || !m.objectURLCredentialLiveLocked(c) {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrConflict
	}
	b, ok := m.objectBuckets[c.BucketID]
	if !ok || b.AccountID != c.AccountID || b.State != "ready" || c.URL.Request.Method == http.MethodPut && receipt.AppID != b.AppID {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrNotFound
	}
	if c.URL.Request.Method == http.MethodPut {
		if _, fenced := m.objectWriteFences[b.ID]; fenced {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrObjectBucketWriteFenced
		}
		var captureErr error
		receipt.Encryption, receipt.EncryptionDefaultRevision, captureErr = m.captureObjectBucketDefaultLocked(receipt.BucketID, receipt.Encryption)
		if captureErr != nil {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, captureErr
		}
		c = bindObjectURLDefault(c, receipt)
		if !validObjectURLReceipt(c, receipt) {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrConflict
		}
	}
	c, err := m.insertObjectURLCredentialLocked(c)
	if err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, err
	}
	if c.URL.Request.Method == http.MethodPut {
		receipt.Origin = "gateway"
		receipt, _, err = m.beginTrackedUploadLocked(receipt, p, false)
	} else {
		err = m.admitObjectURLLocked(c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, "")
	}
	if err != nil {
		delete(m.objectS3Credentials, c.ID)
		return ObjectS3Credential{}, ObjectUploadCompletion{}, err
	}
	return cloneObjectS3Credential(c), cloneObjectUploadCompletion(receipt), nil
}

func (m *MemStore) insertObjectURLCredentialLocked(c ObjectS3Credential) (ObjectS3Credential, error) {
	active := 0
	for id, old := range m.objectS3Credentials {
		if old.ID == c.ID || old.AccessKeyID == c.AccessKeyID {
			return ObjectS3Credential{}, ErrConflict
		}
		if old.BucketID != c.BucketID || old.URL == nil {
			continue
		}
		if old.Status == ObjectS3CredentialStatusActive && old.URL.ExpiresAt.After(m.clock()) {
			active++
		}
		if !old.URL.ExpiresAt.After(m.clock()) && (old.URL.ReceiptID == "" || m.objectUploadCompletions[old.URL.ReceiptID].WritePhase == ObjectUploadSettled) {
			delete(m.objectS3Credentials, id)
		}
	}
	if active >= api.MaxObjectURLCapabilitiesPerBucket {
		return ObjectS3Credential{}, objectURLCapabilityLimitError(int64(active))
	}
	c.CreatedAt = m.clock().UTC()
	c = cloneObjectS3Credential(c)
	m.objectS3Credentials[c.ID] = c
	return cloneObjectS3Credential(c), nil
}
