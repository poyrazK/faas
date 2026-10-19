package datapipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestTableRecoveryReceiptCannotReplaceActiveOrNewerApproval(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString(), SchemaApproval: strings.Repeat("a", 64)}
	p.ID, p.Generation, p.Tables = uuid.NewString(), 1, []string{"recovered"}
	r := &TableRecovery{Table: "recovered", TableOID: 10, SubscriptionOID: 11, PublicationOID: 12, Attempt: 1, State: "approved"}
	p.SchemaChange = &SchemaChange{ID: p.SchemaApproval, Recovery: r}
	r.ID = tableRecoveryID(p, *r)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_change text,schema_phase text,retiring boolean,readable boolean);
 CREATE TABLE public.recovered(id bigint PRIMARY KEY);
 CREATE POLICY gregale_pipeline_read ON public.recovered USING(false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "COMMENT ON TABLE public.recovered IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,1,$2,'verifying',false,true)`, p.ID, p.SchemaApproval); err != nil {
		t.Fatal(err)
	}
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	if err = ensureTableRecoveryReceipt(ctx, target); err != nil {
		t.Fatal(err)
	}
	always := func(context.Context) error { return nil }
	if err = beginTableRecovery(ctx, p, target, always); err != nil {
		t.Fatal(err)
	}
	next := p
	next.SchemaApproval = strings.Repeat("b", 64)
	nextPlan := *p.SchemaChange
	nextRecovery := *r
	nextPlan.Recovery = &nextRecovery
	next.SchemaChange = &nextPlan
	nextRecovery.ID = tableRecoveryID(next, nextRecovery)
	if _, err = pool.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_change=$2 WHERE pipeline_id=$1`, p.ID, next.SchemaApproval); err != nil {
		t.Fatal(err)
	}
	if _, err = loadTableRecovery(ctx, next, target); !errors.Is(err, ErrConflict) {
		t.Fatal("active old recovery was ignored", err)
	}
	if err = beginTableRecovery(ctx, next, target, always); !errors.Is(err, ErrConflict) {
		t.Fatal("active old recovery was replaced", err)
	}
	if _, err = target.Exec(ctx, `UPDATE gregale_pipeline.table_recoveries SET stage='complete' WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if stage, err := loadTableRecovery(ctx, next, target); err != nil || stage != "" {
		t.Fatal("completed prior table addition blocked a new recovery", stage, err)
	}
	if err = beginTableRecovery(ctx, next, target, always); err != nil {
		t.Fatal("new table addition could not recover", err)
	}
	if stage, err := loadTableRecovery(ctx, next, target); err != nil || stage != "retiring" {
		t.Fatal("new recovery receipt missing", stage, err)
	}
	if _, err = loadTableRecovery(ctx, p, target); !errors.Is(err, ErrConflict) {
		t.Fatal("older approval adopted the new recovery", err)
	}
}
