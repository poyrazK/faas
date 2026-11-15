package object

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// The bucket/app are durable workspace ownership. The stable credential ID also
// survives revocation, so a retry cannot mint a replacement for a revoked grant.
func DevBucketBindingID(bucket state.ObjectBucket, prefix string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale/dev-bucket/"+bucket.ID+"/"+prefix)).String()
}

func (s *Service) ValidateDevBuckets(request *api.UpsertDevSessionRequest, plan api.Plan) error {
	normalized, err := api.NormalizeDevBuckets(request.Buckets)
	if err != nil {
		return objectstorage.ErrInvalid
	}
	request.Buckets = normalized
	for _, bucket := range normalized {
		if bucket.Triggers != nil && len(*bucket.Triggers) > 0 && !api.MustLimitsFor(plan).AsyncInvokeAllowed {
			return objectstorage.ErrInvalid
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	if !s.ObjectStorageEnabled() || !s.ObjectStorageSecretSealReady() {
		return objectstorage.ErrUnavailable
	}
	if len(normalized) > s.Host.ObjectStorage().MaxBucketsPerApp {
		return state.ErrConflict
	}
	return nil
}

func (s *Service) EnsureDevBuckets(ctx context.Context, acct state.Account, app state.App, requests []api.DevBucketRequest) (*api.Problem, error) {
	store, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok && len(requests) != 0 {
		return nil, objectstorage.ErrUnavailable
	}
	for _, request := range requests {
		bucket, err := s.reserveBucket(ctx, store, acct.ID, app.ID, CreateBucketRequest{Name: request.Name, Scope: api.DefaultEnvScope, Region: request.Region})
		if err != nil {
			return nil, err
		}
		if bucket.State == "provisioning" {
			bucket, err = s.ProvisionBucket(ctx, store, bucket)
			if err != nil {
				return nil, err
			}
		}
		if bucket.State != "ready" {
			return nil, state.ErrConflict
		}
		if prob, err := s.ensureDevBucketBinding(ctx, acct, app, bucket, request); prob != nil || err != nil {
			return prob, err
		}
		if request.Triggers != nil {
			st, ok := s.Host.Store().(state.ObjectComputeTriggerStore)
			if !ok {
				return nil, objectstorage.ErrUnsupported
			}
			current, err := st.GetObjectBucketNotifications(ctx, acct.ID, app.ID, bucket.ID)
			if err != nil {
				return nil, err
			}
			if _, err = st.ReconcileObjectBucketComputeTriggers(ctx, acct.ID, app.ID, bucket.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &current.Revision, Triggers: *request.Triggers}); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (s *Service) ensureDevBucketBinding(ctx context.Context, acct state.Account, app state.App, bucket state.ObjectBucket, request api.DevBucketRequest) (*api.Problem, error) {
	store, ok := s.Host.Store().(state.ObjectS3CredentialBindingStore)
	if !ok {
		return nil, objectstorage.ErrUnavailable
	}
	if _, found, err := findDevBucketBinding(ctx, store, acct, app, bucket, request); err != nil || found {
		return nil, err
	}
	id := DevBucketBindingID(bucket, request.Prefix)
	existing, err := store.GetObjectS3Credential(ctx, acct.ID, bucket.ID, id)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	// A changed prefix or permission in gregale.yaml replaces this app's
	// earlier developer binding on the bucket, instead of failing every sync
	// until --stop deletes the bucket's data. Only this app's automatic
	// developer bindings are replaced.
	replaced, err := s.revokeStaleDevBucketBindings(ctx, store, acct, app, bucket, request)
	if err != nil {
		return nil, err
	}
	if existing.ID != "" {
		if replaced == 0 {
			// Nothing was replaced, so the stable identity keeps its meaning:
			// a revoked grant is not silently re-minted.
			return nil, matchDevBucketBinding(existing, app, bucket, request)
		}
		// The stable ID belongs to a binding replaced earlier; the new one
		// gets a fresh ID and is found by its attributes.
		id = uuid.NewString()
	}
	credential, prob, err := s.newObjectStorageComputeBinding(ctx, acct, app, bucket, api.CreateObjectStorageComputeBindingRequest{
		Label: "developer", Prefix: request.Prefix, Permission: request.Permission,
	}, id)
	if errors.Is(err, state.ErrConflict) {
		// A concurrent upsert may have won the binding insert. Reload by
		// attributes; collisions with unrelated secrets still fail.
		if _, found, lookupErr := findDevBucketBinding(ctx, store, acct, app, bucket, request); lookupErr == nil && found {
			return nil, nil
		}
	}
	if err == nil && prob == nil {
		s.publishObjectStorageComputeBinding(ctx, acct, app, bucket, credential)
	}
	return prob, err
}

// revokeStaleDevBucketBindings revokes this app's active developer bindings
// on bucket that no longer match the requested prefix and permission. The
// revocation removes their injected settings, so the replacement can take
// the same variable names.
func (s *Service) revokeStaleDevBucketBindings(ctx context.Context, store state.ObjectS3CredentialBindingStore, acct state.Account, app state.App, bucket state.ObjectBucket, request api.DevBucketRequest) (int, error) {
	credentials, err := store.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, credential := range credentials {
		if !isDevBucketBinding(credential, app) || matchDevBucketBinding(credential, app, bucket, request) == nil {
			continue
		}
		if _, err := store.RevokeObjectS3ComputeBinding(ctx, acct.ID, bucket.ID, credential.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			return revoked, err
		}
		s.emitRevocation(ctx, "object_storage.compute_binding_revoked", acct.ID, map[string]any{
			"app_id": app.ID, "bucket_id": bucket.ID, "binding_id": credential.ID, "reason": "developer_settings_changed",
		})
		revoked++
	}
	return revoked, nil
}

// findDevBucketBinding returns this app's active developer binding on
// bucket that matches request, whatever its ID.
func findDevBucketBinding(ctx context.Context, store state.ObjectS3CredentialBindingStore, acct state.Account, app state.App, bucket state.ObjectBucket, request api.DevBucketRequest) (state.ObjectS3Credential, bool, error) {
	credentials, err := store.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil {
		return state.ObjectS3Credential{}, false, err
	}
	for _, credential := range credentials {
		if isDevBucketBinding(credential, app) && matchDevBucketBinding(credential, app, bucket, request) == nil {
			return credential, true, nil
		}
	}
	return state.ObjectS3Credential{}, false, nil
}

func isDevBucketBinding(credential state.ObjectS3Credential, app state.App) bool {
	return credential.Status == state.ObjectS3CredentialStatusActive && credential.ManagedAppID == app.ID &&
		credential.Label == "developer" && credential.RotationParentID == ""
}

func matchDevBucketBinding(credential state.ObjectS3Credential, app state.App, bucket state.ObjectBucket, request api.DevBucketRequest) error {
	if credential.Status != state.ObjectS3CredentialStatusActive || credential.ManagedAppID != app.ID || credential.ManagedScope != bucket.Scope ||
		credential.ManagedPrefix != request.Prefix || credential.Permission != request.Permission || credential.Label != "developer" || credential.RotationParentID != "" {
		return state.ErrConflict
	}
	return nil
}

// Only automatic active roots are described. Omission never recreates bindings.
func (s *Service) DescribeDevBuckets(r *http.Request, app state.App) ([]api.DevBucketResponse, error) {
	if !middleware.HasScope(r, api.ScopesStorageManageSurface...) {
		return nil, nil
	}
	ctx := r.Context()
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	bindings, bindingOK := s.Host.Store().(state.ObjectS3CredentialBindingStore)
	if !ok || !bindingOK {
		return nil, nil
	}
	rows, err := buckets.ListObjectBuckets(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, err
	}
	var out []api.DevBucketResponse
	for _, bucket := range rows {
		credentials, err := bindings.ListObjectS3Credentials(ctx, app.AccountID, bucket.ID)
		if err != nil {
			return nil, err
		}
		var pending map[string]string
		for _, credential := range credentials {
			if credential.ManagedAppID == app.ID && credential.ManagedScope == bucket.Scope && credential.Label == "developer" && credential.ID == DevBucketBindingID(bucket, credential.ManagedPrefix) {
				item := viewObjectStorageComputeBinding(credential)
				if pending == nil {
					if pending, err = bindings.PendingObjectS3CredentialRotations(ctx, app.AccountID, bucket.ID); err != nil {
						return nil, err
					}
				}
				item.RotationPending = pending[credential.ID] != ""
				out = append(out, api.DevBucketResponse{Bucket: viewBucket(bucket), Binding: item})
			}
		}
	}
	return out, nil
}
