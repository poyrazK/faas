package datarepair

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCapturedWritersOnDistinctRowsCommitConcurrently(t *testing.T) {
	e, scope, _ := fixture(t, 2)
	first, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(t.Context()) }()
	if err := Capture(t.Context(), first, scope, uuid.NewString(), "first", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	second, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Rollback(t.Context()) }()
	if _, err := second.Exec(t.Context(), `SET LOCAL lock_timeout='500ms'`); err != nil {
		t.Fatal(err)
	}
	if err := Capture(t.Context(), second, scope, uuid.NewString(), "second", "products", func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=2`)
		return err
	}); err != nil {
		t.Fatal("independent capture blocked", err)
	}
	if err := second.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var firstVisible, secondVisible string
	if err := e.Pool.QueryRow(t.Context(), `SELECT (SELECT category FROM products WHERE id=1),(SELECT category FROM products WHERE id=2)`).Scan(&firstVisible, &secondVisible); err != nil || firstVisible != "bad" || secondVisible != "human" {
		t.Fatal("second writer did not commit independently", firstVisible, secondVisible, err)
	}
	if err := first.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureConcurrentIndexMaintenance(t *testing.T) {
	for _, timing := range []string{"before capture", "after capture"} {
		t.Run(timing, func(t *testing.T) {
			e, scope, _ := fixture(t, 2)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			maintenance, err := e.Pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer maintenance.Release()
			writer, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(t.Context()) }()
			if timing == "before capture" {
				if _, err := writer.Exec(ctx, `UPDATE products SET quantity=2 WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			} else if err := Capture(ctx, writer, scope, uuid.NewString(), "before-index", "products", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE products SET category='human' WHERE id=1`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := maintenance.Exec(ctx, `CREATE INDEX CONCURRENTLY products_quantity_idx ON products(quantity)`)
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var building bool
				if err := e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='products'::regclass AND mode='ShareUpdateExclusiveLock' AND granted)`, maintenance.Conn().PgConn().PID()).Scan(&building); err != nil {
					t.Fatal(err)
				}
				if building {
					break
				}
				select {
				case err := <-done:
					t.Fatal("index build bypassed the existing writer", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			if timing == "before capture" {
				blocked, err := e.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				called := false
				err = Capture(ctx, blocked, scope, uuid.NewString(), "during-index", "products", func(pgx.Tx) error { called = true; return nil })
				_ = blocked.Rollback(t.Context())
				if !errors.Is(err, ErrConflict) || called {
					t.Fatal("capture entered active maintenance", called, err)
				}
				if err := writer.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := writer.Commit(ctx); err != nil {
				t.Fatal("later index build prevented capture finalization", err)
			}
			if err := <-done; err != nil {
				t.Fatal("index build did not finish after writer retirement", err)
			}
		})
	}
}

func TestCaptureStillFencesTableDDL(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	writer, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	if err := Capture(t.Context(), writer, scope, uuid.NewString(), "ddl-fence", "products", func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ddl, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ddl.Rollback(t.Context()) }()
	_, err = ddl.Exec(t.Context(), `SET LOCAL lock_timeout='250ms'; ALTER TABLE products ADD COLUMN additional int`)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatal("schema DDL bypassed capture", err)
	}
}

func TestCaptureSnapshotIsolationStillFencesConcurrentIndexes(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			writer, err := e.Pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(t.Context()) }()
			if err := Capture(t.Context(), writer, scope, uuid.NewString(), "snapshot-fence", "products", func(pgx.Tx) error { return nil }); err != nil {
				t.Fatal(err)
			}
			maintenance, err := e.Pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer maintenance.Release()
			if _, err := maintenance.Exec(t.Context(), `SET lock_timeout='250ms'`); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = maintenance.Exec(t.Context(), `RESET lock_timeout`) }()
			_, err = maintenance.Exec(t.Context(), `CREATE INDEX CONCURRENTLY snapshot_quantity_idx ON products(quantity)`)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "55P03" {
				t.Fatal("index maintenance bypassed retained catalog snapshot", err)
			}
			if err := writer.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCaptureReadinessRequiresMaintenanceInspectionPrivilege(t *testing.T) {
	owner, writer, options, _ := captureTestSetup(t)
	if _, err := owner.Pool.Exec(t.Context(), `REVOKE SELECT ON pg_catalog.pg_locks FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	report, err := writer.CheckCapture(t.Context(), options)
	if err != nil || report.Status != "blocked" {
		t.Fatalf("writer missing maintenance visibility reported ready: %+v %v", report, err)
	}
	for _, finding := range report.Findings {
		if finding.ID != "permissions" || finding.Code != "writer_permissions_missing" {
			continue
		}
		for _, object := range finding.Objects {
			if object == "pg_catalog.pg_locks: SELECT" {
				return
			}
		}
	}
	t.Fatal("missing maintenance inspection grant not diagnosed", report)
}
