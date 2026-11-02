//go:build metal && linux

// adr: 1027 — an in-place major upgrade: pg_upgrade's check refuses a
// PostgreSQL 14 cluster it cannot carry (a reg* column) and leaves it
// intact; once that is gone, the cluster, crashed even, upgrades to 17 with
// pg_upgrade --link, keeping its rows, extension, function and sequence.
package dbvm

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestMetalMajorUpgrade(t *testing.T) {
	env := loadMetalEnv(t)
	base14 := filepath.Join(env.workdir, "base-postgres-14.ext4")
	if _, err := os.Stat(base14); os.IsNotExist(err) {
		buildMetalBase(t, PostgresImages[14], os.Getenv("FAAS_GUEST_INIT"), base14)
	}
	parent := "dbvm-upgrade-metal"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "ujail"), VolumeRoot: filepath.Join(env.workdir, "uvolumes"),
		KernelPath: env.kernel, Bases: map[int]string{14: base14, 17: env.base}, ParentCgroup: parent,
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
	id := "2a4c6e8f-1b3d-4f5a-9c7e-0d2f4a6c8e1b"
	_ = rt.DeleteVolume(ctx, id)
	if err := rt.CreateVolume(ctx, id, 1<<30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background(), id); _ = rt.DeleteVolume(context.Background(), id) })
	boot := func(major, from int) dbguest.Response {
		t.Helper()
		if err := rt.Start(ctx, BootRequest{ID: id, Major: major, VcpuCount: 2, MemSizeMiB: 512, UpgradeFrom: from}); err != nil {
			t.Fatalf("start %d (from %d): %v", major, from, err)
		}
		resp, err := rt.WaitState(ctx, id, dbguest.StateReady, dbguest.StateFailed, dbguest.StateNeedsBootstrap)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	versionNum := func(conn *pgx.Conn) int {
		t.Helper()
		var v int
		if err := conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// A PostgreSQL 14 database with rows, an extension, a function and a
	// sequence position.
	if resp := boot(14, 0); resp.State != dbguest.StateNeedsBootstrap {
		t.Fatalf("fresh volume = %+v", resp)
	}
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
		t.Fatalf("bootstrap: %+v %v", resp, err)
	}
	if _, err := rt.WaitState(ctx, id, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString([]byte("major-upgrade-metal-password-1"))
	createAppRole(t, func(req dbguest.Request) (dbguest.Response, error) { return rt.Control(ctx, id, req) }, password)
	conn := connectRuntime(ctx, t, rt, id, password)
	if v := versionNum(conn); v/10000 != 14 {
		t.Fatalf("server version %d, want 14", v)
	}
	if _, err := conn.Exec(ctx, `CREATE EXTENSION pgcrypto;
CREATE TABLE accounts (id bigserial PRIMARY KEY, email text NOT NULL, secret text NOT NULL);
INSERT INTO accounts (email, secret) SELECT 'user' || g || '@example.com', crypt('pw' || g, gen_salt('bf', 4)) FROM generate_series(1, 2000) g;
CREATE FUNCTION account_count() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM accounts';
CREATE INDEX accounts_email ON accounts (email);
-- pg_upgrade cannot carry reg* columns: its check refuses the cluster.
CREATE TABLE blocker (r regprocedure);`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	// A cluster pg_upgrade cannot carry is refused, untouched, and boots
	// its old major again.
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if resp := boot(17, 14); resp.State != dbguest.StateFailed || resp.Error != dbguest.ErrCodeUpgradeRefused {
		t.Fatalf("upgrade of a cluster with a reg* column = %+v; want %s", resp, dbguest.ErrCodeUpgradeRefused)
	}
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if resp := boot(14, 0); resp.State != dbguest.StateReady {
		t.Fatalf("the refused cluster on 14 = %+v", resp)
	}
	conn = connectRuntime(ctx, t, rt, id, password)
	var count int64
	if err := conn.QueryRow(ctx, `SELECT account_count()`).Scan(&count); err != nil || count != 2000 {
		t.Fatalf("after the refusal: %d rows, %v", count, err)
	}
	if v := versionNum(conn); v/10000 != 14 {
		t.Fatalf("server version %d after the refusal, want 14", v)
	}
	if _, err := conn.Exec(ctx, `DROP TABLE blocker`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	// A cluster that crashed upgrades too: pg_upgrade recovers it on its
	// old binaries first.
	killVM(t, rt, id)
	started := time.Now()
	if resp := boot(17, 14); resp.State != dbguest.StateReady {
		t.Fatalf("upgrade = %+v", resp)
	}
	took := time.Since(started)
	conn = connectRuntime(ctx, t, rt, id, password)
	if v := versionNum(conn); v/10000 != 17 {
		t.Fatalf("server version %d after the upgrade, want 17", v)
	}
	var matches bool
	var next int64
	if err := conn.QueryRow(ctx, `SELECT account_count(), secret = crypt('pw7', secret) FROM accounts WHERE email = 'user7@example.com'`).Scan(&count, &matches); err != nil ||
		count != 2000 || !matches {
		t.Fatalf("after the upgrade: %d rows, pgcrypto check %v, %v", count, matches, err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (email, secret) VALUES ('after@example.com', 'x') RETURNING id`).Scan(&next); err != nil || next != 2001 {
		t.Fatalf("sequence after the upgrade = %d, %v", next, err)
	}
	var usesIndex bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = 'accounts_email')`).Scan(&usesIndex); err != nil || !usesIndex {
		t.Fatalf("index after the upgrade: %v, %v", usesIndex, err)
	}
	_ = conn.Close(ctx)

	// The volume now holds a 17 cluster: a plain 17 boot serves it.
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if resp := boot(17, 0); resp.State != dbguest.StateReady {
		t.Fatalf("plain boot after the upgrade = %+v", resp)
	}
	conn = connectRuntime(ctx, t, rt, id, password)
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, `SELECT account_count()`).Scan(&count); err != nil || count != 2001 {
		t.Fatalf("after a plain boot: %d rows, %v", count, err)
	}
	t.Logf("upgraded PostgreSQL 14 to 17 in place in %v (boot to ready)", took.Round(time.Millisecond))
}
