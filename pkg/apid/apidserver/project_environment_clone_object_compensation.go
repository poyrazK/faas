package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// errCloneTargetBucketsDeleting holds a directly compensated clone until the
// buckets it copied into are gone.
var errCloneTargetBucketsDeleting = errors.New("clone target buckets are still being deleted")

// retireCloneTargetBuckets deletes the buckets a failed clone copied into
// (ADR-20261103160000) and reports how many are not deleted yet. It revokes
// each target's credentials, then claims it for deletion; owned-bucket
// cleanup empties every version before the provider bucket is deleted, and
// the bucket worker finishes a deletion one batch does not.
func (s *server) retireCloneTargetBuckets(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (int, error) {
	op := lease.Operation
	if !cloneOperationHasObjectResources(op) {
		return 0, nil
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	credentials, credentialsOK := s.store.(state.ObjectS3CredentialBindingStore)
	targets, targetsOK := s.store.(state.ProjectEnvironmentCloneTargetBucketStore)
	if !ok || !credentialsOK || !targetsOK {
		return 0, errCloneCompensationUnavailable
	}
	plans, err := s.capturedProjectEnvironmentObjectPlans(ctx, op)
	if err != nil {
		return 0, err
	}
	apps := make(map[string]string, len(plans))
	for _, plan := range plans {
		apps[plan.source.ID] = plan.appID
	}
	remaining := 0
	for _, resource := range op.Resources {
		if resource.Kind != "object_storage" || resource.TargetID == "" {
			continue
		}
		appID, known := apps[resource.SourceID]
		if !known {
			return remaining, state.ErrConflict
		}
		deleted, err := s.retireCloneTargetBucket(ctx, buckets, credentials, targets, op, appID, resource)
		if err != nil {
			return remaining, err
		}
		if !deleted {
			remaining++
		}
	}
	return remaining, nil
}

func (s *server) retireCloneTargetBucket(ctx context.Context, buckets state.ObjectBucketStore, credentials state.ObjectS3CredentialBindingStore,
	targets state.ProjectEnvironmentCloneTargetBucketStore, op state.ProjectEnvironmentCloneOperation, appID string, resource state.ProjectEnvironmentCloneResource) (bool, error) {
	// The unpublished target is hidden from GetObjectBucket; a deleted one
	// reads as not found.
	bucket, err := targets.ProjectEnvironmentCloneTargetBucket(ctx, op.AccountID, appID, op.ID, resource.TargetID)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if bucket.EnvironmentCloneSourceBucketID != resource.SourceID {
		return false, state.ErrConflict
	}
	if bucket.State == "deleting" {
		// The bucket worker owns a deletion already under way.
		return false, nil
	}
	active, err := credentials.ListObjectS3Credentials(ctx, op.AccountID, bucket.ID)
	if err != nil {
		return false, err
	}
	for _, credential := range active {
		if credential.ManagedAppID != "" {
			_, err = credentials.RevokeObjectS3ComputeBinding(ctx, op.AccountID, bucket.ID, credential.ID)
		} else {
			err = credentials.RevokeObjectS3Credential(ctx, op.AccountID, bucket.ID, credential.ID)
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return false, fmt.Errorf("revoke clone target credential: %w", err)
		}
	}
	claimed, err := buckets.ClaimObjectBucket(ctx, op.AccountID, appID, bucket.ID, uuid.NewString(), "deleting")
	if err != nil {
		return false, fmt.Errorf("claim clone target bucket for deletion: %w", err)
	}
	// Once claimed, the bucket stays deleting: a batch that leaves versions
	// behind, or a provider failure, is retried by the bucket worker.
	if err := s.object.ExecuteBucketOperation(ctx, buckets, claimed); err != nil {
		if !errors.Is(err, objectstorage.ErrCleanupPending) {
			s.log.Warn("clone target bucket deletion deferred", "operation_id", op.ID, "bucket_id", bucket.ID, "error", err)
		}
		return false, nil
	}
	return true, nil
}
