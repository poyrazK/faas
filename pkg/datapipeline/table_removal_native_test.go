package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

type liveRemovalWriteResult struct {
	value int64
	err   error
}

func writeDuringLiveRemoval(ctx context.Context, raw string) liveRemovalWriteResult {
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return liveRemovalWriteResult{err: err}
	}
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(closing)
	}()
	var last int64
	for value := int64(1001); ; value++ {
		tx, err := conn.Begin(ctx)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE public.orders SET total=$1 WHERE id=1`, value)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE public.customers SET name=$1 WHERE id=1`, fmt.Sprintf("removal update %d", value))
			}
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				_ = tx.Rollback(closing)
				cancel()
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				err = nil
			}
			return liveRemovalWriteResult{last, err}
		}
		last = value
		select {
		case <-ctx.Done():
			return liveRemovalWriteResult{value: last}
		case <-time.After(15 * time.Millisecond):
		}
	}
}

func assertNativeLiveTableRemoval(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
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
				t.Fatalf("live removal stalled: %+v plan=%+v", p, v.SchemaChange)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	var orderOID, heap, customerOID, subOID, pubOID uint32
	var originalTotal string
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid,pg_relation_filenode('public.orders'),'public.customers'::regclass::oid,
 (SELECT oid FROM pg_subscription WHERE subname=$1),(SELECT total::text FROM public.orders WHERE id=1)`, pipelineName(get())).Scan(&orderOID, &heap, &customerOID, &subOID, &originalTotal); err != nil {
		t.Fatal(err)
	}
	if err := source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1`, pipelineName(get())).Scan(&pubOID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'live removal healthy fixture',7 FROM generate_series(900000,998998) i`); err != nil {
		t.Fatal(err)
	}
	var valid bool
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT (SELECT count(*)=100000 FROM public.orders) AND (SELECT count(*)=100000 FROM public.customers)`).Scan(&valid) == nil && valid
	})
	if _, err := target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_live_removal_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'healthy table was recopied or imported rows cleared'; END $$;
 CREATE TRIGGER reject_live_removal_recopy BEFORE INSERT OR DELETE OR TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_live_removal_recopy(); ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_live_removal_recopy;
 CREATE TRIGGER reject_live_removal_clear BEFORE TRUNCATE ON public.customers FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_live_removal_recopy(); ALTER TABLE public.customers ENABLE ALWAYS TRIGGER reject_live_removal_clear`); err != nil {
		t.Fatal(err)
	}
	oldAPI := startNativeDataAPI(t, ctx, targetURL, reader)
	defer oldAPI.close()
	checkHealthy := func() {
		t.Helper()
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		if status != 200 || strings.TrimSpace(body) == "[]" {
			t.Fatal("healthy reads interrupted", status, body)
		}
	}
	checkCustomer := func(readable bool) {
		t.Helper()
		status, body := oldAPI.requestTable("GET", "alice", "customers", "id=eq.1&select=id,name", "")
		if status != 200 || (strings.TrimSpace(body) != "[]") != readable {
			t.Fatal("selected table gate changed incorrectly", readable, status, body)
		}
	}
	writeCtx, cancelWrites := context.WithCancel(ctx)
	done := make(chan liveRemovalWriteResult, 1)
	go func() { done <- writeDuringLiveRemoval(writeCtx, source.Config().ConnString()) }()
	stopped := false
	var written liveRemovalWriteResult
	stopWrites := func() liveRemovalWriteResult {
		if !stopped {
			cancelWrites()
			select {
			case written = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("live-removal writer did not stop")
			}
			stopped = true
		}
		return written
	}
	defer stopWrites()
	preview := func() Pipeline {
		t.Helper()
		wait(func(v Pipeline) bool { return v.State == "live" && v.ObservationFresh })
		v := get()
		if err := store.TableRemoval(ctx, account, p.ID, v.Revision, "customers", ""); err != nil {
			t.Fatal("request live removal", err, v.State, v.Reason)
		}
		wait(func(v Pipeline) bool { return v.Reason == "table_removal_pending" })
		return get()
	}
	pending := preview()
	firstID := pending.SchemaChange.Cancellation.ID
	checkHealthy()
	checkCustomer(true)
	if err := store.TableRemoval(ctx, account, p.ID, pending.Revision, "", ""); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool { return v.State == "live" && v.ObservationFresh })
	checkCustomer(true)
	pending = preview()
	if err := store.TableRemoval(ctx, account, p.ID, pending.Revision, "", firstID); !errors.Is(err, ErrConflict) {
		t.Fatal("dismissed preview was approved", err)
	}
	if err := store.TableRemoval(ctx, account, p.ID, pending.Revision, "", pending.SchemaChange.Cancellation.ID); err != nil {
		t.Fatal(err)
	}
	approved := get()
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	always := func(context.Context) error { return nil }
	if err = beginTableCancellation(ctx, approved, target, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("unfenced removal began", err)
	}
	checkCustomer(true)
	checkHealthy()
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Cancellation.Stage == "retiring" })
	checkCustomer(false)
	checkHealthy()
	if get().TablesReady != 1 {
		t.Fatal("remaining native readiness lost")
	}
	// Commit source and native withdrawal while losing their acknowledgements.
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
		t.Fatal("native removal acknowledgement was not lost", err)
	}
	if stage, _, err := loadTableCancellation(ctx, approved, target); err != nil || stage != "retiring" {
		t.Fatal("lost native acknowledgement advanced receipt", stage, err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$1 AND srrelid=$2)
 AND EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$1 AND srrelid=$3 AND srsubstate='r')`, subOID, customerOID, orderOID).Scan(&valid); err != nil || !valid {
		t.Fatal("native removal touched healthy tracking", err)
	}
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	refreshReady := false
	engine.RefreshSchema = func(_ context.Context, v Pipeline, c SchemaChange) ([]SchemaRefresh, bool, error) {
		if v.SchemaChange.Cancellation.Kind != "live" || c.ID != v.SchemaChange.Cancellation.ID {
			t.Fatal("live removal reused another API refresh")
		}
		return nil, refreshReady, nil
	}
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Cancellation.Stage == "archived" })
	checkHealthy()
	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	if !slices.Contains(get().Tables, "customers") {
		t.Fatal("selection changed before API refresh")
	}
	retained := retainedTableName(*approved.SchemaChange.Cancellation)
	var retainedName string
	if err = target.QueryRow(ctx, "SELECT name FROM gregale_pipeline."+ident(retained)+" WHERE id=1").Scan(&retainedName); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, "SELECT count(*)=100000 FROM gregale_pipeline."+ident(retained)).Scan(&valid); err != nil || !valid {
		t.Fatal("live rows not retained", err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT has_schema_privilege($1,'gregale_pipeline','USAGE')`, reader).Scan(&valid); err != nil || !valid {
		t.Fatal("retained data became readable", err)
	}
	newAPI := startNativeDataAPI(t, ctx, targetURL, reader)
	defer newAPI.close()
	if oldAPI.fingerprint == newAPI.fingerprint {
		t.Fatal("API contract still includes removed table")
	}
	for _, subject := range []string{"alice", "bob"} {
		if status, body := newAPI.requestTable("GET", subject, "customers", "select=id", ""); status != 404 {
			t.Fatal("removed table remains in API", subject, status, body)
		}
	}
	if result := stopWrites(); result.err != nil || result.value < 1001 {
		t.Fatal("updates failed during removal", result)
	}
	refreshReady = true
	old := get()
	ob := Observation{State: "blocked", Verification: old.Verification}
	if err = engine.reconcileTableRemoval(ctx, old, source.Config().ConnString(), publisher, target, ownerForNativeTable(t, ctx, target), &ob, always); err != nil {
		t.Fatal("complete live removal", err)
	}
	// Lose the final catalog acknowledgement after the mirror result commits.
	restart()
	wait(func(v Pipeline) bool {
		return v.State == "live" && slices.Equal(v.Tables, []string{"orders"}) && v.SchemaChange.Cancellation.State == "complete"
	})
	expected := fmt.Sprintf("%d.00", written.value)
	wait(func(Pipeline) bool {
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		return status == 200 && strings.Contains(body, expected)
	})
	if _, err = source.Exec(ctx, `UPDATE public.customers SET name='no longer synchronized' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, "SELECT name=$1 FROM gregale_pipeline."+ident(retained)+" WHERE id=1", retainedName).Scan(&valid); err != nil || !valid {
		t.Fatal("retained data changed after removal", err)
	}
	if err = target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$1 AND pg_relation_filenode('public.orders')=$2
 AND (SELECT oid=$3 AND subenabled FROM pg_subscription WHERE subname=$4)
 AND (SELECT readable AND NOT retiring AND tables=ARRAY['orders'] FROM gregale_pipeline.mirrors WHERE pipeline_id=$5)`, orderOID, heap, subOID, pipelineName(get()), p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("healthy storage/shared identities changed", err)
	}
	if err = source.QueryRow(ctx, `SELECT (SELECT oid=$1 FROM pg_publication WHERE pubname=$2)
 AND (SELECT count(*)=1 AND bool_and(slot_name=$2) FROM pg_replication_slots WHERE database=current_database())`, pubOID, pipelineName(get())).Scan(&valid); err != nil || !valid {
		t.Fatal("publication/main slot changed or extra copy allocated", err)
	}
	if get().Generation != p.Generation {
		t.Fatal("removal reset generation")
	}
	if err = store.TableRemoval(ctx, account, p.ID, get().Revision, "orders", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("last live table could be removed", err)
	}
	if _, err = target.Exec(ctx, `DROP TRIGGER reject_live_removal_recopy ON public.orders`); err != nil {
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
