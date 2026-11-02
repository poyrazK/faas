package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func readyObjectBucket(t *testing.T, s contractTestStore, accountID, appID, name, scope string) state.ObjectBucket {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	b, err := s.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: accountID, AppID: appID, Name: name, Scope: scope,
		Region: "us-east-1", PhysicalName: "gregale-" + id[:8], BackendID: "test", BackendFingerprint: "fixture"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimObjectBucket(ctx, accountID, appID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	b.State = "ready"
	return b
}

// ADR-20261031193635 slice 3: a branch bucket's overlay names its source,
// tombstones hide the source's keys, and the buckets a branch owns outlive it
// until the reconciler removes them.
func TestMemObjectBucketOverlays(t *testing.T) {
	objectBucketOverlays(t, state.NewMemStore())
}

func objectBucketOverlays(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, branch, deployment := seedProjectEnvironmentBranch(t, s, "overlays@example.com")
	source := readyObjectBucket(t, s, acct.ID, deployment.AppID, "assets", "production")
	overlayBucket := readyObjectBucket(t, s, acct.ID, deployment.AppID, "assets", branch.Slug)

	if _, err := s.ObjectBucketOverlay(ctx, overlayBucket.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("overlay before put: %v", err)
	}
	overlay := state.ObjectBucketOverlay{BucketID: overlayBucket.ID, AccountID: acct.ID, SourceBucketID: source.ID, SourceAppID: source.AppID,
		Consistency: state.ObjectBucketOverlayLiveParent, AsOf: branch.BranchPoint}
	put, err := s.PutObjectBucketOverlay(ctx, overlay)
	if err != nil || put.Source.ID != source.ID || put.Source.PhysicalName != source.PhysicalName {
		t.Fatalf("put overlay = %+v, %v", put, err)
	}
	if again, err := s.PutObjectBucketOverlay(ctx, overlay); err != nil || again.BucketID != overlayBucket.ID {
		t.Fatalf("second put = %+v, %v", again, err)
	}
	conflicting := overlay
	conflicting.SourceBucketID, conflicting.SourceAppID = uuid.NewString(), source.AppID
	if _, err := s.PutObjectBucketOverlay(ctx, conflicting); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlay with another source: %v", err)
	}
	got, err := s.ObjectBucketOverlay(ctx, overlayBucket.ID)
	if err != nil || got.SourceBucketID != source.ID || got.Source.ID != source.ID || got.Consistency != state.ObjectBucketOverlayLiveParent {
		t.Fatalf("overlay = %+v, %v", got, err)
	}
	if _, err := s.ObjectBucketOverlay(ctx, source.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the source has no overlay: %v", err)
	}

	if err := s.AddObjectBucketOverlayTombstones(ctx, overlayBucket.ID, []string{"a.txt", "dir/b.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddObjectBucketOverlayTombstones(ctx, overlayBucket.ID, []string{"a.txt"}); err != nil {
		t.Fatalf("repeated tombstone: %v", err)
	}
	if err := s.AddObjectBucketOverlayTombstones(ctx, source.ID, []string{"a.txt"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("tombstone on a bucket without an overlay: %v", err)
	}
	hidden, err := s.ObjectBucketOverlayTombstones(ctx, overlayBucket.ID, []string{"a.txt", "c.txt", "dir/b.txt"})
	if err != nil || len(hidden) != 2 || !hidden["a.txt"] || !hidden["dir/b.txt"] || hidden["c.txt"] {
		t.Fatalf("tombstones = %v, %v", hidden, err)
	}

	row, err := s.RecordProjectEnvironmentBranchBucket(ctx, state.ProjectEnvironmentBranchBucket{AccountID: acct.ID, ProjectID: project.ID,
		EnvironmentID: branch.EnvironmentID, EnvironmentSlug: branch.Slug, AppID: deployment.AppID, SourceBucketID: source.ID})
	if err != nil || row.ID == "" {
		t.Fatalf("record branch bucket = %+v, %v", row, err)
	}
	if again, err := s.RecordProjectEnvironmentBranchBucket(ctx, row); err != nil || again.ID != row.ID {
		t.Fatalf("second record = %+v, %v", again, err)
	}
	if err := s.SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, overlayBucket.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replaced copy: %v", err)
	}
	if listed, err := s.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID); err != nil || len(listed) != 1 || listed[0].CopyBucketID != overlayBucket.ID {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	if _, err := s.ClaimOrphanedProjectEnvironmentBranchBucket(ctx, uuid.NewString(), time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a live branch's bucket claimed as orphan: %v", err)
	}
	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, branch.Slug); err != nil {
		t.Fatal(err)
	}
	lease := uuid.NewString()
	orphan, err := s.ClaimOrphanedProjectEnvironmentBranchBucket(ctx, lease, time.Now(), time.Minute)
	if err != nil || orphan.ID != row.ID || orphan.CopyBucketID != overlayBucket.ID {
		t.Fatalf("orphan = %+v, %v", orphan, err)
	}
	if err := s.RemoveProjectEnvironmentBranchBucket(ctx, orphan.ID, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("remove without the lease: %v", err)
	}
	if err := s.RemoveProjectEnvironmentBranchBucket(ctx, orphan.ID, lease); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObjectBucketOverlay(ctx, overlayBucket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObjectBucketOverlay(ctx, overlayBucket.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("overlay after delete: %v", err)
	}
	if hidden, err := s.ObjectBucketOverlayTombstones(ctx, overlayBucket.ID, []string{"a.txt"}); err != nil || len(hidden) != 0 {
		t.Fatalf("tombstones after delete = %v, %v", hidden, err)
	}
	if err := s.DeleteObjectBucketOverlay(ctx, overlayBucket.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}
