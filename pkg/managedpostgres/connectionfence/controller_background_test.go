// adr: 590
package connectionfence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestConnectionFenceAllowsUnassignedAutovacuumWorker(t *testing.T) {
	// Use a private server: changing autovacuum cadence on DATABASE_URL would
	// affect other packages and could hide their isolation failures.
	cluster := pgtest.OpenTLSCluster(t)
	t.Setenv("DATABASE_URL", cluster.AdminURL)
	if _, err := cluster.Admin.Exec(t.Context(), "ALTER SYSTEM SET autovacuum_naptime='1s'"); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(t.Context(), "SELECT pg_reload_conf()"); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	// Slow only this disposable table's vacuum so the actual server worker
	// remains present through the maintenance preflight and closure.
	if _, err := f.maintenance.Exec(ctx, `CREATE TABLE vacuum_fixture (value text) WITH
 (autovacuum_vacuum_threshold=0, autovacuum_vacuum_scale_factor=0,
  autovacuum_vacuum_cost_delay=100, autovacuum_vacuum_cost_limit=1);
 INSERT INTO vacuum_fixture SELECT repeat(md5(i::text),32) FROM generate_series(1,2000) i;
 DELETE FROM vacuum_fixture;
 SELECT pg_stat_force_next_flush();`); err != nil {
		t.Fatal(err)
	}
	waitForAutovacuumWorker(t, f)
	closed, err := f.c.Close(ctx, f.request)
	if err != nil || closed.State != "closed" {
		t.Fatalf("server housekeeping prevented closure: %+v %v", closed, err)
	}
	if _, err := f.c.Release(ctx, f.request.Identity); err != nil {
		t.Fatal(err)
	}
}

func waitForAutovacuumWorker(t *testing.T, f fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for {
		var pid int32
		err := f.root.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=$1
 AND backend_type='autovacuum worker' AND usesysid IS NULL LIMIT 1`, f.config.MaintenanceDatabase).Scan(&pid)
		if err == nil {
			var unassigned bool
			if err := f.maintenance.QueryRow(ctx, "SELECT usesysid IS NULL AND usename IS NULL FROM pg_stat_activity WHERE pid=$1", pid).Scan(&unassigned); err != nil || !unassigned {
				t.Fatalf("maintenance owner's worker observation: %t %v", unassigned, err)
			}
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("private server did not start the disposable table's autovacuum worker")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestConnectionFenceRejectsForeignSessionWithDroppedRole(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	role := f.tenant + "_dropped"
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := f.root.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN PASSWORD '"+f.password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := f.root.Exec(cleanup, "DROP ROLE IF EXISTS "+quoted); err != nil {
			t.Error(err)
		}
	})
	database := pgx.Identifier{f.config.MaintenanceDatabase}.Sanitize()
	if _, err := f.root.Exec(ctx, "GRANT CONNECT ON DATABASE "+database+" TO "+quoted); err != nil {
		t.Fatal(err)
	}
	client, err := f.connect(ctx, t, f.config.MaintenanceDatabase, role)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.root.Exec(ctx, "REVOKE ALL ON DATABASE "+database+" FROM "+quoted+"; DROP ROLE "+quoted); err != nil {
		t.Fatal(err)
	}
	var namelessClient bool
	if err := f.maintenance.QueryRow(ctx, "SELECT usesysid IS NOT NULL AND usename IS NULL FROM pg_stat_activity WHERE pid=$1", client.PgConn().PID()).Scan(&namelessClient); err != nil || !namelessClient {
		t.Fatalf("dropped role session identity: %t %v", namelessClient, err)
	}
	if _, err := f.c.Close(ctx, f.request); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("foreign session with dropped role was accepted: %v", err)
	}
}
