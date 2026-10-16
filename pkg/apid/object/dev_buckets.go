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
	id := DevBucketBindingID(bucket, request.Prefix)
	existing, err := store.GetObjectS3Credential(ctx, acct.ID, bucket.ID, id)
	if err == nil {
		return nil, matchDevBucketBinding(existing, app, bucket, request)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	credential, prob, err := s.newObjectStorageComputeBinding(ctx, acct, app, bucket, api.CreateObjectStorageComputeBindingRequest{
		Label: "developer", Prefix: request.Prefix, Permission: request.Permission,
	}, id)
	if errors.Is(err, state.ErrConflict) {
		// A concurrent upsert may have won the atomic binding insert. Reload the
		// exact stable identity; collisions with unrelated secrets still fail.
		if existing, lookupErr := store.GetObjectS3Credential(ctx, acct.ID, bucket.ID, id); lookupErr == nil {
			return nil, matchDevBucketBinding(existing, app, bucket, request)
		}
	}
	if err == nil && prob == nil {
		s.publishObjectStorageComputeBinding(ctx, acct, app, bucket, credential)
	}
	return prob, err
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
		for _, credential := range credentials {
			if credential.ManagedAppID == app.ID && credential.ManagedScope == bucket.Scope && credential.Label == "developer" && credential.ID == DevBucketBindingID(bucket, credential.ManagedPrefix) {
				item := viewObjectStorageComputeBinding(credential)
				wakeID, err := bindings.PendingObjectS3CredentialRotation(ctx, app.AccountID, bucket.ID, credential.ID)
				if err != nil {
					return nil, err
				}
				item.RotationPending = wakeID != ""
				out = append(out, api.DevBucketResponse{Bucket: viewBucket(bucket), Binding: item})
			}
		}
	}
	return out, nil
}
