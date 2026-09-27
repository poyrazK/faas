package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
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
		if row.ManagedAppID == app.ID && row.ManagedPrefix != "" {
			wakeID, err := store.PendingObjectS3CredentialRotation(r.Context(), acct.ID, bucket.ID, row.ID)
			if err != nil {
				bucketProblem(w, err)
				return
			}
			item := viewObjectStorageComputeBinding(row)
			item.RotationPending = wakeID != ""
			items = append(items, item)
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
	credentialID := uuid.NewString()
	secretValues = objectStorageBindingSecretValues(keys, s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, bucket.Name, accessKeyID, secretAccessKey)
	secrets, prob := s.sealObjectStorageBindingValues(acct, app, credentialID, bucket.Scope, secretValues, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	credential, err := store.CreateObjectS3ComputeBinding(r.Context(), state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{
		ID: credentialID, AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: accessKeyID,
		SecretSealed: sealed, KID: recipient.String(), Label: req.Label, Permission: req.Permission,
		Status: state.ObjectS3CredentialStatusActive, ManagedAppID: app.ID, ManagedScope: bucket.Scope, ManagedPrefix: req.Prefix,
	}, Secrets: secrets, MaxCredentialsPerBucket: api.MaxObjectS3CredentialsPerBucket, MaxSecretsPerApp: limits.SecretCountMax})
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.notifyRuntimeConfigChange(r.Context(), db.NotifySecretRotated, acct, app, "binding_created", bucket.Scope, "")
	s.audit.Emit(r.Context(), "object_storage.compute_binding_created", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "scope": bucket.Scope, "prefix": req.Prefix})
	writeJSON(w, http.StatusCreated, viewObjectStorageComputeBinding(credential))
}

func (s *server) sealObjectStorageBindingValues(acct state.Account, app state.App, credentialID, scope string, values []struct{ key, value string }, limits api.Limits) ([]state.AppSecret, *api.Problem) {
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, customerCapacityProblem(s.log, "store object-storage credentials", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", nil)
	}
	hmacKey := hostHMACKey()
	if len(hmacKey) == 0 {
		return nil, customerCapacityProblem(s.log, "store object-storage credentials", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", nil)
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
		return nil, customerCapacityProblem(s.log, "store object-storage credentials", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", nil)
	}
	kid, err := secretbox.IdentityFingerprint(idents)
	if err != nil {
		return nil, customerCapacityProblem(s.log, "fingerprint object-storage credential identity", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", err)
	}
	secrets := make([]state.AppSecret, 0, len(values))
	for _, item := range values {
		valueHash, err := secretbox.ValueFingerprint([]byte(item.value), hmacKey)
		if err != nil {
			return nil, customerCapacityProblem(s.log, "fingerprint object-storage credential", "Credential storage temporarily unavailable",
				"Gregale could not securely store these credentials.",
				"Retry in a few seconds; if it still fails, contact support.", err)
		}
		ciphertext, err := secretbox.SealOne(recipient, item.key, item.value, limits.SecretValueMaxBytes)
		if err != nil {
			if prob := api.AsProblem(err); prob != nil {
				return nil, prob
			}
			return nil, customerCapacityProblem(s.log, "encrypt object-storage credential", "Credential storage temporarily unavailable",
				"Gregale could not securely store these credentials.",
				"Retry in a few seconds; if it still fails, contact support.", err)
		}
		secrets = append(secrets, state.AppSecret{
			AccountID: acct.ID, AppID: app.ID, Scope: scope, Key: item.key, Ciphertext: ciphertext,
			Kid: kid, ValueHash: valueHash, ManagedObjectStorageCredentialID: credentialID,
		})
	}
	return secrets, nil
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
	if err != nil || credential.ManagedAppID != app.ID || credential.ManagedPrefix == "" {
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
	wakeID, err := store.PendingObjectS3CredentialRotation(r.Context(), acct.ID, bucket.ID, credential.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	rotated := credential
	if wakeID == "" {
		if !objectStorageSecretSealReady() {
			bucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		wakeID = uuid.NewString()
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
		keys := objectStorageBindingSecretKeys(credential.ManagedPrefix)
		values := []struct{ key, value string }{{keys.AccessKeyID, accessKeyID}, {keys.SecretAccessKey, secretAccessKey}}
		secrets, prob := s.sealObjectStorageBindingValues(acct, app, credential.ID, credential.ManagedScope, values, api.MustLimitsFor(acct.Plan))
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		rotated, err = store.StageObjectS3CredentialRotation(r.Context(), state.ObjectS3CredentialRotationRequest{
			AccountID: acct.ID, BucketID: bucket.ID, BindingID: credential.ID, WakeID: wakeID,
			AccessKeyID: accessKeyID, SecretSealed: sealed, KID: recipient.String(), Secrets: secrets,
		})
		if err != nil {
			bucketProblem(w, err)
			return
		}
	}
	if err := store.StampObjectS3CredentialRotation(r.Context(), app.ID, wakeID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not stamp runtime configuration change; retry rotation"))
		return
	}
	if _, err := state.InvalidateAppSnapshotsAtExistingStamp(r.Context(), s.store, app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not invalidate application snapshots"))
		return
	}
	deployments, err := s.store.ListDeploymentsForApp(r.Context(), app.ID, 0, 0)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	hasLive := false
	for _, deployment := range deployments {
		if deployment.Status == state.DeployLive {
			hasLive = true
			break
		}
	}
	if !hasLive {
		instances, err := s.store.ListInstancesForApp(r.Context(), app.ID)
		if err != nil {
			bucketProblem(w, err)
			return
		}
		for _, instance := range instances {
			if state.State(instance.State).CountsForRAM() {
				api.WriteProblem(w, api.ErrCapacity("cannot finish rotation while resident instances remain without a live deployment"))
				return
			}
		}
		if err := store.FinalizeObjectS3CredentialRotationsForApp(r.Context(), app.ID, wakeID); err != nil {
			bucketProblem(w, err)
			return
		}
		response := viewObjectStorageComputeBinding(rotated)
		s.audit.Emit(r.Context(), "object_storage.compute_binding_rotated", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "wake_id": wakeID})
		writeJSON(w, http.StatusOK, response)
		return
	}
	claimed := false
	if app.Status == state.AppActive {
		claimed, err = claimAppRestart(r.Context(), s.store, app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not claim runtime configuration refresh"))
			return
		}
	}
	payload, err := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": wakeID})
	if err == nil {
		err = s.notif.Notify(r.Context(), db.NotifyRuntimeConfigRestart, string(payload))
	}
	if err != nil {
		if claimed {
			if releaseErr := releaseAppRestartClaim(context.WithoutCancel(r.Context()), s.store, app.ID); releaseErr != nil {
				s.log.Error("binding rotation: release failed restart claim", "app", app.ID, "err", releaseErr)
			}
		}
		api.WriteProblem(w, api.ErrCapacity("could not queue runtime configuration refresh; retry rotation"))
		return
	}
	s.audit.Emit(r.Context(), "object_storage.compute_binding_rotated", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "wake_id": wakeID})
	response := viewObjectStorageComputeBinding(rotated)
	response.RotationPending = true
	writeJSON(w, http.StatusOK, response)
}
