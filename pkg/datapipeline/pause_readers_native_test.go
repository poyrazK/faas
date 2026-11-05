package datapipeline

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestNativePauseProceedsWhileReaderPolicyIsLocked(t *testing.T) {
	for _, credentialUnavailable := range []bool{false, true} {
		name := "source reachable"
		if credentialUnavailable {
			name = "source credential unavailable"
		}
		t.Run(name, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before')`)
			ctx := t.Context()
			generation, tables := f.pipeline.Generation, slices.Clone(f.pipeline.Tables)
			get := func() {
				t.Helper()
				var err error
				f.pipeline, err = f.store.Get(ctx, f.pipeline.AccountID, f.pipeline.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.Readers(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "preview", "", []string{"bob"}); err != nil {
				t.Fatal(err)
			}
			get()
			if err := f.service.Readers(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, "approve", f.pipeline.ReaderChange.ID, nil); err != nil {
				t.Fatal(err)
			}
			get()
			f.control(t, "pause")
			// Stale workers cannot perform even the independent pause effect.
			f.engine.Reconcile(ctx, f.pipeline, f.raw, func(context.Context) error { return ErrConflict })
			var enabled bool
			if err := f.target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&enabled); err != nil || !enabled {
				t.Fatal("stale worker changed the subscription", enabled, err)
			}
			reader, err := pgx.Connect(ctx, f.target.Config().ConnString())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close(ctx) }()
			tx, err := reader.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err = tx.Exec(ctx, `SELECT * FROM public.orders`); err != nil {
				t.Fatal(err)
			}
			identities := f.worker.Identities
			if credentialUnavailable {
				f.worker.Identities = nil
			}
			f.tick(t)
			if err = f.target.QueryRow(ctx, `SELECT subenabled FROM pg_subscription WHERE subname=$1`, pipelineName(f.pipeline)).Scan(&enabled); err != nil || enabled {
				t.Fatal("reader policy blocked native pause", enabled, err)
			}
			if f.pipeline.State != "paused" || !f.pipeline.ObservationFresh || f.pipeline.Health.Code != "paused" || f.pipeline.ReaderChange.State != "approved" || !slices.Equal(f.pipeline.ReadSubjects, []string{"alice"}) {
				t.Fatal("pause or unapplied reader state was misreported", f.pipeline)
			}
			for until := time.Now().Add(5 * time.Second); ; {
				var active bool
				if err = f.target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subname=$1 AND pid IS NOT NULL)`, pipelineName(f.pipeline)).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if !active {
					break
				}
				if time.Now().After(until) {
					t.Fatal("disabled native worker did not stop")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, err = f.source.Exec(ctx, `UPDATE public.orders SET value='after pause' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			var value string
			if err = f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil || value != "before" {
				t.Fatal("source write applied while paused", value, err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			// The saved reader update still applies while paused once its lock
			// clears, including without a decryptable source credential.
			f.tick(t)
			if f.pipeline.ReaderChange.State != "applied" || !slices.Equal(f.pipeline.ReadSubjects, []string{"bob"}) {
				t.Fatal("pending reader update was lost", f.pipeline.ReaderChange)
			}
			f.worker.Identities = identities
			f.control(t, "resume")
			f.await(t, "live")
			awaitNativeWorkflow(t, f, func(Pipeline) bool {
				return f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value) == nil && value == "after pause"
			})
			if f.pipeline.Generation != generation || !slices.Equal(f.pipeline.Tables, tables) {
				t.Fatal("reader contention replaced the pipeline generation")
			}
		})
	}
}
