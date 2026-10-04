// adr: 531
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

type leasedEnvironmentSnapshotProvider struct {
	*environmentSnapshotProvider
	afterCopy        func() error
	deadlineObserved bool
	retainUntil      time.Time
	retentionCalls   int
}

func (p *leasedEnvironmentSnapshotProvider) ObserveObjectVersionRetention(_ context.Context, _ string, item objectstorage.ObjectVersion) (objectstorage.ObjectVersionRetention, error) {
	p.retentionCalls++
	until := p.retainUntil
	if until.IsZero() {
		until = time.Now().Add(time.Hour)
	}
	return objectstorage.ObjectVersionRetention{VersionID: item.VersionID, MetadataVersion: item.MetadataVersion, RetainedUntil: until}, nil
}

func (p *leasedEnvironmentSnapshotProvider) CopyObjectBetweenBuckets(ctx context.Context, source, target string, request objectstorage.CopyObjectRequest) (objectstorage.CopyObjectResult, error) {
	_, p.deadlineObserved = ctx.Deadline()
	result, err := p.environmentSnapshotProvider.CopyObjectBetweenBuckets(ctx, source, target, request)
	if err == nil && p.afterCopy != nil {
		err = p.afterCopy()
	}
	return result, err
}

func TestLeasedObjectSnapshotCopyRejectsTakeoverAndResumesPinnedVersion(t *testing.T) {
	_, store, acct, project, _ := newProjectLifecycleFixture(t)
	ctx := context.Background()
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "leased-object", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	point := time.Now().UTC().Add(-time.Second)
	provider := &leasedEnvironmentSnapshotProvider{environmentSnapshotProvider: &environmentSnapshotProvider{
		versions: []objectstorage.ObjectVersion{{Key: "data.json", VersionID: "v1", Size: 3, LastModified: point.Add(-time.Second)},
			{Key: "data.json", VersionID: "v2", Size: 3, LastModified: point.Add(time.Second)}},
		bodies: map[string]string{"v1": "old", "v2": "new"},
	}}
	source, target := uuid.NewString(), uuid.NewString()
	if _, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", point); err != nil {
		t.Fatal(err)
	}
	if _, err := captureProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", point); err != nil || provider.listCalls != 1 {
		t.Fatalf("capture retry changed point = %v, lists %d", err, provider.listCalls)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	var replacement state.ProjectEnvironmentCloneLease
	provider.afterCopy = func() error {
		if err := store.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
			return err
		}
		var err error
		replacement, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
		return err
	}
	if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); !errors.Is(err, state.ErrConflict) || copied != 0 || !provider.deadlineObserved {
		t.Fatalf("lost worker authority = copied %d, error %v", copied, err)
	}
	loaded, err := store.ProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, op.ID, source)
	if err != nil || loaded.Objects[0].CopiedAt != nil {
		t.Fatalf("old worker wrote checkpoint = %+v, %v", loaded, err)
	}
	provider.afterCopy, provider.versions = nil, nil
	before := provider.copyCalls
	if _, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, lease, source, target, "source", "target"); !errors.Is(err, state.ErrConflict) || provider.copyCalls != before {
		t.Fatalf("stale worker reached provider = %v, copies %d", err, provider.copyCalls)
	}
	for range 2 {
		if copied, err := copyProjectEnvironmentObjectStorageSnapshotForLease(ctx, store, provider, replacement, source, target, "source", "target"); err != nil || copied != 1 {
			t.Fatalf("replacement copy = %d, %v", copied, err)
		}
	}
	if provider.copyCalls != 2 || provider.listCalls != 1 || provider.lastCopiedVersion != "v1" || provider.destination != "old" {
		t.Fatalf("retry changed source data = copies %d, lists %d, version %s, destination %s", provider.copyCalls, provider.listCalls, provider.lastCopiedVersion, provider.destination)
	}
}
