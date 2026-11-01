//go:build metal && linux

// adr: 1027 — base backups and point-in-time restore in jailed VMs: a base
// backup lands in the archive with its history file, and an empty volume
// boots as the source recovered to an instant between two commits, then
// promotes, takes writes and archives on its own.
package dbvm

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestMetalPointInTimeRestore(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-restore-metal-test"
	parentDir := filepath.Join("/sys/fs/cgroup", parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(parentDir) })
	storeDir := filepath.Join(env.workdir, "restore-store")
	_ = os.RemoveAll(storeDir)
	t.Cleanup(func() { _ = os.RemoveAll(storeDir) })
	store, err := dbarchive.NewLocalStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(env.workdir, "restore.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	t.Cleanup(stopServe)
	tiered := &dbarchive.TieredStore{LocalStore: store}
	go func() {
		_ = (&dbarchive.Server{Store: tiered, Backups: tiered, Retention: tiered}).Serve(serveCtx, ln)
	}()
	leases := &fakeLeaser{}
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "rjail"), VolumeRoot: filepath.Join(env.workdir, "rvolumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: leases,
		ArchiveSocket: sock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	const source, restored, password = "6a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "7b2c3d4e-5f6a-4b7c-9d8e-0f1a2b3c4d5e", "restore-metal-password"
	for _, id := range []string{source, restored} {
		_ = rt.DeleteVolume(ctx, id)
		if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	}
	var stops []time.Duration
	stop := func(id string) {
		t.Helper()
		started := time.Now()
		if err := rt.Stop(ctx, id); err != nil {
			t.Fatal(err)
		}
		stops = append(stops, time.Since(started).Round(time.Millisecond))
	}
	control := func(id string, req dbguest.Request) dbguest.Response {
		t.Helper()
		resp, err := rt.Control(ctx, id, req)
		if err != nil || resp.Error != "" {
			t.Fatalf("%s %s: %+v %v", id[:8], req.Op, resp, err)
		}
		return resp
	}

	if err := rt.Start(ctx, BootRequest{ID: source, Major: 17, VcpuCount: 1, MemSizeMiB: 384}); err != nil {
		t.Fatalf("start source: %v", err)
	}
	control(source, dbguest.Request{Op: dbguest.OpBootstrap})
	if _, err := rt.WaitState(ctx, source, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return rt.Control(ctx, source, req) }, password)
	conn := connectRuntime(ctx, t, rt, source, password)
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec("CREATE TABLE marks (v text PRIMARY KEY)")
	exec("INSERT INTO marks VALUES ('before-backup')")
	// Ship a segment that predates the backup, for pruning to remove.
	control(source, dbguest.Request{Op: dbguest.OpFlushWAL, After: time.Now()})
	started := time.Now()
	backup := control(source, dbguest.Request{Op: dbguest.OpBaseBackup}).Backup
	backupTook := time.Since(started)
	if !dbarchive.ValidBackupID(backup) {
		t.Fatalf("backup id %q", backup)
	}
	exec("INSERT INTO marks VALUES ('after-backup')")
	time.Sleep(1100 * time.Millisecond)
	point := time.Now()
	time.Sleep(1100 * time.Millisecond)
	exec("INSERT INTO marks VALUES ('after-point')")
	_ = conn.Close(ctx)
	started = time.Now()
	control(source, dbguest.Request{Op: dbguest.OpFlushWAL, After: point})
	flushTook := time.Since(started)
	stop(source)
	if _, err := os.Stat(filepath.Join(storeDir, source, "base", backup+".json")); err != nil {
		t.Fatalf("backup manifest: %v", err)
	}
	if !hasHistoryFile(t, filepath.Join(storeDir, source, "wal")) {
		t.Fatal("the backup's history file was not archived")
	}
	// Prune to a retention ending now (with slack for the guest clock):
	// only the backup and the WAL from its start segment stay, and the
	// restore below must still work.
	pruned, err := tiered.Prune(ctx, source, time.Now().Add(30*time.Second))
	if err != nil || pruned.WAL == 0 {
		t.Fatalf("prune = %+v, %v; want WAL before the backup removed", pruned, err)
	}

	started = time.Now()
	if err := rt.Start(ctx, BootRequest{ID: restored, Major: 17, VcpuCount: 1, MemSizeMiB: 384, RestoreSource: source, RestoreTarget: point}); err != nil {
		t.Fatalf("start restore: %v", err)
	}
	resp, err := rt.WaitState(ctx, restored, dbguest.StateReady, dbguest.StateFailed)
	if err != nil || resp.State != dbguest.StateReady {
		t.Fatalf("restore = %+v, %v", resp, err)
	}
	restoreTook := time.Since(started)
	conn = connectRuntime(ctx, t, rt, restored, password)
	rows, err := conn.Query(ctx, "SELECT v FROM marks ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	rows.Close()
	if strings.Join(got, ",") != "after-backup,before-backup" {
		t.Fatalf("restored rows = %v; want the two commits before the point", got)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO marks VALUES ('restored-write')"); err != nil {
		t.Fatalf("restored database is not writable: %v", err)
	}
	_ = conn.Close(ctx)
	// The restore archives on its own timeline and backs up on its own.
	if b := control(restored, dbguest.Request{Op: dbguest.OpBaseBackup}).Backup; !dbarchive.ValidBackupID(b) {
		t.Fatalf("restored backup id %q", b)
	}
	stop(restored)
	if !hasHistoryFile(t, filepath.Join(storeDir, restored, "wal")) {
		t.Fatal("the restore archived nothing of its own")
	}
	// A later boot is an ordinary start of the restored volume.
	if err := rt.Start(ctx, BootRequest{ID: restored, Major: 17, VcpuCount: 1, MemSizeMiB: 384}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.WaitState(ctx, restored, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	conn = connectRuntime(ctx, t, rt, restored, password)
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM marks WHERE v = 'restored-write'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("restored write after reboot = %d, %v", n, err)
	}
	_ = conn.Close(ctx)
	stop(restored)
	// Deleting a database drops its archive through the admin header.
	if err := rt.DropArchive(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, restored)); !os.IsNotExist(err) {
		t.Fatalf("archive after drop: %v", err)
	}
	t.Logf("pruned %d WAL files before the restore", pruned.WAL)

	// Node loss: the source's archive survives only in the bucket, and a
	// node with an empty archive restores it from there.
	bucket := &dbarchive.FileRemote{Dir: filepath.Join(env.workdir, "rbucket")}
	_ = os.RemoveAll(bucket.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(bucket.Dir) })
	if _, err := (&dbarchive.Uploader{Store: store, Remote: bucket, Prefix: "native"}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(storeDir, source)); err != nil {
		t.Fatal(err)
	}
	nodeB := filepath.Join(env.workdir, "rnodeb")
	_ = os.RemoveAll(nodeB)
	t.Cleanup(func() { _ = os.RemoveAll(nodeB) })
	storeB, err := dbarchive.NewLocalStore(filepath.Join(nodeB, "archive"))
	if err != nil {
		t.Fatal(err)
	}
	tieredB := &dbarchive.TieredStore{LocalStore: storeB, Remote: bucket, Prefix: "native"}
	sockB := filepath.Join(nodeB, "b.sock")
	lnB, err := net.Listen("unix", sockB)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = (&dbarchive.Server{Store: tieredB, Backups: tieredB, Retention: tieredB}).Serve(serveCtx, lnB)
	}()
	rtB, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "bj"), VolumeRoot: filepath.Join(nodeB, "volumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: leases,
		ArchiveSocket: sockB,
	})
	if err != nil {
		t.Fatal(err)
	}
	const fromBucket = "8c3d4e5f-6a7b-4c8d-ae9f-1a2b3c4d5e6f"
	if err := rtB.CreateVolume(ctx, fromBucket, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rtB.Stop(context.Background(), fromBucket)
		_ = rtB.DeleteVolume(context.Background(), fromBucket)
	})
	started = time.Now()
	if err := rtB.Start(ctx, BootRequest{ID: fromBucket, Major: 17, VcpuCount: 1, MemSizeMiB: 384, RestoreSource: source, RestoreTarget: point}); err != nil {
		t.Fatalf("start bucket restore: %v", err)
	}
	if resp, err := rtB.WaitState(ctx, fromBucket, dbguest.StateReady, dbguest.StateFailed); err != nil || resp.State != dbguest.StateReady {
		t.Fatalf("bucket restore = %+v, %v", resp, err)
	}
	bucketTook := time.Since(started)
	conn = connectRuntime(ctx, t, rtB, fromBucket, password)
	var marks string
	if err := conn.QueryRow(ctx, "SELECT string_agg(v, ',' ORDER BY v) FROM marks").Scan(&marks); err != nil || marks != "after-backup,before-backup" {
		t.Fatalf("rows restored from the bucket = %q, %v", marks, err)
	}
	_ = conn.Close(ctx)
	if err := rtB.Stop(ctx, fromBucket); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nodeB, "archive", source)); !os.IsNotExist(err) {
		t.Fatalf("node B kept the source's archive: %v", err)
	}
	t.Logf("restore from the bucket to ready %v", bucketTook.Round(time.Millisecond))
	t.Logf("base backup %v, flush %v, restore to ready %v, stops %v", backupTook.Round(time.Millisecond), flushTook.Round(time.Millisecond), restoreTook.Round(time.Millisecond), stops)
}

func hasHistoryFile(t *testing.T, walDir string) bool {
	t.Helper()
	entries, err := os.ReadDir(walDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".backup") || strings.HasSuffix(e.Name(), ".history") {
			return true
		}
	}
	return false
}
