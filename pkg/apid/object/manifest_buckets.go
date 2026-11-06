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

// ErrManifestBucketsOrganization refuses gregale.yaml buckets on an app the
// deploying account does not own: buckets are app-owned and billed to their
// owner, and organization projects bind them explicitly for now.
var ErrManifestBucketsOrganization = errors.New("gregale.yaml buckets are not supported for organization-owned apps yet; bind them with `gregale add bucket`")

// CheckManifestBuckets runs, before any mutation, the checks `gregale add
// bucket` runs for each bucket to create: object storage enabled and able to
// seal credentials, the provider accepting the platform's credentials, fresh
// usage accounting, a known region, and room under the per-app bucket limit.
// app is nil for an app the deploy has not created yet.
func (s *Service) CheckManifestBuckets(ctx context.Context, acct state.Account, app *state.App, buckets []ManifestBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	if app != nil && app.AccountID != acct.ID {
		return ErrManifestBucketsOrganization
	}
	if !s.ObjectStorageEnabled() {
		return ErrObjectStorageDisabled
	}
	if !s.ObjectStorageSecretSealReady() {
		return objectstorage.ErrUnavailable
	}
	creates := 0
	for _, want := range buckets {
		if want.Create {
			creates++
		}
	}
	if creates == 0 {
		return nil
	}
	if s.providerRefusingCredentials() {
		return ErrObjectStorageProviderRefused
	}
	registry := s.Host.ObjectStorage()
	if !registry.CanProvisionAllRegions() {
		return state.ErrObjectUsageStale
	}
	live := map[string]bool{}
	if app != nil {
		store, ok := s.Host.Store().(state.ObjectBucketStore)
		if !ok {
			return objectstorage.ErrUnavailable
		}
		items, err := store.ListObjectBuckets(ctx, acct.ID, app.ID)
		if err != nil {
			return err
		}
		for _, bucket := range items {
			if bucket.State != "deleted" {
				live[bucket.Name+"\x00"+bucket.Scope] = true
			}
		}
	}
	count := len(live)
	for _, want := range buckets {
		if !want.Create || live[want.Name+"\x00"+want.Scope] {
			continue
		}
		if want.Region != "" {
			if _, err := registry.Default(want.Region); err != nil {
				return fmt.Errorf("%w: bucket %q: region %q is not offered", objectstorage.ErrInvalid, want.Name, want.Region)
			}
		}
		live[want.Name+"\x00"+want.Scope] = true
		count++
	}
	if limit := registry.MaxBucketsPerApp; count > limit {
		return &state.ObjectStorageLimitError{Kind: "max_buckets_per_app", Limit: int64(limit), Observed: int64(count), Cause: state.ErrConflict}
	}
	return nil
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
	// Repeat the plan's checks: the apply may run long after the plan.
	if err := s.CheckManifestBuckets(ctx, acct, &app, buckets); err != nil {
		return nil, err
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
	// A deleted binding keeps its row as revoked; its stable ID cannot be
	// reused, so a re-bind after a delete takes a fresh one. The active scan
	// above finds it on every later apply.
	if existing, lookupErr := store.GetObjectS3Credential(ctx, acct.ID, bucket.ID, id); lookupErr == nil && existing.Status != state.ObjectS3CredentialStatusActive {
		id = uuid.NewString()
	}
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
