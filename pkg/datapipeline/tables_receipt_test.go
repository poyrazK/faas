package datapipeline

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestTableSelectionReadGateHandlesUncreatedAndForeignTables(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	p := Pipeline{AccountID: uuid.NewString()}
	p.ID, p.Generation, p.Tables, p.ReadSubjects = uuid.NewString(), 1, []string{"absent", "foreign_table", "owned_table"}, []string{"alice"}
	if _, err := pool.Exec(ctx, `CREATE TABLE public.owned_table(id bigint PRIMARY KEY); CREATE TABLE public.foreign_table(id bigint PRIMARY KEY);
 CREATE POLICY gregale_pipeline_read ON public.owned_table USING(true); CREATE POLICY gregale_pipeline_read ON public.foreign_table USING(true);
 CREATE SCHEMA gregale_pipeline; CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,readable boolean)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "COMMENT ON TABLE public.owned_table IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors VALUES($1,$2,true)`, p.ID, p.Generation); err != nil {
		t.Fatal(err)
	}
	// Closing one table's gate must preserve the shared readable receipt and
	// all other policies, even when a broad selection includes absent tables.
	scope := p
	scope.Tables = []string{"owned_table"}
	if err := updateTableReads(ctx, scope, pool, false); err != nil {
		t.Fatal(err)
	}
	var isolated bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)
 AND (SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid='public.owned_table'::regclass)
 AND (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.foreign_table'::regclass)`, p.ID).Scan(&isolated); err != nil || !isolated {
		t.Fatal("table gate changed shared readability", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = updateMirrorReads(ctx, p, tx, false); err != nil {
		t.Fatal("absent or foreign added table prevented closing existing reads", err)
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT
 (SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid='public.owned_table'::regclass)
 AND (SELECT pg_get_expr(polqual,polrelid)='true' FROM pg_policy WHERE polrelid='public.foreign_table'::regclass)
 AND (SELECT NOT readable FROM gregale_pipeline.mirrors WHERE pipeline_id=$1)`, p.ID).Scan(&valid); err != nil || !valid {
		t.Fatal("read gate touched a foreign table or failed to close owned reads", err)
	}
	if err = updateMirrorReads(ctx, p, tx, true); !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete or foreign selection was opened", err)
	}
}
