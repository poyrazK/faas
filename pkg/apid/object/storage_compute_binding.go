package object

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) objectStorageComputeBindingContext(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucket, state.ObjectS3CredentialBindingStore, bool) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	bucket, credentials, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	bindings, ok := credentials.(state.ObjectS3CredentialBindingStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return state.App{}, state.ObjectBucket{}, nil, false
	}
	if bucket.AppID != app.ID {
		BucketProblem(w, state.ErrNotFound)
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
	return api.ValidObjectStorageBindingPrefix(prefix)
}

func ObjectStorageBindingSecretKeys(prefix string) api.ObjectStorageComputeBindingSecretKeys {
	return api.ObjectStorageComputeBindingSecretKeys{
		Endpoint: prefix + "_ENDPOINT", Region: prefix + "_REGION", Bucket: prefix + "_BUCKET",
		AccessKeyID: prefix + "_ACCESS_KEY_ID", SecretAccessKey: prefix + "_SECRET_ACCESS_KEY", AddressingStyle: prefix + "_ADDRESSING_STYLE",
	}
}

func ObjectStorageBindingSecretValues(keys api.ObjectStorageComputeBindingSecretKeys, endpoint, region, bucket, accessKeyID, secretAccessKey string) []struct{ key, value string } {
	return []struct{ key, value string }{
		{keys.Endpoint, endpoint}, {keys.Region, region}, {keys.Bucket, bucket},
		{keys.AccessKeyID, accessKeyID}, {keys.SecretAccessKey, secretAccessKey}, {keys.AddressingStyle, "path"},
	}
}

func (s *Service) ObjectStorageSecretSealReady() bool {
	if s.Host == nil || s.Host.SecretRecipient() == nil || len(s.Host.HostHMACKey()) == 0 {
		return false
	}
	return len(s.Host.MFAIdentities()) > 0
}

func viewObjectStorageComputeBinding(c state.ObjectS3Credential) api.ObjectStorageComputeBinding {
	return api.ObjectStorageComputeBinding{
		ID: c.ID, BucketID: c.BucketID, Scope: c.ManagedScope, Prefix: c.ManagedPrefix,
		Credential: viewObjectS3Credential(c), SecretKeys: ObjectStorageBindingSecretKeys(c.ManagedPrefix),
	}
}

func (s *Service) ListObjectStorageComputeBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListObjectS3Credentials(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	items := make([]api.ObjectStorageComputeBinding, 0, len(rows))
	for _, row := range rows {
		if row.ManagedAppID == app.ID && row.ManagedPrefix != "" {
			wakeID, err := store.PendingObjectS3CredentialRotation(r.Context(), acct.ID, bucket.ID, row.ID)
			if err != nil {
				BucketProblem(w, err)
				return
			}
			item := viewObjectStorageComputeBinding(row)
			item.RotationPending = wakeID != ""
			items = append(items, item)
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ObjectStorageComputeBindingList{Items: items})
}

func (s *Service) CreateObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return
	}
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	if bucket.State != "ready" {
		BucketProblem(w, state.ErrConflict)
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
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if req.Prefix == "" {
		req.Prefix = defaultObjectStorageBindingPrefix(bucket.Name)
	}
	if !validObjectStorageBindingPrefix(req.Prefix) {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	rows, err := store.ListObjectS3Credentials(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	for _, row := range rows {
		if row.ManagedAppID == app.ID && row.ManagedScope == bucket.Scope && row.ManagedPrefix == req.Prefix {
			BucketProblem(w, state.ErrConflict)
			return
		}
	}
	credential, prob, err := s.newObjectStorageComputeBinding(r.Context(), acct, app, bucket, req, "")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.publishObjectStorageComputeBinding(r.Context(), acct, app, bucket, credential)
	httpkit.WriteJSON(w, http.StatusCreated, viewObjectStorageComputeBinding(credential))
}

// newObjectStorageComputeBinding shares sealing, quota and atomic secret custody
// with the developer workflow. An optional stable ID makes its retries durable.
func (s *Service) newObjectStorageComputeBinding(ctx context.Context, acct state.Account, app state.App, bucket state.ObjectBucket, req api.CreateObjectStorageComputeBindingRequest, credentialID string) (state.ObjectS3Credential, *api.Problem, error) {
	store, ok := s.Host.Store().(state.ObjectS3CredentialBindingStore)
	if !ok {
		return state.ObjectS3Credential{}, nil, objectstorage.ErrUnavailable
	}
	keys := ObjectStorageBindingSecretKeys(req.Prefix)
	secretRows, err := s.Host.Store().ListAppSecretsInScope(ctx, acct.ID, app.ID, bucket.Scope)
	if err != nil {
		return state.ObjectS3Credential{}, nil, err
	}
	keySet := map[string]bool{}
	for _, row := range secretRows {
		keySet[row.Key] = true
	}
	secretValues := ObjectStorageBindingSecretValues(keys, s.Host.ObjectStorage().PublicEndpoint, s.Host.ObjectStorage().PublicRegion, bucket.Name, "", "")
	for _, item := range secretValues {
		if keySet[item.key] {
			return state.ObjectS3Credential{}, nil, state.ErrConflict
		}
	}
	limits := api.MustLimitsFor(acct.Plan)
	count, err := s.Host.Store().CountAppSecrets(ctx, acct.ID, app.ID)
	if err != nil {
		return state.ObjectS3Credential{}, nil, err
	}
	if count+len(secretValues) > limits.SecretCountMax {
		return state.ObjectS3Credential{}, api.ErrPlanLimitSecrets(limits, count+len(secretValues)), nil
	}
	if !s.ObjectStorageSecretSealReady() {
		return state.ObjectS3Credential{}, nil, objectstorage.ErrUnavailable
	}
	recipient := s.Host.SecretRecipient()
	accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		return state.ObjectS3Credential{}, nil, objectstorage.ErrUnavailable
	}
	sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
	if err != nil {
		return state.ObjectS3Credential{}, nil, objectstorage.ErrUnavailable
	}
	if credentialID == "" {
		credentialID = uuid.NewString()
	}
	secretValues = ObjectStorageBindingSecretValues(keys, s.Host.ObjectStorage().PublicEndpoint, s.Host.ObjectStorage().PublicRegion, bucket.Name, accessKeyID, secretAccessKey)
	secrets, prob := s.SealObjectStorageBindingValues(acct, app, credentialID, bucket.Scope, secretValues, limits)
	if prob != nil {
		return state.ObjectS3Credential{}, prob, nil
	}
	credential, err := store.CreateObjectS3ComputeBinding(ctx, state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{
		ID: credentialID, AccountID: acct.ID, BucketID: bucket.ID, AccessKeyID: accessKeyID,
		SecretSealed: sealed, KID: recipient.String(), Label: req.Label, Permission: req.Permission,
		Status: state.ObjectS3CredentialStatusActive, ManagedAppID: app.ID, ManagedScope: bucket.Scope, ManagedPrefix: req.Prefix,
	}, Secrets: secrets, MaxCredentialsPerBucket: api.MaxObjectS3CredentialsPerBucket, MaxSecretsPerApp: limits.SecretCountMax})
	if err != nil {
		return state.ObjectS3Credential{}, nil, err
	}
	return credential, nil, nil
}

func (s *Service) publishObjectStorageComputeBinding(ctx context.Context, acct state.Account, app state.App, bucket state.ObjectBucket, credential state.ObjectS3Credential) {
	s.Host.NotifyRuntimeConfigChange(ctx, db.NotifySecretRotated, acct, app, "binding_created", bucket.Scope, "")
	s.Host.EmitAudit(ctx, "object_storage.compute_binding_created", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "scope": bucket.Scope, "prefix": credential.ManagedPrefix})
}

func (s *Service) SealObjectStorageBindingValues(acct state.Account, app state.App, credentialID, scope string, values []struct{ key, value string }, limits api.Limits) ([]state.AppSecret, *api.Problem) {
	recipient := s.Host.RuntimeSecretRecipient()
	if recipient == nil {
		return nil, httpkit.CustomerCapacityProblem(s.Host.Logger(), "store object-storage credentials", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", nil)
	}
	hmacKey := s.Host.HostHMACKey()
	if len(hmacKey) == 0 {
		return nil, httpkit.CustomerCapacityProblem(s.Host.Logger(), "store object-storage credentials", "Credential storage temporarily unavailable",
			"Gregale could not securely store these credentials.",
			"Retry in a few seconds; if it still fails, contact support.", nil)
	}
	// The kid names the recipient the values are sealed to.
	kid := recipient.String()
	secrets := make([]state.AppSecret, 0, len(values))
	for _, item := range values {
		valueHash, err := secretbox.ValueFingerprint([]byte(item.value), hmacKey)
		if err != nil {
			return nil, httpkit.CustomerCapacityProblem(s.Host.Logger(), "fingerprint object-storage credential", "Credential storage temporarily unavailable",
				"Gregale could not securely store these credentials.",
				"Retry in a few seconds; if it still fails, contact support.", err)
		}
		ciphertext, err := secretbox.SealOneBound(recipient, s.Host.RuntimeSecretBinding(app.AccountID), item.key, item.value, limits.SecretValueMaxBytes)
		if err != nil {
			if prob := api.AsProblem(err); prob != nil {
				return nil, prob
			}
			return nil, httpkit.CustomerCapacityProblem(s.Host.Logger(), "encrypt object-storage credential", "Credential storage temporarily unavailable",
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

func (s *Service) loadObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucket, state.ObjectS3CredentialBindingStore, state.ObjectS3Credential, bool) {
	app, bucket, store, ok := s.objectStorageComputeBindingContext(w, r, acct)
	if !ok {
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	id := r.PathValue("binding")
	if _, err := uuid.Parse(id); err != nil {
		BucketProblem(w, state.ErrNotFound)
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	credential, err := store.GetObjectS3Credential(r.Context(), acct.ID, bucket.ID, id)
	if err != nil || credential.ManagedAppID != app.ID || credential.ManagedPrefix == "" {
		BucketProblem(w, state.ErrNotFound)
		return state.App{}, state.ObjectBucket{}, nil, state.ObjectS3Credential{}, false
	}
	return app, bucket, store, credential, true
}

func (s *Service) DeleteObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, credential, ok := s.loadObjectStorageComputeBinding(w, r, acct)
	if !ok {
		return
	}
	changed, err := store.RevokeObjectS3ComputeBinding(r.Context(), acct.ID, bucket.ID, credential.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if changed {
		s.Host.NotifyRuntimeConfigChange(r.Context(), db.NotifySecretRotated, acct, app, "binding_revoked", credential.ManagedScope, "")
	}
	s.Host.EmitAudit(r.Context(), "object_storage.compute_binding_revoked", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) RotateObjectStorageComputeBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, bucket, store, credential, ok := s.loadObjectStorageComputeBinding(w, r, acct)
	if !ok {
		return
	}
	if credential.Status != state.ObjectS3CredentialStatusActive {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	wakeID, err := store.PendingObjectS3CredentialRotation(r.Context(), acct.ID, bucket.ID, credential.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	rotated := credential
	if wakeID == "" {
		if !s.ObjectStorageSecretSealReady() {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		wakeID = uuid.NewString()
		recipient := s.Host.SecretRecipient()
		accessKeyID, secretAccessKey, err := api.GenerateObjectS3Credential()
		if err != nil {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretAccessKey), 64)
		if err != nil {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		keys := ObjectStorageBindingSecretKeys(credential.ManagedPrefix)
		values := []struct{ key, value string }{{keys.AccessKeyID, accessKeyID}, {keys.SecretAccessKey, secretAccessKey}}
		secrets, prob := s.SealObjectStorageBindingValues(acct, app, credential.ID, credential.ManagedScope, values, api.MustLimitsFor(acct.Plan))
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		rotated, err = store.StageObjectS3CredentialRotation(r.Context(), state.ObjectS3CredentialRotationRequest{
			AccountID: acct.ID, BucketID: bucket.ID, BindingID: credential.ID, WakeID: wakeID,
			AccessKeyID: accessKeyID, SecretSealed: sealed, KID: recipient.String(), Secrets: secrets,
		})
		if err != nil {
			BucketProblem(w, err)
			return
		}
	}
	if err := store.StampObjectS3CredentialRotation(r.Context(), app.ID, wakeID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not stamp runtime configuration change; retry rotation"))
		return
	}
	if _, err := state.InvalidateAppSnapshotsAtExistingStamp(r.Context(), s.Host.Store(), app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not invalidate application snapshots"))
		return
	}
	deployments, err := s.Host.Store().ListDeploymentsForApp(r.Context(), app.ID, 0, 0)
	if err != nil {
		BucketProblem(w, err)
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
		instances, err := s.Host.Store().ListInstancesForApp(r.Context(), app.ID)
		if err != nil {
			BucketProblem(w, err)
			return
		}
		for _, instance := range instances {
			if state.State(instance.State).CountsForRAM() {
				api.WriteProblem(w, api.ErrCapacity("cannot finish rotation while resident instances remain without a live deployment"))
				return
			}
		}
		if err := store.FinalizeObjectS3CredentialRotationsForApp(r.Context(), app.ID, wakeID); err != nil {
			BucketProblem(w, err)
			return
		}
		response := viewObjectStorageComputeBinding(rotated)
		s.Host.EmitAudit(r.Context(), "object_storage.compute_binding_rotated", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "wake_id": wakeID})
		httpkit.WriteJSON(w, http.StatusOK, response)
		return
	}
	claimed := false
	if app.Status == state.AppActive {
		claimed, err = s.Host.ClaimAppRestart(r.Context(), s.Host.Store(), app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not claim runtime configuration refresh"))
			return
		}
	}
	payload, err := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": wakeID})
	if err == nil {
		err = s.Host.Notify(r.Context(), db.NotifyRuntimeConfigRestart, string(payload))
	}
	if err != nil {
		if claimed {
			if releaseErr := s.Host.ReleaseAppRestartClaim(context.WithoutCancel(r.Context()), s.Host.Store(), app.ID); releaseErr != nil {
				s.Host.Logger().Error("binding rotation: release failed restart claim", "app", app.ID, "err", releaseErr)
			}
		}
		api.WriteProblem(w, api.ErrCapacity("could not queue runtime configuration refresh; retry rotation"))
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.compute_binding_rotated", &acct.ID, map[string]any{"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "wake_id": wakeID})
	response := viewObjectStorageComputeBinding(rotated)
	response.RotationPending = true
	httpkit.WriteJSON(w, http.StatusOK, response)
}
