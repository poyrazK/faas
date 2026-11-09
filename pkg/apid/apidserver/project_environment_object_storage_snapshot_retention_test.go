// adr: 590, 20261103160000
package apidserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Pinned versions carry no provider retention (ADR-20261103160000): capture
// and copy never ask for it, a copy is verified against its pinned version,
// and a version the source lost fails the clone as a source change.
func TestLeasedObjectSnapshotNeedsNoRetentionAndFailsOnLostVersion(t *testing.T) {
	_, store, account, project, _ := newProjectLifecycleFixture(t)
	ctx := context.Background()
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "object-retention", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	point := time.Now().UTC().Add(-time.Second)
	// The plain fixture has no retention observer at all.
	provider := &environmentSnapshotProvider{
		versions: []objectstorage.ObjectVersion{
			{Key: "data.json", VersionID: "v1", Size: 3, LastModified: point.Add(-time.Second)},
			{Key: "logo.png", VersionID: "v2", Size: 4, LastModified: point.Add(-time.Second)},
		},
		bodies: map[string]string{"v1": "old", "v2": "logo"},
	}
	source, target := uuid.NewString(), uuid.NewString()
	manifest, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", point)
	if err != nil || len(manifest.Objects) != 2 {
		t.Fatalf("capture without provider retention: %+v, %v", manifest, err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	// The second pinned version is permanently deleted before it is copied.
	delete(provider.bodies, "v2")
	copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target")
	if !errors.Is(err, objectstorage.ErrObjectSnapshotCopyMismatch) || copied != 1 {
		t.Fatalf("lost pinned version: copied %d, error %v", copied, err)
	}
	failure := cloneObjectCopyFailure(err)
	if !errors.Is(failure, state.ErrProjectEnvironmentCloneSourceChanged) || cloneCoordinatorErrorCode(failure) != "source_changed_during_capture" {
		t.Fatalf("lost version is not a source change: %v", failure)
	}
	stored, err := store.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, source)
	if err != nil || stored.Hash != manifest.Hash || stored.Objects[0].CopiedAt == nil || stored.Objects[1].CopiedAt != nil {
		t.Fatalf("copy checkpoint: %+v, %v", stored, err)
	}
	// A retry copies only what is left; the verified v1 copy is not read again.
	provider.bodies = map[string]string{"v2": "logo"}
	if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); err != nil || copied != 2 || provider.copyCalls != 3 {
		t.Fatalf("resume after restored version: copied %d, calls %d, error %v", copied, provider.copyCalls, err)
	}
	if transient := cloneObjectCopyFailure(objectstorage.ErrUnavailable); errors.Is(transient, state.ErrProjectEnvironmentCloneSourceChanged) || !errors.Is(transient, objectstorage.ErrUnavailable) {
		t.Fatal("a transient provider failure must stay retryable")
	}
}
