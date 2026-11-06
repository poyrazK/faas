package object

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ManifestBucket is one gregale.yaml bucket dependency of one app in a
// project apply (ADR-20261106105015 §1). Prefix and Permission are already
// defaulted.
type ManifestBucket struct {
	Name       string
	Scope      string
	Region     string
	Prefix     string
	Permission string
	Label      string
	Create     bool
	Triggers   *[]api.ObjectStorageComputeTrigger
}

// ManifestBucketBindingID is stable per bucket, app and prefix, so a retried
// apply finds the binding it created instead of minting another.
func ManifestBucketBindingID(bucket state.ObjectBucket, appID, prefix string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale/manifest-bucket/"+bucket.ID+"/"+appID+"/"+prefix)).String()
}

// FindManifestBucket returns the live bucket name has in scope for app.
func (s *Service) FindManifestBucket(ctx context.Context, acct state.Account, appID, name, scope string) (state.ObjectBucket, bool, error) {
	store, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		return state.ObjectBucket{}, false, objectstorage.ErrUnavailable
	}
	items, err := store.ListObjectBuckets(ctx, acct.ID, appID)
	if err != nil {
		return state.ObjectBucket{}, false, err
	}
	for _, bucket := range items {
		if bucket.Name == name && bucket.Scope == scope && bucket.State != "deleted" {
			return bucket, true, nil
		}
	}
	return state.ObjectBucket{}, false, nil
}

// EnsureManifestBuckets creates the buckets a manifest declares with
// `create` and binds every declared bucket to app, reusing an active binding
// on the same (app, scope, prefix) as `gregale add bucket` made it. It runs
// after the project's apps exist and before their builds, so the first
// deployment starts with the sealed S3 settings.
func (s *Service) EnsureManifestBuckets(ctx context.Context, acct state.Account, app state.App, buckets []ManifestBucket) (*api.Problem, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	if !s.ObjectStorageEnabled() || !s.ObjectStorageSecretSealReady() {
		return nil, objectstorage.ErrUnavailable
	}
	store, ok := s.Host.Store().(state.ObjectBucketStore)
	bindings, bindingOK := s.Host.Store().(state.ObjectS3CredentialBindingStore)
	if !ok || !bindingOK {
		return nil, objectstorage.ErrUnavailable
	}
	for _, want := range buckets {
		var bucket state.ObjectBucket
		if want.Create {
			reserved, err := s.reserveBucket(ctx, store, acct.ID, app.ID, CreateBucketRequest{Name: want.Name, Scope: want.Scope, Region: want.Region})
			if err != nil {
				return nil, fmt.Errorf("bucket %q: %w", want.Name, err)
			}
			bucket = reserved
			if bucket.State == "provisioning" {
				if bucket, err = s.ProvisionBucket(ctx, store, bucket); err != nil {
					return nil, fmt.Errorf("bucket %q: %w", want.Name, err)
				}
			}
		} else {
			found, exists, err := s.FindManifestBucket(ctx, acct, app.ID, want.Name, want.Scope)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("%w: bucket %q does not exist in scope %q of %s; add `create: true` to create it on deploy", state.ErrNotFound, want.Name, want.Scope, app.Slug)
			}
			bucket = found
		}
		if bucket.State != "ready" {
			return nil, fmt.Errorf("%w: bucket %q is %s; retry after it becomes ready", state.ErrConflict, want.Name, bucket.State)
		}
		if prob, err := s.ensureManifestBucketBinding(ctx, acct, app, bucket, bindings, want); prob != nil || err != nil {
			return prob, err
		}
		if want.Triggers != nil {
			st, ok := s.Host.Store().(state.ObjectComputeTriggerStore)
			if !ok {
				return nil, objectstorage.ErrUnsupported
			}
			current, err := st.GetObjectBucketNotifications(ctx, acct.ID, app.ID, bucket.ID)
			if err != nil {
				return nil, err
			}
			if _, err = st.ReconcileObjectBucketComputeTriggers(ctx, acct.ID, app.ID, bucket.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &current.Revision, Triggers: *want.Triggers}); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (s *Service) ensureManifestBucketBinding(ctx context.Context, acct state.Account, app state.App, bucket state.ObjectBucket, store state.ObjectS3CredentialBindingStore, want ManifestBucket) (*api.Problem, error) {
	credentials, err := store.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil {
		return nil, err
	}
	for _, credential := range credentials {
		if credential.Status != state.ObjectS3CredentialStatusActive || credential.ManagedAppID != app.ID ||
			credential.ManagedScope != bucket.Scope || credential.ManagedPrefix != want.Prefix {
			continue
		}
		if credential.Permission != want.Permission {
			return nil, fmt.Errorf("%w: bucket %q is already bound to %s as %s with permission %q, gregale.yaml asks for %q; delete the binding and redeploy",
				state.ErrConflict, bucket.Name, app.Slug, want.Prefix, credential.Permission, want.Permission)
		}
		return nil, nil
	}
	id := ManifestBucketBindingID(bucket, app.ID, want.Prefix)
	credential, prob, err := s.newObjectStorageComputeBinding(ctx, acct, app, bucket, api.CreateObjectStorageComputeBindingRequest{
		Label: want.Label, Prefix: want.Prefix, Permission: want.Permission,
	}, id)
	if errors.Is(err, state.ErrConflict) {
		// A concurrent apply may have won the insert of the same identity.
		if existing, lookupErr := store.GetObjectS3Credential(ctx, acct.ID, bucket.ID, id); lookupErr == nil &&
			existing.Status == state.ObjectS3CredentialStatusActive && existing.ManagedAppID == app.ID && existing.Permission == want.Permission {
			return nil, nil
		}
	}
	if err == nil && prob == nil {
		s.publishObjectStorageComputeBinding(ctx, acct, app, bucket, credential)
	}
	return prob, err
}
