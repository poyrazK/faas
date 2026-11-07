package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 3: environment branch buckets. For each bucket its
// parent binds, a branch gets an empty bucket in its own scope with an
// overlay on the parent's bucket. The S3 gateway serves the branch's own
// objects first and reads every other key through from the source
// (live_parent), so a branch starts without copying a byte, and its writes
// and deletes stay in the branch.

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
	// serves the branch bucket as a plain empty bucket.
	if _, err := overlays.PutObjectBucketOverlay(ctx, state.ObjectBucketOverlay{
		BucketID: bucket.ID, AccountID: acct.ID, SourceBucketID: source.ID, SourceAppID: source.AppID,
		Consistency: state.ObjectBucketOverlayLiveParent, AsOf: branch.BranchPoint,
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

// bindEnvironmentBranchBucket gives the branch's workload a credential for
// its overlay bucket, shaped like the parent's binding, unless an earlier
// attempt already made one.
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
			return nil
		}
	}
	_, _, err = s.createProjectEnvironmentObjectStorageCredential(ctx, acct, branch.Slug, plan, bucket, environmentBranchObjectEndpoint(branch, s.objectStorage.PublicEndpoint))
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
		active, err := credentials.ListObjectS3Credentials(ctx, row.AccountID, bucketID)
		if err != nil {
			return err
		}
		for _, c := range active {
			if _, err := credentials.RevokeObjectS3ComputeBinding(ctx, row.AccountID, bucketID, c.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				return fmt.Errorf("revoke branch bucket credential: %w", err)
			}
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
