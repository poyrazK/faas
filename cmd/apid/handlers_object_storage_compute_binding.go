package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

var objectStorageBindingPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)

func (s *server) objectStorageComputeBindingContext(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucket, state.ObjectS3CredentialBindingStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	bucket, credentials, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	bindings, ok := credentials.(state.ObjectS3CredentialBindingStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	if bucket.AppID != app.ID {
		bucketProblem(w, state.ErrNotFound)
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	return app, bucket, bindings, true
}

func defaultObjectStorageBindingPrefix(bucketName string) string {
	var b strings.Builder
	b.WriteString("GREGALE_S3_")
	for _, r := range strings.ToUpper(bucketName) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	prefix := strings.TrimRight(b.String(), "_")
	if len(prefix) > 48 {
		prefix = strings.TrimRight(prefix[:48], "_")
	}
	return prefix
}

func validObjectStorageBindingPrefix(prefix string) bool {
	return objectStorageBindingPrefixPattern.MatchString(prefix)
}

func objectStorageBindingSecretKeys(prefix string) api.ObjectStorageComputeBindingSecretKeys {
	return api.ObjectStorageComputeBindingSecretKeys{
		Endpoint: prefix + "_ENDPOINT", Region: prefix + "_REGION", Bucket: prefix + "_BUCKET",
		AccessKeyID: prefix + "_ACCESS_KEY_ID", SecretAccessKey: prefix + "_SECRET_ACCESS_KEY", AddressingStyle: prefix + "_ADDRESSING_STYLE",
	}
}

func objectStorageBindingSecretValues(keys api.ObjectStorageComputeBindingSecretKeys, endpoint, region, bucket, accessKeyID, secretAccessKey string) []struct{ key, value string } {
	return []struct{ key, value string }{
		{keys.Endpoint, endpoint}, {keys.Region, region}, {keys.Bucket, bucket},
		{keys.AccessKeyID, accessKeyID}, {keys.SecretAccessKey, secretAccessKey}, {keys.AddressingStyle, "path"},
	}
}

func objectStorageSecretSealReady() bool {
	if setSecretRecipient() == nil || len(hostHMACKey()) == 0 {
		return false
	}
	if mfaIdentities != nil && len(mfaIdentities()) > 0 {
		return true
	}
	return mfaIdentity != nil && mfaIdentity() != nil
}

func viewObjectStorageComputeBinding(c state.ObjectS3Credential) api.ObjectStorageComputeBinding {
	return api.ObjectStorageComputeBinding{
		ID: c.ID, BucketID: c.BucketID, Scope: c.ManagedScope, Prefix: c.ManagedPrefix,
		Credential: viewObjectS3Credential(c), SecretKeys: objectStorageBindingSecretKeys(c.ManagedPrefix),
	}
}

func (s *server) listObjectStorageComputeBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListObjectS3Credentials(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	items := make([]api.ObjectStorageComputeBinding, 0, len(rows))
	for _, row := range rows {
		if row.ManagedAppID == app.ID && row.ManagedScope == bucket.Scope && row.ManagedPrefix != "" {
			items = append(items, viewObjectStorageComputeBinding(row))
		}
	}
	writeJSON(w, http.StatusOK, api.ObjectStorageComputeBindingList{Items: items})
}

func (s *server) createObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return
	}
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	if bucket.State != "ready" {
		bucketProblem(w, state.ErrConflict)
		return
	}
	var req api.CreateObjectStorageComputeBindingRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	if req.Label == "" {
		req.Label = "compute"
	}
	req.Label = strings.TrimSpace(req.Label)
	if !validObjectS3CredentialLabel(req.Label) || !validObjectS3CredentialPermission(req.Permission) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if req.Prefix == "" {
		req.Prefix = defaultObjectStorageBindingPrefix(bucket.Name)
	}
	if !validObjectStorageBindingPrefix(req.Prefix) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	rows, err := store.ListObjectS3Credentials(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	for _, row := range rows {
		if row.ManagedAppID == app.ID && row.ManagedScope == bucket.Scope && row.ManagedPrefix == req.Prefix {
			bucketProblem(w, state.ErrConflict)
			return
		}
	}
	keys := objectStorageBindingSecretKeys(req.Prefix)
	secretRows, err := s.store.ListAppSecretsInScope(r.Context(), acct.ID, app.ID, bucket.Scope)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	keySet := map[string]bool{}
	for _, row := range secretRows {
		keySet[row.Key] = true
	}
	secretValues := objectStorageBindingSecretValues(keys, s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, bucket.Name, "", "")
	for _, item := range secretValues {
		if keySet[item.key] {
			bucketProblem(w, state.ErrConflict)
			return
		}
	}
	limits := api.MustLimitsFor(acct.Plan)
	count, err := s.store.CountAppSecrets(r.Context(), acct.ID, app.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if count+len(secretValues) > limits.SecretCountMax {
		api.WriteProblem(w, api.ErrPlanLimitSecrets(limits, count+len(secretValues)))
		return
	}
	if !objectStorageSecretSealReady() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	recipient := setSecretRecipient()
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
		ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: accessKeyID,
		SecretSealed: sealed, KID: recipient.String(), Label: req.Label, Permission: req.Permission,
		Status: state.ObjectS3CredentialStatusActive, ManagedAppID: app.ID, ManagedScope: bucket.Scope, ManagedPrefix: req.Prefix,
	}, api.MaxObjectS3CredentialsPerBucket)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	secretValues = objectStorageBindingSecretValues(keys, s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, bucket.Name, accessKeyID, secretAccessKey)
	if prob := s.persistObjectStorageBindingSecrets(r, acct, app, credential.ID, bucket.Scope, secretValues, limits); prob != nil {
		_ = store.RevokeObjectS3Credential(r.Context(), acct.ID, bucket.ID, credential.ID)
		_ = s.store.DeleteManagedObjectStorageSecrets(r.Context(), credential.ID)
		api.WriteProblem(w, prob)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.compute_binding_created", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "scope": bucket.Scope, "prefix": req.Prefix})
	writeJSON(w, http.StatusCreated, viewObjectStorageComputeBinding(credential))
}

func (s *server) persistObjectStorageBindingSecrets(r *http.Request, acct state.Account, app state.App, credentialID, scope string, values []struct{ key, value string }, limits api.Limits) *api.Problem {
	recipient := setSecretRecipient()
	if recipient == nil {
		return api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	hmacKey := hostHMACKey()
	if len(hmacKey) == 0 {
		return api.ErrCapacity("host hmac key not loaded — refusing to seal")
	}
	var idents []*age.X25519Identity
	if mfaIdentities != nil {
		idents = mfaIdentities()
	}
	if len(idents) == 0 && mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			idents = []*age.X25519Identity{id}
		}
	}
	if len(idents) == 0 {
		return api.ErrCapacity("host age identities not loaded — refusing to seal")
	}
	kid, err := secretbox.IdentityFingerprint(idents)
	if err != nil {
		return api.ErrCapacity("could not resolve kid: " + err.Error())
	}
	for _, item := range values {
		valueHash, err := secretbox.ValueFingerprint([]byte(item.value), hmacKey)
		if err != nil {
			return api.ErrCapacity("could not compute value_hash: " + err.Error())
		}
		ciphertext, err := secretbox.SealOne(recipient, item.key, item.value, limits.SecretValueMaxBytes)
		if err != nil {
			if prob := api.AsProblem(err); prob != nil {
				return prob
			}
			return api.ErrCapacity("could not seal secret")
		}
		if err := s.store.PutManagedObjectStorageSecret(r.Context(), state.AppSecret{
			AccountID: acct.ID, AppID: app.ID, Scope: scope, Key: item.key, Ciphertext: ciphertext,
			Kid: kid, ValueHash: valueHash, ManagedObjectStorageCredentialID: credentialID,
		}); err != nil {
			if errors.Is(err, state.ErrConflict) {
				return api.ErrManagedObjectStorageSecretConflict()
			}
			return api.ErrCapacity("could not persist compute binding secret")
		}
	}
	return nil
}

func (s *server) loadObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucket, state.ObjectS3CredentialBindingStore, state.ObjectS3Credential, bool) {
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	id := r.PathValue("binding")
	if _, err := uuid.Parse(id); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	credential, err := store.GetObjectS3Credential(r.Context(), acct.ID, bucket.ID, id)
	if err != nil || credential.ManagedAppID != app.ID || credential.ManagedScope != bucket.Scope || credential.ManagedPrefix == "" {
		bucketProblem(w, state.ErrNotFound)
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	return app, bucket, store, credential, true
}

func (s *server) deleteObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, credential, ok := s.loadObjectStorageComputeBinding(w, r, acct)
	if !ok {
		return
	}
	if credential.Status == state.ObjectS3CredentialStatusActive {
		if err := store.RevokeObjectS3Credential(r.Context(), acct.ID, bucket.ID, credential.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			bucketProblem(w, err)
			return
		}
	}
	if err := s.store.DeleteManagedObjectStorageSecrets(r.Context(), credential.ID); err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.compute_binding_revoked", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) rotateObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, credential, ok := s.loadObjectStorageComputeBinding(w, r, acct)
	if !ok {
		return
	}
	if credential.Status != state.ObjectS3CredentialStatusActive {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	if !objectStorageSecretSealReady() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	recipient := setSecretRecipient()
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
	rotated, err := store.RotateObjectS3Credential(r.Context(), acct.ID, bucket.ID, credential.ID, accessKeyID, sealed, recipient.String())
	if err != nil {
		bucketProblem(w, err)
		return
	}
	keys := objectStorageBindingSecretKeys(credential.ManagedPrefix)
	values := objectStorageBindingSecretValues(keys, s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, bucket.Name, accessKeyID, secretAccessKey)
	if prob := s.persistObjectStorageBindingSecrets(r, acct, app, credential.ID, credential.ManagedScope, values, api.MustLimitsFor(acct.Plan)); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.compute_binding_rotated", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID})
	writeJSON(w, http.StatusOK, viewObjectStorageComputeBinding(rotated))
}
