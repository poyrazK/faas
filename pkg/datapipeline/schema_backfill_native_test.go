package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeSchemaBackfill(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
	t.Helper()
	generation := p.Generation
	var tableOID, subOID uint32
	name := pipelineName(Pipeline{DataPipeline: p})
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid,(SELECT oid FROM pg_subscription WHERE subname=$1)`, name).Scan(&tableOID, &subOID); err != nil {
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
				t.Fatalf("backfill blocked: %+v progress=%+v", p, v.VerificationProgress)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'backfill fixture',7 FROM generate_series(500000,599999) i`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		var count int
		return target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count) == nil && count == 101001
	})
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN status text NOT NULL DEFAULT 'pending'; UPDATE public.orders SET status='custom before approval' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool { return v.Reason == "schema_change_pending" })
	pending := get()
	if pending.SchemaChange == nil || !pending.SchemaChange.Supported || !pending.SchemaChange.After[0].Columns[len(pending.SchemaChange.After[0].Columns)-1].Missing {
		t.Fatal("fast default addition not offered for approval")
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	originalRefresh := engine.RefreshSchema
	defer func() { engine.RefreshSchema = originalRefresh }()
	ready, lateWrite := false, false
	engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		if ready && !lateWrite {
			lateWrite = true
			if _, err := source.Exec(ctx, `UPDATE public.orders SET status='latest status' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
		}
		return nil, ready, nil
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.BackfilledRows >= 8000
	})
	partial := get()
	if partial.VerificationProgress.Rows >= 100000 {
		t.Fatal("backfill was not bounded")
	}
	// A lost catalog lease after UPDATE must roll back data and cursor together.
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := loadVerification(ctx, target, partial)
	if err != nil || checkpoint == nil {
		t.Fatal("private backfill checkpoint absent", err)
	}
	var before, after int64
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders WHERE status IS NOT NULL`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = compareVerificationRange(ctx, partial, publisher, target, partial.SchemaChange.After, checkpoint, 0, nil, func(context.Context) error { return ErrConflict })
	publisher.Close()
	if !errors.Is(err, ErrConflict) {
		t.Fatal("lost lease did not reject backfill", err)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders WHERE status IS NOT NULL`).Scan(&after); err != nil || before != after {
		t.Fatal("uncheckpointed backfill committed", before, after, err)
	}
	rolledBack, err := loadVerification(ctx, target, partial)
	if err != nil || rolledBack == nil || rolledBack.Progress.Rows != partial.VerificationProgress.Rows || rolledBack.Progress.BackfilledRows != partial.VerificationProgress.BackfilledRows {
		t.Fatal("lost lease advanced the durable backfill cursor", err)
	}
	if err = store.CancelVerification(ctx, account, p.ID, p.Revision, partial.VerificationProgress.ID); err != nil {
		t.Fatal(err)
	}
	p = tick()
	stopped := get()
	if stopped.Reason != "verification_cancelled" {
		t.Fatal("backfill stop was ignored", stopped.Reason)
	}
	if err = target.QueryRow(ctx, `SELECT count(*) FROM public.orders WHERE status IS NOT NULL`).Scan(&after); err != nil || before != after {
		t.Fatal("stop changed applied backfill batches", err)
	}
	if err = store.ApproveSchema(ctx, account, p.ID, p.Revision, stopped.SchemaChange.ID, true); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.Rows >= 16000 && v.VerificationProgress.State == "running"
	})
	partial = get()
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET verification_progress='{}' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	restart()
	if _, err = source.Exec(ctx, `UPDATE public.orders SET status='changed behind cursor' WHERE id=1;
 UPDATE public.orders SET id=-5,status='moved behind cursor' WHERE id=500000;
 DELETE FROM public.orders WHERE id=500001;
 INSERT INTO public.orders(id,customer,total,status) VALUES(700000,'new during backfill',9,'new during backfill')`); err != nil {
		t.Fatal(err)
	}
	p = tick()
	resumed := get()
	if resumed.VerificationProgress.ID != partial.VerificationProgress.ID || resumed.VerificationProgress.Rows <= partial.VerificationProgress.Rows || resumed.VerificationProgress.BackfilledRows <= partial.VerificationProgress.BackfilledRows {
		t.Fatal("backfill failed to resume private progress", resumed.VerificationProgress)
	}
	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	var required, noDefault, gated bool
	if err = target.QueryRow(ctx, `SELECT attnotnull,NOT atthasdef FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname='status'`).Scan(&required, &noDefault); err != nil || !required || !noDefault {
		t.Fatal("API refresh preceded required constraint or copied default", err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&gated); err != nil || !gated {
		t.Fatal("backfill reopened reads before API refresh", err)
	}
	fresh := startNativeDataAPI(t, ctx, targetURL, reader)
	defer fresh.close()
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,status", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("required field exposed before refresh completion: %d %s", status, body)
	}
	ready = true
	wait(func(v Pipeline) bool { return v.State == "live" })
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,status", ""); status != 200 || !strings.Contains(body, "latest status") {
		t.Fatalf("backfill lost concurrent update: %d %s", status, body)
	}
	if status, body := fresh.request("GET", "bob", "id=eq.1&select=id,status", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("backfill bypassed reader restrictions: %d %s", status, body)
	}
	if status, _ := fresh.request("PATCH", "alice", "id=eq.1", `{"status":"forbidden"}`); status != 403 {
		t.Fatal("backfill made mirror writable", status)
	}
	var same, values bool
	if err = target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$2 AND (SELECT oid=$3 FROM pg_subscription WHERE subname=$1),
 (SELECT status='moved behind cursor' FROM public.orders WHERE id=-5) AND (SELECT status='new during backfill' FROM public.orders WHERE id=700000)
 AND NOT EXISTS(SELECT 1 FROM public.orders WHERE status IS NULL OR id=500001)`, name, tableOID, subOID).Scan(&same, &values); err != nil || !same || !values || p.Generation != generation {
		t.Fatal("backfill changed replication identity or lost source values", same, values, err)
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id>=500000 OR id<0`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		var count int
		return target.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&count) == nil && count == 1001
	})
	return p
}
