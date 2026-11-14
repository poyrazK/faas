// adr: 20261027090013 step 7 — persisted workspaces reserve their bytes
// within the account's quota under its lock, become ready only within the
// reservation, start sessions only while ready and unexpired, and are swept
// once expired, deleted on request or never completed.
package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func reserve(t *testing.T, s *agents.Store, sandboxID string, max, quota int64, expires time.Time) (agents.SandboxSnapshot, error) {
	t.Helper()
	return s.ReserveSandboxSnapshot(context.Background(), agents.SnapshotReservation{SnapshotID: uuid.NewString(), AccountID: account,
		SandboxID: sandboxID, MaxBytes: max, QuotaBytes: quota, ExpiresAt: expires})
}

const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSandboxSnapshotQuotaAndCompletion(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{})
	hour := time.Now().Add(time.Hour)
	first, err := reserve(t, s, sb.ID, 600, 1000, hour)
	if err != nil || first.ReservedBytes != 600 || first.StorageKey != agents.SandboxSnapshotKey(account, first.ID) || first.State != agents.SandboxSnapshotPending {
		t.Fatalf("first = %+v, %v", first, err)
	}
	// The pending reservation counts in full; what is left caps the next.
	second, err := reserve(t, s, sb.ID, 600, 1000, hour)
	if err != nil || second.ReservedBytes != 400 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	var limit *agents.LimitError
	if _, err := reserve(t, s, sb.ID, 600, 1000, hour); !errors.As(err, &limit) || limit.Limit != "SandboxSnapshotBytesPerAccount" || limit.Observed != 1000 {
		t.Fatalf("over quota = %v", err)
	}
	if err := s.CompleteSandboxSnapshot(ctx, second.ID, 401, digest); !errors.Is(err, agents.ErrSnapshotUnavailable) {
		t.Fatalf("an archive over its reservation completed: %v", err)
	}
	// A ready snapshot counts its size, not its reservation.
	if err := s.CompleteSandboxSnapshot(ctx, first.ID, 100, digest); err != nil {
		t.Fatal(err)
	}
	if third, err := reserve(t, s, sb.ID, 600, 1000, hour); err != nil || third.ReservedBytes != 500 {
		t.Fatalf("third = %+v, %v", third, err)
	}
	got, err := s.SandboxSnapshot(ctx, account, first.ID)
	if err != nil || got.State != agents.SandboxSnapshotReady || got.Bytes != 100 || got.SHA256 != digest || got.ReadyAt == nil {
		t.Fatalf("ready = %+v, %v", got, err)
	}
	if err := s.CompleteSandboxSnapshot(ctx, first.ID, 100, digest); !errors.Is(err, agents.ErrSnapshotUnavailable) {
		t.Fatalf("a ready snapshot completed twice: %v", err)
	}
	list, err := s.SandboxSnapshots(ctx, account, agents.SandboxSnapshotFilter{Limit: 10})
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if err := s.RequestSandboxSnapshotDelete(ctx, account, first.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.SandboxSnapshots(ctx, account, agents.SandboxSnapshotFilter{Limit: 10}); len(list) != 2 {
		t.Fatalf("a deleted snapshot is listed: %d", len(list))
	}
	if err := s.RequestSandboxSnapshotDelete(ctx, other, first.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account deleted it: %v", err)
	}
}

func TestSandboxFromASnapshot(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	source := admitSandbox(t, s, agents.SandboxAdmission{})
	ready, _ := reserve(t, s, source.ID, 100, 1000, time.Now().Add(time.Hour))
	if err := s.CompleteSandboxSnapshot(ctx, ready.ID, 10, digest); err != nil {
		t.Fatal(err)
	}
	pending, _ := reserve(t, s, source.ID, 100, 1000, time.Now().Add(time.Hour))
	expired, _ := reserve(t, s, source.ID, 100, 1000, time.Now().Add(-time.Second))
	if err := s.CompleteSandboxSnapshot(ctx, expired.ID, 10, digest); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pending.ID, expired.ID, uuid.NewString(), "not-a-uuid"} {
		if _, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{WorkspaceSnapshotID: id})); !errors.Is(err, agents.ErrSnapshotUnavailable) {
			t.Fatalf("started from %s: %v", id, err)
		}
	}
	if _, err := s.CloseSandbox(ctx, account, source.ID); err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{WorkspaceSnapshotID: ready.ID}))
	if err != nil || sb.WorkspaceSnapshotID != ready.ID {
		t.Fatalf("create = %+v, %v", sb, err)
	}
	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || claim.WorkspaceKey != ready.StorageKey || claim.WorkspaceMissing {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
}

func TestSweepableSandboxSnapshots(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{})
	hour := time.Now().Add(time.Hour)
	kept, _ := reserve(t, s, sb.ID, 100, 1000, hour)
	stale, _ := reserve(t, s, sb.ID, 100, 1000, hour)
	expired, _ := reserve(t, s, sb.ID, 100, 1000, time.Now().Add(-time.Second))
	asked, _ := reserve(t, s, sb.ID, 100, 1000, hour)
	if _, err := pool.Exec(ctx, `UPDATE sandbox_workspace_snapshots SET created_at=now()-interval '2 hours' WHERE id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestSandboxSnapshotDelete(ctx, account, asked.ID); err != nil {
		t.Fatal(err)
	}
	due, err := s.SweepableSandboxSnapshots(ctx, time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range due {
		got[d.ID] = true
	}
	if len(due) != 3 || !got[stale.ID] || !got[expired.ID] || !got[asked.ID] || got[kept.ID] {
		t.Fatalf("sweepable = %+v", due)
	}
	if err := s.DeleteSandboxSnapshot(ctx, stale.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteSandboxSnapshot(ctx, stale.ID, 1, digest); !errors.Is(err, agents.ErrSnapshotUnavailable) {
		t.Fatalf("a swept reservation completed: %v", err)
	}
}
