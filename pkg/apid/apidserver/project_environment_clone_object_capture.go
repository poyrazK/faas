package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// captureProjectEnvironmentCloneObjectManifests pins each source bucket's
// versions as of the clone's data point, then moves the clone to copying
// (ADR-20261103160000). It waits cloneDataPointSettle past the point first,
// so versions written just before it are listed whatever the provider's
// clock. Before copying it checks the exact totals against the account's
// storage limits; a clone past them fails and is compensated.
func (s *server) captureProjectEnvironmentCloneObjectManifests(ctx context.Context, store projectEnvironmentCloneCoordinatorStore, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	op := lease.Operation
	if op.Status != state.CloneOperationCapturing || !cloneOperationHasObjectResources(op) {
		return lease, nil
	}
	point, err := captureCloneDataPoint(ctx, store, lease)
	if err != nil {
		return lease, err
	}
	if time.Now().Before(point.Add(s.cloneDataPointSettle)) {
		return lease, nil
	}
	lease, err = s.captureProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("capture clone object versions: %w", err)
	}
	bytes, keys, err := s.capturedCloneObjectTotals(ctx, lease.Operation)
	if err != nil {
		return lease, err
	}
	account, err := state.ProjectResourceAccountByID(ctx, s.store, op.ProjectID)
	if err != nil {
		return lease, err
	}
	if err := s.cloneObjectCapacityCheckBytes(ctx, account, bytes, keys); err != nil {
		return lease, err
	}
	op = lease.Operation
	updated, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, "")
	if err == nil {
		lease.Operation = updated
	}
	return lease, err
}

// capturedCloneObjectTotals sums the bytes and keys of every bucket manifest
// the clone pinned.
func (s *server) capturedCloneObjectTotals(ctx context.Context, op state.ProjectEnvironmentCloneOperation) (bytes, keys int64, err error) {
	store, ok := s.store.(cloneObjectWorkerStore)
	if !ok {
		return 0, 0, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	for _, resource := range op.Resources {
		if resource.Kind != "object_storage" {
			continue
		}
		if resource.Status != "captured" {
			return 0, 0, state.ErrConflict
		}
		manifest, err := store.ProjectEnvironmentCloneObjectManifest(ctx, op.AccountID, op.ProjectID, op.ID, resource.SourceID)
		if err != nil {
			return 0, 0, err
		}
		if err := validateCapturedCloneObjectManifest(op, resource, manifest); err != nil {
			return 0, 0, err
		}
		for _, object := range manifest.Objects {
			bytes += object.Source.Size
			keys++
		}
	}
	return bytes, keys, nil
}

// ensureCloneTargetVersioning turns versioning on for a clone's target
// bucket and reports whether it is on, so the stage keeps its source's
// history semantics and can itself be cloned (ADR-20261103160000).
func (s *server) ensureCloneTargetVersioning(ctx context.Context, bucket state.ObjectBucket) (bool, error) {
	versioning, ok := s.store.(state.ObjectBucketVersioningStore)
	targets, targetsOK := s.store.(state.ProjectEnvironmentCloneTargetBucketStore)
	if !ok || !targetsOK {
		return false, objectstorage.ErrUnavailable
	}
	// The unpublished target is hidden from GetObjectBucketVersioning.
	current, err := targets.ProjectEnvironmentCloneTargetVersioning(ctx, bucket.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return false, err
	}
	if err == nil && current.State == "ready" && current.ObservedStatus == "Enabled" &&
		current.PropagationUntil != nil && !current.PropagationUntil.After(time.Now()) {
		return true, nil
	}
	if err == nil && current.DesiredStatus == "Enabled" {
		return false, nil
	}
	if _, err := versioning.RequestObjectBucketVersioning(ctx, bucket.AccountID, bucket.AppID, bucket.ID, "Enabled"); err != nil {
		return false, fmt.Errorf("enable clone target versioning: %w", err)
	}
	return false, nil
}

// cloneObjectCopyFailure reads a failed pinned-version copy: a version the
// source no longer holds, or one whose bytes changed, means the source
// changed under the clone, which fails it (ADR-20261103160000).
func cloneObjectCopyFailure(err error) error {
	if errors.Is(err, objectstorage.ErrNotFound) || errors.Is(err, objectstorage.ErrObjectSnapshotCopyMismatch) {
		return fmt.Errorf("%w: %w", state.ErrProjectEnvironmentCloneSourceChanged, err)
	}
	return err
}

func cloneOperationHasObjectResources(op state.ProjectEnvironmentCloneOperation) bool {
	for _, resource := range op.Resources {
		if resource.Kind == "object_storage" {
			return true
		}
	}
	return false
}
