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
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	capture := fake.pauses[0]
	mem := "snap/sandbox-session-" + account + "/captures/" + capture + "/v2/mem"
	snap := state.Snapshot{StorageKey: mem}
	for _, key := range []string{mem, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		blobs.blob[key] = []byte("x")
	}
	done = holdSandbox(t, sbx)
	waitFor(t, "resumed", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	resume := fake.starts[1].GetResume()
	if resume == nil || resume.GetStorageKey() != mem || resume.GetNodeId() != "agents-1" || resume.GetFcVersion() != "1.10.0" ||
		resume.GetMemBytes() != 2<<30 || fake.starts[1].GetWorkspaceKey() != "" {
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
	if _, ok := blobs.blob[mem]; ok {
		t.Fatal("a closed session's capture was kept")
	}
	if _, _, closes := fake.counts(); closes != 0 {
		t.Fatalf("a paused session has no VM to close, got %d closes", closes)
	}
}
