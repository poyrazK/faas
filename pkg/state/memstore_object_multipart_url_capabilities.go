package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartURLCapabilityStore = (*MemStore)(nil)

func (m *MemStore) IssueObjectMultipartURLCredential(_ context.Context, c ObjectS3Credential, expected ObjectMultipartUpload, p api.ObjectStoragePolicy) (ObjectS3Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.objectMultipartUploads[expected.ID]
	if !validObjectURLMultipartUpload(c, u, m.clock()) || u.AppID != expected.AppID || u.ProviderUploadID != expected.ProviderUploadID || !u.Protection.Equal(expected.Protection) || u.EncryptionDefaultRevision != expected.EncryptionDefaultRevision || !u.Encryption.Equal(expected.Encryption) || !m.objectURLCredentialLiveLocked(c) {
		return ObjectS3Credential{}, ErrConflict
	}
	if _, fenced := m.objectWriteFences[c.BucketID]; fenced {
		return ObjectS3Credential{}, ErrObjectBucketWriteFenced
	}
	c, err := m.insertObjectURLCredentialLocked(c)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	if err = m.admitObjectURLLocked(c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, ""); err != nil {
		delete(m.objectS3Credentials, c.ID)
		return ObjectS3Credential{}, err
	}
	return cloneObjectS3Credential(c), nil
}

func (m *MemStore) BeginObjectURLMultipartPart(_ context.Context, id, token string, p api.ObjectStoragePolicy) error {
	if token == "" || len(token) > 128 {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.objectS3Credentials[id]
	if c.URL == nil || c.URL.Multipart == nil || !m.objectURLCredentialLiveLocked(c) {
		return ErrConflict
	}
	part := c.URL.Multipart
	u := m.objectMultipartUploads[part.UploadID]
	now := m.clock()
	if !validObjectURLMultipartUpload(c, u, now) {
		return ErrConflict
	}
	if _, held := m.objectWriteFences[u.BucketID]; held {
		return ErrObjectBucketWriteFenced
	}
	old := m.objectMultipartTransfers[u.ID][part.PartNumber]
	_, reused := m.objectMultipartPartWriters[multipartPartWriterKey{u.ID, part.PartNumber, token}]
	if reused || m.multipartPartWriterPendingLocked(u.ID, part.PartNumber) || old.token != "" && old.unsafeUntil.After(now) {
		return ErrConflict
	}
	key := objectProviderRequestKey(u.BucketID, time.Now().UTC())
	if m.objectProviderRequests[key] >= api.MaxObjectStoragePolicyValue {
		return ErrConflict
	}
	if err := m.admitObjectURLLocked(c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, ""); err != nil {
		return err
	}
	if m.objectMultipartTransfers == nil {
		m.objectMultipartTransfers = map[string]map[int32]multipartPartTransfer{}
	}
	if m.objectMultipartTransfers[u.ID] == nil {
		m.objectMultipartTransfers[u.ID] = map[int32]multipartPartTransfer{}
	}
	m.objectMultipartTransfers[u.ID][part.PartNumber] = multipartPartTransfer{token: token, unsafeUntil: now.Add(multipartTransferWindow()), tracked: true}
	m.reserveMultipartPartWriterLocked(u.ID, part.PartNumber, token)
	u.PartRevision++
	u.UpdatedAt = now.UTC()
	m.objectMultipartUploads[u.ID] = u
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	m.objectProviderRequests[key]++
	return nil
}
