package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeStalledRepair(t *testing.T) *nativeRecoveryFixture {
	t.Helper()
	f := nativeTwoTableFixture(t)
	ctx := t.Context()
	if _, err := f.target.Exec(ctx, `UPDATE public.orders SET value='corrupt' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "live_table_mismatch" })
	if err := f.store.TableRecovery(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", "orders"); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_recovery_pending" })
	if err := f.store.TableRecovery(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Recovery.Stage == "published" })
	if _, err := f.target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_cleanup_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected copy failure';END $$;
 CREATE TRIGGER reject_cleanup_copy BEFORE INSERT ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_cleanup_copy();ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_cleanup_copy`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool {
		return p.SchemaChange.Recovery.Stage == "copying" && len(p.SchemaChange.Copies) == 1 && p.SchemaChange.Copies[0].NativeState == "d"
	})
	return f
}

func nativePauseAndLoseMainSlot(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	f.control(t, "pause")
	f.await(t, "paused")
	for until := time.Now().Add(5 * time.Second); ; {
		var active bool
		if err := f.source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE database=current_database() AND active)`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if !active {
			break
		}
		if time.Now().After(until) {
			t.Fatal("paused native workers did not stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := f.source.Exec(ctx, `SELECT pg_drop_replication_slot($1)`, pipelineName(f.pipeline)); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMissingMainSlotCleanupWithoutCapacity(t *testing.T) {
	for _, copying := range []bool{false, true} {
		t.Run(fmt.Sprintf("copying=%t", copying), func(t *testing.T) {
			var f *nativeRecoveryFixture
			if copying {
				f = nativeStalledRepair(t)
			} else {
				f = newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
			}
			ctx := t.Context()
			nativePauseAndLoseMainSlot(t, f)
			var limit, used int
			if err := f.source.QueryRow(ctx, `SELECT current_setting('max_replication_slots')::int,(SELECT count(*) FROM pg_replication_slots)`).Scan(&limit, &used); err != nil {
				t.Fatal(err)
			}
			if copying && used != 1 {
				t.Fatal("fixture has no native table-sync slot", used)
			}
			for i := range limit - used {
				if _, err := f.source.Exec(ctx, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, fmt.Sprintf("cleanup_other_%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			f.control(t, "delete")
			f.await(t, "deleted")
			f.assertRetired(t, f.pipeline)
			var remaining int
			if err := f.source.QueryRow(ctx, `SELECT count(*) FROM pg_replication_slots WHERE database=current_database()`).Scan(&remaining); err != nil || remaining != limit-used {
				t.Fatal("cleanup left native slots or removed unrelated slots", remaining, err)
			}
			var exists bool
			if err := f.target.QueryRow(ctx, `SELECT to_regclass('public.orders') IS NOT NULL`).Scan(&exists); err != nil || !exists {
				t.Fatal("deletion removed imported data", exists, err)
			}
		})
	}
}

func TestNativeMissingMainSlotCleanupRollsBackUnconfirmedDrop(t *testing.T) {
	f := nativeStalledRepair(t)
	ctx := t.Context()
	nativePauseAndLoseMainSlot(t, f)
	source, err := pgxpool.New(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var original string
	if err = f.target.QueryRow(ctx, `SELECT subconninfo FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&original); err != nil {
		t.Fatal(err)
	}
	// Native DROP with no main slot tolerates connection failures. Its sync slot
	// must prevent commit, and rollback must restore the original slot identity.
	if _, err = f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" CONNECTION "+literal("host=127.0.0.1 port=1 dbname=unreachable connect_timeout=1")); err != nil {
		t.Fatal(err)
	}
	err = dropSubscriptionWithoutMainSlot(ctx, f.pipeline, source, f.target, true, func(context.Context) error { return nil })
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("unconfirmed source cleanup committed", err)
	}
	var slot string
	if err = f.target.QueryRow(ctx, `SELECT subslotname FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&slot); err != nil || slot != pipelineName(f.pipeline) {
		t.Fatal("rollback lost subscription ownership", slot, err)
	}
	if _, err = f.target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(f.pipeline))+" CONNECTION "+literal(original)); err != nil {
		t.Fatal(err)
	}
	// Remote sync-slot removal cannot roll back. Replay must tolerate its loss
	// after the current-intent fence rejects the destination commit.
	err = dropSubscriptionWithoutMainSlot(ctx, f.pipeline, source, f.target, true, func(context.Context) error { return ErrConflict })
	if !errors.Is(err, ErrConflict) {
		t.Fatal("lost intent committed cleanup", err)
	}
	if err = f.target.QueryRow(ctx, `SELECT subslotname FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&slot); err != nil || slot != pipelineName(f.pipeline) {
		t.Fatal("failed fence lost ownership", slot, err)
	}
	f.control(t, "delete")
	f.await(t, "deleted")
	f.assertRetired(t, f.pipeline)
}
