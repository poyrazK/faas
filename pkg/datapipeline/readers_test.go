package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestReaderPreviewAcceptsMaximumEscapedSubjectSets(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	makeSubjects := func(character string) []string {
		result := make([]string, api.DataPipelineSubjectsMax)
		for i := range result {
			suffix := strconv.Itoa(i)
			result[i] = strings.Repeat(character, api.DataPipelineSubjectMaxBytes-len(suffix)) + suffix
		}
		return result
	}
	before, after := makeSubjects("\x01"), makeSubjects("\x02")
	slices.Sort(before)
	encoded, _ := json.Marshal(before)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET read_subjects=$2 WHERE id=$1`, p.ID, encoded); err != nil {
		t.Fatal(err)
	}
	if err := service.Readers(t.Context(), p.AccountID, p.ID, p.Revision, "preview", "", after); err != nil {
		t.Fatal("valid subject limits exceeded the preview metadata bound", err)
	}
}

func TestReadersRequireExactPreviewAndPreserveWorkflow(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	get := func() Pipeline {
		t.Helper()
		v, err := store.Get(ctx, p.AccountID, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, subjects := range [][]string{nil, {"bob", "bob"}, {""}, {"bob\nadmin"}, {strings.Repeat("b", api.DataPipelineSubjectMaxBytes+1)}} {
		if err := service.Readers(ctx, p.AccountID, p.ID, p.Revision, "preview", "", subjects); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid subjects accepted", err)
		}
	}
	if err := service.Readers(ctx, uuid.NewString(), p.ID, p.Revision, "preview", "", []string{"bob"}); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign request accepted", err)
	}
	if err := service.Readers(ctx, p.AccountID, p.ID, p.Revision, "preview", "", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	preview := get()
	retired := get()
	retired.State = "resync_required"
	if CanChangeReaders(retired) || canApproveReaders(retired) {
		t.Fatal("retired generation accepted reader management")
	}

	if !slices.Equal(preview.ReadSubjects, p.ReadSubjects) || preview.ReaderChange.State != "preview" || preview.SchemaChange.ID != plan.ID {
		t.Fatal("preview changed access or schema")
	}
	if err := service.Readers(ctx, p.AccountID, p.ID, preview.Revision, "approve", strings.Repeat("a", 64), nil); !errors.Is(err, ErrConflict) {
		t.Fatal("different preview approved", err)
	}
	if _, err := store.Control(ctx, p.AccountID, p.ID, preview.Revision, "pause"); err != nil {
		t.Fatal(err)
	}
	changed := get()
	if err := service.Readers(ctx, p.AccountID, p.ID, changed.Revision, "approve", preview.ReaderChange.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("intervening revision accepted", err)
	}
	if err := service.Readers(ctx, p.AccountID, p.ID, changed.Revision, "discard", "", nil); err != nil {
		t.Fatal(err)
	}
	changed = get()
	if err := service.Readers(ctx, p.AccountID, p.ID, changed.Revision, "preview", "", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	preview = get()
	if err := service.Readers(ctx, p.AccountID, p.ID, preview.Revision, "approve", preview.ReaderChange.ID, []string{"mallory"}); err != nil {
		t.Fatal(err)
	}
	approved := get()
	if approved.ReaderChange.State != "approved" || !slices.Equal(approved.ReaderChange.After, []string{"bob"}) || !slices.Equal(approved.ReadSubjects, p.ReadSubjects) {
		t.Fatal("approval applied or trusted a replacement form")
	}
	if err := service.Readers(ctx, p.AccountID, p.ID, approved.Revision, "discard", "", nil); !errors.Is(err, ErrConflict) {
		t.Fatal("approved intent discarded", err)
	}
	if _, err := store.Control(ctx, p.AccountID, p.ID, approved.Revision, "resync"); !errors.Is(err, ErrConflict) {
		t.Fatal("resync discarded approved revocation", err)
	}
	// A new exposure approval must wait for the acknowledged reader set,
	// even if a failed worker has refreshed the ordinary observation.
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET desired_state='running',state='blocked',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ApproveSchema(ctx, p.AccountID, p.ID, approved.Revision, plan.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatal("schema exposure approved with an unacknowledged reader change", err)
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	applied := *approved.ReaderChange
	applied.State = "applied"
	wrong := applied
	wrong.ID = strings.Repeat("f", 64)
	if err = store.Finish(ctx, leased, Observation{State: "paused", ReaderChange: &wrong}); err != nil {
		t.Fatal(err)
	}
	if get().ReaderChange.State != "approved" {
		t.Fatal("unrelated result completed reader change")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	stale := leased
	stale.LeaseToken = uuid.NewString()
	if err = store.Finish(ctx, stale, Observation{ReaderOnly: true, ReaderChange: &applied}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale lease acknowledged reader change", err)
	}
	if get().ReaderChange.State != "approved" || get().LeaseToken != leased.LeaseToken {
		t.Fatal("stale acknowledgement changed current intent or lease")
	}
	if err = store.Finish(ctx, leased, Observation{ReaderOnly: true, State: "paused", ReaderChange: &applied}); err != nil {
		t.Fatal(err)
	}
	final := get()
	if final.State != leased.State || final.ObservedAt != leased.ObservedAt || final.ReaderChange.State != "applied" || !slices.Equal(final.ReadSubjects, []string{"bob"}) || final.Generation != p.Generation || final.SchemaChange.ID != plan.ID || !slices.Equal(final.Tables, p.Tables) {
		t.Fatal("reader completion changed unrelated workflow")
	}
}

func TestReaderPoliciesAreAtomicAndStaleGateUsesAppliedAuthority(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Revision: 3, ReadSubjects: []string{"alice"}, Tables: []string{"customers", "orders"}}}
	c := &ReaderChange{State: "approved", Generation: 1, PreviewRevision: 2, Before: p.ReadSubjects, After: []string{"bob's\\subject"}, Tables: p.Tables}
	c.ID = readerChangeID(p, *c)
	p.ReaderChange = c
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,retiring boolean,readable boolean);
 CREATE TABLE public.customers(id bigint PRIMARY KEY); INSERT INTO public.customers VALUES(1);
 CREATE TABLE public.orders(id bigint PRIMARY KEY); INSERT INTO public.orders VALUES(1);
 ALTER TABLE public.customers ENABLE ROW LEVEL SECURITY; CREATE POLICY gregale_pipeline_read ON public.customers FOR SELECT USING(false);
 ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY; CREATE POLICY gregale_pipeline_read ON public.orders FOR SELECT USING(true)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range p.Tables {
		if _, err := pool.Exec(ctx, "COMMENT ON TABLE "+tableIdent(table)+" IS "+literal(marker(p))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,false,true)`, p.ID); err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	if _, err = applyReaders(ctx, p, target, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("lost fence committed access", err)
	}
	var valid bool
	if err = target.QueryRow(ctx, `SELECT (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.orders'::regclass) AND to_regclass('gregale_pipeline.reader_access') IS NULL`).Scan(&valid); err != nil || !valid {
		t.Fatal("partial reader update committed", err)
	}
	// COPY holds a row-exclusive lock on the closed table. Reader changes must
	// finish without waiting for that transaction or touching its gate.
	copyConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer copyConn.Release()
	copyTx, err := copyConn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyTx.Rollback(ctx) }()
	if _, err = copyTx.Exec(ctx, `LOCK TABLE public.customers IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	applied, err := applyReaders(ctx, p, target, func(context.Context) error { return nil })
	if err != nil || applied.State != "applied" {
		t.Fatal("reader update blocked by closed table", err)
	}
	if err = target.QueryRow(ctx, `SELECT (SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid='public.customers'::regclass) AND (SELECT subjects=$2::text[] FROM gregale_pipeline.reader_access WHERE pipeline_id=$1) AND (SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)`, p.ID, c.After).Scan(&valid); err != nil || !valid {
		t.Fatal("reader update opened quarantine or changed shared gate", err)
	}
	_ = copyTx.Rollback(ctx)
	// Replay can acknowledge the committed change while paused and with an
	// unreachable source. It never needs source capacity or schema refresh.
	paused := p
	paused.DesiredState, paused.State, paused.Reason = "paused", "paused", "source_unavailable"
	engine := &Engine{Target: func(c context.Context, _, _ string, run func(*pgx.Conn, string) error) error { return run(target, "") }, openSource: func(context.Context, string) (*pgxpool.Pool, error) {
		t.Fatal("reader acknowledgement reached source reconciliation")
		return nil, ErrUnavailable
	}}
	ob := engine.Reconcile(ctx, paused, "", func(context.Context) error { return nil })
	if !ob.ReaderOnly || ob.ReaderChange == nil || ob.ReaderChange.State != "applied" || ob.State != "paused" || ob.Reason != "source_unavailable" {
		t.Fatal("source failure prevented reader acknowledgement", ob)
	}
	// Simulate losing the commit response, restarting, and opening a gate with
	// an old catalog reader list. The private applied set must win.
	if _, err = applyReaders(ctx, p, target, func(context.Context) error { return nil }); err != nil {
		t.Fatal("lost reply replay failed", err)
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = updateTableReads(ctx, p, tx, true); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT bool_and(pg_get_expr(polqual,polrelid) NOT LIKE '%alice%' AND pg_get_expr(polqual,polrelid) LIKE '%bob%') FROM pg_policy WHERE polrelid IN ('public.orders'::regclass,'public.customers'::regclass)`).Scan(&valid); err != nil || !valid {
		t.Fatal("stale worker restored revoked access", err)
	}
}
