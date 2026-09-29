package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentSnapshotProvider struct {
	objectstorage.Provider
	versions             []objectstorage.ObjectVersion
	bodies               map[string]string
	destination          string
	listCalls, copyCalls int
	lastCopiedVersion    string
	corruptDestination   bool
}

func (*environmentSnapshotProvider) BucketVersioningEnabled(context.Context, string) (bool, error) {
	return true, nil
}

func (p *environmentSnapshotProvider) ListObjectVersions(context.Context, string, string, int32) (objectstorage.ObjectVersionPage, error) {
	p.listCalls++
	return objectstorage.ObjectVersionPage{Items: p.versions}, nil
}

func (p *environmentSnapshotProvider) CopyObjectBetweenBuckets(_ context.Context, _, _ string, request objectstorage.CopyObjectRequest) (objectstorage.CopyObjectResult, error) {
	p.copyCalls++
	p.lastCopiedVersion = request.SourceVersion
	p.destination = p.bodies[request.SourceVersion]
	if p.corruptDestination {
		p.destination = "bad"
	}
	return objectstorage.CopyObjectResult{ETag: "target-etag"}, nil
}

func (p *environmentSnapshotProvider) ReadObjectVersion(_ context.Context, _, _, version string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.bodies[version])), nil
}

func (p *environmentSnapshotProvider) ReadObject(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.destination)), nil
}

func TestProjectEnvironmentObjectSnapshotRetryUsesCapturedVersions(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "snapshot-retry@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
		AccountID: account.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
		IdempotencyKey: "snapshot-retry", SourceRevisionHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID,
		state.CloneOperationPending, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	provider := &environmentSnapshotProvider{
		versions: []objectstorage.ObjectVersion{
			{Key: "data.json", VersionID: "v1", Size: 3, LastModified: capturedAt.Add(-time.Second)},
			{Key: "data.json", VersionID: "v2", Size: 3, LastModified: capturedAt.Add(time.Second)},
		},
		bodies: map[string]string{"v1": "old", "v2": "new"},
	}
	sourceBucket, targetBucket := uuid.NewString(), uuid.NewString()
	manifest, err := captureProjectEnvironmentObjectStorageSnapshot(ctx, store, provider, account.ID, project.ID,
		op.ID, sourceBucket, targetBucket, "source", capturedAt)
	if err != nil || len(manifest.Objects) != 1 || manifest.Objects[0].Source.VersionID != "v1" {
		t.Fatalf("capture = %+v, %v", manifest, err)
	}
	provider.versions = nil // Source listings may change or expire after capture.
	if _, err := captureProjectEnvironmentObjectStorageSnapshot(ctx, store, provider, account.ID, project.ID,
		op.ID, sourceBucket, targetBucket, "source", capturedAt); err != nil || provider.listCalls != 1 {
		t.Fatalf("capture retry: %v, list calls %d", err, provider.listCalls)
	}
	if _, err := captureProjectEnvironmentObjectStorageSnapshot(ctx, store, provider, account.ID, project.ID,
		op.ID, sourceBucket, uuid.NewString(), "source", capturedAt); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed destination = %v, want conflict", err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID,
		state.CloneOperationCapturing, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	provider.corruptDestination = true
	if _, err := copyProjectEnvironmentObjectStorageSnapshot(ctx, store, provider, account.ID, project.ID,
		op.ID, sourceBucket, targetBucket, "source", "target"); !errors.Is(err, objectstorage.ErrObjectSnapshotCopyMismatch) {
		t.Fatalf("corrupt copy = %v, want mismatch", err)
	}
	loaded, err := store.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, sourceBucket)
	if err != nil || loaded.Objects[0].CopiedAt != nil {
		t.Fatalf("corrupt copy checkpoint = %+v, %v", loaded, err)
	}
	provider.corruptDestination = false
	for attempt := 0; attempt < 2; attempt++ {
		completed, err := copyProjectEnvironmentObjectStorageSnapshot(ctx, store, provider, account.ID, project.ID,
			op.ID, sourceBucket, targetBucket, "source", "target")
		if err != nil || completed != 1 {
			t.Fatalf("copy attempt %d = %d, %v", attempt, completed, err)
		}
	}
	if provider.copyCalls != 2 || provider.lastCopiedVersion != "v1" || provider.destination != "old" {
		t.Fatalf("copy replay: calls %d, version %s, destination %q", provider.copyCalls, provider.lastCopiedVersion, provider.destination)
	}
}
