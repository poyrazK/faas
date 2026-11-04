// adr: 20261027090013 §1, §7 — agentd claims a queued session under a lease
// within the account's live limit, shared with runs; only the lease holder
// moves it; a lapsed lease requeues a starting session and fails a live one;
// a session still queued at its end expires.
package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxLifecycle(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{IdleTimeoutSec: 120})
	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok-a", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || claim.RAMMB != 1024 || claim.LeaseToken != "tok-a" || claim.IdleTimeout != 2*time.Minute ||
		claim.CloseRequested || claim.Plan == "" {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
	if _, found, _ := s.ClaimSandbox(ctx, "agentd", "tok-b", ttl, maxLive(5)); found {
		t.Fatal("a starting session was claimed twice")
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, "tok-other", "node-a", "sandbox-vm"); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("another token marked the session ready: %v", err)
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, "tok-a", "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Sandbox(ctx, account, sb.ID)
	if got.State != agents.SandboxReady || got.NodeID != "node-a" || got.InstanceID != "sandbox-vm" {
		t.Fatalf("ready session = %+v", got)
	}

	// Renewing records the last call and returns apid's changes.
	seen := time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)
	if _, err := s.UpdateSandboxTimeouts(ctx, account, sb.ID, 60, got.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseSandbox(ctx, account, sb.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.RenewSandbox(ctx, sb.ID, "tok-a", ttl, seen)
	if err != nil || !lease.CloseRequested || lease.IdleTimeout != time.Minute {
		t.Fatalf("renew = %+v, %v", lease, err)
	}
	if got, _ = s.Sandbox(ctx, account, sb.ID); !got.LastActivityAt.Equal(seen) {
		t.Fatalf("last activity %v, want %v", got.LastActivityAt, seen)
	}
	if _, err := s.RenewSandbox(ctx, sb.ID, "tok-other", ttl, seen); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("another token renewed: %v", err)
	}

	for _, frozen := range []bool{true, false, true} {
		if err := s.SetSandboxFrozen(ctx, sb.ID, "tok-a", frozen); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ = s.Sandbox(ctx, account, sb.ID); got.State != agents.SandboxFrozen {
		t.Fatalf("state %s, want frozen", got.State)
	}
	if err := s.FinishSandbox(ctx, sb.ID, "tok-a", agents.SandboxReady, "x", ""); err == nil {
		t.Fatal("finished into a live state")
	}
	if err := s.FinishSandbox(ctx, sb.ID, "tok-a", agents.SandboxClosed, "closed", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Sandbox(ctx, account, sb.ID)
	if got.State != agents.SandboxClosed || got.OutcomeCode != "closed" || got.ClosedAt == nil {
		t.Fatalf("closed session = %+v", got)
	}
	if err := s.FinishSandbox(ctx, sb.ID, "tok-a", agents.SandboxFailed, "late", ""); !errors.Is(err, agents.ErrLeaseLost) {
		t.Fatalf("an ended session ended again: %v", err)
	}
}

func TestSandboxesShareTheLiveLimitWithRuns(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	admit(t, s, agents.Admission{})
	admitSandbox(t, s, agents.SandboxAdmission{})
	admitSandbox(t, s, agents.SandboxAdmission{})
	if _, found, err := s.ClaimRun(ctx, "agentd", "run-a", ttl, maxLive(2)); err != nil || !found {
		t.Fatalf("claim run: %v %v", found, err)
	}
	if _, found, err := s.ClaimSandbox(ctx, "agentd", "sbx-a", ttl, maxLive(2)); err != nil || !found {
		t.Fatalf("claim the first session: %v %v", found, err)
	}
	if _, found, _ := s.ClaimSandbox(ctx, "agentd", "sbx-b", ttl, maxLive(2)); found {
		t.Fatal("a session was claimed past the account's live limit")
	}
}

func TestSandboxLeaseExpiry(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	starting := admitSandbox(t, s, agents.SandboxAdmission{})
	live := admitSandbox(t, s, agents.SandboxAdmission{})
	stale := admitSandbox(t, s, agents.SandboxAdmission{ExpiresAt: time.Now().Add(time.Hour)})
	for _, id := range []string{starting.ID, live.ID} {
		claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok-"+id, ttl, maxLive(5))
		if err != nil || !found || claim.ID != id {
			t.Fatalf("claim %s: %+v %v %v", id, claim, found, err)
		}
	}
	if err := s.MarkSandboxReady(ctx, live.ID, "tok-"+live.ID, "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET lease_expires_at=now()-interval '1 second' WHERE lease_token IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	requeued, failed, expired, err := s.ExpireSandboxLeases(ctx)
	if err != nil || requeued != 1 || failed != 1 || expired != 1 {
		t.Fatalf("expire = %d requeued, %d failed, %d expired, %v", requeued, failed, expired, err)
	}
	for id, want := range map[string]string{starting.ID: agents.SandboxQueued, live.ID: agents.SandboxFailed, stale.ID: agents.SandboxExpired} {
		if got, _ := s.Sandbox(ctx, account, id); got.State != want {
			t.Errorf("%s: state %s, want %s", id, got.State, want)
		}
	}
	if got, _ := s.Sandbox(ctx, account, live.ID); got.OutcomeCode != "lease_lost" {
		t.Fatalf("failed outcome %q", got.OutcomeCode)
	}
}
