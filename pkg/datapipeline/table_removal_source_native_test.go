package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeTwoTableFixture(t *testing.T) *nativeRecoveryFixture {
	t.Helper()
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before');CREATE TABLE public.customers(id int PRIMARY KEY,name text);INSERT INTO public.customers VALUES(1,'customer')`)
	ctx := t.Context()
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) { return nil, true, nil }
	if _, err := f.store.pool.Exec(ctx, `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '1 day' WHERE connection_id=$1`, f.connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RequestCheck(ctx, f.pipeline.AccountID, f.connection.ID, f.connection.Revision, ""); err != nil {
		t.Fatal(err)
	}
	check, err := f.store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.FinishCheck(ctx, check, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}, {Name: "customers", Supported: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.RequestTables(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, []string{"customers"}); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_addition_pending" })
	if err = f.store.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	return f
}

func nativeLiveRemovalFixture(t *testing.T) *nativeRecoveryFixture {
	t.Helper()
	f := nativeTwoTableFixture(t)
	if err := f.store.TableRemoval(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "customers", ""); err != nil {
		t.Fatal(err)
	}
	return f
}

func approveNativeLiveRemoval(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_removal_pending" })
	if err := f.store.TableRemoval(t.Context(), f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "", f.pipeline.SchemaChange.Cancellation.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.pipeline, err = f.store.Get(t.Context(), f.pipeline.AccountID, f.pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func changeNativeRemovalSource(ctx context.Context, t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	if _, err := f.source.Exec(ctx, `ALTER TABLE public.orders DROP COLUMN value;ALTER TABLE public.orders ADD COLUMN value text NOT NULL DEFAULT 'source changed'`); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLiveRemovalRejectsSourceDriftThroughoutRetirement(t *testing.T) {
	for _, stage := range []string{"requested", "pending", "approved", "retiring", "detached", "archived", "refreshing", "lost completion acknowledgement"} {
		t.Run(stage, func(t *testing.T) {
			f := nativeLiveRemovalFixture(t)
			ctx := t.Context()
			generation := f.pipeline.Generation
			role := nativeReaderRole(t, f)
			if nativeReadAs(t, f, role, "alice") != 1 {
				t.Fatal("live source fixture is unreadable")
			}
			if stage == "pending" {
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "table_removal_pending" })
			} else if stage != "requested" {
				approveNativeLiveRemoval(t, f)
				if stage != "approved" {
					want := stage
					if stage == "refreshing" || stage == "lost completion acknowledgement" {
						want = "archived"
					}
					awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Cancellation.Stage == want })
				}
			}
			if stage == "lost completion acknowledgement" {
				// Commit the private completion without saving its catalog observation.
				ob := f.engine.Reconcile(ctx, f.pipeline, f.raw, func(context.Context) error { return nil })
				if ob.State != "live" || ob.SchemaChange.Cancellation.State != "complete" {
					t.Fatal("completion fixture did not commit", ob)
				}
			}
			if stage == "refreshing" {
				// Mutate after this tick's metadata check, before completion commits.
				f.engine.RefreshSchema = func(refreshCtx context.Context, _ Pipeline, _ SchemaChange) ([]SchemaRefresh, bool, error) {
					changeNativeRemovalSource(refreshCtx, t, f)
					return nil, true, nil
				}
			} else {
				changeNativeRemovalSource(ctx, t, f)
			}
			f.tick(t)
			if f.pipeline.State != "resync_required" || f.pipeline.Reason != "source_schema_changed" || !f.pipeline.ObservationFresh {
				t.Fatal("removal concealed source drift", f.pipeline.State, f.pipeline.Reason)
			}
			if f.pipeline.Generation != generation || nativeReadAs(t, f, role, "alice") != 0 {
				t.Fatal("source drift replaced the generation or left obsolete rows readable")
			}
			var value string
			if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil || value != "before" {
				t.Fatal("source drift replaced imported data", value, err)
			}
			if stage == "refreshing" {
				var saved string
				if err := f.target.QueryRow(ctx, `SELECT stage FROM gregale_pipeline.table_cancellations WHERE pipeline_id=$1`, f.pipeline.ID).Scan(&saved); err != nil || saved != "archived" {
					t.Fatal("source drift committed removal completion", saved, err)
				}
			}
		})
	}
}

func TestNativeLiveRemovalAllowsRetainedSourceToDisappear(t *testing.T) {
	f := nativeLiveRemovalFixture(t)
	ctx := t.Context()
	approveNativeLiveRemoval(t, f)
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Cancellation.Stage == "archived" })
	retained := retainedTableName(*f.pipeline.SchemaChange.Cancellation)
	if _, err := f.source.Exec(ctx, `DROP TABLE public.customers;UPDATE public.orders SET value='still syncing' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.Cancellation.State == "complete" })
	awaitNativeWorkflow(t, f, func(Pipeline) bool {
		var value string
		return f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value) == nil && value == "still syncing"
	})
	var name string
	if err := f.target.QueryRow(ctx, "SELECT name FROM gregale_pipeline."+ident(retained)+" WHERE id=1").Scan(&name); err != nil || name != "customer" {
		t.Fatal("retained data changed", name, err)
	}
}

func TestNativeLiveRemovalHoldsSourceLockThroughCompletionFence(t *testing.T) {
	f := nativeLiveRemovalFixture(t)
	ctx := t.Context()
	approveNativeLiveRemoval(t, f)
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Cancellation.Stage == "archived" })
	source, err := pgxpool.New(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = f.source.Exec(ctx, `SET lock_timeout='50ms'`); err != nil {
		t.Fatal(err)
	}
	result := cancellationResult(f.pipeline)
	called := false
	err = completeLiveRemoval(ctx, f.pipeline, source, f.target, &result, "live", func(context.Context) error {
		called = true
		_, ddlErr := f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN blocked_column text`)
		var pg *pgconn.PgError
		if !errors.As(ddlErr, &pg) || pg.Code != "55P03" {
			t.Fatal("source DDL was not fenced", ddlErr)
		}
		return ErrConflict
	})
	if !called || !errors.Is(err, ErrConflict) {
		t.Fatal("lost intent committed removal", called, err)
	}
	stage, _, err := loadTableCancellation(ctx, f.pipeline, f.target)
	if err != nil || stage != "archived" {
		t.Fatal("failed fence committed completion", stage, err)
	}
	if _, err = f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN released_column text`); err != nil {
		t.Fatal("source lock leaked after failed completion", err)
	}
}
