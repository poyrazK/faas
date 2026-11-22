// adr: 20261122132055 §1, §2 — a persistent session pauses instead of
// ending: on request or at its run limit agentd asks vmmd to capture it,
// records the capture and leaves the session paused with no lease and no VM;
// a refused pause leaves it running and is tried again. A resume request
// makes it claimable: the claim restores its capture, preferring its node,
// marks the session ready with the capture used, and the sweep deletes the
// used capture's objects. A paused session closed or past its retention ends
// without a VM.
package agentd_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

func queuePersistent(t *testing.T, s *agentstore.Store, runFor time.Duration) string {
	t.Helper()
	sb, err := s.CreateSandbox(context.Background(), agentstore.SandboxAdmission{AccountID: account, Kind: agentstore.SandboxPersistent,
		Preset: "codex", RAMMB: 2048, VCPU: 2, VolumeGB: 10, NetworkMode: "registries", IdleTimeoutSec: 600,
		ExpiresAt: time.Now().Add(runFor), QueuedMax: 100, PersistentMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	return sb.ID
}

func TestPersistentSandboxPausesOnRequestAndResumes(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{}
	blobs := &memStore{blob: map[string][]byte{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: blobs})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if start := fake.starts[0]; !start.GetPersistent() || start.GetVcpu() != 2 || start.GetRamMb() != 2048 || start.GetResume() != nil {
		t.Fatalf("start = %v", start)
	}
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxPaused || sb.PausedAt == nil || sb.PauseRequestedAt != nil || sb.Durability != "node" ||
		sb.NodeID != "" || sb.InstanceID != "" || sb.ClosedAt != nil {
		t.Fatalf("paused session = %+v", sb)
	}
	if _, _, closes := fake.counts(); closes != 0 || len(fake.pauses) != 1 {
		t.Fatalf("%d closes, pauses %v", closes, fake.pauses)
	}
	// A paused session is claimable only once asked to resume.
	if found, err := sbx.ClaimOnce(ctx); err != nil || found {
		t.Fatalf("claimed a paused session nobody resumed: %v %v", found, err)
	}
	// adr: 20261122132055 §2 — the capture is on its node until the shared
	// store holds all of it; then its durability is remote.
	capture := fake.pauses[0]
	mem := "snap/sandbox-session-" + account + "/captures/" + capture + "/v2/mem"
	snap := state.Snapshot{StorageKey: mem}
	blobs.blob[mem] = []byte("x")
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, id); sb.Durability != "node" {
		t.Fatalf("durability %q with part of the capture uploaded", sb.Durability)
	}
	for _, key := range []string{state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		blobs.blob[key] = []byte("x")
	}
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, id); sb.Durability != "node" {
		t.Fatalf("durability %q without the volume backup", sb.Durability)
	}
	backups, _ := fake.volumeState()
	pauseBackup := backups[len(backups)-1]
	blobs.blob[volbackup.ManifestKey(account, id, pauseBackup)] = []byte("{}")
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, id); sb.Durability != "remote" || sb.State != agentstore.SandboxPaused {
		t.Fatalf("an uploaded capture = %s %q", sb.State, sb.Durability)
	}
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	done = holdSandbox(t, sbx)
	waitFor(t, "resumed", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	resume := fake.starts[1].GetResume()
	if resume == nil || resume.GetStorageKey() != mem || resume.GetNodeId() != "agents-1" || resume.GetFcVersion() != "1.10.0" ||
		resume.GetMemBytes() != 2<<30 || fake.starts[1].GetWorkspaceKey() != "" || resume.GetRequireNode() ||
		fake.starts[1].GetVolumeGb() != 10 || fake.starts[1].GetVolumeBackupId() != pauseBackup {
		t.Fatalf("resume start = %v", fake.starts[1])
	}
	sb = sandboxState(t, store, id)
	if sb.ResumedCold || sb.PausedAt != nil || sb.Durability != "" || sb.NodeID != "agents-1" || !sb.ExpiresAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("resumed session = %+v", sb)
	}
	// The capture is used: the sweep deletes its objects and its row.
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for key := range blobs.blob {
		if strings.Contains(key, capture) {
			t.Fatalf("the used capture's %s was kept", key)
		}
	}
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxClosed {
		t.Fatalf("closed session = %s", sb.State)
	}
}

func TestPersistentSandboxPausesAtItsRunLimitAfterARefusal(t *testing.T) {
	_, store := openStore(t)
	id := queuePersistent(t, store, 400*time.Millisecond)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	fake.scripts[id] = "pause-busy"
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{}))
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxPaused || len(fake.pauses) != 2 {
		t.Fatalf("session = %s after pauses %v", sb.State, fake.pauses)
	}
	// It expires its retention from the pause, not its run limit.
	if !sb.ExpiresAt.After(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("paused session expires %v", sb.ExpiresAt)
	}
}

// A pause vmmd answers after agentd stopped waiting is still taken: the
// session went back to ready, asks no other pause meanwhile, and pauses
// with the late capture rather than failing as a lost VM.
func TestPersistentSandboxTakesALateCapture(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}, lateBy: 900 * time.Millisecond}
	fake.scripts[id] = "pause-late"
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Tick: 50 * time.Millisecond, PauseTimeout: 400 * time.Millisecond})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "back to ready after the pause timed out", func() bool {
		fake.mu.Lock()
		asked := len(fake.pauses)
		fake.mu.Unlock()
		return asked == 1 && sandboxState(t, store, id).State == agentstore.SandboxReady
	})
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxPaused || sb.ClosedAt != nil || len(fake.pauses) != 1 {
		t.Fatalf("session = %+v after pauses %v", sb, fake.pauses)
	}
}

// A persistent session whose every pause is refused ends a while past its
// run limit, as an ephemeral one ends at its lifetime, rather than run on.
func TestPersistentSandboxEndsWhenItCannotPause(t *testing.T) {
	_, store := openStore(t)
	id := queuePersistent(t, store, 400*time.Millisecond)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	fake.scripts[id] = "pause-refused"
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{PersistentOverrun: 300 * time.Millisecond}))
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxExpired || sb.OutcomeCode != "lifetime" || len(fake.pauses) == 0 {
		t.Fatalf("session = %+v after pauses %v", sb, fake.pauses)
	}
	if _, _, closes := fake.counts(); closes != 1 {
		t.Fatalf("%d closes, want the VM closed", closes)
	}
}

// pausedSession is a persistent session paused once, with its capture.
func pausedSession(t *testing.T, store *agentstore.Store, sbx *agentd.Sandboxes, fake *fakeSandboxSchedd) (id, capture string) {
	t.Helper()
	ctx := context.Background()
	id = queuePersistent(t, store, time.Hour)
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return id, fake.pauses[len(fake.pauses)-1]
}

func consumedCapture(t *testing.T, store *agentstore.Store, id string) (agentstore.SandboxCapture, bool) {
	t.Helper()
	captures, err := store.ConsumedSandboxCaptures(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range captures {
		if c.ID == id {
			return c, true
		}
	}
	return agentstore.SandboxCapture{}, false
}

// A session closed while it resumes never restores its capture: the
// capture is used, so the sweep deletes it rather than keep it live.
func TestSandboxClosedWhileResumingUsesItsCapture(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	id, capture := pausedSession(t, store, sbx, fake)
	fake.mu.Lock()
	fake.scripts[id] = "resume-stall"
	fake.mu.Unlock()
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	done := holdSandbox(t, sbx)
	waitFor(t, "resuming", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxResuming })
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxClosed {
		t.Fatalf("session = %s", sb.State)
	}
	if _, ok := consumedCapture(t, store, capture); !ok {
		t.Fatal("the capture of a session closed while resuming is still live")
	}
}

// A resume refused for want of its capture's node goes back to paused and
// is not claimed again at once.
func TestSandboxResumeRefusedForItsNodeBacksOff(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	id, _ := pausedSession(t, store, sbx, fake)
	fake.mu.Lock()
	fake.scripts[id] = "capture-node-busy"
	fake.mu.Unlock()
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
		t.Fatalf("claim = %v, %v", found, err)
	}
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxPaused || sb.ResumeRequestedAt == nil {
		t.Fatalf("a refused resume = %+v", sb)
	}
	if found, err := sbx.ClaimOnce(ctx); err != nil || found {
		t.Fatalf("claimed again at once: %v, %v", found, err)
	}
}

// A pause whose answer never reached agentd may still have written its
// capture: it is recorded used under its key, so the sweep deletes it.
func TestSandboxPauseLostWithItsStreamIsSwept(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	fake.scripts[id] = "pause-break"
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxFailed {
		t.Fatalf("session = %s", sb.State)
	}
	c, ok := consumedCapture(t, store, fake.pauses[0])
	want := "snap/sandbox-session-" + account + "/captures/" + fake.pauses[0] + "/v2/mem"
	if !ok || c.StorageKey != want || !c.HasBacking {
		t.Fatalf("abandoned capture = %+v (recorded %v), want key %s", c, ok, want)
	}
}

func TestPersistentSandboxResumesColdWhenTheRestoreFails(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.scripts[id] = "restore-fails"
	fake.mu.Unlock()
	done = holdSandbox(t, sbx)
	waitFor(t, "resumed", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if sb := sandboxState(t, store, id); !sb.ResumedCold {
		t.Fatalf("a cold resume = %+v", sb)
	}
	// Nothing was uploaded, so only the capture's node could restore it.
	if r := fake.starts[1].GetResume(); r == nil || !r.GetRequireNode() || r.GetNodeId() != "agents-1" {
		t.Fatalf("a node-only resume = %v", r)
	}
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestPausedSandboxClosedWithoutAVM(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{}
	blobs := &memStore{blob: map[string][]byte{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: blobs})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	mem := "snap/sandbox-session-" + account + "/captures/" + fake.pauses[0] + "/v2/mem"
	blobs.blob[mem] = []byte("x")
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxClosed || sb.OutcomeCode != "closed" || sb.ClosedAt == nil {
		t.Fatalf("closed paused session = %+v", sb)
	}
	// The node is still uploading the capture: deleting the part that landed
	// would let the rest land after it with no row to sweep it.
	if _, ok := blobs.blob[mem]; !ok {
		t.Fatal("a capture still uploading was deleted")
	}
	snap := state.Snapshot{StorageKey: mem}
	for _, key := range []string{state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap)} {
		blobs.blob[key] = []byte("x")
	}
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(blobs.blob) != 0 {
		t.Fatalf("a closed session's uploaded capture was kept: %v", blobs.blob)
	}
	if _, _, closes := fake.counts(); closes != 0 {
		t.Fatalf("a paused session has no VM to close, got %d closes", closes)
	}
}
