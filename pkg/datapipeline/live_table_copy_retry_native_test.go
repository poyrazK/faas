package datapipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeLiveRepairRetriesStalledAndMissingCopies(t *testing.T) {
	f := nativeTwoTableFixture(t)
	ctx := t.Context()
	generation := f.pipeline.Generation
	role := nativeReaderRole(t, f)
	var healthyHeap, subscription uint32
	if err := f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.customers'),(SELECT oid FROM pg_subscription WHERE subname=$1)`, pipelineName(f.pipeline)).Scan(&healthyHeap, &subscription); err != nil {
		t.Fatal(err)
	}
	if _, err := f.target.Exec(ctx, `UPDATE public.orders SET value='corrupt' WHERE id=1;
 CREATE TABLE gregale_pipeline.copy_retry_audit(n int);
 CREATE FUNCTION gregale_pipeline.audit_copy_retry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO gregale_pipeline.copy_retry_audit VALUES(1);RETURN NULL;END $$;
 CREATE TRIGGER audit_copy_retry AFTER TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.audit_copy_retry()`); err != nil {
		t.Fatal(err)
	}
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "live_table_mismatch" })
	approve := func(copyRetry bool, attempt int) string {
		t.Helper()
		if !slices.Equal(RecoverableTables(f.pipeline), []string{"orders"}) {
			t.Fatal("wrong recovery scope", RecoverableTables(f.pipeline), f.pipeline.Reason)
		}
		if err := f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", "customers"); !errors.Is(err, ErrConflict) {
			t.Fatal("healthy table offered for replacement", err)
		}
		if err := f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", "orders"); err != nil {
			t.Fatal(err)
		}
		awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_recovery_pending" })
		r := f.pipeline.SchemaChange.Recovery
		if r.Kind != "live" || r.CopyRetry != copyRetry || r.Attempt != attempt || nativeReadAs(t, f, role, "alice") != 0 {
			t.Fatal("preview lost its kind, attempt or read gate", r)
		}
		before := *r
		before.CopyRetry = !copyRetry
		if beforeID := tableRecoveryID(f.pipeline, before); beforeID == r.ID {
			t.Fatal("copy-retry consent was not bound to approval")
		} else if err := f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, beforeID); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong recovery mode approved", err)
		}
		var clears int
		if err := f.target.QueryRow(t.Context(), `SELECT count(*) FROM gregale_pipeline.copy_retry_audit`).Scan(&clears); err != nil || clears != attempt-1 {
			t.Fatal("preview cleared imported rows", clears, err)
		}
		if err := f.store.TableRecovery(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, r.ID); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	first := approve(false, 1)
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "published" })
	if _, err := f.target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_retry_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected copy failure';END $$;
 CREATE TRIGGER reject_retry_copy BEFORE INSERT ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_retry_copy();ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_retry_copy`); err != nil {
		t.Fatal(err)
	}
	waitCopy := func() {
		t.Helper()
		awaitNativeWorkflow(t, f, func(p Pipeline) bool {
			return p.SchemaChange.Recovery.Stage == "copying" && len(p.SchemaChange.Copies) == 1 && p.SchemaChange.Copies[0].NativeState == "d"
		})
	}
	waitCopy()
	copies, err := loadTableCopies(ctx, f.pipeline, f.target)
	if err != nil {
		t.Fatal(err)
	}
	copies[0].ProgressAt = time.Now().Add(-api.DataPipelineCopyStallTimeout - time.Minute)
	if err = saveTableCopies(ctx, f.pipeline, f.target, copies); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.Reason != "table_copy_stalled" {
		t.Fatal("copy did not stall", f.pipeline.Reason)
	}
	second := approve(true, 2)
	if first == second {
		t.Fatal("new approval reused the previous attempt")
	}
	waitCopy()
	// Lose the catalog acknowledgement of native copy registration. Recovery
	// must derive its missing-tracking observation from the private receipt.
	if _, err = f.store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=jsonb_set(schema_change,'{Recovery,Stage}','"published"') WHERE id=$1`, f.pipeline.ID); err != nil {
		t.Fatal(err)
	}
	publisher, err := pgxpool.New(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	current := func(context.Context) error { return nil }
	// Lose just the repaired relation using native DDL, preserving the private
	// copying receipt, shared stream and the remaining table's ready state.
	if err = recoveryPublication(ctx, f.pipeline, publisher, false, current); err != nil {
		t.Fatal(err)
	}
	if _, err = f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" REFRESH PUBLICATION WITH (copy_data=false)"); err != nil {
		t.Fatal(err)
	}
	if err = recoveryPublication(ctx, f.pipeline, publisher, true, current); err != nil {
		t.Fatal(err)
	}
	if _, err = f.target.Exec(ctx, `DROP TRIGGER reject_retry_copy ON public.orders`); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.Reason != "table_addition_tracking_lost" || f.pipeline.SchemaChange.Copies[0].Phase != "missing" {
		t.Fatal("missing copy tracking was not immediately recoverable", f.pipeline.Reason, f.pipeline.SchemaChange.Copies)
	}
	if _, err = f.source.Exec(ctx, `UPDATE public.customers SET name='healthy CDC continues' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(Pipeline) bool {
		var name string
		return f.target.QueryRow(ctx, `SELECT name FROM public.customers WHERE id=1`).Scan(&name) == nil && name == "healthy CDC continues"
	})
	third := approve(true, 3)
	if third == second {
		t.Fatal("tracking-loss retry reused the old approval")
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "copying" })
	// Commit completion without its catalog acknowledgement, then replay the
	// receipt with the copy-retry mode included in its saved approval identity.
	for until := time.Now().Add(10 * time.Second); ; {
		ob := f.engine.Reconcile(ctx, f.pipeline, f.raw, current)
		if ob.State == "live" && ob.SchemaChange != nil && ob.SchemaChange.Recovery.State == "complete" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("fresh repaired copy did not finish", ob)
		}
		time.Sleep(25 * time.Millisecond)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.Recovery.State == "complete" })
	var intact bool
	if err = f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.customers')=$1
 AND (SELECT oid=$2 FROM pg_subscription WHERE subname=$3)
 AND (SELECT count(*)=3 FROM gregale_pipeline.copy_retry_audit)
 AND (SELECT value='before' FROM public.orders WHERE id=1)`, healthyHeap, subscription, pipelineName(f.pipeline)).Scan(&intact); err != nil || !intact || f.pipeline.Generation != generation || nativeReadAs(t, f, role, "alice") != 1 {
		t.Fatal("copy retry changed healthy storage, cleared twice, lost data or failed to reopen", intact, err)
	}
}
