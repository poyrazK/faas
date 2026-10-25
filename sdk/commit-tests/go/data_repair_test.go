package commit_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// ADR-20261016: the SDK capture belongs to the same transaction as the
// existing operation receipt and never reruns on committed response replay.
func TestDataRepairOperationTransaction(t *testing.T) {
	path := os.Getenv("GREGALE_DATA_REPAIR_SCHEMA")
	if path == "" {
		t.Skip("generated repair schema required")
	}
	db, ctx := operationDatabase(t)
	schema, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE public.products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('public.products',ARRAY['category']);INSERT INTO public.products(id,category) VALUES(1,'old'),(2,'old')`); err != nil {
		t.Fatal(err)
	}
	input := operationInput(t)
	config := preview.DataRepairCapture{Table: "public.products", Environment: "production", Release: "go-import-v1"}
	first, err := preview.WithOperationTransaction(ctx, db, input, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
		return preview.WithDataRepairCapture(ctx, tx, input, config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
			_, err := tx.ExecContext(ctx, `UPDATE public.products SET category='new'`)
			return preview.OperationOutcome{Result: json.RawMessage(`{"changed":2}`)}, err
		})
	})
	if err != nil || first.Replayed {
		t.Fatalf("capture: %+v %v", first, err)
	}
	input.Generation++
	replay, err := preview.WithOperationTransaction(ctx, db, input, func(preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
		t.Fatal("committed callback reran")
		return preview.OperationOutcome{}, nil
	})
	if err != nil || !replay.Replayed || string(replay.Body) != string(first.Body) {
		t.Fatalf("receipt replay: %+v %v", replay, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&input.OperationID); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("validation failed")
	_, err = preview.WithOperationTransaction(ctx, db, input, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
		return preview.WithDataRepairCapture(ctx, tx, input, config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
			if _, err := tx.ExecContext(ctx, `UPDATE public.products SET category='failed'`); err != nil {
				return preview.OperationOutcome{}, err
			}
			return preview.OperationOutcome{}, wantErr
		})
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("callback error: %v", err)
	}
	var operations, changes, updated int
	err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM gregale_repair.operations),(SELECT count(*) FROM gregale_repair.changes),(SELECT count(*) FROM public.products WHERE category='new' AND gregale_row_version=2)`).Scan(&operations, &changes, &updated)
	if err != nil || operations != 1 || changes != 2 || updated != 2 {
		t.Fatalf("atomic capture: %d %d %d %v", operations, changes, updated, err)
	}
}

func TestDataRepairCaptureReadinessRestrictedWriter(t *testing.T) {
	path := os.Getenv("GREGALE_DATA_REPAIR_SCHEMA")
	if path == "" {
		t.Skip("generated repair schema required")
	}
	db, ctx := operationDatabase(t)
	schema, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE public.products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('public.products',ARRAY['category']);INSERT INTO public.products(id,category) VALUES(1,'old'),(2,'old')`); err != nil {
		t.Fatal(err)
	}
	var role, database string
	if err = db.QueryRowContext(ctx, `SELECT 'repair_writer_go_'||replace(gen_random_uuid()::text,'-',''),current_database()`).Scan(&role, &database); err != nil {
		t.Fatal(err)
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = db.ExecContext(ctx, "CREATE ROLE "+quoted+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(clean, "DROP OWNED BY "+quoted+";DROP ROLE "+quoted); err != nil {
			t.Error(err)
		}
	})
	grants, err := os.ReadFile(filepath.Join("..", "..", "data-repair-tests", "writer-grants.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, strings.ReplaceAll(string(grants), "__WRITER_ROLE__", quoted)); err != nil {
		t.Fatal(err)
	}
	writer := func(setting string) *sql.DB {
		t.Helper()
		cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Database = database
		pool := stdlib.OpenDB(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			if setting != "" {
				if _, err := conn.Exec(ctx, setting); err != nil {
					return err
				}
			}
			_, err := conn.Exec(ctx, "SET ROLE "+quoted)
			return err
		}))
		t.Cleanup(func() { _ = pool.Close() })
		return pool
	}
	application := writer("")
	input := operationInput(t)
	o := preview.DataRepairCaptureReadinessOptions{Scope: preview.DataRepairCaptureScope{AccountID: input.AccountID, AppID: input.AppID, PlatformTenantID: input.PlatformTenantID, Environment: "production"}, Table: "public.products", Release: "go-writer-v1"}
	check := func(pool *sql.DB, status, code string) preview.DataRepairCaptureReadinessReport {
		t.Helper()
		r, err := preview.CheckDataRepairCapture(ctx, pool, o)
		if err != nil || !r.Valid() || r.Status != status || r.Profile != "capture" {
			t.Fatalf("capture readiness: %+v %v", r, err)
		}
		if code != "" {
			found := false
			for _, f := range r.Findings {
				if f.Code == code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", code, r)
			}
		}
		return r
	}
	check(application, "ready", "")
	o.ReceiptProtocol = "customer"
	check(application, "ready", "")
	o.ReceiptProtocol = "managed"
	var plans, operations, changes, receipts int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM gregale_repair.plans),(SELECT count(*) FROM gregale_repair.operations),(SELECT count(*) FROM gregale_repair.changes),(SELECT count(*) FROM public.gregale_operation_inbox)`).Scan(&plans, &operations, &changes, &receipts); err != nil || plans+operations+changes+receipts != 0 {
		t.Fatal("diagnostics wrote durable state", err)
	}
	config := preview.DataRepairCapture{Table: o.Table, Environment: o.Scope.Environment, Release: o.Release}
	result, err := preview.WithOperationTransaction(ctx, application, input, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
		return preview.WithDataRepairCapture(ctx, tx, input, config, func(tx preview.OperationSQLTransaction) (preview.OperationOutcome, error) {
			_, err := tx.ExecContext(ctx, `UPDATE products SET category='new'`)
			return preview.OperationOutcome{Result: json.RawMessage(`{"changed":2}`)}, err
		})
	})
	if err != nil || result.Replayed {
		t.Fatal("restricted writer capture failed", err)
	}
	if _, err := db.ExecContext(ctx, "REVOKE EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	check(application, "blocked", "writer_permissions_missing")
	if _, err := db.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) TO "+quoted+";ALTER TABLE products DISABLE TRIGGER gregale_repair_capture"); err != nil {
		t.Fatal(err)
	}
	check(application, "blocked", "triggers_invalid")
	if _, err := db.ExecContext(ctx, "ALTER TABLE products ENABLE TRIGGER gregale_repair_capture"); err != nil {
		t.Fatal(err)
	}
	check(writer("SET session_replication_role='replica'"), "blocked", "capture_session_invalid")
	check(writer("SET default_transaction_read_only='on'"), "blocked", "capture_session_invalid")
	check(application, "ready", "")
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM products WHERE category='new' AND gregale_row_version=2`).Scan(&changes); err != nil || changes != 2 {
		t.Fatal("readiness changed captured row versions", changes, err)
	}
	if _, err := application.ExecContext(ctx, `SELECT gregale_repair.create_plan(gen_random_uuid(),'{}','')`); err == nil {
		t.Fatal("writer received repair authority")
	}
}
