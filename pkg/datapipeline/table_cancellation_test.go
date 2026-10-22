package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func cancellationTestPlan(p Pipeline) SchemaChange {
	before := schemaTestRelations()
	after := append([]relation{{Name: "customers", OID: 300, Columns: before[0].Columns}}, before...)
	plan := tablePlan(p, before, after)
	plan.State = "verifying"
	return plan
}

func TestTableCancellationIDBindsExactRetirementAndRetention(t *testing.T) {
	p := Pipeline{SchemaApproval: strings.Repeat("a", 64), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 2}}
	c := TableCancellation{Table: "customers", RequestID: uuid.NewString(), Retention: "retain_private", TableOID: 1, SourceTableOID: 2, SubscriptionOID: 3, PublicationOID: 4}
	id := tableCancellationID(p, c)
	for _, mutate := range []func(*Pipeline, *TableCancellation){
		func(p *Pipeline, _ *TableCancellation) { p.Generation++ }, func(p *Pipeline, _ *TableCancellation) { p.SchemaApproval = strings.Repeat("b", 64) },
		func(_ *Pipeline, c *TableCancellation) { c.Table = "orders" }, func(_ *Pipeline, c *TableCancellation) { c.RequestID = uuid.NewString() },
		func(_ *Pipeline, c *TableCancellation) { c.TableOID++ }, func(_ *Pipeline, c *TableCancellation) { c.SourceTableOID++ },
		func(_ *Pipeline, c *TableCancellation) { c.SubscriptionOID++ }, func(_ *Pipeline, c *TableCancellation) { c.PublicationOID++ },
		func(_ *Pipeline, c *TableCancellation) { c.Retention = "delete" },
		func(_ *Pipeline, c *TableCancellation) { c.Kind = "live" },
	} {
		copyP, copyC := p, c
		mutate(&copyP, &copyC)
		if tableCancellationID(copyP, copyC) == id {
			t.Fatal("cancellation omitted an approval boundary")
		}
	}
}

func TestTableReaddCannotReuseAnEarlierProofIdentity(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1}, SchemaChange: &SchemaChange{SelectionID: uuid.NewString()}}
	first := cancellationTestPlan(p)
	p.SchemaChange.SelectionID = uuid.NewString()
	second := tablePlan(p, first.Before, first.After)
	if first.ID == second.ID || first.AfterHash != second.AfterHash {
		t.Fatal("a re-add reused approval identity or changed the structural schema")
	}
	p.SchemaApproval, p.SchemaChange = first.ID, &first
	oldProof, _ := verificationID(p, first.After)
	p.SchemaApproval, p.SchemaChange = second.ID, &second
	newProof, _ := verificationID(p, second.After)
	if newProof == oldProof {
		t.Fatal("fresh selection adopted an old verification proof")
	}
}

func TestTableCancellationApprovalIsExactOwnedFreshAndFenced(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	plan := cancellationTestPlan(p)
	plan.Retry = api.DataPipelineSchemaRetriesMax // Cancellation needs no copy attempt/capacity.
	encoded, _ := json.Marshal(plan)
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET tables='["customers","orders"]',schema_change=$2,schema_approval=$3,reason='table_addition_source_changed',retry_at=clock_timestamp() WHERE id=$1`, p.ID, encoded, plan.ID); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"desired_state='paused'", "state='resync_required'", "schema_approval=''", `schema_change=jsonb_set(schema_change,'{State}','"complete"')`, "observed_at=clock_timestamp()-interval '1 day'"} {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE data_pipelines SET "+mutation+" WHERE id=$1", p.ID); err != nil {
			t.Fatal(err)
		}
		n, err := store.q.PipelineRequestTableCancellation(ctx, tx, sqlc.PipelineRequestTableCancellationParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, TableName: "customers", RequestID: uuid.NewString(), MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
		_ = tx.Rollback(ctx)
		if err != nil || n != 0 {
			t.Fatal("unsafe cancellation accepted", mutation, n, err)
		}
	}
	for _, tc := range []struct {
		account, table string
		revision       int64
	}{{uuid.NewString(), "customers", p.Revision}, {p.AccountID, "orders", p.Revision}, {p.AccountID, "customers", p.Revision + 1}} {
		if err := service.TableCancellation(ctx, tc.account, p.ID, tc.revision, tc.table, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign/stale/baseline cancellation accepted", err)
		}
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, p.Revision, "customers", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live"}); !errors.Is(err, ErrConflict) {
		t.Fatal("old worker overwrote cancellation", err)
	}
	requested, _ := store.Get(ctx, p.AccountID, p.ID)
	if len(requested.Tables) != 2 || requested.SchemaChange.Cancellation.State != "requested" {
		t.Fatal("request changed selection")
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, requested.Revision, "", strings.Repeat("a", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("unobserved request approved", err)
	}
	c := TableCancellation{Table: "customers", RequestID: requested.SchemaChange.Cancellation.RequestID, Retention: "retain_private", TableOID: 401, SourceTableOID: 300, SubscriptionOID: 402, PublicationOID: 403, State: "pending", Stage: "preview"}
	p.SchemaApproval = plan.ID
	c.ID = tableCancellationID(p, c)
	plan.Cancellation = &c
	encoded, _ = json.Marshal(plan)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2,reason='table_cancellation_pending',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID, encoded); err != nil {
		t.Fatal(err)
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, requested.Revision, "", strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("different preview approved", err)
	}
	if err = service.ApproveSchema(ctx, p.AccountID, p.ID, requested.Revision, plan.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("retry bypassed pending cancellation", err)
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, requested.Revision, "", c.ID); err != nil {
		t.Fatal(err)
	}
	approved, _ := store.Get(ctx, p.AccountID, p.ID)
	if approved.SchemaChange.Cancellation.State != "approved" || len(approved.Tables) != 2 || approved.Generation != p.Generation || approved.SchemaChange.Retry != plan.Retry {
		t.Fatal("approval changed selection/copy budget")
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, approved.Revision, "customers", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("approved cleanup replaced", err)
	}
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	result := cancellationResult(approved)
	if err = store.Finish(ctx, leased, Observation{State: "live", SchemaChange: &result}); err != nil {
		t.Fatal(err)
	}
	finished, _ := store.Get(ctx, p.AccountID, p.ID)
	if !slices.Equal(finished.Tables, []string{"orders"}) || finished.SchemaApproval != result.ID || finished.Generation != p.Generation {
		t.Fatal("completion did not atomically reduce selection", finished)
	}
}

func TestTableCancellationRetainsRowsAndRebasesRemainingCopiesAtomically(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString()}
	p.ID, p.Generation, p.Tables = uuid.NewString(), 1, []string{"customers", "orders", "payments"}
	plan := cancellationTestPlan(p)
	plan.After = append(plan.After, relation{Name: "payments", OID: 301, Columns: plan.After[0].Columns})
	plan = tablePlan(p, plan.Before, plan.After)
	p.SchemaApproval, p.SchemaChange = plan.ID, &plan
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline; REVOKE ALL ON SCHEMA gregale_pipeline FROM PUBLIC;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_hash text,schema_spec jsonb,schema_change text,schema_phase text,table_stage text,tables text[],retiring boolean,readable boolean);
 CREATE TABLE public.customers(id bigint PRIMARY KEY,name text); INSERT INTO public.customers VALUES(1,'retained');
 ALTER TABLE public.customers ENABLE ROW LEVEL SECURITY; CREATE POLICY gregale_pipeline_read ON public.customers FOR SELECT USING(false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "COMMENT ON TABLE public.customers IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan.After)
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,$2,$3,$4,'verifying','copying',$5,false,true)`, p.ID, plan.AfterHash, encoded, plan.ID, p.Tables); err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	if err = ensureTableCancellationReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = ensureTableCopyReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	c := TableCancellation{Table: "customers", RequestID: uuid.NewString(), Retention: "retain_private", State: "approved"}
	if err = target.QueryRow(ctx, `SELECT 'public.customers'::regclass::oid`).Scan(&c.TableOID); err != nil {
		t.Fatal(err)
	}
	c.ID = tableCancellationID(p, c)
	plan.Cancellation = &c
	always := func(context.Context) error { return nil }
	stale := func(context.Context) error { return ErrConflict }
	progress := TableCopyProgress{Table: "payments", Phase: "copying", NativeState: "d", PeakRows: 1234, ProgressAt: time.Now().Add(-time.Minute)}
	if err = saveTableCopies(ctx, p, target, []TableCopyProgress{{Table: "customers"}, progress}); err != nil {
		t.Fatal(err)
	}
	if err = beginTableCancellation(ctx, p, target, stale); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if stage, _, err := loadTableCancellation(ctx, p, target); err != nil || stage != "" {
		t.Fatal("stale intent committed", stage, err)
	}
	if err = beginTableCancellation(ctx, p, target, always); err != nil {
		t.Fatal(err)
	}
	if err = saveCancellationStage(ctx, p, target, "detached", always); err != nil {
		t.Fatal(err)
	}
	if err = archiveCancelledTable(ctx, p, target, stale); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var valid bool
	if err = target.QueryRow(ctx, `SELECT to_regclass('public.customers')::oid=$1 AND (SELECT count(*)=1 FROM public.customers)`, c.TableOID).Scan(&valid); err != nil || !valid {
		t.Fatal("stale archive moved/deleted rows", err)
	}
	if err = archiveCancelledTable(ctx, p, target, always); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = target.QueryRow(ctx, "SELECT count(*) FROM gregale_pipeline."+ident(retainedTableName(c))).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("imported rows not retained", rows, err)
	}
	result := cancellationResult(p)
	if err = completeTableCancellation(ctx, p, target, &result, "copying", stale); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if stage, _, err := loadTableCancellation(ctx, p, target); err != nil || stage != "archived" {
		t.Fatal("stale completion committed", stage, err)
	}
	if err = completeTableCancellation(ctx, p, target, &result, "copying", always); err != nil {
		t.Fatal(err)
	}
	stage, recovered, err := loadTableCancellation(ctx, p, target)
	if err != nil || stage != "complete" || !slices.Equal(recovered.AddedTables, []string{"payments"}) || recovered.ID == plan.ID {
		t.Fatal("lost completion not recovered", stage, err)
	}
	if len(recovered.Copies) != 1 || recovered.Copies[0].PeakRows != 1234 || !recovered.Copies[0].ProgressAt.Equal(progress.ProgressAt) {
		t.Fatal("remaining copy progress lost", recovered.Copies)
	}
	if err = target.QueryRow(ctx, `SELECT (SELECT readable AND schema_change=$2 AND tables=ARRAY['orders','payments'] FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)
 AND (SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid=$3)`, p.ID, result.ID, c.TableOID).Scan(&valid); err != nil || !valid {
		t.Fatal("shared/read gate changed", err)
	}
}
