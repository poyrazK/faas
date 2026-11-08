//go:build metal && linux

// adr: 1027 — a primary that runs short of volume space turns read-only
// before PostgreSQL would PANIC, keeps platform sessions working, gives up
// its ballast at the edge, and turns writable again once space is freed.
package dbvm

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestMetalStorageGuard(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-storage-metal"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "sjail"), VolumeRoot: filepath.Join(env.workdir, "svolumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: &fakeLeaser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := "9c2e5f3b-7d4a-4b6c-8e9f-2a3b4c5d6e7f"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	if err := rt.Start(ctx, BootRequest{ID: id, Major: 17, VcpuCount: 1, MemSizeMiB: 384}); err != nil {
		t.Fatal(err)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("storage-guard-metal-password-01"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return rt.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, rt, id, password)
	defer func() { _ = conn.Close(ctx) }()

	status := func() dbguest.StorageStatus {
		t.Helper()
		resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpStatus})
		if err != nil || resp.State != dbguest.StateReady || resp.Storage == nil {
			t.Fatalf("status = %+v, %v", resp, err)
		}
		return *resp.Storage
	}
	await := func(what string, ok func(dbguest.StorageStatus) bool) dbguest.StorageStatus {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for {
			st := status()
			if ok(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: storage still %+v", what, st)
			}
			time.Sleep(time.Second)
		}
	}
	// Uncompressed, unlogged rows: about 2 KiB of volume each, no WAL.
	if _, err := conn.Exec(ctx, `CREATE UNLOGGED TABLE big (b text); ALTER TABLE big ALTER b SET STORAGE PLAIN`); err != nil {
		t.Fatal(err)
	}
	fill := func(c *pgx.Conn, bytes int64) error {
		_, err := c.Exec(ctx, `INSERT INTO big SELECT repeat('x', 1800) FROM generate_series(1, $1::int)`, max(bytes/2048, 1))
		return err
	}
	sqlState := func(err error) string {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.Code
		}
		return ""
	}

	// A 1 GiB volume turns read-only under 128 MiB free. Let the guard's
	// first pass lay the ballast, then fill to about 100 MiB free in steps
	// it can keep up with.
	time.Sleep(12 * time.Second)
	start := status()
	if start.ReadOnly || start.Low || start.TotalBytes < 900<<20 {
		t.Fatalf("fresh database storage = %+v", start)
	}
	for st := start; st.FreeBytes > 100<<20; st = status() {
		if err := fill(conn, min(64<<20, st.FreeBytes-100<<20)); err != nil {
			if sqlState(err) == "25006" {
				break // the guard turned read-only between steps
			}
			t.Fatalf("fill at %d free: %v", st.FreeBytes, err)
		}
	}
	readOnly := await("read-only", func(s dbguest.StorageStatus) bool { return s.ReadOnly && s.Low })
	t.Logf("read-only at %d MiB free of %d MiB", readOnly.FreeBytes>>20, readOnly.TotalBytes>>20)

	// Customer writes fail, reads work, platform sessions are exempt.
	if _, err := conn.Exec(ctx, `INSERT INTO big VALUES ('refused')`); sqlState(err) != "25006" {
		t.Fatalf("write while read-only = %v, want 25006", err)
	}
	var rows int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM big`).Scan(&rows); err != nil || rows == 0 {
		t.Fatalf("read while read-only = %d, %v", rows, err)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpAdminSQL, SQL: "CREATE ROLE guard_exempt NOLOGIN"}); err != nil || resp.Error != "" {
		t.Fatalf("admin while read-only = %+v, %v", resp, err)
	}

	// A session that overrides read-only can fill to the edge; the guard
	// gives up the ballast, and PostgreSQL keeps running.
	if _, err := conn.Exec(ctx, `SET default_transaction_read_only = off`); err != nil {
		t.Fatal(err)
	}
	for st := status(); st.FreeBytes >= 32<<20; st = status() {
		if err := fill(conn, min(16<<20, st.FreeBytes-16<<20)); err != nil {
			if sqlState(err) == "53100" {
				break // disk full on the table: an ERROR, not a PANIC
			}
			t.Fatalf("override fill at %d free: %v", st.FreeBytes, err)
		}
	}
	edge := status()
	released := await("ballast released", func(s dbguest.StorageStatus) bool { return s.FreeBytes >= edge.FreeBytes+16<<20 })
	t.Logf("ballast released: %d MiB free -> %d MiB", edge.FreeBytes>>20, released.FreeBytes>>20)
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM big`).Scan(&rows); err != nil {
		t.Fatalf("database after the edge: %v", err)
	}

	// Freeing space turns it writable again, ballast laid back.
	if _, err := conn.Exec(ctx, `TRUNCATE big`); err != nil {
		t.Fatal(err)
	}
	writable := await("writable", func(s dbguest.StorageStatus) bool { return !s.ReadOnly && !s.Low })
	t.Logf("writable again at %d MiB free", writable.FreeBytes>>20)
	fresh := connectRuntime(ctx, t, rt, id, password)
	defer func() { _ = fresh.Close(ctx) }()
	if _, err := fresh.Exec(ctx, `INSERT INTO big VALUES ('accepted')`); err != nil {
		t.Fatalf("write after space came back: %v", err)
	}
	// The ballast is back with the writable mode (one guard pass lays it,
	// then decides): free space is near the start's, not a ballast more.
	if writable.FreeBytes >= start.FreeBytes+16<<20 {
		t.Fatalf("ballast not laid again: %d MiB free, %d MiB at the start", writable.FreeBytes>>20, start.FreeBytes>>20)
	}
}
