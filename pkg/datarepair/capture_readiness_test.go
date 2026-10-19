package datarepair

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCaptureReadinessProgramDeterministic(t *testing.T) {
	want := CaptureReadinessProgram()
	for range 32 {
		if !bytes.Equal(want, CaptureReadinessProgram()) {
			t.Fatal("capture probe generation must be reproducible across SDKs")
		}
	}
}

func captureTestSetup(t *testing.T) (*Engine, *Engine, CaptureReadinessOptions, string) {
	t.Helper()
	e, s, _ := fixture(t, 2)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE public.gregale_operation_inbox(operation_id uuid PRIMARY KEY,account_id uuid NOT NULL,app_id uuid NOT NULL,platform_tenant_id uuid,request_digest bytea NOT NULL,response_body text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{"capture_writer_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := e.Pool.Exec(t.Context(), "CREATE ROLE "+role+" NOLOGIN; GRANT USAGE ON SCHEMA public,gregale_repair TO "+role+"; GRANT SELECT ON gregale_repair.enrollments,products TO "+role+"; GRANT UPDATE ON products TO "+role+"; GRANT SELECT,INSERT,UPDATE ON gregale_repair.operations,gregale_repair.changes TO "+role+"; GRANT SELECT,INSERT ON public.gregale_operation_inbox TO "+role); err != nil {
		t.Fatal(err)
	}
	functions, _ := doctorFunctions()
	for _, f := range functions {
		if slices.Contains([]string{"begin_operation", "finish_operation", "check_enrollment", "fingerprint"}, f.Name) {
			if _, err := e.Pool.Exec(t.Context(), "GRANT EXECUTE ON FUNCTION gregale_repair."+f.Name+"("+f.Args+") TO "+role); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		if _, err := e.Pool.Exec(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	cfg := e.Pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+role); return err }
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return e, &Engine{Pool: pool}, CaptureReadinessOptions{Scope: s, Table: "public.products", Release: "writer-v1", ReceiptProtocol: "managed"}, role
}

func TestCaptureReadinessRestrictedWriter(t *testing.T) {
	owner, writer, o, _ := captureTestSetup(t)
	r, err := writer.CheckCapture(t.Context(), o)
	if err != nil || !r.Valid() || r.Status != "ready" {
		t.Fatalf("restricted writer readiness: %+v %v", r, err)
	}
	tx, err := writer.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = Capture(t.Context(), tx, o.Scope, uuid.NewString(), o.Release, o.Table, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE products SET category='human'`)
		return err
	})
	if err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatalf("ready writer could not capture: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := owner.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gregale_repair.changes`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("writer capture missing: %d %v", count, err)
	}
}

func TestCaptureReadinessWriterFailures(t *testing.T) {
	for _, tc := range []struct{ name, sql, id, code string }{
		{"finish privilege", "REVOKE EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) FROM ", "permissions", "writer_permissions_missing"},
		{"receipt privilege", "REVOKE INSERT ON public.gregale_operation_inbox FROM ", "permissions", "writer_permissions_missing"},
		{"capture disabled", `ALTER TABLE products DISABLE TRIGGER gregale_repair_capture`, "triggers", "triggers_invalid"},
		{"schema drift", `ALTER TABLE products ADD COLUMN note text`, "fingerprint", "fingerprint_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, writer, o, role := captureTestSetup(t)
			sql := tc.sql
			if strings.HasPrefix(sql, "REVOKE") {
				sql += role
			}
			if _, err := owner.Pool.Exec(t.Context(), sql); err != nil {
				t.Fatal(err)
			}
			r, err := writer.CheckCapture(t.Context(), o)
			if err != nil || !r.Valid() || r.Status != "blocked" {
				t.Fatalf("writer did not block: %+v %v", r, err)
			}
			found := false
			for _, f := range r.Findings {
				if f.ID == tc.id && f.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing finding %s %s: %+v", tc.id, tc.code, r)
			}
		})
	}
}

func TestCaptureReadinessColumnGrantDoesNotAuthorizeEnrollmentLock(t *testing.T) {
	owner, writer, o, role := captureTestSetup(t)
	if _, err := owner.Pool.Exec(t.Context(), "REVOKE UPDATE ON products FROM "+role+";GRANT UPDATE(category) ON products TO "+role); err != nil {
		t.Fatal(err)
	}
	r, err := writer.CheckCapture(t.Context(), o)
	if err != nil || r.Status != "blocked" {
		t.Fatalf("column grant appeared ready: %+v %v", r, err)
	}
	found := false
	for _, f := range r.Findings {
		for _, object := range f.Objects {
			if object == "table: UPDATE, DELETE or TRUNCATE (enrollment lock)" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing enrollment lock privilege diagnosis", r)
	}
	tx, err := writer.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = Capture(t.Context(), tx, o.Scope, uuid.NewString(), o.Release, o.Table, func(pgx.Tx) error { called = true; return nil })
	_ = tx.Rollback(t.Context())
	if err == nil || called {
		t.Fatal("fixture did not prove the missing table lock permission", err, called)
	}
}

func TestCaptureReadinessReadsNoPayloadAndExecutesNoHelpers(t *testing.T) {
	owner, writer, o, _ := captureTestSetup(t)
	var source string
	if err := owner.Pool.QueryRow(t.Context(), `SELECT id::text FROM gregale_repair.operations LIMIT 1`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	p := approved(t, owner, o.Scope, source)
	if _, err := owner.Pool.Exec(t.Context(), `CREATE OR REPLACE FUNCTION gregale_repair.fingerprint(target regclass) RETURNS text LANGUAGE sql STABLE SET search_path=pg_catalog AS $$SELECT pg_advisory_lock(99218)::text$$`); err != nil {
		t.Fatal(err)
	}
	writer.CheckSQL = "SELECT pg_advisory_lock(99219) IS NULL"
	before := validationState(t, owner, p.ID)
	r, err := writer.CheckCapture(t.Context(), o)
	if err != nil || !r.Valid() || r.Status != "blocked" {
		t.Fatalf("incompatible helper not reported: %+v %v", r, err)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "capture_functions_incompatible" {
			found = true
		}
	}
	if !found {
		t.Fatal("changed capture helper was accepted", r)
	}
	if after := validationState(t, owner, p.ID); after != before {
		t.Fatal("capture readiness changed rows, journals or a saved plan")
	}
	var locks int
	if err := owner.Pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid IN (99218,99219) AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&locks); err != nil || locks != 0 {
		t.Fatal("capture readiness executed an installed helper or check", locks, err)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "pg_advisory_lock") || strings.Contains(string(raw), `"category"`) {
		t.Fatal("capture readiness exposed SQL or row payload", string(raw))
	}
}
