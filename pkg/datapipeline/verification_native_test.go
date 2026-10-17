package datapipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeLargeVerification(t *testing.T, ctx context.Context, store *PostgresStore, engine *Engine, source, target *pgx.Conn, targetURL, reader, account string, tick func() api.DataPipeline, restart func(), p api.DataPipeline) api.DataPipeline {
	t.Helper()
	generation := p.Generation
	var tableOID uint32
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid`).Scan(&tableOID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'large fixture',12.34 FROM generate_series(100000,199999) i; ALTER TABLE public.orders ADD COLUMN large_note text`); err != nil {
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
	p = tick()
	pending := get()
	if pending.SchemaChange == nil || p.Reason != "schema_change_pending" {
		t.Fatalf("large preview missing: %+v", p)
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, pending.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	original := engine.RefreshSchema
	defer func() { engine.RefreshSchema = original }()
	ready, refreshCalled := false, false
	engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) {
		refreshCalled = true
		return nil, ready, nil
	}
	wait := func(condition func(Pipeline) bool) {
		t.Helper()
		for until := time.Now().Add(60 * time.Second); ; {
			p = tick()
			v := get()
			if condition(v) {
				return
			}
			if p.State == "resync_required" || time.Now().After(until) {
				t.Fatalf("large verification blocked: pipeline=%+v progress=%+v", p, v.VerificationProgress)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(func(v Pipeline) bool { return v.VerificationProgress != nil && v.VerificationProgress.Rows >= 8000 })
	partial := get()
	firstRun := partial.VerificationProgress.ID
	if partial.VerificationProgress.Rows >= 100000 {
		t.Fatal("fixture did not exercise bounded progress")
	}
	// Cancellation still wins if source storage changes before reconciliation.
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ALTER COLUMN large_note TYPE text USING CASE WHEN large_note IS NULL THEN NULL ELSE large_note||'' END`); err != nil {
		t.Fatal(err)
	}
	// Cancel is revision/run bound, and must leave schema reads closed.
	if err := store.CancelVerification(ctx, account, p.ID, p.Revision, firstRun); err != nil {
		t.Fatal(err)
	}
	p = tick()
	cancelled := get()
	if cancelled.Reason != "verification_cancelled" || cancelled.VerificationProgress.State != "cancelled" {
		t.Fatalf("cancel not observed: %+v", cancelled)
	}
	var gated bool
	if err := target.QueryRow(ctx, `SELECT NOT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&gated); err != nil || !gated {
		t.Fatal("cancel reopened reads", err)
	}
	var triggers int
	if err := target.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='public.orders'::regclass AND tgname IN ('gregale_verify_rows','gregale_verify_truncate')`).Scan(&triggers); err != nil || triggers != 0 {
		t.Fatal("cancel retained verification triggers", triggers, err)
	}
	if err := store.ApproveSchema(ctx, account, p.ID, p.Revision, cancelled.SchemaChange.ID, true); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.ID != firstRun && v.VerificationProgress.Rows >= 8000
	})
	partial = get()
	run := partial.VerificationProgress.ID
	// A data-preserving physical rewrite must invalidate the old proof without
	// asking again for approval of an unchanged logical schema.
	approvedSchema := partial.SchemaChange.ID
	if _, err := source.Exec(ctx, `ALTER TABLE public.orders ALTER COLUMN large_note TYPE text USING CASE WHEN large_note IS NULL THEN NULL ELSE large_note||'' END`); err != nil {
		t.Fatal(err)
	}
	oldPhysical := run
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.ID != oldPhysical && v.VerificationProgress.Rows >= 8000
	})
	partial = get()
	if partial.SchemaChange.ID != approvedSchema || partial.VerificationProgress.Restarts < 1 {
		t.Fatal("physical rewrite changed logical schema approval")
	}
	run, rows := partial.VerificationProgress.ID, partial.VerificationProgress.Rows
	// Lose catalog progress and rebuild the worker. The private committed cursor
	// is authoritative; verified ranges must not be rescanned from the beginning.
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET verification_progress='{}' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	restart()
	if _, err := source.Exec(ctx, `UPDATE public.orders SET large_note='changed after comparison' WHERE id=1; INSERT INTO public.orders(id,customer,total,large_note) VALUES(-1,'behind cursor',1,'inserted'); UPDATE public.orders SET id=-2 WHERE id=100000; DELETE FROM public.orders WHERE id=100001; INSERT INTO public.orders(id,customer,total) VALUES(400000,'ahead of cursor',2)`); err != nil {
		t.Fatal(err)
	}
	// Native apply is asynchronous. Observe the mutations and their dirty
	// journal before letting the worker consume that journal; sampling after a
	// tick can miss either the apply or an already rechecked dirty range.
	for until := time.Now().Add(15 * time.Second); ; {
		var applied bool
		err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.orders WHERE id=1 AND large_note='changed after comparison')
 AND EXISTS(SELECT 1 FROM public.orders WHERE id=-1) AND EXISTS(SELECT 1 FROM public.orders WHERE id=-2)
 AND NOT EXISTS(SELECT 1 FROM public.orders WHERE id=100001) AND EXISTS(SELECT 1 FROM public.orders WHERE id=400000)`).Scan(&applied)
		if err != nil {
			t.Fatal(err)
		}
		if applied {
			break
		}
		if time.Now().After(until) {
			t.Fatal("native mutations did not reach the mirror")
		}
		time.Sleep(20 * time.Millisecond)
	}
	dirty, err := countDirtyVerification(ctx, partial, target, partial.SchemaChange.After)
	if err != nil || dirty < 1 {
		t.Fatal("native mutations did not invalidate previously checked ranges", dirty, err)
	}
	p = tick()
	resumed := get()
	if resumed.VerificationProgress == nil || resumed.VerificationProgress.ID != run || resumed.VerificationProgress.Rows <= rows {
		t.Fatalf("private progress was not resumed: %+v", resumed.VerificationProgress)
	}

	wait(func(v Pipeline) bool { return v.Reason == "schema_api_refresh_pending" })
	if !refreshCalled {
		t.Fatal("API refresh never requested")
	}
	fresh := startNativeDataAPI(t, ctx, targetURL, reader)
	defer fresh.close()
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,large_note", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("large mirror opened before refresh: %d %s", status, body)
	}
	// Keep a previously verified range changing through finalization. The final
	// short all-table fence must recheck it and must never accept stale digests.
	writerCtx, stopWriter := context.WithCancel(ctx)
	writerDone := make(chan error, 1)
	writerReady := make(chan struct{})
	go func() {
		conn, err := pgx.Connect(writerCtx, source.Config().ConnString())
		if err != nil {
			writerDone <- err
			return
		}
		defer func() {
			closing, cancel := context.WithTimeout(context.WithoutCancel(writerCtx), 2*time.Second)
			defer cancel()
			_ = conn.Close(closing)
		}()
		first := true
		for {
			if _, err = conn.Exec(writerCtx, `UPDATE public.orders SET large_note='late value' WHERE id=1`); err != nil {
				if writerCtx.Err() != nil {
					err = nil
				}
				writerDone <- err
				return
			}
			if first {
				close(writerReady)
				first = false
			}
			select {
			case <-writerCtx.Done():
				writerDone <- nil
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	defer stopWriter()
	select {
	case <-writerReady:
	case err := <-writerDone:
		t.Fatal("concurrent writer failed", err)
	}
	ready = true
	wait(func(v Pipeline) bool { return v.State == "live" })
	stopWriter()
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	completed := get()
	if completed.VerificationProgress.State != "passed" || completed.VerificationProgress.Rows < 100000 || completed.Generation != generation {
		t.Fatalf("large verification not proven: %+v", completed.VerificationProgress)
	}
	var preserved bool
	if err := target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$1`, tableOID).Scan(&preserved); err != nil || !preserved {
		t.Fatal("large verification recopied mirror", err)
	}
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,large_note", ""); status != 200 || !strings.Contains(body, "late value") {
		t.Fatalf("large API did not converge: %d %s", status, body)
	}
	if status, body := fresh.request("GET", "bob", "id=eq.1&select=id,large_note", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("large verification lost RLS")
	}
	// Corruption of an already checked range must invalidate the final proof.
	manual, err := store.Control(ctx, account, p.ID, p.Revision, "verify")
	if err != nil {
		t.Fatal(err)
	}
	p = manual.DataPipeline
	wait(func(v Pipeline) bool {
		return v.VerificationProgress != nil && v.VerificationProgress.ID != run && v.VerificationProgress.Rows >= 8000
	})
	if _, err = target.Exec(ctx, `UPDATE public.orders SET large_note='corrupt destination' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(60 * time.Second); ; {
		p = tick()
		if p.State == "blocked" && p.Reason == "live_table_mismatch" {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("dirty-range corruption accepted: %+v", p)
		}
	}
	if status, body := fresh.request("GET", "alice", "id=eq.1&select=id,large_note", ""); status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("mismatch reopened reads")
	}
	// Restore the original fixture cardinality before explicit recovery.
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id>=100000 OR id<0`); err != nil {
		t.Fatal(err)
	}
	reset, err := store.Control(ctx, account, p.ID, p.Revision, "resync")
	if err != nil {
		t.Fatal(err)
	}
	p = reset.DataPipeline
	wait(func(v Pipeline) bool { return v.State == "live" })
	// A value-changing rewrite emits no ordinary CDC row events. Reusing old
	// clean ranges here would be a false proof, even though every type is unchanged.
	if _, err = source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'rewrite fixture',5 FROM generate_series(300000,319999) i`); err != nil {
		t.Fatal(err)
	}
	manual, err = store.Control(ctx, account, p.ID, p.Revision, "verify")
	if err != nil {
		t.Fatal(err)
	}
	p = manual.DataPipeline
	wait(func(v Pipeline) bool { return v.VerificationProgress != nil && v.VerificationProgress.Rows >= 8000 })
	beforeRewrite := get().VerificationProgress.ID
	if _, err = source.Exec(ctx, `ALTER TABLE public.orders ALTER COLUMN large_note TYPE text USING coalesce(large_note,'changed without CDC')`); err != nil {
		t.Fatal(err)
	}
	wait(func(v Pipeline) bool { return v.State == "blocked" && v.Reason == "live_table_mismatch" })
	if get().VerificationProgress.ID == beforeRewrite {
		t.Fatal("source rewrite reused old clean ranges")
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id>=100000 OR id<0`); err != nil {
		t.Fatal(err)
	}
	reset, err = store.Control(ctx, account, p.ID, p.Revision, "resync")
	if err != nil {
		t.Fatal(err)
	}
	p = reset.DataPipeline
	wait(func(v Pipeline) bool { return v.State == "live" })
	return p
}
