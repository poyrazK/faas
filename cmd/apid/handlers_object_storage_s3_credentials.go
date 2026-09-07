package main

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func viewObjectS3Credential(c state.ObjectS3Credential) api.ObjectS3Credential {
	return api.ObjectS3Credential{
		ID: c.ID, BucketID: c.BucketID, AccessKeyID: c.AccessKeyID, Label: c.Label,
		Permission: c.Permission, Status: c.Status, CreatedAt: c.CreatedAt,
		LastUsedAt: c.LastUsedAt, RevokedAt: c.RevokedAt,
	}
}

func (s *server) objectS3CredentialStore(w http.ResponseWriter, r *http.Request, acct state.Account, requireReady bool) (state.ObjectBucket, state.ObjectS3CredentialStore, bool) {
	bucket, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, false
	}
	if requireReady && bucket.State != "ready" {
		bucketProblem(w, state.ErrConflict)
		return state.ObjectBucket{}, nil, false
	}
	store, ok := s.store.(state.ObjectS3CredentialStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, false
	}
	return bucket, store, true
}

func validObjectS3CredentialLabel(label string) bool {
	if label = strings.TrimSpace(label); len(label) < 1 || len(label) > 64 || !utf8.ValidString(label) {
		return false
	}
	for _, r := range label {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validObjectS3CredentialPermission(permission string) bool {
	return permission == api.ObjectBucketPermissionRead || permission == api.ObjectBucketPermissionWrite || permission == api.ObjectBucketPermissionReadWrite
}

func (s *server) createObjectS3Credential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	bucket, store, ok := s.objectS3CredentialStore(w, r, acct, true)
	if !ok {
		return
	}
	var req api.CreateObjectS3CredentialRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if !validObjectS3CredentialLabel(req.Label) || !validObjectS3CredentialPermission(req.Permission) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
	if err != nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	credential, err := store.CreateObjectS3Credential(r.Context(), state.ObjectS3Credential{
		ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID,
		AccessKeyID: accessKeyID, SecretSealed: sealed, KID: recipient.String(),
		Label: req.Label, Permission: req.Permission, Status: state.ObjectS3CredentialStatusActive,
	}, api.MaxObjectS3CredentialsPerBucket)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.s3_credential_created", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "credential_id": credential.ID, "permission": credential.Permission,
	})
	writeJSON(w, http.StatusCreated, api.ObjectS3CredentialSecret{
		ObjectS3Credential: viewObjectS3Credential(credential), SecretAccessKey: secretAccessKey,
		Endpoint: s.objectStorage.PublicEndpoint, Region: s.objectStorage.PublicRegion, AddressingStyle: "path",
	})
}

func (s *server) listObjectS3Credentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, store, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok {
		return
	}
	credentials, err := store.ListObjectS3Credentials(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	items := make([]api.ObjectS3Credential, 0, len(credentials))
	for _, credential := range credentials {
		items = append(items, viewObjectS3Credential(credential))
	}
	writeJSON(w, http.StatusOK, api.ObjectS3CredentialList{Items: items})
}

func (s *server) revokeObjectS3Credential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, store, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok {
		return
	}
	credentialID := r.PathValue("credential")
	if _, err := uuid.Parse(credentialID); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	if err := store.RevokeObjectS3Credential(r.Context(), acct.ID, bucket.ID, credentialID); err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.s3_credential_revoked", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "credential_id": credentialID,
	})
	w.WriteHeader(http.StatusNoContent)
}
