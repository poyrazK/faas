//go:build metal && linux

// adr: 1027 — a database VM imports another PostgreSQL database: the
// guest's pg_dump reaches the source through the host relay, which speaks
// TLS to it and verifies its certificate, and the copy is restored in one
// transaction as the target's schema owner. An extension the owner cannot
// create fails the import until it is skipped.
package dbvm

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// tlsSourceShim fronts a database VM like a remote PostgreSQL server that
// requires TLS: it answers the SSLRequest, terminates TLS with cert and
// relays to the VM's data port.
func tlsSourceShim(t *testing.T, rt *Runtime, id string, cert tls.Certificate) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				request := make([]byte, 8)
				if _, err := io.ReadFull(conn, request); err != nil || !bytes.Equal(request, sslRequest) {
					return
				}
				if _, err := conn.Write([]byte{'S'}); err != nil {
					return
				}
				client := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				vm, err := rt.Dial(context.Background(), id)
				if err != nil {
					return
				}
				defer func() { _ = vm.Close() }()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(vm, client); done <- struct{}{} }()
				go func() { _, _ = io.Copy(client, vm); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func admin(ctx context.Context, t *testing.T, rt *Runtime, id, sql string) {
	t.Helper()
	if resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpAdminSQL, SQL: sql}); err != nil || resp.Error != "" {
		t.Fatalf("admin sql: %+v %v", resp, err)
	}
}

// awaitImport polls the import until it is done or failed.
func awaitImport(ctx context.Context, t *testing.T, rt *Runtime, id string) dbguest.ImportStatus {
	t.Helper()
	for {
		resp, err := rt.Control(ctx, id, dbguest.Request{Op: dbguest.OpImportStatus})
		if err != nil || resp.Import == nil {
			t.Fatalf("import status: %+v %v", resp, err)
		}
		if resp.Import.State == dbguest.ImportDone || resp.Import.State == dbguest.ImportFailed {
			return *resp.Import
		}
		select {
		case <-ctx.Done():
			t.Fatal("import never finished")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ownerSetup is the driver's database layout (initSQL): a schema owner
// role owning the database and schema public.
const ownerSetup = `CREATE ROLE gregale_schema_owner NOLOGIN NOINHERIT;
CREATE DATABASE gregale OWNER gregale_schema_owner;
\connect gregale
ALTER SCHEMA public OWNER TO gregale_schema_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;`

func TestMetalImportFromAnotherPostgres(t *testing.T) {
	env := loadMetalEnv(t)
	parent := "dbvm-import-metal"
	if err := os.MkdirAll(filepath.Join("/sys/fs/cgroup", parent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", parent)) })
	cert, roots := testCert(t, "localhost")
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(env.workdir, "ijail"), VolumeRoot: filepath.Join(env.workdir, "ivolumes"),
		KernelPath: env.kernel, Bases: map[int]string{17: env.base}, ParentCgroup: parent,
		JailerBin: envOr("FAAS_JAILER", "jailer"), FirecrackerBin: env.firecracker, Leases: &fakeLeaser{},
		ImportRoots: roots, ImportAllowPrivate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	source, target := "6a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "7b2c3d4e-5f6a-4b7c-9d8e-0f1a2b3c4d5e"
	for _, id := range []string{source, target} {
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
		admin(ctx, t, rt, id, ownerSetup)
	}
	const password = "source-migration-password"
	admin(ctx, t, rt, source, `CREATE ROLE src_mig LOGIN PASSWORD '`+password+`' IN ROLE gregale_schema_owner;
GRANT CONNECT ON DATABASE gregale TO src_mig;
\connect gregale
CREATE EXTENSION pg_stat_statements;
SET ROLE gregale_schema_owner;
CREATE EXTENSION pgcrypto;
CREATE TABLE accounts (id bigserial PRIMARY KEY, email text NOT NULL UNIQUE, token bytea NOT NULL DEFAULT gen_random_bytes(16));
COMMENT ON TABLE accounts IS 'customer accounts';
INSERT INTO accounts (email) SELECT 'user' || g || '@example.com' FROM generate_series(1, 20000) g;
CREATE FUNCTION account_count() RETURNS bigint LANGUAGE plpgsql AS $$ BEGIN RETURN (SELECT count(*) FROM accounts); END $$;
CREATE VIEW recent AS SELECT * FROM accounts ORDER BY id DESC LIMIT 10;
CREATE SCHEMA billing;
CREATE TABLE billing.invoices (id int PRIMARY KEY, account bigint REFERENCES accounts(id));
INSERT INTO billing.invoices SELECT g, g FROM generate_series(1, 100) g;
-- pg_restore refreshes it, so it records the session the restore ran as.
CREATE MATERIALIZED VIEW restored_by AS SELECT session_user::text AS session_role;`)

	port := tlsSourceShim(t, rt, source, cert)
	if err := rt.SetImportSource(target, ImportSource{Host: "localhost", Port: port}); err != nil {
		t.Fatal(err)
	}
	const operation, otherOperation = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f", "4d5e6f7a-8b9c-4d0e-9f1a-2b3c4d5e6f7a"
	importAs := func(id string, skip ...string) dbguest.Request {
		return dbguest.Request{Op: dbguest.OpImport, Import: &dbguest.ImportSource{Database: "gregale", User: "src_mig", Password: password,
			TargetDatabase: "gregale", Owner: "gregale_schema_owner", SkipExtensions: skip, OperationID: id}}
	}
	importReq := func(skip ...string) dbguest.Request { return importAs(operation, skip...) }
	// pg_stat_statements is not trusted: the owner cannot create it.
	if resp, err := rt.Control(ctx, target, importReq()); err != nil || resp.Import == nil || resp.Import.State != dbguest.ImportRunning {
		t.Fatalf("import = %+v, %v", resp, err)
	}
	if status := awaitImport(ctx, t, rt, target); status.State != dbguest.ImportFailed || status.Error != dbguest.ErrCodeExtension ||
		!slices.Equal(status.MissingExtensions, []string{"pg_stat_statements"}) {
		t.Fatalf("import with an untrusted extension = %+v; want extension_unavailable naming pg_stat_statements", status)
	}
	started := time.Now()
	if resp, err := rt.Control(ctx, target, importReq("pg_stat_statements")); err != nil || resp.Import == nil {
		t.Fatalf("import = %+v, %v", resp, err)
	}
	status := awaitImport(ctx, t, rt, target)
	took := time.Since(started)
	if status.State != dbguest.ImportDone || status.SourceMajor != 17 || status.OperationID != operation {
		t.Fatalf("import = %+v", status)
	}
	// A finished import is reported under its operation, not run again,
	// and another operation is refused, before and after a restart: the
	// lineage committed with the copy.
	expectLineage := func(when string) {
		t.Helper()
		if resp, err := rt.Control(ctx, target, importReq("pg_stat_statements")); err != nil || resp.Error != "" || resp.Import == nil ||
			resp.Import.State != dbguest.ImportDone || resp.Import.OperationID != operation {
			t.Fatalf("%s: same operation = %+v, %v", when, resp, err)
		}
		if resp, err := rt.Control(ctx, target, importAs(otherOperation, "pg_stat_statements")); err != nil || resp.Error != dbguest.ErrCodeImportOperationMismatch ||
			resp.Import == nil || resp.Import.OperationID != operation {
			t.Fatalf("%s: other operation = %+v, %v; want %s", when, resp, err, dbguest.ErrCodeImportOperationMismatch)
		}
	}
	expectLineage("before a restart")
	if err := rt.Stop(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx, BootRequest{ID: target, Major: 17, VcpuCount: 1, MemSizeMiB: 384}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.WaitState(ctx, target, dbguest.StateReady); err != nil {
		t.Fatal(err)
	}
	if resp, err := rt.Control(ctx, target, dbguest.Request{Op: dbguest.OpImportStatus}); err != nil || resp.Import == nil ||
		resp.Import.State != dbguest.ImportDone || resp.Import.OperationID != operation {
		t.Fatalf("status after a restart = %+v, %v", resp, err)
	}
	expectLineage("after a restart")

	admin(ctx, t, rt, target, `CREATE ROLE checker LOGIN PASSWORD 'checker-password' IN ROLE gregale_schema_owner; GRANT CONNECT ON DATABASE gregale TO checker;`)
	cfg, err := pgx.ParseConfig("host=dbvm user=checker dbname=gregale sslmode=disable password=checker-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return rt.Dial(ctx, target) }
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var count, invoices int64
	var owner, comment, extOwner string
	var statements bool
	if err := conn.QueryRow(ctx, `SELECT account_count(), (SELECT count(*) FROM billing.invoices),
		(SELECT tableowner FROM pg_tables WHERE tablename = 'accounts'), obj_description('accounts'::regclass),
		(SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid = e.extowner WHERE e.extname = 'pgcrypto'),
		EXISTS (SELECT FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&count, &invoices, &owner, &comment, &extOwner, &statements); err != nil {
		t.Fatal(err)
	}
	if count != 20000 || invoices != 100 || owner != "gregale_schema_owner" || comment != "customer accounts" || extOwner != "gregale_schema_owner" || statements {
		t.Fatalf("imported: %d accounts, %d invoices, owner %s, comment %q, pgcrypto owner %s, pg_stat_statements %v",
			count, invoices, owner, comment, extOwner, statements)
	}
	// The restore ran as the import role, never the superuser: no
	// attribute, a member of the owner and of nothing else.
	var restoredBy string
	if err := conn.QueryRow(ctx, `SELECT session_role FROM restored_by`).Scan(&restoredBy); err != nil || restoredBy != "gregale_import" {
		t.Fatalf("restore session = %q, %v; want gregale_import", restoredBy, err)
	}
	var importRole string
	if err := conn.QueryRow(ctx, `SELECT r.rolname || '|' || r.rolsuper || '|' || r.rolcreaterole || '|' || r.rolcreatedb || '|' || r.rolreplication || '|' || r.rolbypassrls || '|' ||
		(SELECT string_agg(g.rolname, ',') FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid WHERE m.member = r.oid)
		FROM pg_roles r WHERE r.rolname = 'gregale_import'`).Scan(&importRole); err != nil || importRole != "gregale_import|false|false|false|false|false|gregale_schema_owner" {
		t.Fatalf("import role = %q, %v", importRole, err)
	}
	var next int64
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (email) VALUES ('after@example.com') RETURNING id`).Scan(&next); err != nil || next != 20001 {
		t.Fatalf("sequence after import = %d, %v; want 20001", next, err)
	}
	t.Logf("imported 20000 rows over the TLS relay in %v (source PostgreSQL %s)", took.Round(time.Millisecond), strconv.Itoa(status.SourceMajor))
}
