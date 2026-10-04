// adr: 569
package main

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

func TestLeasedObjectSnapshotRequiresRetainedSourceBeforeCaptureAndCopy(t *testing.T) {
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
	provider := &leasedEnvironmentSnapshotProvider{environmentSnapshotProvider: &environmentSnapshotProvider{
		versions: []objectstorage.ObjectVersion{{Key: "data.json", VersionID: "v1", Size: 3, LastModified: point.Add(-time.Second)}},
		bodies:   map[string]string{"v1": "old"},
	}}
	source, target := uuid.NewString(), uuid.NewString()
	for _, fault := range []string{"missing_capability", "short_retention"} {
		var selected objectstorage.Provider = provider
		if fault == "missing_capability" {
			selected = provider.environmentSnapshotProvider
		} else {
			provider.retainUntil = time.Now().Add(time.Minute)
		}
		if _, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, selected, lease, source, target, "source", point); !errors.Is(err, objectstorage.ErrObjectSnapshotRetentionUnavailable) {
			t.Fatalf("accepted %s capture: %v", fault, err)
		}
		if _, err := store.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, source); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("committed unprotected %s manifest: %v", fault, err)
		}
	}
	provider.retainUntil = time.Now().Add(time.Hour)
	manifest, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", point)
	if err != nil || len(manifest.Objects) != 1 {
		t.Fatalf("protected capture: %+v, %v", manifest, err)
	}
	lists := provider.listCalls
	provider.retainUntil = time.Now().Add(-time.Second)
	if _, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", point); !errors.Is(err, objectstorage.ErrObjectSnapshotRetentionUnavailable) || provider.listCalls != lists {
		t.Fatalf("expired capture was adopted or relisted: %v, lists %d", err, provider.listCalls)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); !errors.Is(err, objectstorage.ErrObjectSnapshotRetentionUnavailable) || copied != 0 || provider.copyCalls != 0 {
		t.Fatalf("consumed unprotected source: copied %d, calls %d, error %v", copied, provider.copyCalls, err)
	}
	stored, err := store.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, source)
	if err != nil || stored.Hash != manifest.Hash || stored.Objects[0].CopiedAt != nil {
		t.Fatalf("changed rejected manifest: %+v, %v", stored, err)
	}
	provider.retainUntil = time.Now().Add(time.Hour)
	if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); err != nil || copied != 1 || provider.copyCalls != 1 {
		t.Fatalf("protected source copy: copied %d, error %v", copied, err)
	}
	// Once the exact bytes are verified and durably checkpointed at the target,
	// replay no longer depends on the continued existence of the source version.
	observations := provider.retentionCalls
	provider.retainUntil, provider.bodies = time.Now().Add(-time.Second), nil
	if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); err != nil || copied != 1 || provider.copyCalls != 1 || provider.retentionCalls != observations {
		t.Fatalf("durable target replay depended on source: copied %d, error %v", copied, err)
	}
	if code := cloneCoordinatorErrorCode(objectstorage.ErrObjectSnapshotRetentionUnavailable); code != "object_snapshot_retention_unavailable" {
		t.Fatalf("retention diagnostic: %q", code)
	}
}
