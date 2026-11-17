package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 5 follow-up: every capture attempt's objects are
// tracked until imaged deletes them; only a ready capture's key is kept.
func TestMemEnvironmentBranchCaptureObjects(t *testing.T) {
	environmentBranchCaptureObjects(t, state.NewMemStore())
}

func TestPgEnvironmentBranchCaptureObjects(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchCaptureObjects(t, s)
}

func environmentBranchCaptureObjects(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	_, _, _, deployment := seedProjectEnvironmentBranch(t, s, "branch-capture-objects@example.com")
	parentID, err := s.EnvironmentBranchLineageParent(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestEnvironmentBranchCapture(ctx, parentID); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	garbage := func() []string {
		t.Helper()
		objects, err := s.EnvironmentBranchCaptureGarbage(ctx, later, 10)
		if err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(objects))
		for _, o := range objects {
			if o.DeploymentID != parentID {
				t.Fatalf("object %+v names another deployment", o)
			}
			keys = append(keys, o.StorageKey)
		}
		return keys
	}
	key := func() string { return state.SnapshotCaptureMemKey(parentID, state.SnapshotTierWarm, uuid.NewString()) }

	// Attempt 1 records its key and fails: the key is garbage once its lease
	// is gone, never while an attempt may still be writing.
	lease := uuid.NewString()
	claimed, err := s.ClaimEnvironmentBranchCapture(ctx, lease, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := key()
	if err := s.RecordEnvironmentBranchCaptureObject(ctx, claimed.ID, uuid.NewString(), first); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("record without the lease: %v", err)
	}
	if err := s.RecordEnvironmentBranchCaptureObject(ctx, claimed.ID, lease, "snap/../etc/v2/mem"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("record a foreign key: %v", err)
	}
	if err := s.RecordEnvironmentBranchCaptureObject(ctx, claimed.ID, lease, first); err != nil {
		t.Fatal(err)
	}
	if got := garbage(); len(got) != 0 {
		t.Fatalf("garbage during a live attempt = %v", got)
	}
	if objects, err := s.EnvironmentBranchCaptureGarbage(ctx, time.Now().Add(-time.Hour), 10); err != nil || len(objects) != 0 {
		t.Fatalf("garbage inside the grace = %v, %v", objects, err)
	}
	if err := s.FailEnvironmentBranchCapture(ctx, claimed.ID, lease, "snapshot failed", false); err != nil {
		t.Fatal(err)
	}
	if got := garbage(); len(got) != 1 || got[0] != first {
		t.Fatalf("garbage after a failed attempt = %v, want [%s]", got, first)
	}

	// Attempt 2 publishes: its key is kept while the capture is ready.
	lease = uuid.NewString()
	if claimed, err = s.ClaimEnvironmentBranchCapture(ctx, lease, time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	second := key()
	if err := s.RecordEnvironmentBranchCaptureObject(ctx, claimed.ID, lease, second); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishEnvironmentBranchCapture(ctx, claimed.ID, lease, state.EnvironmentBranchCaptureArtifact{FCVersion: "1.12.0", StorageKey: second}); err != nil {
		t.Fatal(err)
	}
	if got := garbage(); len(got) != 1 || got[0] != first {
		t.Fatalf("garbage with a ready capture = %v, want [%s]", got, first)
	}
	if err := s.ForgetEnvironmentBranchCaptureObject(ctx, second); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("forget the ready capture's key: %v", err)
	}
	if err := s.ForgetEnvironmentBranchCaptureObject(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got := garbage(); len(got) != 0 {
		t.Fatalf("garbage after forgetting = %v", got)
	}

	// A retired capture's key becomes garbage.
	if err := s.RetireEnvironmentBranchCapture(ctx, claimed.ID, "restore fell back"); err != nil {
		t.Fatal(err)
	}
	if got := garbage(); len(got) != 1 || got[0] != second {
		t.Fatalf("garbage after retiring = %v, want [%s]", got, second)
	}
	if err := s.ForgetEnvironmentBranchCaptureObject(ctx, second); err != nil {
		t.Fatal(err)
	}
}
