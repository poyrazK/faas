// adr: 20261122132055 §4 — a persistent session's workspace volume: agentd
// asks vmmd for it with its newest backup, records the node holding the
// newest copy and each backup, resumes with the pause's backup, deletes the
// copy on a node the session left and backups a newer one superseded, and
// deletes an ended session's volume and backups.
package agentd_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

func volumeRow(t *testing.T, pool *pgxpool.Pool, id string) (node, latest string, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT node_id, coalesce(latest_backup_id::text, '') FROM sandbox_volumes WHERE session_id=$1`, id).
		Scan(&node, &latest)
	return node, latest, err == nil
}

func backupStates(t *testing.T, pool *pgxpool.Pool, id string) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text, state FROM sandbox_volume_backups WHERE session_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var bid, st string
		if err := rows.Scan(&bid, &st); err != nil {
			t.Fatal(err)
		}
		out[bid] = st
	}
	return out
}

func TestPersistentSandboxVolumeLifecycle(t *testing.T) {
	pool, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	blobs := &memStore{blob: map[string][]byte{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: blobs})

	// A new session gets its volume, never backed up, backed up while it runs.
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if start := fake.starts[0]; start.GetVolumeGb() != 10 || start.GetVolumeBackupId() != "" ||
		start.GetVolumeBackupIntervalMs() != int64(api.SandboxVolumeBackupSec)*1000 {
		t.Fatalf("start = %v", start)
	}
	waitFor(t, "the running backup", func() bool { _, latest, _ := volumeRow(t, pool, id); return latest != "" })
	backups, _ := fake.volumeState()
	running := backups[0]
	if node, latest, _ := volumeRow(t, pool, id); node != "agents-1" || latest != running {
		t.Fatalf("volume on %q, newest %q", node, latest)
	}

	// The pause backs it up as the capture left it; that is its newest.
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	backups, _ = fake.volumeState()
	pause := backups[1]
	if _, latest, _ := volumeRow(t, pool, id); latest != pause || backupStates(t, pool, id)[pause] != "uploading" {
		t.Fatalf("after the pause: newest %q, backups %v", latest, backupStates(t, pool, id))
	}
	// The pause's upload lands with the capture: durability remote, the
	// backup stored, and the running one superseded and deleted.
	mem := "snap/sandbox-session-" + account + "/captures/" + fake.pauses[0] + "/v2/mem"
	snap := state.Snapshot{StorageKey: mem}
	for _, key := range []string{mem, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap),
		volbackup.ManifestKey(account, id, pause), volbackup.ManifestKey(account, id, running)} {
		blobs.blob[key] = []byte("x")
	}
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, id); sb.Durability != "remote" {
		t.Fatalf("durability %q", sb.Durability)
	}
	if st := backupStates(t, pool, id); st[pause] != "stored" || st[running] != "deleted" {
		t.Fatalf("backups %v", st)
	}
	if _, ok := blobs.blob[volbackup.ManifestKey(account, id, running)]; ok {
		t.Fatal("the superseded backup's manifest was kept")
	}

	// It grows while paused, never shrinks.
	if ok, err := store.GrowPausedSandboxVolume(ctx, account, id, 20); err != nil || !ok {
		t.Fatalf("grow = %v %v", ok, err)
	}
	if ok, err := store.GrowPausedSandboxVolume(ctx, account, id, 5); err != nil || ok {
		t.Fatalf("shrink = %v %v", ok, err)
	}

	// The resume lands on another node: it asks for the pause's backup at
	// the grown size, and the copy on the node it left goes.
	fake.mu.Lock()
	fake.scripts[id] = "elsewhere"
	fake.mu.Unlock()
	if _, err := store.ResumeSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	done = holdSandbox(t, sbx)
	waitFor(t, "resumed", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	if start := fake.starts[1]; start.GetVolumeGb() != 20 || start.GetVolumeBackupId() != pause {
		t.Fatalf("resume start = %v", start)
	}
	waitFor(t, "the stale copy deleted", func() bool {
		_, deletes := fake.volumeState()
		return slices.Contains(deletes, "agents-1/"+id)
	})
	if node, _, _ := volumeRow(t, pool, id); node != "agents-2" {
		t.Fatalf("volume on %q after the move", node)
	}

	// The session ends: its node copy, every backup and the row go.
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, deletes := fake.volumeState(); !slices.Contains(deletes, "agents-2/"+id) {
		t.Fatalf("deletes = %v", deletes)
	}
	if _, _, ok := volumeRow(t, pool, id); ok {
		t.Fatal("an ended session's volume row was kept")
	}
	for bid, st := range backupStates(t, pool, id) {
		if st != "deleted" {
			t.Fatalf("backup %s is %s", bid, st)
		}
		if _, ok := blobs.blob[volbackup.ManifestKey(account, id, bid)]; ok {
			t.Fatalf("backup %s's manifest was kept", bid)
		}
	}
}
