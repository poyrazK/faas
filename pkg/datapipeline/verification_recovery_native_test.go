package datapipeline

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeSingleTableLiveRepairCompletes(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'original')`)
	ctx := t.Context()
	if _, err := f.target.Exec(ctx, `UPDATE public.orders SET value='corrupt' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.control(t, "verify")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return liveTableRepair(p.SchemaChange) })
	f.service.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	if err := f.service.TableRecovery(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, ""); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.SchemaChange.Recovery.State == "pending" })
	if err := f.service.TableRecovery(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	f.engine.RefreshSchema = func(context.Context, Pipeline, SchemaChange) ([]SchemaRefresh, bool, error) { return nil, true, nil }
	generation := f.pipeline.Generation
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	var value string
	if err := f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value); err != nil || value != "original" {
		t.Fatal("single-table repair did not restore source rows", value, err)
	}
	if f.pipeline.Generation != generation || len(f.pipeline.Tables) != 1 || f.pipeline.Tables[0] != "orders" {
		t.Fatal("table repair replaced pipeline intent")
	}
	if _, err := f.source.Exec(ctx, `UPDATE public.orders SET value='continued' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool {
		return p.State == "live" && f.target.QueryRow(ctx, `SELECT value FROM public.orders WHERE id=1`).Scan(&value) == nil && value == "continued"
	})
}

func TestNativeReaderChangesAndPauseRetainManualIntent(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders SELECT i,'original' FROM generate_series(1,50000)i`)
	ctx := t.Context()
	f.control(t, "verify")
	f.tick(t)
	first := *f.pipeline.VerificationProgress
	if first.State != "running" || first.Rows < 1 || first.ManualAttempt == 0 {
		t.Fatal("verification fixture is not partial", first)
	}
	change := func(action, approval string, subjects []string) {
		t.Helper()
		if err := f.service.Readers(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, action, approval, subjects); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func() {
		t.Helper()
		p := f.pipeline.VerificationProgress
		if p.ID != first.ID || p.IntentID != first.IntentID || p.ManualAttempt != first.ManualAttempt || !p.StartedAt.Equal(first.StartedAt) {
			t.Fatal("unrelated intent restarted manual verification", first, *p)
		}
	}
	change("preview", "", []string{"bob"})
	f.tick(t)
	unchanged()
	if f.pipeline.VerificationProgress.Rows <= first.Rows {
		t.Fatal("reader preview discarded saved ranges")
	}
	change("discard", "", nil)
	f.tick(t)
	unchanged()
	change("preview", "", []string{"bob"})
	f.tick(t)
	unchanged()
	change("approve", f.pipeline.ReaderChange.ID, nil)
	f.tick(t)
	unchanged()
	if f.pipeline.ReaderChange.State != "applied" {
		t.Fatal("reader change was not acknowledged")
	}
	rows := f.pipeline.VerificationProgress.Rows
	before, err := loadSourceSlot(ctx, f.pipeline, f.target)
	if err != nil || before == nil {
		t.Fatal("stream witness unavailable before pause", before, err)
	}
	f.control(t, "pause")
	f.tick(t)
	unchanged()
	if f.pipeline.State != "paused" || f.pipeline.VerificationProgress.Rows != rows {
		t.Fatal("pause did not retain saved progress")
	}
	f.control(t, "resume")
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.VerificationProgress.Rows > rows })
	resumed := f.pipeline.VerificationProgress
	if resumed.IntentID != first.IntentID || resumed.ManualAttempt != first.ManualAttempt {
		t.Fatal("resume lost the original manual verification intent", first, *resumed)
	}
	if resumed.ID != first.ID {
		// Unobserved progress from a new sender must invalidate physical ranges,
		// while retaining the manual request and its cancellation identity.
		after, err := loadSourceSlot(ctx, f.pipeline, f.target)
		if err != nil || after == nil || after.Epoch == "" || after.Epoch == before.Epoch {
			t.Fatal("manual proof restarted without a changed stream epoch", after, err)
		}
		first = *resumed
	}
	unchanged()
	for until, rows := time.Now().Add(time.Minute), int64(0); ; {
		f.tick(t)
		unchanged()
		if f.pipeline.Verification == "passed" {
			break
		}
		if progress := f.pipeline.VerificationProgress; progress.Rows > rows {
			rows = progress.Rows
			until = time.Now().Add(time.Minute)
		}
		if time.Now().After(until) || f.pipeline.State == "resync_required" {
			t.Fatal("manual verification stopped advancing", f.pipeline.DataPipeline, f.pipeline.VerificationProgress)
		}
		time.Sleep(25 * time.Millisecond)
	}
	unchanged()
	f.control(t, "verify")
	f.tick(t)
	second := *f.pipeline.VerificationProgress
	if second.ID == first.ID || second.ManualAttempt <= first.ManualAttempt {
		t.Fatal("explicit verification reused a completed proof")
	}
	if err := f.service.CancelVerification(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, second.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if f.pipeline.VerificationProgress.State != "cancelled" {
		t.Fatal("manual check was not cancelled")
	}
	f.control(t, "verify")
	f.tick(t)
	if f.pipeline.VerificationProgress.ID == second.ID {
		t.Fatal("retry reused the cancelled proof")
	}
}
