package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 3: environment branch buckets. For each bucket its
// parent binds, a branch gets an empty bucket in its own scope with an
// overlay on the parent's bucket. The S3 gateway serves the branch's own
// objects first and reads every other key through from the source, so a
// branch starts without copying a byte, and its writes and deletes stay in
// the branch. A source with versioning on is read as of the branch point
// (as_of), like the branch's PostgreSQL copies; any other source is read
// live (live_parent). Each credential bound to the branch bucket gets a copy
// grant on the source, so a copy of a key only the parent holds copies from
// the parent (copy-through).

var errEnvironmentBranchBucketLimit = errors.New("the workload has no room for another bucket; a branch needs one bucket for each bucket its parent binds")

// environmentBranchBucketName is the branch bucket's deterministic name: one
// per source bucket per branch environment, so a branch re-created with the
// same slug never adopts its predecessor's bucket.
func environmentBranchBucketName(environmentID, slug, sourceBucketID string) string {
	sum := sha256.Sum256([]byte(environmentID + "\x00" + sourceBucketID))
	return "br-" + slug + "-" + hex.EncodeToString(sum[:6])
}

// overlayEnvironmentBranchBuckets gives the branch one overlay bucket per
// source bucket and binds it into the branch's scope once per parent
// binding. Every step is idempotent, so a retry resumes.
func (s *server) overlayEnvironmentBranchBuckets(ctx context.Context, acct state.Account, branch state.ProjectEnvironmentBranch, plans []projectEnvironmentBindingClone) error {
	var objectPlans []projectEnvironmentBindingClone
	for _, plan := range plans {
		if plan.kind == "object_storage" {
			objectPlans = append(objectPlans, plan)
		}
	}
	if len(objectPlans) == 0 {
		return nil
	}
	if !s.object.ObjectStorageEnabled() {
		return objectstorage.ErrUnavailable
	}
	if !s.object.ObjectStorageSecretSealReady() {
		return errors.New("object storage secret sealing is unavailable")
	}
	overlays := map[string]state.ObjectBucket{}
	for _, plan := range objectPlans {
		bucket, ok := overlays[plan.bucket.ID]
		if !ok {
			var err error
			if bucket, err = s.ensureEnvironmentBranchBucket(ctx, acct, branch, plan.bucket); err != nil {
				return fmt.Errorf("overlay bucket %q for workload %q: %w", plan.bucket.Name, plan.app.Slug, err)
			}
			overlays[plan.bucket.ID] = bucket
		}
		if err := s.bindEnvironmentBranchBucket(ctx, acct, branch, plan, bucket); err != nil {
			return fmt.Errorf("bind overlay bucket %q for workload %q: %w", plan.bucket.Name, plan.app.Slug, err)
		}
	}
	return nil
}

// ensureEnvironmentBranchBucket records the branch's ownership, reserves the
// bucket like any new bucket in the source's region, links its overlay and
// provisions it.
func (s *server) ensureEnvironmentBranchBucket(ctx context.Context, acct state.Account, branch state.ProjectEnvironmentBranch, source state.ObjectBucket) (state.ObjectBucket, error) {
	owned, ok := s.store.(state.ProjectEnvironmentBranchBucketStore)
	overlays, overlaysOK := s.store.(state.ObjectBucketOverlayStore)
	buckets, bucketsOK := s.store.(state.ObjectBucketStore)
	if !ok || !overlaysOK || !bucketsOK {
		return state.ObjectBucket{}, errors.New("environment branch buckets are unavailable")
	}
	row, err := owned.RecordProjectEnvironmentBranchBucket(ctx, state.ProjectEnvironmentBranchBucket{
		AccountID: acct.ID, ProjectID: branch.ProjectID, EnvironmentID: branch.EnvironmentID, EnvironmentSlug: branch.Slug,
		AppID: source.AppID, SourceBucketID: source.ID,
	})
	if err != nil {
		return state.ObjectBucket{}, err
	}
	name := environmentBranchBucketName(branch.EnvironmentID, branch.Slug, source.ID)
	existing, err := buckets.ListObjectBuckets(ctx, acct.ID, source.AppID)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	live, reserved := 0, false
	for _, b := range existing {
		if b.State == "deleted" {
			continue
		}
		live++
		reserved = reserved || b.Name == name && b.Scope == branch.Slug
	}
	if !reserved && live >= s.objectStorage.MaxBucketsPerApp {
		return state.ObjectBucket{}, errEnvironmentBranchBucketLimit
	}
	id := uuid.NewString()
	bucket, err := s.object.ReserveRegionBucket(ctx, buckets, state.ObjectBucket{
		ID: id, AccountID: acct.ID, AppID: source.AppID, Name: name, Scope: branch.Slug, Region: source.Region,
		PhysicalName:                   objectstorage.PhysicalBucketPrefix + strings.ReplaceAll(id, "-", ""),
		EnvironmentCloneSourceBucketID: source.ID,
	})
	if err != nil {
		return state.ObjectBucket{}, err
	}
	if bucket.EnvironmentCloneSourceBucketID != source.ID || bucket.PublicRead || bucket.ServeAt != "" {
		return state.ObjectBucket{}, state.ErrConflict
	}
	if err := owned.SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, bucket.ID); err != nil {
		return state.ObjectBucket{}, err
	}
	// The overlay exists before the bucket is ready, so the gateway never
	// serves the branch bucket as a plain empty bucket. A retry keeps the
	// consistency the first attempt chose, even if the source's versioning
	// changed since.
	consistency, err := s.environmentBranchOverlayConsistency(ctx, overlays, acct, bucket.ID, source)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	if _, err := overlays.PutObjectBucketOverlay(ctx, state.ObjectBucketOverlay{
		BucketID: bucket.ID, AccountID: acct.ID, SourceBucketID: source.ID, SourceAppID: source.AppID,
		Consistency: consistency, AsOf: branch.BranchPoint,
	}); err != nil {
		return state.ObjectBucket{}, err
	}
	if bucket.State == "provisioning" {
		if bucket, err = s.object.ProvisionBucket(ctx, buckets, bucket); err != nil {
			return state.ObjectBucket{}, err
		}
	}
	if bucket.State != "ready" {
		return state.ObjectBucket{}, state.ErrConflict
	}
	return bucket, nil
}

// environmentBranchOverlayConsistency is as_of when the source bucket's
// versioning is observed on and settled, so the gateway can read the
// version current at the branch point, and live_parent otherwise. An
// overlay an earlier attempt recorded keeps its consistency.
func (s *server) environmentBranchOverlayConsistency(ctx context.Context, overlays state.ObjectBucketOverlayStore, acct state.Account, bucketID string, source state.ObjectBucket) (string, error) {
	existing, err := overlays.ObjectBucketOverlay(ctx, bucketID)
	switch {
	case err == nil:
		return existing.Consistency, nil
	case !errors.Is(err, state.ErrNotFound):
		return "", err
	}
	versioning, ok := s.store.(state.ObjectBucketVersioningStore)
	if !ok {
		return state.ObjectBucketOverlayLiveParent, nil
	}
	v, err := versioning.GetObjectBucketVersioning(ctx, acct.ID, source.AppID, source.ID)
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrConflict):
		// No versioning row, or a source that is not ready: read it live.
		return state.ObjectBucketOverlayLiveParent, nil
	case err != nil:
		return "", err
	}
	if environmentBranchSourceVersioned(v, time.Now()) {
		return state.ObjectBucketOverlayAsOf, nil
	}
	return state.ObjectBucketOverlayLiveParent, nil
}

// environmentBranchSourceVersioned reports whether a source bucket keeps
// every version from now on: versioning observed on, no transition in
// flight, and its propagation window over.
func environmentBranchSourceVersioned(v state.ObjectBucketVersioning, now time.Time) bool {
	return v.ObservedStatus == "Enabled" && v.State == "ready" && (v.PropagationUntil == nil || !v.PropagationUntil.After(now))
}

// bindEnvironmentBranchBucket gives the branch's workload a credential for
// its overlay bucket, shaped like the parent's binding, unless an earlier
// attempt already made one, and that credential a copy grant on the source.
func (s *server) bindEnvironmentBranchBucket(ctx context.Context, acct state.Account, branch state.ProjectEnvironmentBranch, plan projectEnvironmentBindingClone, bucket state.ObjectBucket) error {
	credentials, ok := s.store.(state.ObjectS3CredentialBindingStore)
	if !ok {
		return errors.New("object storage credential store is unavailable")
	}
	active, err := credentials.ListObjectS3Credentials(ctx, acct.ID, bucket.ID)
	if err != nil {
		return err
	}
	for _, c := range active {
		if c.Status == state.ObjectS3CredentialStatusActive && c.ManagedAppID == plan.app.ID &&
			c.ManagedScope == branch.Slug && c.ManagedPrefix == plan.objectCred.ManagedPrefix {
			return s.grantEnvironmentBranchCopyThrough(ctx, acct, bucket, plan.bucket, c)
		}
	}
	created, _, err := s.createProjectEnvironmentObjectStorageCredential(ctx, acct, branch.Slug, plan, bucket, environmentBranchObjectEndpoint(branch, s.objectStorage.PublicEndpoint))
	if err != nil {
		return err
	}
	return s.grantEnvironmentBranchCopyThrough(ctx, acct, bucket, plan.bucket, created)
}

// grantEnvironmentBranchCopyThrough lets credential copy from the overlay's
// source, which it can already read through the overlay. A read-only
// credential, or buckets the provider cannot copy between, get no grant:
// copy-through then answers the branch's own 404, as before.
func (s *server) grantEnvironmentBranchCopyThrough(ctx context.Context, acct state.Account, bucket, source state.ObjectBucket, credential state.ObjectS3Credential) error {
	if credential.Permission != state.ObjectBucketPermissionWrite && credential.Permission != state.ObjectBucketPermissionReadWrite {
		return nil
	}
	grants, ok := s.store.(state.ObjectS3CopySourceStore)
	if !ok {
		return nil
	}
	_, err := grants.SetObjectS3CopySource(ctx, acct.ID, bucket.ID, credential.ID, source.ID, "")
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		if s.log != nil {
			s.log.Info("environment branch: no copy-through grant", "bucket_id", bucket.ID, "source_bucket_id", source.ID, "err", err)
		}
		return nil
	}
	return err
}

// environmentBranchObjectEndpoint is the S3 endpoint a branch's bindings
// name: a contained branch reaches its buckets only through the bridge
// object relay (ADR-20261031193635 slice 6c), every other one through the
// public endpoint.
func environmentBranchObjectEndpoint(branch state.ProjectEnvironmentBranch, public string) string {
	switch branch.EgressMode {
	case state.EnvironmentBranchEgressContained, state.EnvironmentBranchEgressSimulated:
		return api.EnvironmentBranchObjectRelayEndpoint()
	default:
		return public
	}
}

// deleteEnvironmentBranchBucket revokes a gone branch's credentials on its
// bucket, then empties and deletes the bucket and drops its overlay. A bucket
// reserved before its row recorded it is found by name.
func (s *server) deleteEnvironmentBranchBucket(ctx context.Context, row state.ProjectEnvironmentBranchBucket) error {
	buckets, ok := s.store.(state.ObjectBucketStore)
	credentials, credentialsOK := s.store.(state.ObjectS3CredentialBindingStore)
	overlays, overlaysOK := s.store.(state.ObjectBucketOverlayStore)
	if !ok || !credentialsOK || !overlaysOK {
		return errors.New("environment branch buckets are unavailable")
	}
	if s.objectStorage == nil {
		return objectstorage.ErrUnavailable // the row waits for object storage to return
	}
	bucketID := row.CopyBucketID
	if bucketID == "" {
		listed, err := buckets.ListObjectBuckets(ctx, row.AccountID, row.AppID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		name := environmentBranchBucketName(row.EnvironmentID, row.EnvironmentSlug, row.SourceBucketID)
		for _, b := range listed {
			if b.Name == name && b.Scope == row.EnvironmentSlug && b.EnvironmentCloneSourceBucketID == row.SourceBucketID {
				bucketID = b.ID
			}
		}
		if bucketID == "" {
			return nil // never reserved
		}
	}
	bucket, err := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, bucketID)
	if errors.Is(err, state.ErrNotFound) {
		return overlays.DeleteObjectBucketOverlay(ctx, bucketID)
	}
	if err != nil {
		return err
	}
	if bucket.State != "deleted" {
		if err := revokeObjectBucketCredentials(ctx, credentials, row.AccountID, bucketID); err != nil {
			return err
		}
		if err := s.deleteProjectEnvironmentObjectStorageCloneBucket(ctx, row.AccountID, row.AppID, row.EnvironmentSlug, bucketID, row.SourceBucketID); err != nil {
			return err
		}
		if bucket, err = buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, bucketID); err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil && bucket.State != "deleted" {
			return fmt.Errorf("branch bucket is %s, not deleted: %w", bucket.State, objectstorage.ErrUnavailable)
		}
	}
	return overlays.DeleteObjectBucketOverlay(ctx, bucketID)
}

// revokeEnvironmentBranchBucketCredentials revokes every credential on a
// branch's bucket.
func (s *server) revokeEnvironmentBranchBucketCredentials(ctx context.Context, accountID, bucketID string) error {
	credentials, ok := s.store.(state.ObjectS3CredentialBindingStore)
	if !ok {
		return errors.New("environment branch buckets are unavailable")
	}
	return revokeObjectBucketCredentials(ctx, credentials, accountID, bucketID)
}

func revokeObjectBucketCredentials(ctx context.Context, credentials state.ObjectS3CredentialBindingStore, accountID, bucketID string) error {
	active, err := credentials.ListObjectS3Credentials(ctx, accountID, bucketID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return err
	}
	for _, c := range active {
		if _, err := credentials.RevokeObjectS3ComputeBinding(ctx, accountID, bucketID, c.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("revoke branch bucket credential: %w", err)
		}
	}
	return nil
}
