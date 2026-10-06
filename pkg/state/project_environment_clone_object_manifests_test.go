package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemProjectEnvironmentCloneObjectManifestIsImmutableAndResumable(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	acct, err := s.CreateAccount(ctx, "clone-manifest@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
		IdempotencyKey: "clone-one", SourceRevisionHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, CloneOperationPending, CloneOperationCapturing, 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	object := ProjectEnvironmentCloneObjectVersion{Key: "data.json", VersionID: "v1", Size: 4, LastModified: capturedAt.Add(-time.Second)}
	hash, err := ProjectEnvironmentCloneObjectManifestHash([]ProjectEnvironmentCloneObjectVersion{object})
	if err != nil {
		t.Fatal(err)
	}
	manifest := ProjectEnvironmentCloneObjectManifest{
		OperationID: op.ID, SourceBucketID: uuid.NewString(), TargetBucketID: uuid.NewString(),
		CapturedAt: capturedAt, Hash: hash,
		Objects: []ProjectEnvironmentCloneObjectCheckpoint{{Source: object}},
	}
	created, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, manifest)
	if err != nil || created.Hash != hash {
		t.Fatalf("put manifest = %+v, %v", created, err)
	}
	created.Objects[0].Source.VersionID = "tampered"
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, manifest); err != nil {
		t.Fatalf("idempotent manifest replay: %v", err)
	}
	changed := manifest
	changed.TargetBucketID = uuid.NewString()
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed target = %v, want conflict", err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectManifest(ctx, "other", project.ID, op.ID, manifest.SourceBucketID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account manifest = %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, CloneOperationCapturing, CloneOperationCopying, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	verified := strings.Repeat("b", 64)
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "target-etag", verified); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "target-etag", verified); err != nil {
		t.Fatalf("copy checkpoint replay: %v", err)
	}
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "different", verified); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed copy receipt = %v", err)
	}
	loaded, err := s.ProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID)
	if err != nil || loaded.Objects[0].CopiedAt == nil || loaded.Objects[0].VerifiedSHA256 != verified || loaded.Objects[0].Source.VersionID != "v1" {
		t.Fatalf("stored manifest = %+v, %v", loaded, err)
	}
}
