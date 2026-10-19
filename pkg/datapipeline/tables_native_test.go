package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeTableAddition(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
	t.Helper()
	generation := p.Generation
	name := pipelineName(Pipeline{DataPipeline: p})
	var orderOID, subOID, pubOID uint32
	var originalTotal string
	if err := target.QueryRow(ctx, `SELECT total::text FROM public.orders WHERE id=1`).Scan(&originalTotal); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid,(SELECT oid FROM pg_subscription WHERE subname=$1)`, name).Scan(&orderOID, &subOID); err != nil {
		t.Fatal(err)
	}
	if err := source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1`, name).Scan(&pubOID); err != nil {
		t.Fatal(err)
	}
	oldAPI := startNativeDataAPI(t, ctx, targetURL, reader)
	defer oldAPI.close()
	if _, err := source.Exec(ctx, `CREATE TABLE public.customers(id bigint PRIMARY KEY,name text NOT NULL,joined_at timestamptz NOT NULL DEFAULT '2026-10-17T00:00Z');
 INSERT INTO public.customers(id,name) SELECT i,'customer '||i FROM generate_series(1,100000) i`); err != nil {
		t.Fatal(err)
	}
	get := func() Pipeline {
		t.Helper()
		v, err := store.Get(ctx, account, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	wait := func(condition func(Pipeline) bool) {
		t.Helper()
		for until := time.Now().Add(time.Minute); ; {
			p = tick()
			v := get()
			if condition(v) {
				return
			}
			if p.State == "resync_required" || time.Now().After(until) {
				t.Fatalf("table addition stalled: %+v plan=%+v progress=%+v", p, v.SchemaChange, v.VerificationProgress)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Settle the preceding fixture's final observation before requesting an
	// operation that deliberately requires a live, completed workflow.
	wait(func(v Pipeline) bool {
		return v.State == "live" && v.SchemaChange != nil && v.SchemaChange.State == "complete" && (v.VerificationProgress == nil || v.VerificationProgress.State == "passed")
	})
	current := get()
	c, err := store.Connection(ctx, account, p.SourceConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	check := engine.Check(ctx, job, source.Config().ConnString())
	if err = store.FinishCheck(ctx, job, check); err != nil {
		t.Fatal(err)
	}
	if err = store.RequestTables(ctx, account, p.ID, current.Revision, []string{"customers"}); err != nil {
		t.Fatal("table selection failed", err, get())
	}
	wait(func(v Pipeline) bool { return v.Reason == "table_addition_pending" })
	pending := get()
	if pending.SchemaChange == nil || !pending.SchemaChange.Supported {
		t.Fatal("table preview missing")
	}
	if status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", ""); status != 200 || strings.TrimSpace(body) == "[]" {
		t.Fatalf("pending table approval interrupted old reads: %d %s", status, body)
	}
	var absent bool
	if err = target.QueryRow(ctx, `SELECT to_regclass('public.customers') IS NULL`).Scan(&absent); err != nil || !absent {
		t.Fatal("new table appeared before approval", err)
	}
	if err = store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	approved := get()
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	if err = ensureTableReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	// Commit source publication extension without its target acknowledgement.
	// Reconciliation must recognize the exact approved membership on restart.
	always := func(context.Context) error { return nil }
	if err = prepareAddedTables(ctx, approved, publisher, target, ownerForNativeTable(t, ctx, target), *approved.SchemaChange, always); err != nil {
		t.Fatal(err)
	}
	if err = publishAddedTables(ctx, approved, publisher, *approved.SchemaChange, always); err != nil {
		t.Fatal(err)
	}
	if err = publishAddedTables(ctx, approved, publisher, *approved.SchemaChange, always); err != nil {
		t.Fatal("publication acknowledgement could not be recovered", err)
	}
	if err = saveTableStage(ctx, approved, target, "published", always); err != nil {
		t.Fatal(err)
	}
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
	if _, err = locked.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,51949))`, p.ID); err != nil {
		t.Fatal(err)
	}
	// Block inside a real COPY after PostgreSQL has created its sync slot.
	if _, err = target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.hold_customers_copy() RETURNS trigger LANGUAGE plpgsql AS `+literal(`BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(`+literal(p.ID)+`,51949)); RETURN NEW; END`)+`;
 CREATE TRIGGER hold_customers_copy BEFORE INSERT ON public.customers FOR EACH ROW EXECUTE FUNCTION gregale_pipeline.hold_customers_copy(); ALTER TABLE public.customers ENABLE ALWAYS TRIGGER hold_customers_copy;
 CREATE TABLE gregale_pipeline.initial_recopy_audit(n int);
 CREATE FUNCTION gregale_pipeline.audit_initial_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO gregale_pipeline.initial_recopy_audit VALUES(1); RETURN NULL; END $$;
 CREATE TRIGGER audit_initial_recopy AFTER TRUNCATE ON public.customers FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.audit_initial_recopy()`); err != nil {
		t.Fatal(err)
	}
	// REFRESH commits outside the receipt transaction. Lose its following stage
	// write and recover from existing native relation entries without another copy.
	checks := 0
	_, err = refreshAddedTables(ctx, approved, publisher, target, "published", func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("fixture failed to lose refresh acknowledgement", err)
	}
	var stage string
	if err = target.QueryRow(ctx, `SELECT table_stage FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&stage); err != nil || stage != "published" {
		t.Fatal("failed refresh receipt advanced", stage, err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.Reason == "table_addition_copying" })
	restart()
	assertNativeInitialCopyRecovery(t, ctx, store, publisher, source, target, &oldAPI, get, wait, restart)
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=88.88 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	// Poll the independent reader without advancing the approved recovery.
	for until := time.Now().Add(time.Minute); ; {
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		if status == 200 && strings.Contains(body, "88.88") {
			break
		}
		if time.Now().After(until) {
			t.Fatal("healthy CDC stalled behind active COPY")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = source.Exec(ctx, `UPDATE public.customers SET name='updated during copy' WHERE id=1; INSERT INTO public.customers(id,name) VALUES(100001,'inserted during copy'); DELETE FROM public.customers WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if err = locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(20 * time.Second); ; {
		var ready bool
		if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid=r.srsubid WHERE s.subname=$1 AND r.srrelid='public.customers'::regclass AND r.srsubstate='r')`, name).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if time.Now().After(until) {
			t.Fatal("native recopy did not finish after releasing gate")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A third preview was approved while COPY was blocked. Native completion
	// before durable recovery starts must withdraw that approval, never clear again.
	restart()
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	refreshReady := false
	engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		return nil, refreshReady, nil
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.Rows >= 8000 && v.VerificationProgress.State == "running"
	})
	partial := get()
	if partial.SchemaChange.Recovery != nil || partial.SchemaChange.Retry != 3 {
		t.Fatal("finished COPY was needlessly recovered", partial.SchemaChange.Recovery, partial.SchemaChange.Retry)
	}
	var clears int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM gregale_pipeline.initial_recopy_audit`).Scan(&clears); err != nil || clears != 2 {
		t.Fatal("finished COPY was cleared again", clears, err)
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER hold_customers_copy ON public.customers; DROP FUNCTION gregale_pipeline.hold_customers_copy();
 DROP TRIGGER audit_initial_recopy ON public.customers; DROP FUNCTION gregale_pipeline.audit_initial_recopy(); DROP TABLE gregale_pipeline.initial_recopy_audit`); err != nil {
		t.Fatal(err)
	}
	if partial.VerificationProgress.TablesTotal != 1 || partial.VerificationProgress.Table == "orders" {
		t.Fatal("existing tables entered the new-table proof", partial.VerificationProgress)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET verification_progress='{}' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	restart()
	p = tick()
	resumed := get()
	if resumed.VerificationProgress == nil || resumed.VerificationProgress.ID != partial.VerificationProgress.ID || resumed.VerificationProgress.Rows <= partial.VerificationProgress.Rows {
		t.Fatal("new-table verification restarted instead of resuming", resumed.VerificationProgress)
	}
	assertNativeTableRecovery(t, ctx, store, engine, publisher, source, target, targetURL, &oldAPI, get, wait, restart)
	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	fresh := startNativeDataAPI(t, ctx, targetURL, reader)
	defer fresh.close()
	if fresh.fingerprint == oldAPI.fingerprint {
		t.Fatal("table addition did not change the API contract")
	}
	if status, body := fresh.requestTable("GET", "alice", "customers", "id=eq.1&select=id,name", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("unverified table exposed rows: %d %s", status, body)
	}
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,total", ""); status != 200 || !strings.Contains(body, "88.88") {
		t.Fatalf("API refresh gated existing tables: %d %s", status, body)
	}
	refreshReady = true
	wait(func(v Pipeline) bool { return v.State == "live" && v.SchemaChange.State == "complete" })
	// Final proof, read gates and completion commit together in the mirror.
	// Recover a lost catalog acknowledgement without recopying or stale progress.
	completed := get()
	lost := *completed.SchemaChange
	lost.State = "refreshing"
	lostRecovery := *lost.Recovery
	lostRecovery.State, lostRecovery.Stage = "approved", "verifying"
	lost.Recovery = &lostRecovery
	lostProgress := *completed.VerificationProgress
	lostProgress.State = "running"
	lostJSON, _ := json.Marshal(lost)
	progressJSON, _ := json.Marshal(lostProgress)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,verification_progress=$3 WHERE id=$1`, p.ID, lostJSON, progressJSON); err != nil {
		t.Fatal(err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.State == "live" && v.SchemaChange.State == "complete" })
	if recovered := get(); recovered.SchemaChange.Recovery.State != "complete" || recovered.VerificationProgress.State != "passed" {
		t.Fatal("completion acknowledgement lost its durable proof", recovered.VerificationProgress)
	}
	if status, body := fresh.requestTable("GET", "alice", "customers", "id=eq.1&select=id,name", ""); status != 200 || !strings.Contains(body, "updated during copy") {
		t.Fatalf("new table data unavailable: %d %s", status, body)
	}
	if status, body := fresh.requestTable("GET", "bob", "customers", "id=eq.1&select=id,name", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("new table leaked rows: %d %s", status, body)
	}
	if status, _ := fresh.requestTable("PATCH", "alice", "customers", "id=eq.1", `{"name":"forbidden"}`); status != 403 {
		t.Fatal("new table became writable", status)
	}
	var same, values bool
	if err = target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$2 AND (SELECT oid=$3 FROM pg_subscription WHERE subname=$1),
 (SELECT count(*)=100000 FROM public.customers) AND NOT EXISTS(SELECT 1 FROM public.customers WHERE id=2)
 AND EXISTS(SELECT 1 FROM public.customers WHERE id=100001 AND name='inserted during copy')`, name, orderOID, subOID).Scan(&same, &values); err != nil || !same || !values || p.Generation != generation {
		t.Fatal("table addition replaced old resources or lost data", same, values, err)
	}
	if err = source.QueryRow(ctx, `SELECT oid=$2 FROM pg_publication WHERE pubname=$1`, name, pubOID).Scan(&same); err != nil || !same {
		t.Fatal("publication was replaced", err)
	}
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=$1::numeric WHERE id=1`, originalTotal); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		var value string
		return target.QueryRow(ctx, `SELECT total::text FROM public.orders WHERE id=1`).Scan(&value) == nil && value == originalTotal
	})
	return p
}

func ownerForNativeTable(t *testing.T, ctx context.Context, target *pgx.Conn) string {
	t.Helper()
	var owner string
	if err := target.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='orders'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}
