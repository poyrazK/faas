package datapipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeSourceRewriteRejectsObsoleteValues(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before'),(2,'other')`)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	before, _, err := inspectSourceTable(ctx, f.source, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.source.Exec(ctx, `ALTER TABLE public.orders ALTER COLUMN value TYPE text USING upper(value)`); err != nil {
		t.Fatal(err)
	}
	after, _, err := inspectSourceTable(ctx, f.source, "orders")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("fixture changed logical metadata", err)
	}
	f.tick(t)
	if nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("rewrite left obsolete values readable")
	}
	for range 10 {
		if f.pipeline.State == "resync_required" {
			break
		}
		f.tick(t)
	}
	if f.pipeline.State != "resync_required" || f.pipeline.Reason != "source_storage_changed" {
		t.Fatal("rewrite mismatch did not require resync", f.pipeline.State, f.pipeline.Reason)
	}
	var retained string
	if err = f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&retained); err != nil || retained != "before" {
		t.Fatal("rewrite replaced imported rows", retained, err)
	}
}

func TestNativeSourceStorageRequalifiesMaintenanceAndLegacyMirrors(t *testing.T) {
	for _, kind := range []string{"maintenance", "legacy", "lost proof acknowledgement"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
			ctx := t.Context()
			role := nativeReaderRole(t, f)
			var heap, subscription uint32
			if err := f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders'),(SELECT oid FROM pg_subscription WHERE subname=$1)`, pipelineName(f.pipeline)).Scan(&heap, &subscription); err != nil {
				t.Fatal(err)
			}
			if kind == "legacy" {
				if _, err := f.target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors DROP COLUMN source_storage`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.source.Exec(ctx, `VACUUM FULL public.orders`); err != nil {
				t.Fatal(err)
			}
			if kind == "lost proof acknowledgement" {
				committed := false
				for range 10 {
					ob := f.engine.Reconcile(ctx, f.pipeline, f.raw, func(context.Context) error { return nil })
					if ob.Verification == "passed" {
						committed = true
						break
					}
				}
				if !committed {
					t.Fatal("fixture did not lose a committed proof acknowledgement")
				}
				// Reconcile without storing observations, then replay the old intent.
			}
			awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.Verification == "passed" })
			var preserved bool
			if err := f.target.QueryRow(ctx, `SELECT pg_relation_filenode('public.orders')=$1 AND (SELECT oid FROM pg_subscription WHERE subname=$2)=$3 AND (SELECT schema_phase='' AND source_storage<>'{}'::jsonb FROM gregale_pipeline.mirrors WHERE pipeline_id=$4)`, heap, pipelineName(f.pipeline), subscription, f.pipeline.ID).Scan(&preserved); err != nil || !preserved || nativeReadAs(t, f, role, "alice") != 1 {
				t.Fatal("qualification replaced storage or retained the read gate", preserved, err)
			}
		})
	}
}

func TestNativeSourceStorageGateSurvivesLostFence(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'retained')`)
	ctx := t.Context()
	role := nativeReaderRole(t, f)
	if _, err := f.source.Exec(ctx, `VACUUM FULL public.orders`); err != nil {
		t.Fatal(err)
	}
	source, err := pgxpool.New(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	relations, _, err := inspectSource(ctx, f.pipeline, source)
	if err != nil {
		t.Fatal(err)
	}
	if gated, gateErr := gateSourceStorage(ctx, f.pipeline, source, f.target, relations, func(context.Context) error { return nil }); gateErr != nil || !gated {
		t.Fatal("rewrite was not gated", gated, gateErr)
	}
	ob := &Observation{}
	for range 10 {
		ready, verifyErr := verifyResumable(ctx, f.pipeline, source, f.target, relations, ob, func(context.Context) error { return nil })
		if verifyErr != nil {
			t.Fatal(verifyErr)
		}
		if ready {
			if sealed, sealErr := sealVerification(ctx, f.pipeline, source, f.target, relations, ob, func(context.Context) error { return nil }, nil); sealErr != nil || !sealed {
				t.Fatal("could not seal fixture", sealed, sealErr)
			}
			break
		}
	}
	if _, err = f.source.Exec(ctx, `SET lock_timeout='50ms'`); err != nil {
		t.Fatal(err)
	}
	called := false
	err = acknowledgeSourceStorage(ctx, f.pipeline, source, f.target, relations, func(context.Context) error {
		called = true
		_, ddlErr := f.source.Exec(ctx, `ALTER TABLE public.orders ALTER COLUMN value TYPE text USING upper(value)`)
		var pg *pgconn.PgError
		if !errors.As(ddlErr, &pg) || pg.Code != "55P03" {
			t.Fatal("rewrite was not fenced", ddlErr)
		}
		return ErrConflict
	})
	if !called || !errors.Is(err, ErrConflict) || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("lost intent reopened reads", called, err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.Verification == "passed" })
}

func TestNativeSourceStorageStoppedProofRequiresExplicitRetry(t *testing.T) {
	for _, cause := range []string{"storage", "continuity"} {
		for _, stop := range []string{"cancelled", "limited"} {
			t.Run(cause+"/"+stop, func(t *testing.T) {
				initial := `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,20000)i`
				if stop == "limited" {
					initial = largeVerificationKeySQL("orders", 32)
				}
				f := newNativeRecoveryFixture(t, initial)
				ctx := t.Context()
				role := nativeReaderRole(t, f)
				reason := "source_storage_verification"
				var err error
				if cause == "continuity" {
					reason = "replication_continuity_verification"
					_, err = f.target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors DROP COLUMN source_slot`)
				} else {
					_, err = f.source.Exec(ctx, `VACUUM FULL public.orders`)
				}
				if err != nil {
					t.Fatal(err)
				}
				f.tick(t)
				v := f.pipeline.VerificationProgress
				if v == nil || v.State != "running" && v.State != "limited" {
					t.Fatal("storage qualification did not start", v)
				}
				if stop == "cancelled" {
					if err := f.service.CancelVerification(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, v.ID); err != nil {
						t.Fatal(err)
					}
				}
				for range 2 {
					f.tick(t)
				}
				if f.pipeline.State != "blocked" || f.pipeline.Reason != reason || f.pipeline.VerificationProgress.State != stop || f.pipeline.VerifyRequested || nativeReadAs(t, f, role, "alice") != 0 {
					t.Fatal("stopped proof reopened reads or retried automatically", f.pipeline.DataPipeline)
				}
				if stop == "limited" {
					if _, err := f.source.Exec(ctx, `DELETE FROM public.orders`); err != nil {
						t.Fatal(err)
					}
				}
				f.control(t, "verify")
				awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.Verification == "passed" })
			})
		}
	}
}

func TestNativeSourceStorageGuardProtectsHealthyTablesDuringRemoval(t *testing.T) {
	f := nativeLiveRemovalFixture(t)
	role := nativeReaderRole(t, f)
	approveNativeLiveRemoval(t, f)
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Cancellation.Stage == "archived" })
	if _, err := f.source.Exec(t.Context(), `ALTER TABLE public.orders ALTER COLUMN value TYPE text USING upper(value)`); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.State != "resync_required" || f.pipeline.Reason != "source_storage_changed" || nativeReadAs(t, f, role, "alice") != 0 {
		t.Fatal("table removal concealed a rewrite of a healthy table", f.pipeline.DataPipeline)
	}
}
