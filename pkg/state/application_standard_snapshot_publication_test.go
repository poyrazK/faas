package state

// Real MemStore/PgStore lifecycle tests; native byte facts remain simulated.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardSnapshotPublicationTestStore interface {
	Store
	standardSnapshotTestStore
}

func standardSnapshotCacheRow(depID string, g runtimeadmission.SnapshotGrant, a runtimeadmission.SnapshotAcknowledgment) Snapshot {
	tier := SnapshotTierInit
	if g.Mode == "warm" {
		tier = SnapshotTierWarm
	}
	return Snapshot{DeploymentID: depID, ApplicationStandardCaptureToken: g.Token, StorageKey: g.MemoryKey,
		FCVersion: g.FCVersion, MemBytes: a.Capture.Memory.Bytes, DiskBytes: a.Capture.VMState.Bytes,
		StoredBytes: 4096, Tier: tier, BaseImageVersion: "runner-test"}
}

func standardSnapshotPublication(t *testing.T, s standardSnapshotPublicationTestStore, mode string) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, mode)
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	a := standardSnapshotAck(g)
	snap := standardSnapshotCacheRow(ins.DeploymentID, g, a)
	if _, err := s.CreateSnapshot(t.Context(), snap); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unacknowledged bytes published", err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	standardSnapshotBadRows(t, s, snap)
	stored, err := s.PublishSnapshotIfRuntimeFresh(t.Context(), snap, ins.ID, ins.StartedAt)
	if err != nil || stored.ApplicationStandardCaptureToken != g.Token {
		t.Fatal("matching capture reference did not survive publication", err)
	}
	if _, err := s.CreateSnapshot(t.Context(), snap); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate publication changed cache identity", err)
	}
	standardSnapshotPublicationAfterCleanup(t, s, ins, stored, g)
}

func standardSnapshotBadRows(t *testing.T, s standardSnapshotPublicationTestStore, valid Snapshot) {
	t.Helper()
	for _, tc := range []struct {
		name string
		edit func(*Snapshot)
	}{
		{"missing reference", func(s *Snapshot) { s.ApplicationStandardCaptureToken = "" }},
		{"unknown reference", func(s *Snapshot) { s.ApplicationStandardCaptureToken = uuid.NewString() }},
		{"noncanonical reference", func(s *Snapshot) {
			s.ApplicationStandardCaptureToken = strings.ReplaceAll(s.ApplicationStandardCaptureToken, "-", "")
		}},
		{"key", func(s *Snapshot) { s.StorageKey += "-other" }},
		{"scope", func(s *Snapshot) { s.DeploymentID = uuid.NewString() }},
		{"Firecracker", func(s *Snapshot) { s.FCVersion += "-other" }},
		{"memory", func(s *Snapshot) { s.MemBytes++ }},
		{"vmstate", func(s *Snapshot) { s.DiskBytes++ }},
		{"physical bytes", func(s *Snapshot) { s.StoredBytes = -1 }},
		{"tier", func(s *Snapshot) {
			if s.Tier == SnapshotTierWarm {
				s.Tier = SnapshotTierInit
			} else {
				s.Tier = SnapshotTierWarm
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			tc.edit(&bad)
			if _, err := s.CreateSnapshot(t.Context(), bad); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal("different or unreferenced capture published", err)
			}
		})
	}
}

func standardSnapshotPublicationAfterCleanup(t *testing.T, s standardSnapshotPublicationTestStore, ins Instance, stored Snapshot, g runtimeadmission.SnapshotGrant) {
	t.Helper()
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestSnapshotForTier(t.Context(), ins.DeploymentID, stored.Tier)
	if err != nil || got.ID != stored.ID || got.ApplicationStandardCaptureToken != g.Token {
		t.Fatal("source cleanup erased cache association", err)
	}
	got, err = s.LatestSnapshot(t.Context(), ins.DeploymentID)
	if err != nil || got.ApplicationStandardCaptureToken != g.Token {
		t.Fatal("latest cache lost capture identity", err)
	}
	if err := s.MarkSnapshotStale(t.Context(), stored.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := s.DeleteSnapshotsByID(t.Context(), []string{stored.ID}); err != nil || count != 1 {
		t.Fatal("GC refused cache row", err)
	}
	if standardSnapshotGet(t, s, g).Acknowledgment == nil {
		t.Fatal("cache GC erased historical capture")
	}
	stored.ID, stored.CreatedAt = "", time.Time{}
	stored.Stale = false
	if _, err := s.CreateSnapshot(t.Context(), stored); err != nil {
		t.Fatal("historical association depended on live source", err)
	}
	standardSnapshotPublicationOwnerErasure(t, s, ins, g)
}

func standardSnapshotPublicationOwnerErasure(t *testing.T, s standardSnapshotPublicationTestStore, ins Instance, g runtimeadmission.SnapshotGrant) {
	t.Helper()
	if _, err := s.ScheduleAppDeletion(t.Context(), ins.AppID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), ins.AppID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), ins.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestSnapshot(t.Context(), ins.DeploymentID); !errors.Is(err, ErrNotFound) {
		t.Fatal("owner erasure retained cache row", err)
	}
	b := g.Parent.Binding
	if _, err := s.GetApplicationStandardSnapshotCapture(t.Context(), b.AccountID, b.AppID, b.DeploymentID, g.Token); !errors.Is(err, ErrNotFound) {
		t.Fatal("owner erasure retained capture history", err)
	}
}

func standardSnapshotNamespaceBeforeGrant(t *testing.T, s standardSnapshotPublicationTestStore) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, "warm")
	legacy, err := s.CreateSnapshot(t.Context(), Snapshot{DeploymentID: ins.DeploymentID, StorageKey: req.MemoryKey, FCVersion: req.FCVersion, Tier: SnapshotTierWarm})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); !errors.Is(err, ErrConflict) {
			t.Fatal("preexisting namespace gained capture authority", err)
		}
		if err := s.MarkSnapshotStale(t.Context(), legacy.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteSnapshotsByID(t.Context(), []string{legacy.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req); err != nil {
		t.Fatal("GC did not release legacy row", err)
	}
}

func TestMemStandardSnapshotWarmPublication(t *testing.T) {
	standardSnapshotPublication(t, NewMemStore(), "warm")
}
func TestMemStandardSnapshotParkPublication(t *testing.T) {
	standardSnapshotPublication(t, NewMemStore(), "park")
}
func TestMemStandardSnapshotNamespaceBeforeGrant(t *testing.T) {
	standardSnapshotNamespaceBeforeGrant(t, NewMemStore())
}
