//go:build !no_pg

package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func newStandardRestorePGStore(t *testing.T) state.StandardSnapshotPublicationTestStore {
	s, _ := state.RuntimeCapturePGStore(t)
	return s
}
func TestPgStandardSnapshotRestoreAuthority(t *testing.T) {
	state.StandardRestoreAuthority(t, newStandardRestorePGStore(t))
}
func TestPgStandardSnapshotRestoreRefusal(t *testing.T) {
	state.StandardRestoreRefusal(t, newStandardRestorePGStore)
}
func TestPgStandardSnapshotRestoreFinalPublicationFence(t *testing.T) {
	state.StandardRestorePublicationRefusal(t, newStandardRestorePGStore)
}

func TestPgStandardSnapshotRestoreFreshTargetRefusesOldPolicy(t *testing.T) {
	state.StandardRestoreFreshTargetRefusal(t, newStandardRestorePGStore(t))
}

func TestPgStandardSnapshotRestoreCatalogContention(t *testing.T) {
	for _, table := range []string{"snapshots", "application_standard_snapshot_captures"} {
		t.Run(table, func(t *testing.T) {
			s, pool := state.RuntimeCapturePGStore(t)
			f := state.StandardRestoreTestFixture(t, s)
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
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatal("catalog lock bypassed fail-fast fence", err)
			}
			// Busy replays roll back before each backoff; only a lock wait exceeds this.
			if time.Since(start) > runtimeAdmissionBusyBudget()+time.Second {
				t.Fatal("catalog contention held parent locks")
			}
			state.AssertStandardRestoreNoGrant(t, s, f.Binding.Token)
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
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, state.StateRunning, state.ConsumedNativeReceipt(b, f.Capture)); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatal("publication bypassed catalog lock", err)
			}
			state.AssertNativeBootUnpublished(t, s, f.Target)
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, state.StateRunning, state.ConsumedNativeReceipt(b, f.Capture)); err != nil {
				t.Fatal("publication retry refused", err)
			}
		})
	}
}
