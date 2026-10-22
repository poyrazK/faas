package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func assertNativeInitialCopyRecovery(t *testing.T, ctx context.Context, store *PostgresStore, publisher *pgxpool.Pool, source, target *pgx.Conn, oldAPI *nativeDataAPI, get func() Pipeline, wait func(func(Pipeline) bool), restart func()) {
	t.Helper()
	p := get()
	if _, err := source.Exec(ctx, `INSERT INTO public.orders(id,customer,total) SELECT i,'healthy copy fixture',7 FROM generate_series(900000,998998) i`); err != nil {
		t.Fatal(err)
	}
	var valid bool
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=100000 FROM public.orders`).Scan(&valid) == nil && valid
	})
	var heap uint32
	if err := target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')`).Scan(&heap); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(ctx, `CREATE FUNCTION gregale_pipeline.reject_initial_healthy_recopy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'healthy table was recopied'; END $$;
 CREATE TRIGGER reject_initial_healthy_recopy BEFORE INSERT OR DELETE OR TRUNCATE ON public.orders FOR EACH STATEMENT EXECUTE FUNCTION gregale_pipeline.reject_initial_healthy_recopy();
	 ALTER TABLE public.orders ENABLE ALWAYS TRIGGER reject_initial_healthy_recopy`); err != nil {
		t.Fatal(err)
	}
	active := func() int32 {
		t.Helper()
		var pid int32
		wait(func(v Pipeline) bool {
			for _, c := range v.SchemaChange.Copies {
				if c.Table == "customers" && c.NativeState == "d" && c.CountersKnown && c.WorkerActive {
					return target.QueryRow(ctx, `SELECT pid FROM pg_stat_subscription WHERE subname=$1 AND relid='public.customers'::regclass`, pipelineName(v)).Scan(&pid) == nil && pid != 0
				}
			}
			return false
		})
		return pid
	}
	age := func() {
		t.Helper()
		p = get()
		copies, err := loadTableCopies(ctx, p, target)
		if err != nil {
			t.Fatal(err)
		}
		for i := range copies {
			copies[i].ProgressAt = time.Now().Add(-api.DataPipelineCopyStallTimeout - time.Minute)
		}
		if err = saveTableCopies(ctx, p, target, copies); err != nil {
			t.Fatal(err)
		}
		wait(func(v Pipeline) bool { return v.Reason == "table_copy_stalled" && CanRecoverTable(v) })
	}
	approve := func() {
		t.Helper()
		p = get()
		if err := store.TableRecovery(ctx, p.AccountID, p.ID, p.Revision, "", "orders"); !errors.Is(err, ErrConflict) {
			t.Fatal("baseline table offered copy recovery", err)
		}
		if err := store.TableRecovery(ctx, p.AccountID, p.ID, p.Revision, "", "customers"); err != nil {
			t.Fatal(err)
		}
		wait(func(v Pipeline) bool { return v.Reason == "table_recovery_pending" })
		pending := get()
		if pending.SchemaChange.Recovery.Kind != "copy" {
			t.Fatal("copy preview depended on a verification mismatch")
		}
		if err := store.TableRecovery(ctx, p.AccountID, p.ID, pending.Revision, pending.SchemaChange.Recovery.ID); err != nil {
			t.Fatal(err)
		}
	}
	oldPID := active()
	var subOID, tableOID uint32
	if err := target.QueryRow(ctx, `SELECT s.oid,'public.customers'::regclass::oid FROM pg_subscription s WHERE s.subname=$1`, pipelineName(p)).Scan(&subOID, &tableOID); err != nil {
		t.Fatal(err)
	}
	var slots []string
	prefix := fmt.Sprintf("pg_%d_sync_%d_", subOID, tableOID)
	if err := source.QueryRow(ctx, `SELECT coalesce(array_agg(slot_name::text),'{}') FROM pg_replication_slots WHERE database=current_database() AND left(slot_name,length($1))=$1`, prefix).Scan(&slots); err != nil || len(slots) != 1 {
		t.Fatal("active sync slot was not created", slots, err)
	}
	age()
	// Losing catalog progress and restarting must preserve the private stall clock.
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=jsonb_set(schema_change,'{Copies}','[]') WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	restart()
	wait(func(v Pipeline) bool { return CanRecoverTable(v) && len(v.SchemaChange.Copies) == 1 })
	approve()
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "retiring" })
	approved := get()
	always := func(context.Context) error { return nil }
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
		t.Fatal("active-copy retirement did not lose its receipt", err)
	}
	if stage, err := loadTableRecovery(ctx, approved, target); err != nil || stage != "retiring" {
		t.Fatal("retirement stage advanced without receipt", stage, err)
	}
	if err = target.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1) AND NOT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$2 AND srrelid=$3)`, oldPID, subOID, tableOID).Scan(&valid); err != nil || !valid {
		t.Fatal("COPY worker or relation survived retirement", err)
	}
	if err = source.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1)`, slots[0]).Scan(&valid); err != nil || !valid {
		t.Fatal("temporary sync slot survived native retirement", err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "copying" })
	newPID := active()
	if newPID == oldPID {
		t.Fatal("old copy worker was reused")
	}
	if _, err = source.Exec(ctx, `UPDATE public.orders SET total=66.66 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		status, body := oldAPI.request("GET", "alice", "id=eq.1&select=id,total", "")
		return status == 200 && strings.Contains(body, "66.66")
	})
	// Lose tracking through supported native DDL, while the private copy receipt
	// still says copying. No direct system-catalog edits are used by this fixture.
	approved = get()
	if err = recoveryPublication(ctx, approved, publisher, false, always); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(approved))+" REFRESH PUBLICATION WITH (copy_data=false)"); err != nil {
		t.Fatal(err)
	}
	if err = recoveryPublication(ctx, approved, publisher, true, always); err != nil {
		t.Fatal(err)
	}
	restart()
	wait(func(v Pipeline) bool { return v.Reason == "table_addition_tracking_lost" && CanRecoverTable(v) })
	approve()
	wait(func(v Pipeline) bool { return v.SchemaChange.Recovery.Stage == "copying" })
	_ = active()
	restart()
	if err = target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT count(*)=100000 FROM public.orders)
 AND (SELECT count(*)=2 FROM gregale_pipeline.initial_recopy_audit) AND (SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$2)`, heap, p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("healthy storage changed or recovery cleared more than once per approval", err)
	}
	// Prepare a third exact approval, then let native copying finish before its
	// durable intent starts. The caller releases the COPY gate on return.
	if _, err = target.Exec(ctx, `DROP TRIGGER reject_initial_healthy_recopy ON public.orders; DROP FUNCTION gregale_pipeline.reject_initial_healthy_recopy()`); err != nil {
		t.Fatal(err)
	}
	if _, err = source.Exec(ctx, `DELETE FROM public.orders WHERE id>=900000`); err != nil {
		t.Fatal(err)
	}
	wait(func(Pipeline) bool {
		return target.QueryRow(ctx, `SELECT count(*)=1001 FROM public.orders`).Scan(&valid) == nil && valid
	})
	age()
	approve()
}
