//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentCloneObjectManifestHashMatchesProvider(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 123456789, time.UTC)
	providerVersion := objectstorage.ObjectVersion{Key: "data.json", VersionID: "v1", MetadataVersion: "3", Size: 4, ETag: "tag", LastModified: at}
	stateVersion := state.ProjectEnvironmentCloneObjectVersion{
		Key: providerVersion.Key, VersionID: providerVersion.VersionID, MetadataVersion: providerVersion.MetadataVersion,
		Size: providerVersion.Size, ETag: providerVersion.ETag, LastModified: providerVersion.LastModified,
	}
	providerHash, err := objectstorage.ObjectManifestHash([]objectstorage.ObjectVersion{providerVersion})
	if err != nil {
		t.Fatal(err)
	}
	stateHash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{stateVersion})
	if err != nil || stateHash != providerHash {
		t.Fatalf("manifest hashes differ: provider=%q state=%q err=%v", providerHash, stateHash, err)
	}
}

func TestPgProjectEnvironmentCloneObjectManifestResume(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	acct, err := s.CreateAccount(ctx, "clone-manifest-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "clone-manifest-" + uuid.NewString()[:8]})
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
	if op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, state.CloneOperationPending, state.CloneOperationCapturing, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 29, 12, 0, 0, 123456789, time.UTC)
	version := state.ProjectEnvironmentCloneObjectVersion{Key: "data.json", VersionID: "v1", Size: 4, LastModified: at.Add(-time.Second)}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{version})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{
		OperationID: op.ID, SourceBucketID: uuid.NewString(), TargetBucketID: uuid.NewString(), CapturedAt: at, Hash: hash,
		Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{{Source: version}},
	}
	created, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, manifest)
	if err != nil || len(created.Objects) != 1 {
		t.Fatalf("put = %+v, %v", created, err)
	}
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, manifest); err != nil {
		t.Fatalf("replay = %v", err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectManifest(ctx, uuid.NewString(), project.ID, op.ID, manifest.SourceBucketID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account lookup = %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, state.CloneOperationCapturing, state.CloneOperationCopying, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	verified := strings.Repeat("b", 64)
	for range 2 {
		if err := s.MarkProjectEnvironmentCloneObjectCopied(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID, version.Key, version.VersionID, "target-etag", verified); err != nil {
			t.Fatalf("copy checkpoint = %v", err)
		}
	}
	loaded, err := s.ProjectEnvironmentCloneObjectManifest(ctx, acct.ID, project.ID, op.ID, manifest.SourceBucketID)
	if err != nil || loaded.Objects[0].CopiedAt == nil || loaded.Objects[0].VerifiedSHA256 != verified {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
	loadedHash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{loaded.Objects[0].Source})
	if err != nil || loadedHash != hash || !loaded.CapturedAt.Equal(at) {
		t.Fatalf("snapshot precision changed on reload: hash=%q captured=%s err=%v", loadedHash, loaded.CapturedAt, err)
	}
	if _, err := s.PutProjectEnvironmentCloneObjectManifest(context.Background(), acct.ID, project.ID, manifest); err != nil {
		t.Fatalf("replay after copying = %v", err)
	}
}
