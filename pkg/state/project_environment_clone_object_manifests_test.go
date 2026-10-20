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

func TestMemProjectEnvironmentCloneObjectManifestIsImmutableAndResumable(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "clone-manifest@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
		AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "staging",
		IdempotencyKey: "clone-one", SourceRevisionHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, state.CloneOperationPending, state.CloneOperationCapturing, 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	object := state.ProjectEnvironmentCloneObjectVersion{Key: "data.json", VersionID: "v1", Size: 4, LastModified: capturedAt.Add(-time.Second)}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{object})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{
		OperationID: op.ID, SourceBucketID: uuid.NewString(), TargetBucketID: uuid.NewString(),
		CapturedAt: capturedAt, Hash: hash,
		Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{{Source: object}},
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
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed target = %v, want conflict", err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectManifest(ctx, "other", project.ID, op.ID, manifest.SourceBucketID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account manifest = %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, state.CloneOperationCapturing, state.CloneOperationCopying, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	verified := strings.Repeat("b", 64)
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "target-etag", verified); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "target-etag", verified); err != nil {
		t.Fatalf("copy checkpoint replay: %v", err)
	}
	if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, object.Key, object.VersionID, "different", verified); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed copy receipt = %v", err)
	}
	loaded, err := s.ProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID)
	if err != nil || loaded.Objects[0].CopiedAt == nil || loaded.Objects[0].VerifiedSHA256 != verified || loaded.Objects[0].Source.VersionID != "v1" {
		t.Fatalf("stored manifest = %+v, %v", loaded, err)
	}
}
