// adr:566
package copyarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type archiveFixture struct {
	root, source, target              *pgx.Conn
	owner, member, targetName, pgDump string
	requirement                       copyinventory.DatabaseExport
}

func newArchiveFixture(t *testing.T) archiveFixture {
	t.Helper()
	dsn, pgDump := os.Getenv("DATABASE_URL"), os.Getenv("FAAS_COPY_PG_DUMP")
	if dsn == "" || pgDump == "" {
		t.Skip("DATABASE_URL and FAAS_COPY_PG_DUMP required for real copy archive contracts")
	}
	if !filepath.IsAbs(pgDump) {
		t.Fatal("FAAS_COPY_PG_DUMP must be absolute")
	}
	root, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := archiveFixture{root: root, owner: "grg_archive_owner_" + id, member: "grg_archive_member_" + id, targetName: "grg_archive_target_" + id, pgDump: pgDump}
	sourceName := "grg_archive/ db?é#%_" + id
	roles, dbs := []string{}, []string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, conn := range []*pgx.Conn{f.source, f.target} {
			if conn != nil {
				_ = conn.Close(ctx)
			}
		}
		for _, db := range dbs {
			if _, err := root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
				t.Error(err)
			}
		}
		for _, role := range roles {
			if _, err := root.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error(err)
			}
		}
	})
	for _, role := range []string{f.owner, f.member} {
		if _, err := root.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'archive-password-not-in-argv'"); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role)
	}
	for _, db := range []string{sourceName, f.targetName} {
		if _, err := root.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, db)
	}
	cfg := root.Config().Copy()
	cfg.User, cfg.Password, cfg.Database = f.owner, "archive-password-not-in-argv", sourceName
	f.source, err = pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately use an ordinary database owner. A superuser-only successful
	// dump would not qualify the managed capture reader's permissions.
	statements := []string{
		"CREATE SCHEMA app",
		"CREATE TYPE app.event_state AS ENUM ('pending','done')",
		"CREATE TABLE app.accounts (id bigserial PRIMARY KEY, name text UNIQUE NOT NULL, note bytea NOT NULL, touched int NOT NULL DEFAULT 0)",
		"CREATE SEQUENCE app.event_number INCREMENT 3 START 77 MAXVALUE 777",
		"CREATE TABLE app.events (number bigint DEFAULT nextval('app.event_number'), account_id bigint REFERENCES app.accounts, payload jsonb, state app.event_state)",
		"CREATE INDEX events_payload ON app.events USING gin(payload)",
		"CREATE VIEW app.account_names AS SELECT id,name FROM app.accounts",
		"CREATE FUNCTION app.touch_account() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN NEW.touched := NEW.touched + 1; RETURN NEW; END $$",
		"CREATE TRIGGER touch_account BEFORE INSERT OR UPDATE ON app.accounts FOR EACH ROW EXECUTE FUNCTION app.touch_account()",
		"INSERT INTO app.accounts(name,note) VALUES ('private-row-é',decode('00ff10','hex')),('second-row',decode('abcd','hex'))",
		"INSERT INTO app.events(account_id,payload,state) VALUES (1,'{\"secret\":\"private-json\"}','done')",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA app GRANT SELECT ON TABLES TO " + pgx.Identifier{f.member}.Sanitize(),
		"GRANT USAGE ON SCHEMA app TO " + pgx.Identifier{f.member}.Sanitize(),
		"GRANT SELECT ON app.accounts TO " + pgx.Identifier{f.member}.Sanitize(),
		"COMMENT ON TABLE app.accounts IS 'private-schema-comment'",
		"CREATE TABLE app.large_object_refs (oid oid PRIMARY KEY)",
		"INSERT INTO app.large_object_refs VALUES (lo_from_bytea(0,decode('deadbeef00','hex')))",
	}
	for _, sql := range statements {
		if _, err := f.source.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	var loOID uint32
	if err := f.source.QueryRow(t.Context(), "SELECT oid FROM app.large_object_refs").Scan(&loOID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(t.Context(), fmt.Sprintf("GRANT SELECT ON LARGE OBJECT %d TO %s", loOID, pgx.Identifier{f.member}.Sanitize())); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(t.Context(), "SET default_transaction_read_only=on"); err != nil {
		t.Fatal(err)
	}
	var inventoryCfg copyinventory.Config
	inventoryCfg.DatabaseName, inventoryCfg.RoleName = sourceName, f.owner
	inventoryCfg.FingerprintKey[0] = 23
	if err := f.source.QueryRow(t.Context(), "SELECT current_setting('server_version_num')::int/10000,d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user").Scan(&inventoryCfg.PostgresMajor, &inventoryCfg.DatabaseOID, &inventoryCfg.RoleOID); err != nil {
		t.Fatal(err)
	}
	i, err := copyinventory.Read(t.Context(), f.source, inventoryCfg)
	if err != nil {
		t.Fatal(err)
	}
	scope := archiveRequirement().Scope
	scope.PostgresMajor = inventoryCfg.PostgresMajor
	plan, err := i.PlanExports(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := plan.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range req {
		if d.Database.OID == inventoryCfg.DatabaseOID {
			f.requirement = d
		}
	}
	if !validRequirement(f.requirement) {
		t.Fatal("source database missing from complete export plan")
	}
	cfg.Database = f.targetName
	f.target, err = pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestArchiveExportsEncryptedRealDatabaseAndRestoresAllTestedContent(t *testing.T) {
	f := newArchiveFixture(t)
	key, _ := age.GenerateX25519Identity()
	var cipher bytes.Buffer
	receipt, err := Export(t.Context(), f.source, f.requirement, f.pgDump, key.Recipient(), &cipher, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(cipher.Bytes())
	if !receipt.Scope.Equal(f.requirement.Scope) || receipt.InventoryFingerprint != f.requirement.InventoryFingerprint || receipt.SourceDatabaseOID != f.requirement.Database.OID || receipt.PlainBytes < 1000 || receipt.CiphertextBytes != int64(cipher.Len()) || receipt.CiphertextSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("export receipt does not identify the complete encrypted stream")
	}
	for _, secret := range []string{f.owner, f.requirement.Database.Name, "archive-password-not-in-argv", "private-row-é", "private-json", "private-schema-comment"} {
		if bytes.Contains(cipher.Bytes(), []byte(secret)) {
			t.Fatal("archive exposed plaintext")
		}
		out, _ := json.Marshal(receipt)
		if bytes.Contains(out, []byte(secret)) {
			t.Fatal("receipt exposed private SQL identity/data")
		}
	}
	reader, err := Open([]*age.X25519Identity{key}, f.requirement, bytes.NewReader(cipher.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	dumpPath := filepath.Join(t.TempDir(), "private.dump")
	dump, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	n, copyErr := io.Copy(dump, reader)
	closeErr := dump.Close()
	if copyErr != nil || closeErr != nil || n != receipt.PlainBytes {
		t.Fatalf("full age stream: %v %v", copyErr, closeErr)
	}
	// This test-only restore qualifies dump coverage into a fresh local DB.
	// It is not a production importer or a globally complete stage receipt.
	dsn, env, err := dumpConnection(f.target.Config(), f.requirement.Scope.PostgresMajor)
	if err != nil {
		t.Fatal(err)
	}
	// The target restore writes; only this fixture removes the export read-only
	// PGOPTIONS. Password and all other connection controls remain isolated.
	for n := range env {
		if strings.HasPrefix(env[n], "PGOPTIONS=") {
			env[n] = "PGOPTIONS=-c search_path=pg_catalog"
		}
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), "--exit-on-error", "--single-transaction", "--dbname="+dsn, dumpPath)
	cmd.Env = env
	var diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("local restore failed: %v (diagnostic bytes=%d)", err, diagnostic.Len())
	}
	if diagnostic.Len() != 0 {
		t.Fatal("restore produced unchecked diagnostics")
	}
	verifyArchiveDataset(t, f)
}

func verifyArchiveDataset(t *testing.T, f archiveFixture) {
	t.Helper()
	for _, conn := range []*pgx.Conn{f.source, f.target} {
		var valid bool
		query := `SELECT
		 (SELECT count(*)=2 AND bool_and(touched=1) FROM app.accounts) AND
		 (SELECT encode(note,'hex')='00ff10' FROM app.accounts WHERE name='private-row-é') AND
		 (SELECT number=77 AND payload->>'secret'='private-json' AND state='done' FROM app.events) AND
		 (SELECT last_value=2 AND is_called FROM app.accounts_id_seq) AND
		 (SELECT last_value=77 AND is_called FROM app.event_number) AND
		 (SELECT count(*)=2 FROM app.account_names) AND
		 (SELECT encode(lo_get(oid),'hex')='deadbeef00' FROM app.large_object_refs) AND
		 has_table_privilege($1,'app.accounts','SELECT') AND
		 (SELECT EXISTS(SELECT 1 FROM aclexplode(m.lomacl) a,pg_roles r WHERE a.grantee=r.oid AND r.rolname=$1 AND a.privilege_type='SELECT') FROM pg_largeobject_metadata m WHERE m.oid=(SELECT oid FROM app.large_object_refs)) AND
		 obj_description('app.accounts'::regclass)='private-schema-comment' AND
		 (SELECT count(*)=1 FROM pg_indexes WHERE schemaname='app' AND indexname='events_payload') AND
		 (SELECT count(*)=1 FROM pg_trigger WHERE tgrelid='app.accounts'::regclass AND tgname='touch_account') AND
		 (SELECT count(*)=1 FROM pg_default_acl WHERE defaclrole=(SELECT oid FROM pg_roles WHERE rolname=$2) AND defaclnamespace='app'::regnamespace)`
		if err := conn.QueryRow(t.Context(), query, f.member, f.owner).Scan(&valid); err != nil || !valid {
			t.Fatalf("restored/source dataset or permissions differ: %v", err)
		}
	}
	var valid bool
	if err := f.target.QueryRow(t.Context(), "INSERT INTO app.accounts(name,note) VALUES ('target-only',decode('ee','hex')) RETURNING id=3 AND touched=1").Scan(&valid); err != nil || !valid {
		t.Fatalf("restored sequence/trigger behavior: %v", err)
	}
	if err := f.source.QueryRow(t.Context(), "SELECT count(*)=2 FROM app.accounts").Scan(&valid); err != nil || !valid {
		t.Fatalf("target write changed source data: %v", err)
	}
}

func writeFakeDump(t *testing.T, major int, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pg_dump")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'pg_dump (PostgreSQL) %d.1\\n'; exit 0; fi\n%s\n", major, body)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArchiveExportRejectsSQLPinsTransactionsAndClosedRequirements(t *testing.T) {
	f := newArchiveFixture(t)
	key, _ := age.GenerateX25519Identity()
	for _, tc := range []struct {
		name string
		edit func(*copyinventory.DatabaseExport)
		want error
	}{
		{"database_oid", func(d *copyinventory.DatabaseExport) { d.Database.OID++ }, pgerrors.ErrConflict},
		{"database_name", func(d *copyinventory.DatabaseExport) { d.Database.Name = f.targetName }, pgerrors.ErrConflict},
		{"reader_role", func(d *copyinventory.DatabaseExport) { d.AuthenticatedReaderRoleOID++ }, pgerrors.ErrConflict},
		{"major", func(d *copyinventory.DatabaseExport) { d.Scope.PostgresMajor++ }, pgerrors.ErrConflict},
		{"closed_requirement", func(d *copyinventory.DatabaseExport) { d.CapturedAllowConnections = false }, pgerrors.ErrUnavailable},
		{"invalid_scope", func(d *copyinventory.DatabaseExport) { d.Scope.OperationID = "invalid" }, pgerrors.ErrInvalid},
		{"invalid_name", func(d *copyinventory.DatabaseExport) { d.Database.Name = string([]byte{0xff}) }, pgerrors.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := f.requirement
			tc.edit(&d)
			var out bytes.Buffer
			if r, err := Export(t.Context(), f.source, d, f.pgDump, key.Recipient(), &out, 4<<20); !errors.Is(err, tc.want) || r != (Receipt{}) || out.Len() != 0 {
				t.Fatalf("rejected requirement dispatched dump: %v", err)
			}
		})
	}
	tx, err := f.source.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Export(t.Context(), f.source, f.requirement, f.pgDump, key.Recipient(), io.Discard, 4<<20); !errors.Is(err, pgerrors.ErrConflict) || r != (Receipt{}) {
		t.Fatalf("borrowed open transaction: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Exec(t.Context(), "SET default_transaction_read_only=off"); err != nil {
		t.Fatal(err)
	}
	if r, err := Export(t.Context(), f.source, f.requirement, f.pgDump, key.Recipient(), io.Discard, 4<<20); !errors.Is(err, pgerrors.ErrConflict) || r != (Receipt{}) {
		t.Fatalf("read-write reader: %v", err)
	}
	if r, err := Export(t.Context(), nil, f.requirement, f.pgDump, key.Recipient(), io.Discard, 4<<20); !errors.Is(err, pgerrors.ErrConflict) || r != (Receipt{}) {
		t.Fatalf("missing reader: %v", err)
	}
}

func TestArchiveExportFailuresNeverReturnSuccessfulReceipt(t *testing.T) {
	f := newArchiveFixture(t)
	key, _ := age.GenerateX25519Identity()
	major := f.requirement.Scope.PostgresMajor
	for _, tc := range []struct {
		name, body string
		major      int
		budget     int64
		want       error
	}{
		{"warning", "printf PGDMP; printf 'private-secret-diagnostic' >&2", major, 4 << 20, pgerrors.ErrUnsupported},
		{"failed_dump", "printf 'private-secret-diagnostic' >&2; exit 1", major, 4 << 20, pgerrors.ErrUnavailable},
		{"bad_magic", "printf WRONGARCHIVE", major, 4 << 20, pgerrors.ErrConflict},
		{"short_dump", "printf PGDM", major, 4 << 20, pgerrors.ErrConflict},
		{"quota", "printf PGDMP", major, 4, pgerrors.ErrQuotaExceeded},
		{"major_tool", "printf PGDMP", major + 1, 4 << 20, pgerrors.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r, err := Export(t.Context(), f.source, f.requirement, writeFakeDump(t, tc.major, tc.body), key.Recipient(), &out, tc.budget)
			if !errors.Is(err, tc.want) || r != (Receipt{}) || strings.Contains(fmt.Sprint(err), "private-secret") {
				t.Fatalf("failed dump returned receipt/secret: %v", err)
			}
		})
	}
	if r, err := Export(t.Context(), f.source, f.requirement, f.pgDump, key.Recipient(), io.Discard, 4); !errors.Is(err, pgerrors.ErrQuotaExceeded) || r != (Receipt{}) {
		t.Fatalf("real dump quota: %v", err)
	}
	if r, err := Export(t.Context(), f.source, f.requirement, f.pgDump, key.Recipient(), failingArchiveWriter{}, 4<<20); !errors.Is(err, pgerrors.ErrUnavailable) || r != (Receipt{}) || strings.Contains(fmt.Sprint(err), "private-secret") {
		t.Fatalf("storage failure receipt/secret: %v", err)
	}
	t.Run("changed_post_dump_identity", func(t *testing.T) {
		out := &callbackArchiveWriter{callback: func() error {
			_, err := f.source.Exec(t.Context(), "SET default_transaction_read_only=off")
			return err
		}}
		r, err := Export(t.Context(), f.source, f.requirement, writeFakeDump(t, major, "printf PGDMP"), key.Recipient(), out, 4<<20)
		if !errors.Is(err, pgerrors.ErrConflict) || r != (Receipt{}) || !out.called {
			t.Fatalf("post-dump identity change accepted: %v", err)
		}
		if _, err := f.source.Exec(t.Context(), "SET default_transaction_read_only=on"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("canceled_dump", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		if r, err := Export(ctx, f.source, f.requirement, writeFakeDump(t, major, "while :; do :; done"), key.Recipient(), io.Discard, 4<<20); !errors.Is(err, context.DeadlineExceeded) || r != (Receipt{}) {
			t.Fatalf("cancelled process: %v", err)
		}
	})
}

type failingArchiveWriter struct{}

func (failingArchiveWriter) Write([]byte) (int, error) {
	return 0, errors.New("private-secret-storage-error")
}

type callbackArchiveWriter struct {
	callback func() error
	called   bool
}

func (w *callbackArchiveWriter) Write(p []byte) (int, error) {
	if !w.called {
		w.called = true
		if err := w.callback(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
