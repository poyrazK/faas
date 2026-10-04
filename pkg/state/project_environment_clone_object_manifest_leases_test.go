// adr: 569
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectManifestLeaseTestStore interface {
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneLeasedObjectManifestStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateProject(context.Context, state.Project) (state.Project, error)
}

func TestMemCloneObjectManifestRejectsLostWorkerAuthority(t *testing.T) {
	cloneObjectManifestLeaseContract(t, state.NewMemStore())
}

func cloneObjectManifestLeaseContract(t *testing.T, s cloneObjectManifestLeaseTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@clone-object-lease.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "object-lease"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "object-worker", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	point := time.Now().UTC().Add(-time.Second)
	version := state.ProjectEnvironmentCloneObjectVersion{Key: "data.json", VersionID: "source-version", Size: 4, LastModified: point.Add(-time.Second)}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{version})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{OperationID: op.ID, SourceBucketID: uuid.NewString(), TargetBucketID: uuid.NewString(), CapturedAt: point, Hash: hash,
		Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{{Source: version}}}
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, manifest); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy writer bypassed a claimed worker: %v", err)
	}
	for _, fault := range []string{"token", "revision", "status", "account", "project"} {
		bad := lease
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision--
		case "status":
			bad.Operation.Status = state.CloneOperationPending
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		}
		if _, err := s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, bad, manifest); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("invalid %s authority accepted: %v", fault, err)
		}
	}
	if _, err := s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, lease, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, manifest.SourceBucketID)
	if err != nil || len(loaded.Objects) != 1 || !loaded.CapturedAt.Equal(point) || loaded.Objects[0].CopiedAt != nil {
		t.Fatalf("capture changed = %+v, %v", loaded, err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	verified := strings.Repeat("b", 64)
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, account.ID, project.ID, op.ID, manifest.SourceBucketID, version.Key, version.VersionID, "etag", verified); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy writer bypassed a released lease: %v", err)
	}
	// Expiry rejects mutation before a replacement worker claims the operation.
	expiring, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if err := s.MarkProjectEnvironmentCloneObjectCopiedForLease(ctx, expiring, manifest.SourceBucketID, version.Key, version.VersionID, "etag", verified); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker checkpointed: %v", err)
	}
	replacement, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []state.ProjectEnvironmentCloneLease{lease, expiring} {
		if _, err := s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, old, manifest); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old worker replayed manifest after takeover: %v", err)
		}
		if err := s.MarkProjectEnvironmentCloneObjectCopiedForLease(ctx, old, manifest.SourceBucketID, version.Key, version.VersionID, "etag", verified); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("old worker checkpointed after takeover: %v", err)
		}
	}
	if _, err := s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, replacement, manifest); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.MarkProjectEnvironmentCloneObjectCopiedForLease(ctx, replacement, manifest.SourceBucketID, version.Key, version.VersionID, "etag", verified); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkProjectEnvironmentCloneObjectCopiedForLease(ctx, replacement, manifest.SourceBucketID, version.Key, version.VersionID, "changed", verified); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed receipt accepted: %v", err)
	}
	loaded, err = s.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, manifest.SourceBucketID)
	if err != nil || loaded.Objects[0].CopiedAt == nil || loaded.Objects[0].VerifiedSHA256 != verified || loaded.Objects[0].Source.VersionID != version.VersionID {
		t.Fatalf("replacement lost pinned copy = %+v, %v", loaded, err)
	}
}
