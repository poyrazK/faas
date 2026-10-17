package object

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) issueSignedBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account, req objectstorage.SignRequest) {
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	req, err := objectstorage.NormalizePublicSignRequest(req, s.Host.ObjectStorage().MaxSinglePutBytes)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	permission := state.ObjectBucketPermissionRead
	if req.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !s.AuthorizeBucketData(w, r, b, permission) {
		return
	}
	if err := objectstorage.ValidateConditionalPut(provider, api.ObjectWriteConditions{IfMatch: req.IfMatch, IfNoneMatch: req.IfNoneMatch}); err != nil {
		BucketProblem(w, err)
		return
	}
	if req.VersionID != "" {
		if _, capable := provider.(objectstorage.VersionReadPresigner); !capable {
			BucketProblem(w, objectstorage.ErrUnsupported)
			return
		}
		refs, capable := s.Host.Store().(state.ObjectVersionReferenceStore)
		if !capable {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		if _, err := refs.ResolveObjectVersion(r.Context(), b.AccountID, b.ID, req.Key, req.VersionID); err != nil {
			BucketProblem(w, err)
			return
		}
	}
	store, ok := s.Host.Store().(state.ObjectURLCapabilityStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	c, receipt, secret, err := s.prepareObjectURLCredential(r, b, req, permission)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	c, receipt, err = store.IssueObjectURLCredential(r.Context(), c, receipt, s.Host.ObjectStorage().Accounting)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	now := c.URL.ExpiresAt.Add(-time.Duration(c.URL.Request.ExpiresIn) * time.Second)
	out, err := objectstorage.PresignPublicObject(r.Context(), s.Host.ObjectStorage().PublicEndpoint, s.Host.ObjectStorage().PublicRegion, b.Name, c.AccessKeyID, secret, objectstorage.SignRequest(c.URL.Request), now)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	out.UploadID = receipt.ID
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) prepareObjectURLCredential(r *http.Request, b state.ObjectBucket, req objectstorage.SignRequest, permission string) (state.ObjectS3Credential, state.ObjectUploadCompletion, string, error) {
	return s.prepareObjectURLCredentialWithLimit(r, b, req, permission, s.Host.ObjectStorage().MaxSinglePutBytes)
}

func (s *Service) prepareObjectURLCredentialWithLimit(r *http.Request, b state.ObjectBucket, req objectstorage.SignRequest, permission string, maxBytes int64) (state.ObjectS3Credential, state.ObjectUploadCompletion, string, error) {
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, Label: "signed-url", Permission: permission, Status: state.ObjectS3CredentialStatusActive}
	receipt := state.ObjectUploadCompletion{}
	to := s.Host.SecretRecipient()
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
	protection := state.ObjectWriteProtectionSnapshot{}
	if req.Method == http.MethodPut {
		protection, err = s.resolveObjectWriteProtection(r.Context(), b, req.Protection)
	}
	if err != nil {
		return c, receipt, "", err
	}
	encryption, err := s.resolveObjectURLEncryption(b, req)
	if err != nil {
		return c, receipt, "", err
	}
	if !encryption.Empty() {
		selection := encryption.Clone().Selection
		req.Encryption = &selection
	}
	req, err = objectstorage.NormalizePublicSignRequest(req, maxBytes)
	if err != nil {
		return c, receipt, "", err
	}
	c.URL = &state.ObjectURLCapability{Request: api.ObjectSignRequest(req), ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(time.Duration(req.ExpiresIn) * time.Second)}
	if p, ok := httpkit.PrincipalFrom(r); ok && p.Key != nil {
		c.URL.APIKeyID = p.Key.ID
	}
	if req.Method == http.MethodPut {
		enroll, err := s.objectNodeSettlementEnrollment(b)
		if err != nil {
			return c, receipt, "", err
		}
		c.URL.ReceiptID = objectstorage.NewWriteReceiptID()
		receipt = state.ObjectUploadCompletion{ID: c.URL.ReceiptID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: c.ID, Key: req.Key, Bytes: *req.SizeBytes, ContentType: req.ContentType, Status: "pending", Origin: "gateway", QueueNodeSettlement: enroll, Protection: protection, Encryption: encryption}
	}
	return c, receipt, secret, nil
}

func (s *Service) resolveObjectURLEncryption(b state.ObjectBucket, req objectstorage.SignRequest) (state.ObjectEncryptionSnapshot, error) {
	if req.Encryption == nil {
		return state.ObjectEncryptionSnapshot{}, nil
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
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
