package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeTableCancellation(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
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
				t.Fatalf("cancellation stalled: %+v plan=%+v", p, v.SchemaChange)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	var orderHeap uint32
	var subOID, pubOID uint32
	var originalTotal string
	if err := target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders'),(SELECT oid FROM pg_subscription WHERE subname=$1),(SELECT total::text FROM public.orders WHERE id=1)`, pipelineName(get())).Scan(&orderHeap, &subOID, &originalTotal); err != nil {
		t.Fatal(err)
	}
	if err := source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1`, pipelineName(get())).Scan(&pubOID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `CREATE TABLE public.invoices(id bigint PRIMARY KEY,name text); CREATE TABLE public.payments(LIKE public.invoices INCLUDING ALL);
 INSERT INTO public.invoices SELECT i,'invoice '||i FROM generate_series(1,100000) i;
 INSERT INTO public.payments SELECT i,'payment '||i FROM generate_series(1,100000) i;
 INSERT INTO public.orders(id,customer,total) SELECT i,'cancel healthy fixture',7 FROM generate_series(900000,998998) i`); err != nil {
		t.Fatal(err)
	}
	var valid bool
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=100000 FROM public.orders`).Scan(&valid) == nil && valid
	})
	if _, err := target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_cancel_healthy_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'healthy table was recopied'; END $$;
 CREATE TRIGGER reject_cancel_healthy_recopy BEFORE INSERT OR DELETE OR TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_cancel_healthy_recopy(); ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_cancel_healthy_recopy`); err != nil {
		t.Fatal(err)
	}
	oldAPI := startNativeDataAPI(t, ctx, targetURL, reader)
	defer oldAPI.close()
	request := func(tables []string) Pipeline {
		t.Helper()
		c, err := store.Connection(ctx, account, p.SourceConnectionID)
		if err != nil {
			t.Fatal(err)
		}
		// Advance only the fixture's retry clock so a fast preceding scenario
		// cannot coalesce discovery performed before these source tables existed.
		if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '1 day' WHERE connection_id=$1`, c.ID); err != nil {
			t.Fatal(err)
		}
		checkRequest, err := store.RequestCheck(ctx, account, c.ID, c.Revision, "")
		if err != nil {
			t.Fatal(err)
		}
		if checkRequest.State == "pending" || checkRequest.State == "running" {
			job, err := store.ClaimCheck(ctx, uuid.NewString())
			if err != nil {
				t.Fatal("claim connection check", err)
			}
			check := engine.Check(ctx, job, source.Config().ConnString())
			if err = store.FinishCheck(ctx, job, check); err != nil {
				t.Fatal(err)
			}
		}
		wait(func(v Pipeline) bool { return v.State == "live" && v.ObservationFresh })
		v := get()
		if err = store.RequestTables(ctx, account, p.ID, v.Revision, tables); err != nil {
			t.Fatal("request table additions", tables, err, v.State, v.Reason)
		}
		wait(func(v Pipeline) bool { return v.Reason == "table_addition_pending" })
		v = get()
		if err = store.ApproveSchema(ctx, account, p.ID, v.Revision, v.SchemaChange.ID, false); err != nil {
			t.Fatal("approve table additions", err, v.SchemaChange)
		}
		return get()
	}
	approved := request([]string{"invoices", "payments"})
	originalChange := approved.SchemaApproval
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	always := func(context.Context) error { return nil }
	if err = prepareAddedTables(ctx, approved, publisher, target, ownerForNativeTable(t, ctx, target), *approved.SchemaChange, always); err != nil {
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
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = locked.Rollback(closing)
	}()
	for _, table := range []string{"invoices", "payments"} {
		if _, err = locked.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,51950))`, p.ID+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = target.Exec(ctx, `INSERT INTO public.invoices VALUES(-1,'retain this committed row');
 CREATE FUNCTION gregale_pipeline.hold_cancel_copy() RETURNS trigger LANGUAGE plpgsql AS `+literal(`BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(`+literal(p.ID)+`||TG_TABLE_NAME,51950)); RETURN NEW; END`)+`;
 CREATE TRIGGER hold_cancel_copy BEFORE INSERT ON public.invoices FOR EACH ROW EXECUTE FUNCTION gregale_pipeline.hold_cancel_copy(); ALTER TABLE public.invoices ENABLE ALWAYS TRIGGER hold_cancel_copy;
 CREATE TRIGGER hold_cancel_copy BEFORE INSERT ON public.payments FOR EACH ROW EXECUTE FUNCTION gregale_pipeline.hold_cancel_copy(); ALTER TABLE public.payments ENABLE ALWAYS TRIGGER hold_cancel_copy`); err != nil {
		t.Fatal(err)
	}
	if err = publishAddedTables(ctx, approved, publisher, *approved.SchemaChange, always); err != nil {
		t.Fatal(err)
	}
	if err = saveTableStage(ctx, approved, target, "published", always); err != nil {
		t.Fatal(err)
	}
	if _, err = refreshAddedTables(ctx, approved, publisher, target, "published", always); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool {
		return len(v.SchemaChange.Copies) == 2 && v.SchemaChange.Copies[0].WorkerActive && v.SchemaChange.Copies[1].WorkerActive
	})
	var invoicePID, paymentPID int32
	var invoiceOID uint32
	if err = target.QueryRow(ctx, `SELECT 'public.invoices'::regclass::oid,(SELECT pid FROM pg_stat_subscription WHERE subid=$1 AND relid='public.invoices'::regclass),(SELECT pid FROM pg_stat_subscription WHERE subid=$1 AND relid='public.payments'::regclass)`, subOID).Scan(&invoiceOID, &invoicePID, &paymentPID); err != nil {
		t.Fatal(err)
	}
	var copySlot string
	if err = source.QueryRow(ctx, `SELECT slot_name::text FROM pg_replication_slots WHERE database=current_database() AND starts_with(slot_name,$1)`, fmt.Sprintf("pg_%d_sync_%d_", subOID, invoiceOID)).Scan(&copySlot); err != nil {
		t.Fatal(err)
	}
	cancel := func(table string) Pipeline {
		t.Helper()
		v := get()
		if err := store.TableCancellation(ctx, account, p.ID, v.Revision, "orders", ""); !errors.Is(err, ErrConflict) {
			t.Fatal("baseline cancellation accepted", err)
		}
		if err := store.TableCancellation(ctx, account, p.ID, v.Revision, table, ""); err != nil {
			t.Fatal("request cancellation", table, err, v.State, v.Reason, v.SchemaChange)
		}
		wait(func(v Pipeline) bool {
			return v.Reason == "table_cancellation_pending" && v.SchemaChange.Cancellation.State == "pending"
		})
		v = get()
		if err := store.TableCancellation(ctx, account, p.ID, v.Revision, "", v.SchemaChange.Cancellation.ID); err != nil {
			t.Fatal("approve cancellation", table, err, v.State, v.Reason, v.SchemaChange.Cancellation)
		}
		return get()
	}
	approved = cancel("invoices")
	if err = ensureTableCancellationReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = beginTableCancellation(ctx, approved, target, always); err != nil {
		t.Fatal("active copy blocked durable intent", err)
	}
	// Lose both the source withdrawal reply and the native retirement receipt.
	if err = withdrawCancelledPublication(ctx, approved, publisher, always); err != nil {
		t.Fatal(err)
	}
	checks := 0
	err = retireCancelledRelation(ctx, approved, publisher, target, func(context.Context) error {
		checks++
		if checks == 2 {
			return ErrUnavailable
		}
		return nil
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("retirement acknowledgement was not lost", err)
	}
	if stage, _, err := loadTableCancellation(ctx, approved, target); err != nil || stage != "retiring" {
		t.Fatal("lost acknowledgement advanced receipt", stage, err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1) AND NOT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$2 AND srrelid=$3)
 AND EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$2 AND relid='public.payments'::regclass AND pid=$4)`, invoicePID, subOID, invoiceOID, paymentPID).Scan(&valid); err != nil || !valid {
		t.Fatal("native retirement touched the other copy", err)
	}
	if err = source.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1)`, copySlot).Scan(&valid); err != nil || !valid {
		t.Fatal("cancelled sync slot leaked", err)
	}
	// Cleanup binds owned identities, so later source definition drift cannot
	// force recopy of a table the customer explicitly chose to abandon.
	if _, err = source.Exec(ctx, `ALTER TABLE public.invoices ADD COLUMN changed_after_approval text`); err != nil {
		t.Fatal(err)
	}
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	refreshReady := false
	engine.RefreshSchema = func(_ context.Context, v Pipeline, c SchemaChange) ([]SchemaRefresh, bool, error) {
		if v.SchemaChange.Cancellation == nil || c.ID != v.SchemaChange.Cancellation.ID {
			t.Fatal("cancellation reused onboarding refresh")
		}
		return nil, refreshReady, nil
	}
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Cancellation.Stage == "archived" })
	if get().TablesReady != 2 {
		t.Fatal("cancellation lost healthy native readiness")
	}
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=55.55 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(15 * time.Second); ; {
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		if status == 200 && strings.Contains(body, "55.55") {
			break
		}
		if time.Now().After(until) {
			t.Fatal("healthy reads/CDC stopped during cancellation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	if !slices.Contains(get().Tables, "invoices") {
		t.Fatal("selection completed before API refresh")
	}
	private := retainedTableName(*approved.SchemaChange.Cancellation)
	if err = target.QueryRow(ctx, "SELECT count(*)=1 FROM gregale_pipeline."+ident(private)).Scan(&valid); err != nil || !valid {
		t.Fatal("committed rows were not retained", err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT has_schema_privilege($1,'gregale_pipeline','USAGE')`, reader).Scan(&valid); err != nil || !valid {
		t.Fatal("reader can access retained rows", err)
	}
	newAPI := startNativeDataAPI(t, ctx, targetURL, reader)
	if status, body := newAPI.requestTable("GET", "alice", "invoices", "select=id", ""); status != 404 {
		t.Fatal("cancelled table remains in API", status, body)
	}
	newAPI.close()
	// Commit completion but discard its catalog acknowledgement.
	refreshReady = true
	old := get()
	ob := Observation{State: "blocked", Verification: old.Verification}
	if err = engine.reconcileTableCancellation(ctx, old, source.Config().ConnString(), publisher, target, ownerForNativeTable(t, ctx, target), &ob, always); err != nil {
		t.Fatal(err)
	}
	if old.SchemaApproval == ob.SchemaChange.ID {
		t.Fatal("subset reused old proof identity")
	}
	restart()
	wait(func(v Pipeline) bool {
		return v.SchemaChange.Cancellation.State == "complete" && !slices.Contains(v.Tables, "invoices")
	})
	if !slices.Equal(get().SchemaChange.AddedTables, []string{"payments"}) {
		t.Fatal("remaining onboarding lost")
	}
	if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$1 AND relid='public.payments'::regclass AND pid=$2)`, subOID, paymentPID).Scan(&valid); err != nil || !valid {
		t.Fatal("remaining copy restarted", err)
	}
	if err = locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Native completion before the next tick leaves the table gated. Cancel it
	// with all 100k committed rows, exercising retained data and last-table exit.
	for until := time.Now().Add(20 * time.Second); ; {
		if err = target.QueryRow(ctx, `SELECT count(*)=100000 FROM public.payments`).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if valid {
			break
		}
		if time.Now().After(until) {
			t.Fatal("remaining copy did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cancel("payments")
	wait(func(v Pipeline) bool { return v.State == "live" && v.SchemaChange.Cancellation.State == "complete" })
	// Re-adding the original source uses a fresh table and verification intent.
	approved = request([]string{"invoices"})
	if approved.SchemaApproval == originalChange {
		t.Fatal("re-add adopted cancelled approval")
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.State == "running" && v.VerificationProgress.Rows >= 8000
	})
	if err = target.QueryRow(ctx, `SELECT 'public.invoices'::regclass::oid<>$1 AND (SELECT count(*)=100000 FROM public.invoices)`, invoiceOID).Scan(&valid); err != nil || !valid {
		t.Fatal("re-add reused retained data", err)
	}
	_ = cancel("invoices")
	wait(func(v Pipeline) bool { return v.State == "live" && v.SchemaChange.Cancellation.State == "complete" })
	if err = target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT count(*)=100000 FROM public.orders)
 AND (SELECT oid=$2 FROM pg_subscription WHERE subname=$3) AND (SELECT count(*)=2 FROM pg_subscription_rel WHERE srsubid=$2)
 AND (SELECT count(*)=3 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gregale_pipeline' AND starts_with(c.relname,'retained_') AND c.relkind='r')`, orderHeap, subOID, pipelineName(get())).Scan(&valid); err != nil || !valid {
		t.Fatal("cancellation changed healthy storage/native identities", err)
	}
	if err = source.QueryRow(ctx, `SELECT oid=$1 FROM pg_publication WHERE pubname=$2`, pubOID, pipelineName(get())).Scan(&valid); err != nil || !valid {
		t.Fatal("publication replaced", err)
	}
	if !slices.Equal(get().Tables, []string{"customers", "orders"}) || get().Generation != p.Generation {
		t.Fatal("final selection/generation changed")
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER reject_cancel_healthy_recopy ON public.orders; DROP FUNCTION gregale_pipeline.reject_cancel_healthy_recopy()`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id BETWEEN 900000 AND 998998`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=$1 WHERE id=1`, originalTotal); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=1001 AND bool_or(id=1 AND total::text=$1) FROM public.orders`, originalTotal).Scan(&valid) == nil && valid
	})
	return p
}
