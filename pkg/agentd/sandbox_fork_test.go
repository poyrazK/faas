// adr: 20261122132055 §3 — forks: agentd takes a running source's fork
// capture when apid asks, starts each fork from it at once on the capture's
// node, or once the shared store holds it on any node when that node refuses
// (its volume from the source's fork backup), keeps the capture
// and the backup while a fork waits, consumes the capture once none does,
// and fails a fork whose source ended before it was captured.
package agentd_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

func forkSandbox(t *testing.T, s *agentstore.Store, sourceID, kind string, count int) []string {
	t.Helper()
	forks, err := s.ForkSandbox(context.Background(), agentstore.SandboxForkAdmission{AccountID: account, SourceID: sourceID,
		Count: count, Kind: kind, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100, PersistentMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(forks))
	for _, f := range forks {
		if f.ForkedFrom != sourceID || f.State != agentstore.SandboxQueued || f.Kind != kind || f.VolumeGB != 10 {
			t.Fatalf("fork %+v", f)
		}
		ids = append(ids, f.ID)
	}
	return ids
}

func forkLocalRefused(t *testing.T, pool *pgxpool.Pool, id string) bool {
	t.Helper()
	var refused bool
	if err := pool.QueryRow(context.Background(),
		`SELECT fork_local_refused_at IS NOT NULL FROM sandbox_sessions WHERE id=$1`, id).Scan(&refused); err != nil {
		t.Fatal(err)
	}
	return refused
}

func lastStart(t *testing.T, fake *fakeSandboxSchedd, id string) *scheddpb.SandboxRunStart {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for i := len(fake.starts) - 1; i >= 0; i-- {
		if fake.starts[i].GetSandboxId() == id {
			return fake.starts[i]
		}
	}
	t.Fatalf("no start for %s", id)
	return nil
}

func TestSandboxForkOfARunningSession(t *testing.T) {
	pool, store := openStore(t)
	ctx := context.Background()
	src := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	blobs := &memStore{blob: map[string][]byte{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: blobs})
	srcDone := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, src).State == agentstore.SandboxReady })

	forks := forkSandbox(t, store, src, agentstore.SandboxEphemeral, 2)
	first, second := forks[0], forks[1]
	fake.mu.Lock()
	fake.scripts[second] = "full" // the capture's node has no room for it
	fake.mu.Unlock()
	waitFor(t, "the fork capture", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.forks) == 1
	})
	fake.mu.Lock()
	captureID := fake.forks[0]
	forkBackup := fake.volumeBackups[len(fake.volumeBackups)-1]
	fake.mu.Unlock()
	waitFor(t, "the fork recorded", func() bool { return backupStates(t, pool, src)[forkBackup] == "uploading" })
	// The source runs on. Its forks start at once on the capture's node; the
	// one that node refuses waits for the upload.
	if sb := sandboxState(t, store, src); sb.State != agentstore.SandboxReady {
		t.Fatalf("source %s after the fork", sb.State)
	}
	mem := "snap/sandbox-session-" + account + "/captures/" + captureID + "/v2/mem"
	// Either claimer may take either fork: the one driving the refused fork
	// returns, the other holds the first fork until it closes.
	holdA, holdB := holdSandbox(t, sbx), holdSandbox(t, sbx)
	waitFor(t, "the first fork ready on the capture's node", func() bool {
		return sandboxState(t, store, first).State == agentstore.SandboxReady
	})
	waitFor(t, "the second fork refused", func() bool { return forkLocalRefused(t, pool, second) })
	var forkDone <-chan bool
	select {
	case <-holdA:
		forkDone = holdB
	case <-holdB:
		forkDone = holdA
	case <-time.After(10 * time.Second):
		t.Fatal("the refused fork's claim did not return")
	}
	for _, id := range []string{first, second} {
		r := lastStart(t, fake, id).GetResume()
		if r == nil || !r.GetFork() || !r.GetRequireNode() || r.GetNodeId() == "" || r.GetStorageKey() != mem {
			t.Fatalf("fork %s before the upload: resume = %v", id, r)
		}
	}
	if sb := sandboxState(t, store, second); sb.State != agentstore.SandboxQueued {
		t.Fatalf("refused fork %s", sb.State)
	}
	if found, err := sbx.ClaimOnce(ctx); err != nil || found {
		t.Fatalf("a refused fork claimed before its capture was uploaded: %v %v", found, err)
	}

	snap := state.Snapshot{StorageKey: mem}
	for _, key := range []string{mem, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), volbackup.ManifestKey(account, src, forkBackup)} {
		if err := blobs.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	// The replica that recorded the capture watches its upload: the forks
	// become claimable without waiting for a sweep.
	waitFor(t, "the upload seen", func() bool { return backupStates(t, pool, src)[forkBackup] == "stored" })
	fake.mu.Lock()
	delete(fake.scripts, second)
	fake.mu.Unlock()

	// The source ends while the second fork waits: its volume and the fork
	// backup stay.
	if _, err := store.CloseSandbox(ctx, account, src); err != nil {
		t.Fatal(err)
	}
	<-srcDone
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := blobs.blob[volbackup.ManifestKey(account, src, forkBackup)]; !ok {
		t.Fatal("the fork backup went while a fork waits for it")
	}
	secondDone := holdSandbox(t, sbx)
	waitFor(t, "the second fork ready", func() bool { return sandboxState(t, store, second).State == agentstore.SandboxReady })
	// Each fork restores the capture with the source's volume backup; the
	// refused one, from the shared store on any node.
	for _, id := range []string{first, second} {
		start := lastStart(t, fake, id)
		r := start.GetResume()
		if r == nil || !r.GetFork() || r.GetStorageKey() != mem || start.GetPersistent() ||
			start.GetVolumeGb() != 10 || start.GetVolumeBackupId() != forkBackup || start.GetVolumeSourceSandboxId() != src ||
			start.GetVolumeBackupIntervalMs() != 0 || r.GetRequireNode() != (id == first) {
			t.Fatalf("fork %s start = %v", id, start)
		}
	}

	// No fork waits any more: the capture is consumed and its objects go,
	// then the source's volume and backups.
	for _, id := range []string{first, second} {
		if _, err := store.CloseSandbox(ctx, account, id); err != nil {
			t.Fatal(err)
		}
	}
	<-forkDone
	<-secondDone
	if _, err := pool.Exec(ctx, `UPDATE sandbox_session_captures SET created_at=now()-interval '2 minutes' WHERE id=$1`, captureID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := sbx.SweepOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := blobs.blob[mem]; ok {
		t.Fatal("the spent fork capture was kept")
	}
	if _, ok := blobs.blob[volbackup.ManifestKey(account, src, forkBackup)]; ok {
		t.Fatal("the ended source's fork backup was kept")
	}
}

func TestSandboxForkOfAPausedSessionAndOrphans(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	src := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	blobs := &memStore{blob: map[string][]byte{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: blobs})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, src).State == agentstore.SandboxReady })
	if _, err := store.PauseSandbox(ctx, account, src); err != nil {
		t.Fatal(err)
	}
	<-done

	// A paused source's forks restore its pause capture once it is uploaded.
	forks := forkSandbox(t, store, src, agentstore.SandboxPersistent, 1)
	fake.mu.Lock()
	pause, pauseBackup := fake.pauses[0], fake.volumeBackups[len(fake.volumeBackups)-1]
	fake.mu.Unlock()
	mem := "snap/sandbox-session-" + account + "/captures/" + pause + "/v2/mem"
	snap := state.Snapshot{StorageKey: mem}
	for _, key := range []string{mem, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), volbackup.ManifestKey(account, src, pauseBackup)} {
		blobs.blob[key] = []byte("x")
	}
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	done = holdSandbox(t, sbx)
	waitFor(t, "the fork ready", func() bool { return sandboxState(t, store, forks[0]).State == agentstore.SandboxReady })
	fake.mu.Lock()
	start := fake.starts[len(fake.starts)-1]
	fake.mu.Unlock()
	if r := start.GetResume(); r == nil || !r.GetFork() || r.GetStorageKey() != mem || !start.GetPersistent() ||
		start.GetVolumeBackupId() != pauseBackup || start.GetVolumeSourceSandboxId() != src ||
		start.GetVolumeBackupIntervalMs() == 0 {
		t.Fatalf("fork start = %v", start)
	}
	if sb := sandboxState(t, store, src); sb.State != agentstore.SandboxPaused {
		t.Fatalf("source %s after its fork started", sb.State)
	}
	if _, err := store.CloseSandbox(ctx, account, forks[0]); err != nil {
		t.Fatal(err)
	}
	<-done

	// A fork of a running source that ends before it is captured fails.
	src2 := queuePersistent(t, store, time.Hour)
	fake.mu.Lock()
	fake.scripts[src2] = "no-fork"
	fake.mu.Unlock()
	if _, err := store.ForkSandbox(ctx, agentstore.SandboxForkAdmission{AccountID: account, SourceID: src2, Count: 1,
		Kind: agentstore.SandboxEphemeral, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100, PersistentMax: 10}); !errors.Is(err, agentstore.ErrSandboxNotForkable) {
		t.Fatalf("a fork of a queued session: %v", err)
	}
	done = holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, src2).State == agentstore.SandboxReady })
	orphan := forkSandbox(t, store, src2, agentstore.SandboxEphemeral, 1)[0]
	waitFor(t, "the fork asked", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.forks) > 0
	})
	if _, err := store.CloseSandbox(ctx, account, src2); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, orphan); sb.State != agentstore.SandboxFailed || sb.OutcomeCode != "fork_source_ended" {
		t.Fatalf("orphaned fork %s %s", sb.State, sb.OutcomeCode)
	}
}
