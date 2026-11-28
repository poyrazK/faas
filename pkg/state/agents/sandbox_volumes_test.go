// adr: 20261122132055 §4 — a persistent session's volume outlives its VM: a
// lost VM or lapsed lease pauses the session for a cold resume instead of
// ending it (which would delete the volume and its backups), and a grow
// while paused uses the capture up, whose memory holds the old filesystem.
package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

// liveWithVolume admits and starts a session under token, makes it
// persistent with a volume when volume is set, and returns its ID.
func liveWithVolume(t *testing.T, pool *pgxpool.Pool, s *agents.Store, token string, persistent, volume bool) string {
	t.Helper()
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{})
	claim, found, err := s.ClaimSandbox(ctx, "agentd", token, ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID {
		t.Fatalf("claim: %+v %v %v", claim, found, err)
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, token, "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	if persistent {
		if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET kind='persistent', volume_gb=10 WHERE id=$1`, sb.ID); err != nil {
			t.Fatal(err)
		}
	}
	if volume {
		if _, err := pool.Exec(ctx, `INSERT INTO sandbox_volumes (session_id, account_id, node_id, size_gb)
			SELECT id, account_id, 'node-a', 10 FROM sandbox_sessions WHERE id=$1`, sb.ID); err != nil {
			t.Fatal(err)
		}
	}
	return sb.ID
}

func TestLostPersistentSessionPausesForAColdResume(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	withVolume := liveWithVolume(t, pool, s, "tok-volume", true, true)
	without := liveWithVolume(t, pool, s, "tok-without", true, false)
	// Within the reattach grace both VMs may still run: a replica may take
	// them over (ADR-20261122132055 §7).
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET lease_expires_at=now()-interval '1 second' WHERE lease_token IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if requeued, failed, _, err := s.ExpireSandboxLeases(ctx); err != nil || requeued != 0 || failed != 0 {
		t.Fatalf("expire within the grace = %d requeued, %d failed, %v", requeued, failed, err)
	}
	// Past it (SandboxReattachGraceSec is minutes, not hours) the VMs are gone.
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET lease_expires_at=now()-interval '1 hour' WHERE lease_token IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	requeued, failed, _, err := s.ExpireSandboxLeases(ctx)
	if err != nil || requeued != 1 || failed != 1 {
		t.Fatalf("expire = %d requeued, %d failed, %v", requeued, failed, err)
	}
	got, err := s.Sandbox(ctx, account, withVolume)
	if err != nil || got.State != agents.SandboxPaused {
		t.Fatalf("a persistent session with a volume whose lease lapsed: %+v %v", got, err)
	}
	// Pro keeps a paused session 30 days.
	if left := time.Until(got.ExpiresAt); left < 29*24*time.Hour || left > 31*24*time.Hour {
		t.Fatalf("paused for %s, want the plan's retention", left)
	}
	if got, _ := s.Sandbox(ctx, account, without); got.State != agents.SandboxFailed {
		t.Fatalf("a persistent session without a volume = %s, want failed", got.State)
	}

	// agentd saw the VM end while it held the lease.
	held := liveWithVolume(t, pool, s, "tok-held", true, true)
	ephemeral := liveWithVolume(t, pool, s, "tok-ephemeral", false, false)
	if paused, err := s.PauseLostSandbox(ctx, ephemeral, "tok-ephemeral", time.Hour); err != nil || paused {
		t.Fatalf("an ephemeral session paused: %v %v", paused, err)
	}
	if paused, err := s.PauseLostSandbox(ctx, held, "tok-other", time.Hour); err != nil || paused {
		t.Fatalf("another lease paused the session: %v %v", paused, err)
	}
	if paused, err := s.PauseLostSandbox(ctx, held, "tok-held", time.Hour); err != nil || !paused {
		t.Fatalf("a lost VM's session was not paused: %v %v", paused, err)
	}
	if got, _ := s.Sandbox(ctx, account, held); got.State != agents.SandboxPaused {
		t.Fatalf("lost VM's session = %s, want paused", got.State)
	}
}

func TestGrowPausedVolumeUsesUpTheCapture(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	id := liveWithVolume(t, pool, s, "tok-a", true, true)
	if err := s.BeginSandboxPause(ctx, id, "tok-a"); err != nil {
		t.Fatal(err)
	}
	const captureID = "44444444-4444-4444-4444-444444444444"
	prefix := "snap/sandbox-session-" + account + "/captures/" + captureID + "/v2/"
	capture := agents.SandboxCapture{ID: captureID, NodeID: "node-a", StorageKey: prefix + "mem",
		VMStateStorageKey: prefix + "vmstate", FCVersion: "1.10.0", MemBytes: 1 << 30}
	if err := s.MarkSandboxPaused(ctx, id, "tok-a", capture, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	captureState := func() string {
		t.Helper()
		var st string
		if err := pool.QueryRow(ctx, `SELECT state FROM sandbox_session_captures WHERE id=$1`, capture.ID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if grown, err := s.GrowPausedSandboxVolume(ctx, account, id, 10); err != nil || !grown || captureState() != "live" {
		t.Fatalf("the same size = %v %v, capture %s", grown, err, captureState())
	}
	if grown, err := s.GrowPausedSandboxVolume(ctx, account, id, 20); err != nil || !grown || captureState() != "consumed" {
		t.Fatalf("a larger size = %v %v, capture %s; the resume would restore memory over a grown filesystem", grown, err, captureState())
	}
}
