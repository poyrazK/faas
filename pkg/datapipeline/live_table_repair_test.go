package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestLiveTableRepairRequiresSeparateExactApproval(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	service.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	relations := append([]relation{{Name: "customers", OID: 300, Columns: schemaTestRelations()[0].Columns}}, schemaTestRelations()...)
	plan := liveRepairPlan(p, relations, strings.Repeat("a", 64), "customers")
	if columns, err := approvedBackfillColumns(Pipeline{SchemaChange: &plan, SchemaApproval: plan.ID}, relations[0]); err != nil || len(columns) != 0 {
		t.Fatal("repair enabled column backfill instead of native recopy", columns, err)
	}
	plan.Recovery.TableOID, plan.Recovery.SubscriptionOID, plan.Recovery.PublicationOID = 401, 402, 403
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET tables='["customers","orders"]',schema_change='{}',schema_approval='',state='live',initialized=true,retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	progress := &VerificationProgress{ID: plan.SelectionID, State: "mismatch", Table: "customers", Tables: []string{"customers", "orders"}}
	if err = store.Finish(ctx, leased, Observation{State: "blocked", Reason: "live_table_mismatch", SchemaChange: &plan, VerificationProgress: progress, Verification: "mismatch"}); err != nil {
		t.Fatal(err)
	}
	detected, _ := store.Get(ctx, p.AccountID, p.ID)
	if detected.SchemaApproval != plan.ID || detected.SchemaChange.Recovery.State != "detected" || !slices.Equal(detected.Tables, []string{"customers", "orders"}) || detected.Generation != p.Generation {
		t.Fatal("detection lost scope or approved a replacement")
	}
	if !slices.Equal(RecoverableTables(detected), []string{"customers"}) || len(CancellableTables(detected)) != 0 || len(RemovableTables(detected)) != 0 {
		t.Fatal("repair exposed another table/workflow")
	}
	for _, retry := range []bool{false, true} {
		if err = service.ApproveSchema(ctx, p.AccountID, p.ID, detected.Revision, plan.ID, retry); !errors.Is(err, ErrConflict) {
			t.Fatal("schema approval bypassed repair consent", retry, err)
		}
	}
	for _, tc := range []struct {
		account, table string
		revision       int64
	}{{uuid.NewString(), "customers", detected.Revision}, {p.AccountID, "orders", detected.Revision}, {p.AccountID, "customers", detected.Revision + 1}} {
		if err = service.TableRecovery(ctx, tc.account, p.ID, tc.revision, "", tc.table); !errors.Is(err, ErrConflict) {
			t.Fatal("foreign/stale/healthy repair accepted", err)
		}
	}
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, detected.Revision, "", "customers"); err != nil {
		t.Fatal(err)
	}
	requested, _ := store.Get(ctx, p.AccountID, p.ID)
	if requested.SchemaChange.Recovery.Kind != "live" || requested.SchemaChange.Recovery.TableOID != 401 || requested.SchemaChange.SelectionID != plan.SelectionID {
		t.Fatal("preview lost quarantined identity")
	}
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, requested.Revision, strings.Repeat("b", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("unobserved repair approved", err)
	}
	plan = *requested.SchemaChange
	plan.Recovery.State, plan.Recovery.Stage, plan.Recovery.Attempt = "pending", "preview", 1
	plan.Recovery.ID = tableRecoveryID(requested, *plan.Recovery)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "blocked", Reason: "table_recovery_pending", SchemaChange: &plan, VerificationProgress: progress}); err != nil {
		t.Fatal(err)
	}
	pending, _ := store.Get(ctx, p.AccountID, p.ID)
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, pending.Revision, strings.Repeat("c", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("different preview approved", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET observed_at=clock_timestamp()-interval '1 day' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, pending.Revision, plan.Recovery.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("stale repair approved", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	service.CapacityStatus = nil
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, pending.Revision, plan.Recovery.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("repair accepted without capacity", err)
	}
	service.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	if err = service.TableRecovery(ctx, p.AccountID, p.ID, pending.Revision, plan.Recovery.ID); err != nil {
		t.Fatal(err)
	}
	approved, _ := store.Get(ctx, p.AccountID, p.ID)
	if approved.SchemaChange.Recovery.State != "approved" || approved.SchemaChange.Retry != 1 || approved.VerificationProgress != nil || approved.Generation != p.Generation || !slices.Equal(approved.Tables, detected.Tables) {
		t.Fatal("repair approval changed pipeline selection/generation")
	}
	if err = service.ApproveSchema(ctx, p.AccountID, p.ID, approved.Revision, plan.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("schema retry bypassed active repair", err)
	}
}

func TestLiveTableQuarantineGateAndReceiptAreAtomic(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"customers", "orders"}}}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_hash text,schema_spec jsonb,schema_change text,schema_phase text,table_stage text,tables text[],retiring boolean,readable boolean);
 CREATE TABLE public.customers(id bigint PRIMARY KEY); INSERT INTO public.customers VALUES(1);
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
	relations := append([]relation{{Name: "customers", OID: 300, Columns: schemaTestRelations()[0].Columns}}, schemaTestRelations()...)
	plan := liveRepairPlan(p, relations, strings.Repeat("a", 64), "customers")
	p.SchemaChange, p.SchemaApproval = &plan, plan.ID
	spec, _ := json.Marshal(relations)
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,$2,$3,'','','complete',$4,false,true)`, p.ID, plan.BeforeHash, spec, p.Tables); err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	if err = target.QueryRow(ctx, `SELECT 'public.customers'::regclass::oid`).Scan(&plan.Recovery.TableOID); err != nil {
		t.Fatal(err)
	}
	if err = ensureLiveRepairReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = commitLiveQuarantine(ctx, p, target, plan, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("lost quarantine fence accepted", err)
	}
	var valid bool
	if err = target.QueryRow(ctx, `SELECT (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.customers'::regclass)
 AND NOT EXISTS(SELECT 1 FROM gregale_pipeline.live_repairs)
 AND (SELECT schema_phase='' AND readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)`, p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("unfenced quarantine committed", err)
	}
	if err = commitLiveQuarantine(ctx, p, target, plan, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT (SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid='public.customers'::regclass)
 AND (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.orders'::regclass)
 AND (SELECT schema_phase='verifying' AND readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)
 AND (SELECT count(*)=1 FROM public.customers)`, p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("quarantine touched healthy reads or imported rows", err)
	}
	saved, stage, err := loadLiveRepair(ctx, p, target)
	if err != nil || stage != "quarantined" || saved.ID != plan.ID || saved.Recovery.TableOID != plan.Recovery.TableOID {
		t.Fatal("lost catalog acknowledgement cannot recover quarantine", err)
	}
}
