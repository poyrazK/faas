package datapipeline

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestNativeFastDefaultVacuumPreservesSchemaAndRestartsProof(t *testing.T) {
	for _, workflow := range []string{"manual", "backfill"} {
		t.Run(workflow, func(t *testing.T) {
			initial := `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i;`
			if workflow == "manual" {
				initial += `ALTER TABLE public.orders ADD COLUMN status text NOT NULL DEFAULT 'pending'`
			}
			f := newNativeRecoveryFixture(t, initial)
			ctx := t.Context()
			f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) { return nil, true, nil }
			if workflow == "manual" {
				f.control(t, "verify")
			} else {
				if _, err := f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN status text NOT NULL DEFAULT 'pending'`); err != nil {
					t.Fatal(err)
				}
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_change_pending" })
				if err := f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool {
				return p.VerificationProgress != nil && p.VerificationProgress.State == "running" && p.VerificationProgress.Rows > 0
			})
			first, approval, generation := *f.pipeline.VerificationProgress, f.pipeline.SchemaApproval, f.pipeline.Generation
			var table, subscription uint32
			if err := f.target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid,(SELECT oid FROM pg_subscription WHERE subname=$1)`, pipelineName(f.pipeline)).Scan(&table, &subscription); err != nil {
				t.Fatal(err)
			}
			if _, err := f.source.Exec(ctx, `VACUUM FULL public.orders`); err != nil {
				t.Fatal(err)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.VerificationProgress.ID != first.ID && p.VerificationProgress.Rows > 0 })
			if f.pipeline.SchemaApproval != approval || f.pipeline.Generation != generation || f.pipeline.VerificationProgress.IntentID != first.IntentID || f.pipeline.VerificationProgress.Restarts < 1 {
				t.Fatal("materialization changed logical approval or reused physical proof", f.pipeline)
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.VerificationProgress.State == "passed" })
			var same bool
			if err := f.target.QueryRow(ctx, `SELECT 'public.orders'::regclass::oid=$1 AND EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$2 AND oid=$3)`, table, pipelineName(f.pipeline), subscription).Scan(&same); err != nil || !same {
				t.Fatal("vacuum replaced native identities", same, err)
			}
			if workflow == "manual" {
				assertNativePendingPauseRemainsObservable(t, f)
			}
		})
	}
}

func assertNativePendingPauseRemainsObservable(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	target := f.engine.Target
	defer func() { f.engine.Target = target }()
	f.engine.Target = func(context.Context, string, string, func(*pgx.Conn, string) error) error { return ErrUnavailable }
	f.control(t, "pause")
	if f.pipeline.Health.Code != "pause_pending" {
		t.Fatal("pause intent claimed completion", f.pipeline.Health)
	}
	f.tick(t)
	if f.pipeline.State != "blocked" || f.pipeline.Health.Code != "pause_failed" {
		t.Fatal("destination outage hid failed pause", f.pipeline.DataPipeline)
	}
	if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='still replicating' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(Pipeline) bool {
		var value string
		return f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value) == nil && value == "still replicating"
	})
	f.engine.Target = target
	f.tick(t)
	var enabled bool
	if err := f.target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&enabled); err != nil || enabled || f.pipeline.Health.Code != "paused" {
		t.Fatal("pause acknowledgement preceded native disable", enabled, f.pipeline.Health, err)
	}
}
