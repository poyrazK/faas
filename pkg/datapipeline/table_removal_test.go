package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLiveTableRemovalApprovalIsOwnedFreshExactAndCannotRemoveLastTable(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET state='live',reason='',tables='["customers","orders"]',schema_change='{}',schema_approval='',initialized=true,retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"desired_state='paused'", "state='blocked'", "initialized=false", "tables='[\"customers\"]'", "verify_requested=true", `verification_progress='{"State":"running"}'`, `schema_change='{"State":"verifying"}'`, "observed_at=clock_timestamp()-interval '1 day'"} {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE data_pipelines SET "+mutation+" WHERE id=$1", p.ID); err != nil {
			t.Fatal(err)
		}
		n, err := store.q.PipelineRequestTableRemoval(ctx, tx, sqlc.PipelineRequestTableRemovalParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, TableName: "customers", Plan: []byte("{}"), MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
		_ = tx.Rollback(ctx)
		if err != nil || n != 0 {
			t.Fatal("unsafe live removal accepted", mutation, n, err)
		}
	}
	for _, tc := range []struct {
		account, table string
		revision       int64
	}{{uuid.NewString(), "customers", p.Revision}, {p.AccountID, "missing", p.Revision}, {p.AccountID, "customers", p.Revision + 1}} {
		if err := service.TableRemoval(ctx, tc.account, p.ID, tc.revision, tc.table, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign/stale/unselected removal accepted", err)
		}
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, p.Revision, "customers", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live"}); !errors.Is(err, ErrConflict) {
		t.Fatal("old worker replaced removal intent", err)
	}
	requested, _ := store.Get(ctx, p.AccountID, p.ID)
	if requested.SchemaChange.Cancellation.Kind != "live" || len(requested.Tables) != 2 || requested.SchemaApproval != "" {
		t.Fatal("preview changed selection/approval")
	}
	if _, err = store.Control(ctx, p.AccountID, p.ID, requested.Revision, "verify"); !errors.Is(err, ErrConflict) {
		t.Fatal("manual verification bypassed pending removal", err)
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, requested.Revision, "", strings.Repeat("a", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("unobserved preview approved", err)
	}
	oldRequest := requested.SchemaChange.Cancellation.RequestID
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, requested.Revision, "", ""); err != nil {
		t.Fatal(err)
	}
	kept, _ := store.Get(ctx, p.AccountID, p.ID)
	if kept.SchemaChange != nil || kept.State != "live" || len(kept.Tables) != 2 || kept.SchemaApproval != "" {
		t.Fatal("dismissal changed live selection")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, kept.Revision, "customers", ""); err != nil {
		t.Fatal(err)
	}
	requested, _ = store.Get(ctx, p.AccountID, p.ID)
	if requested.SchemaChange.Cancellation.RequestID == oldRequest {
		t.Fatal("preview reused prior request identity")
	}
	before := append([]relation{{Name: "customers", OID: 300, Columns: schemaTestRelations()[0].Columns}}, schemaTestRelations()...)
	plan := liveRemovalPlan(requested, before)
	c := *plan.Cancellation
	c.State, c.Stage = "pending", "preview"
	c.TableOID, c.SourceTableOID, c.SubscriptionOID, c.PublicationOID = 401, 300, 402, 403
	virtual := requested
	virtual.SchemaApproval = plan.ID
	c.ID = tableCancellationID(virtual, c)
	plan.Cancellation = &c
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "blocked", Reason: "table_removal_pending", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	pending, _ := store.Get(ctx, p.AccountID, p.ID)
	if pending.SchemaApproval != "" {
		t.Fatal("worker approved removal without confirmation")
	}
	if err = service.TableCancellation(ctx, p.AccountID, p.ID, pending.Revision, "", c.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("onboarding confirmation approved live removal", err)
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, pending.Revision, "", strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("different removal identity approved", err)
	}
	for _, mutation := range []string{"observed_at=clock_timestamp()-interval '1 day'", `schema_change=jsonb_set(schema_change,'{Cancellation,Kind}','""')`, `schema_change=jsonb_set(schema_change,'{Cancellation,Retention}','"delete"')`, "tables='[\"customers\"]'"} {
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE data_pipelines SET "+mutation+" WHERE id=$1", p.ID); err != nil {
			t.Fatal(err)
		}
		n, err := store.q.PipelineApproveTableRemoval(ctx, tx, sqlc.PipelineApproveTableRemovalParams{AccountID: p.AccountID, ID: p.ID, Revision: pending.Revision, CancellationID: c.ID, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
		_ = tx.Rollback(ctx)
		if err != nil || n != 0 {
			t.Fatal("unsafe removal confirmation accepted", mutation, n, err)
		}
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, pending.Revision, "", c.ID); err != nil {
		t.Fatal(err)
	}
	approved, _ := store.Get(ctx, p.AccountID, p.ID)
	if approved.SchemaApproval != plan.ID || approved.SchemaChange.Cancellation.State != "approved" || len(approved.Tables) != 2 || approved.Generation != p.Generation {
		t.Fatal("approval changed saved selection before cleanup")
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, approved.Revision, "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("approved removal was dismissed", err)
	}
	result := cancellationResult(approved)
	if !slices.Equal(relationNames(result.Before), []string{"orders"}) || result.State != "complete" || !result.Supported || len(result.AddedTables) != 0 {
		t.Fatal("removal did not establish remaining baseline", result)
	}
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live", SchemaChange: &result}); err != nil {
		t.Fatal(err)
	}
	finished, _ := store.Get(ctx, p.AccountID, p.ID)
	if !slices.Equal(finished.Tables, []string{"orders"}) || finished.SchemaApproval != result.ID || finished.Generation != p.Generation {
		t.Fatal("selection was not reduced atomically")
	}
	if err = service.TableRemoval(ctx, p.AccountID, p.ID, finished.Revision, "orders", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("last table removed", err)
	}
	if _, err = store.Control(ctx, p.AccountID, p.ID, finished.Revision, "delete"); err != nil {
		t.Fatal("ordinary disconnect no longer available", err)
	}
}

func TestLiveTableRemovalGatesOnlyApprovedTableAndRetainsRows(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"customers", "orders"}}}
	p.SchemaChange = &SchemaChange{Cancellation: &TableCancellation{Kind: "live", Table: "customers", RequestID: uuid.NewString(), Retention: "retain_private", State: "approved"}}
	before := append([]relation{{Name: "customers", OID: 300, Columns: schemaTestRelations()[0].Columns}}, schemaTestRelations()...)
	plan := liveRemovalPlan(p, before)
	p.SchemaChange = &plan
	p.SchemaApproval = plan.ID
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline; REVOKE ALL ON SCHEMA gregale_pipeline FROM PUBLIC;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_hash text,schema_spec jsonb,schema_change text,schema_phase text,table_stage text,tables text[],retiring boolean,readable boolean);
 CREATE TABLE public.customers(id bigint PRIMARY KEY,name text); INSERT INTO public.customers VALUES(1,'retained live row');
 CREATE TABLE public.orders(id bigint PRIMARY KEY); INSERT INTO public.orders VALUES(1);
 ALTER TABLE public.customers ENABLE ROW LEVEL SECURITY; CREATE POLICY gregale_pipeline_read ON public.customers FOR SELECT USING(true);
 ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY; CREATE POLICY gregale_pipeline_read ON public.orders FOR SELECT USING(true)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range p.Tables {
		if _, err := pool.Exec(ctx, "COMMENT ON TABLE "+tableIdent(table)+" IS "+literal(marker(p))); err != nil {
			t.Fatal(err)
		}
	}
	spec, _ := json.Marshal(before)
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,$2,$3,'','','complete',$4,false,true)`, p.ID, plan.BeforeHash, spec, p.Tables); err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	c := plan.Cancellation
	if err = target.QueryRow(ctx, `SELECT 'public.customers'::regclass::oid`).Scan(&c.TableOID); err != nil {
		t.Fatal(err)
	}
	c.ID = tableCancellationID(p, *c)
	if err = ensureTableCancellationReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = ensureTableCopyReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	always := func(context.Context) error { return nil }
	stale := func(context.Context) error { return ErrConflict }
	checkGates := func(closed bool) {
		t.Helper()
		var valid bool
		if err := target.QueryRow(ctx, `SELECT (SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)
 AND (SELECT pg_get_expr(polqual,polrelid)=$2 FROM pg_policy WHERE polrelid='public.customers'::regclass)
 AND (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.orders'::regclass)`, p.ID, fmt.Sprint(!closed)).Scan(&valid); err != nil || !valid {
			t.Fatal("scoped read gates changed", closed, err)
		}
	}
	if err = validateLiveRemovalTable(ctx, p, target); err != nil {
		t.Fatal(err)
	}
	checkGates(false)
	if err = beginTableCancellation(ctx, p, target, stale); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	checkGates(false)
	if stage, _, err := loadTableCancellation(ctx, p, target); err != nil || stage != "" {
		t.Fatal("unfenced gate/intent committed", stage, err)
	}
	if _, err = target.Exec(ctx, `CREATE POLICY unrelated_reader ON public.customers FOR SELECT USING(true)`); err != nil {
		t.Fatal(err)
	}
	if err = beginTableCancellation(ctx, p, target, always); !isBlockedReason(err, "table_cancellation_conflict") {
		t.Fatal("unrelated read policy adopted", err)
	}
	if _, err = target.Exec(ctx, `DROP POLICY unrelated_reader ON public.customers`); err != nil {
		t.Fatal(err)
	}
	for i := range api.DataPipelineRetainedTablesMax {
		if _, err = target.Exec(ctx, "CREATE TABLE gregale_pipeline."+ident(fmt.Sprintf("retained_limit_%d", i))+"(id int)"); err != nil {
			t.Fatal(err)
		}
	}
	if err = beginTableCancellation(ctx, p, target, always); !isBlockedReason(err, "table_cancellation_retention_limit") {
		t.Fatal("retention capacity was not checked before gating", err)
	}
	checkGates(false)
	for i := range api.DataPipelineRetainedTablesMax {
		if _, err = target.Exec(ctx, "DROP TABLE gregale_pipeline."+ident(fmt.Sprintf("retained_limit_%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err = beginTableCancellation(ctx, p, target, always); err != nil {
		t.Fatal(err)
	}
	checkGates(true)
	if err = saveCancellationStage(ctx, p, target, "detached", always); err != nil {
		t.Fatal(err)
	}
	if err = archiveCancelledTable(ctx, p, target, always); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = target.QueryRow(ctx, "SELECT count(*) FROM gregale_pipeline."+ident(retainedTableName(*c))).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("live rows not retained", rows, err)
	}
	result := cancellationResult(p)
	if err = completeTableCancellation(ctx, p, target, &result, "complete", stale); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if stage, _, err := loadTableCancellation(ctx, p, target); err != nil || stage != "archived" {
		t.Fatal("unfenced completion committed", stage, err)
	}
	if err = completeTableCancellation(ctx, p, target, &result, "complete", always); err != nil {
		t.Fatal(err)
	}
	stage, replayed, err := loadTableCancellation(ctx, p, target)
	if err != nil || stage != "complete" || !slices.Equal(replayed.RequestedTables, []string{"orders"}) || !slices.Equal(relationNames(replayed.Before), []string{"orders"}) {
		t.Fatal("completion replay lost remaining baseline", stage, err)
	}
	var valid bool
	if err = target.QueryRow(ctx, `SELECT (SELECT readable AND schema_phase='' AND tables=ARRAY['orders'] FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)
 AND (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.orders'::regclass)`, p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("completion closed shared/healthy gate", err)
	}
}
