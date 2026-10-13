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
	go func() { _ = (&dbarchive.Server{Store: store, Backups: store}).Serve(serveCtx, ln) }()
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "rjail"), VolumeRoot: filepath.Join(env.workdir, "rvolumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: &fakeLeaser{},
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
