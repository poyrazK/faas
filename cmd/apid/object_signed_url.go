package main

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) issueSignedBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account, req objectstorage.SignRequest) {
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, _, _, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	req, err := objectstorage.NormalizePublicSignRequest(req, s.objectStorage.MaxSinglePutBytes)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	permission := state.ObjectBucketPermissionRead
	if req.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !s.authorizeBucketData(w, r, b, permission) {
		return
	}
	store, ok := s.store.(state.ObjectURLCapabilityStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	c, receipt, secret, err := s.prepareObjectURLCredential(r, b, req, permission)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	now := c.URL.ExpiresAt.Add(-time.Duration(c.URL.Request.ExpiresIn) * time.Second)
	out, err := objectstorage.PresignPublicObject(r.Context(), s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, b.Name, c.AccessKeyID, secret, objectstorage.SignRequest(c.URL.Request), now)
	if err == nil {
		_, receipt, err = store.IssueObjectURLCredential(r.Context(), c, receipt, s.objectStorage.Accounting)
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	out.UploadID = receipt.ID
	writeJSON(w, http.StatusOK, out)
}

func (s *server) prepareObjectURLCredential(r *http.Request, b state.ObjectBucket, req objectstorage.SignRequest, permission string) (state.ObjectS3Credential, state.ObjectUploadCompletion, string, error) {
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, Label: "signed-url", Permission: permission, Status: state.ObjectS3CredentialStatusActive}
	receipt := state.ObjectUploadCompletion{}
	if setSecretRecipient == nil {
		return c, receipt, "", objectstorage.ErrUnavailable
	}
	to := setSecretRecipient()
	if to == nil {
		return c, receipt, "", objectstorage.ErrUnavailable
	}
	access, secret, err := api.GenerateObjectS3Credential()
	if err != nil {
		return c, receipt, "", objectstorage.ErrUnavailable
	}
	c.AccessKeyID = access
	c.KID = to.String()
	c.SecretSealed, err = secretbox.SealBytes(to, s3gateway.CredentialSecretNamespace, []byte(secret), 64)
	if err != nil {
		return c, receipt, "", objectstorage.ErrUnavailable
	}
	encryption, err := s.resolveObjectURLEncryption(b, req)
	if err != nil {
		return c, receipt, "", err
	}
	if !encryption.Empty() {
		selection := encryption.Clone().Selection
		req.Encryption = &selection
	}
	req, err = objectstorage.NormalizePublicSignRequest(req, s.objectStorage.MaxSinglePutBytes)
	if err != nil {
		return c, receipt, "", err
	}
	c.URL = &state.ObjectURLCapability{Request: api.ObjectSignRequest(req), ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(time.Duration(req.ExpiresIn) * time.Second)}
	if p, ok := principalFrom(r); ok && p.Key != nil {
		c.URL.APIKeyID = p.Key.ID
	}
	if req.Method == http.MethodPut {
		c.URL.ReceiptID = uuid.NewString()
		receipt = state.ObjectUploadCompletion{ID: c.URL.ReceiptID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: c.ID, Key: req.Key, Bytes: *req.SizeBytes, ContentType: req.ContentType, Status: "pending", Origin: "gateway", Encryption: encryption}
	}
	return c, receipt, secret, nil
}

func (s *server) resolveObjectURLEncryption(b state.ObjectBucket, req objectstorage.SignRequest) (state.ObjectEncryptionSnapshot, error) {
	if req.Encryption == nil {
		return state.ObjectEncryptionSnapshot{}, nil
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return state.ObjectEncryptionSnapshot{}, err
	}
	if _, ok := backend.Provider.(objectstorage.ObjectEncryptionProvider); !ok {
		return state.ObjectEncryptionSnapshot{}, objectstorage.ErrUnsupported
	}
	owner, err := uuid.Parse(b.AccountID)
	if err != nil {
		return state.ObjectEncryptionSnapshot{}, objectstorage.ErrConfiguration
	}
	return backend.Encryption.Resolve(owner.String(), *req.Encryption)
}
