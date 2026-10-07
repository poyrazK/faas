//go:build !no_pg

package state

import (
	"errors"
	"testing"
	"time"
)

func newStandardRestorePGStore(t *testing.T) standardSnapshotPublicationTestStore {
	s, _ := runtimeCapturePGStore(t)
	return s
}
func TestPgStandardSnapshotRestoreAuthority(t *testing.T) {
	standardRestoreAuthority(t, newStandardRestorePGStore(t))
}
func TestPgStandardSnapshotRestoreRefusal(t *testing.T) {
	standardRestoreRefusal(t, newStandardRestorePGStore)
}
func TestPgStandardSnapshotRestoreFinalPublicationFence(t *testing.T) {
	standardRestorePublicationRefusal(t, newStandardRestorePGStore)
}

func TestPgStandardSnapshotRestoreFreshTargetRefusesOldPolicy(t *testing.T) {
	standardRestoreFreshTargetRefusal(t, newStandardRestorePGStore(t))
}

func TestPgStandardSnapshotRestoreCatalogContention(t *testing.T) {
	for _, table := range []string{"snapshots", "application_standard_snapshot_captures"} {
		t.Run(table, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			f := standardRestoreTestFixture(t, s)
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if table == "snapshots" {
				_, err = tx.Exec(t.Context(), `SELECT 1 FROM snapshots WHERE id=$1 FOR UPDATE`, f.Snapshot.ID)
			} else {
				_, err = tx.Exec(t.Context(), `SELECT 1 FROM application_standard_snapshot_captures WHERE token=$1 FOR UPDATE`, f.Grant.Token)
			}
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatal("catalog lock bypassed fail-fast fence", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("catalog contention held parent locks")
			}
			assertStandardRestoreNoGrant(t, s, f.Binding.Token)
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
			if err != nil {
				t.Fatal("contention poisoned retry", err)
			}
			tx, err = pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if _, err := tx.Exec(t.Context(), `SELECT 1 FROM snapshots WHERE id=$1 FOR UPDATE`, f.Snapshot.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, consumedNativeReceipt(b, f.Capture)); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatal("publication bypassed catalog lock", err)
			}
			assertNativeBootUnpublished(t, s, f.Target)
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, consumedNativeReceipt(b, f.Capture)); err != nil {
				t.Fatal("publication retry refused", err)
			}
		})
	}
}
