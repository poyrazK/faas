//go:build metal && linux

// adr: 1027 — WAL archiving: PostgreSQL's archive_command in a jailed VM
// ships segments through guest-init and the runtime's relay into a
// dbarchive store, and a clean stop drains the final segment.
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

func archivedSegments(t *testing.T, dir, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, id, "wal"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if dbarchive.ValidWALName(e.Name()) && !strings.Contains(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestMetalWALArchiving(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-archive-metal-test"
	parentDir := filepath.Join("/sys/fs/cgroup", parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(parentDir) })
	storeDir := filepath.Join(env.workdir, "archive-store")
	_ = os.RemoveAll(storeDir)
	t.Cleanup(func() { _ = os.RemoveAll(storeDir) })
	store, err := dbarchive.NewLocalStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(env.workdir, "arch.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	t.Cleanup(stopServe)
	go func() { _ = (&dbarchive.Server{Store: store}).Serve(serveCtx, ln) }()

	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "archjail"), VolumeRoot: filepath.Join(env.workdir, "archvolumes"),
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := "5e2f7a9c-3b4d-4e6f-8a1b-2c3d4e5f6a7b"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	boot := BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("start: %v", err)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	sql := "CREATE TABLE archived AS SELECT g FROM generate_series(1, 200000) g;\nSELECT pg_switch_wal();\n"
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpAdminSQL, SQL: sql}); err != nil || resp.Error != "" {
		t.Fatalf("admin sql: %+v %v", resp, err)
	}
	var before []string
	deadline := time.Now().Add(time.Minute)
	for {
		if before = archivedSegments(t, storeDir, id); len(before) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no WAL segment reached the archive")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpAdminSQL, SQL: "INSERT INTO archived VALUES (-1);\n"}); err != nil || resp.Error != "" {
		t.Fatalf("admin sql: %+v %v", resp, err)
	}
	// A clean stop switches WAL and waits for the archiver: the row just
	// written must be in an archived segment when the VM is gone.
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	after := archivedSegments(t, storeDir, id)
	if len(after) <= len(before) {
		t.Fatalf("stop did not drain the final segment: %v before, %v after", before, after)
	}
	// Archiving keeps working across a wake.
	if err := rt.Start(ctx, boot); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	t.Logf("archived %d segments (%d before the stop drain)", len(archivedSegments(t, storeDir, id)), len(before))
}
