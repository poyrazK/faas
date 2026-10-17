package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func assertNativeTableRecovery(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, publisher *pgxpool.Pool, source, target *pgx.Conn, targetURL string, oldAPI *nativeDataAPI, get func() Pipeline, wait func(func(Pipeline) bool), restart func()) {
	t.Helper()
	// Both tables have 100k rows. Corrupt only the new table after the API
	// refresh callback: the final seal must retain the failed table's name.
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'healthy recovery fixture',7 FROM generate_series(800000,898998) i`); err != nil {
		t.Fatal(err)
	}
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	corrupted := false
	engine.RefreshSchema = func(c context.Context, p Pipeline, change SchemaChange) ([]SchemaRefresh, bool, error) {
		if !corrupted {
			if _, err := target.Exec(c, `UPDATE public.customers SET name='corrupt imported row' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			corrupted = true
			return nil, true, nil
		}
		return originalRefresh(c, p, change)
	}
	wait(func(p Pipeline) bool { return p.Reason == "replica_mismatch" })
	failed := get()
	if !CanRecoverTable(failed) {
		t.Fatal("failed new table cannot be recovered", failed.VerificationProgress)
	}
	failedRun := failed.VerificationProgress.ID
	wait(func(Pipeline) bool {
		var count int
		return target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count) == nil && count == 100000
	})
	var heap uint32
	if err := target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')`).Scan(&heap); err != nil {
		t.Fatal(err)
	}
	// Fail native COPY/DELETE/TRUNCATE attempts on the healthy table, but allow
	// ordinary CDC updates. Also count committed truncates of the failed table.
	if _, err := target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_healthy_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'healthy table was recopied'; END $$;
 CREATE TRIGGER reject_healthy_recopy BEFORE INSERT OR DELETE OR TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_healthy_recopy();
 ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_healthy_recopy;
 CREATE TABLE gregale_pipeline.recovery_test_audit(n int);
 CREATE FUNCTION gregale_pipeline.audit_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO gregale_pipeline.recovery_test_audit VALUES(1); RETURN NULL; END $$;
 CREATE TRIGGER audit_recopy AFTER TRUNCATE ON public.customers FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.audit_recopy()`); err != nil {
		t.Fatal(err)
	}
	if err := store.TableRecovery(ctx, failed.AccountID, failed.ID, failed.Revision, ""); err != nil {
		t.Fatal(err)
	}
	wait(func(p Pipeline) bool { return p.Reason == "table_recovery_pending" })
	pending := get()
	if pending.SchemaChange.Recovery.State != "pending" || pending.SchemaChange.Recovery.Table != "customers" {
		t.Fatal("exact recovery preview missing")
	}
	var intact bool
	if err := target.QueryRow(ctx, `SELECT name='corrupt imported row' FROM public.customers WHERE id=1`).Scan(&intact); err != nil || !intact {
		t.Fatal("preview replaced data", err)
	}
	if err := store.TableRecovery(ctx, pending.AccountID, pending.ID, pending.Revision, pending.SchemaChange.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `ALTER TABLE public.customers ADD COLUMN recovery_drift text`); err != nil {
		t.Fatal(err)
	}
	wait(func(p Pipeline) bool { return p.Reason == "table_addition_source_changed" })
	if err := target.QueryRow(ctx, `SELECT name='corrupt imported row' FROM public.customers WHERE id=1`).Scan(&intact); err != nil || !intact {
		t.Fatal("source drift allowed destructive recovery", err)
	}
	if _, err := source.Exec(ctx, `ALTER TABLE public.customers DROP COLUMN recovery_drift`); err != nil {
		t.Fatal(err)
	}
	wait(func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "retiring" })
	approved := get()
	always := func(context.Context) error { return nil }
	// Lose the source DROP acknowledgement, then lose the independent REFRESH
	// acknowledgement. Replays must recognize the exact missing native relation.
	if err := recoveryPublication(ctx, approved, publisher, false, always); err != nil {
		t.Fatal(err)
	}
	if err := recoveryPublication(ctx, approved, publisher, false, always); err != nil {
		t.Fatal(err)
	}
	checks := 0
	err := retireRecoveredRelation(ctx, approved, publisher, target, func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed to lose retirement receipt", err)
	}
	if stage, err := loadTableRecovery(ctx, approved, target); err != nil || stage != "retiring" {
		t.Fatal("retirement acknowledgement advanced", stage, err)
	}
	restart()
	wait(func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "detached" })
	// Losing the fence before target COMMIT rolls back both TRUNCATE and stage.
	if err = clearRecoveredTable(ctx, approved, publisher, target, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("stale clear accepted", err)
	}
	if err = target.QueryRow(ctx, `SELECT name='corrupt imported row' FROM public.customers WHERE id=1`).Scan(&intact); err != nil || !intact {
		t.Fatal("failed clear committed", err)
	}
	wait(func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "cleared" })
	restart()
	// The source ADD can commit before its private acknowledgement as well.
	if err = recoveryPublication(ctx, approved, publisher, true, always); err != nil {
		t.Fatal(err)
	}
	if err = recoveryPublication(ctx, approved, publisher, true, always); err != nil {
		t.Fatal(err)
	}
	wait(func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "published" })
	locker, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = locker.Close(closing)
	}()
	locked, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(ctx) }()
	if _, err = locked.Exec(ctx, `LOCK TABLE public.customers IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	checks = 0
	err = copyRecoveredRelation(ctx, approved, publisher, target, func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed to lose copy receipt", err)
	}
	if stage, err := loadTableRecovery(ctx, approved, target); err != nil || stage != "published" {
		t.Fatal("copy acknowledgement advanced", stage, err)
	}
	restart()
	wait(func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "copying" })
	restart()
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=99.99 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		return status == 200 && strings.Contains(body, "99.99")
	})
	if _, err = source.Exec(ctx, `UPDATE public.customers SET name='updated during recovery' WHERE id=3;
 INSERT INTO public.customers(id,name) VALUES(100002,'inserted during recovery'); DELETE FROM public.customers WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	if err = locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wait(func(p Pipeline) bool {
		return p.VerificationProgress != nil && p.VerificationProgress.State == "running" && p.VerificationProgress.Rows >= 8000
	})
	if v := get().VerificationProgress; v.ID == failedRun || v.TablesTotal != 1 {
		t.Fatal("recovery reused the failed proof or verified healthy tables", v)
	}
	restart()
	var valid bool
	if err = target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT count(*)=100000 FROM public.orders)
 AND (SELECT count(*)=1 FROM gregale_pipeline.recovery_test_audit)
 AND (SELECT name='updated during copy' FROM public.customers WHERE id=1)
 AND (SELECT name='updated during recovery' FROM public.customers WHERE id=3)
 AND EXISTS(SELECT 1 FROM public.customers WHERE id=100002) AND NOT EXISTS(SELECT 1 FROM public.customers WHERE id=4)
 AND (SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$2)`, heap, approved.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("recovery changed healthy storage, cleared twice, lost CDC or closed shared reads", valid, err)
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER reject_healthy_recopy ON public.orders; DROP FUNCTION gregale_pipeline.reject_healthy_recopy();
 DROP TRIGGER audit_recopy ON public.customers; DROP FUNCTION gregale_pipeline.audit_recopy(); DROP TABLE gregale_pipeline.recovery_test_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id>=800000; UPDATE public.orders SET total=88.88 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=1001 AND bool_or(id=1 AND total=88.88) FROM public.orders`).Scan(&valid) == nil && valid
	})
}
