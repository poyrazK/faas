package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestSchemaBackfillCopiesTypedSourceValuesAndFencesConstraints(t *testing.T) {
	source, destination := pgtest.OpenMigrated(t), pgtest.OpenMigrated(t)
	ctx := t.Context()
	acquired, err := destination.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	p := Pipeline{AccountID: uuid.NewString()}
	p.ID, p.Generation, p.Revision, p.Tables = uuid.NewString(), 1, 1, []string{"backfill_case"}
	base := `CREATE TABLE public.backfill_case(a text COLLATE "C",b timestamptz,original text NOT NULL,PRIMARY KEY(a,b));
 INSERT INTO public.backfill_case VALUES('a','2020-01-01T00:00Z','keep a'),('b','2020-01-02T00:00Z','keep b')`
	if _, err = source.Exec(ctx, base); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, base); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, "COMMENT ON TABLE public.backfill_case IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	beforeTable, _, err := inspectSourceTable(ctx, source, p.Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	before := []relation{beforeTable}
	if _, err = source.Exec(ctx, `CREATE SEQUENCE public.backfill_values START 100;
 ALTER TABLE public.backfill_case ADD COLUMN status text NOT NULL DEFAULT 'pending',
 ADD COLUMN payload bytea DEFAULT decode('00ff','hex'), ADD COLUMN document json DEFAULT '{"k":[1,null,"v"]}',
 ADD COLUMN issued timestamptz DEFAULT '2020-02-01T03:04:05+03',
 ADD COLUMN token bigint NOT NULL DEFAULT nextval('public.backfill_values');
 UPDATE public.backfill_case SET status='custom',payload=NULL WHERE a='b'`); err != nil {
		t.Fatal(err)
	}
	afterTable, _, err := inspectSourceTable(ctx, source, p.Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	after := []relation{afterTable}
	plan := schemaPlan(p, before, after)
	if !plan.Supported {
		t.Fatal("default and required additions rejected", plan)
	}
	p.SchemaChange, p.SchemaApproval = &plan, plan.ID
	encoded, _ := json.Marshal(before)
	if _, err = target.Exec(ctx, `CREATE SCHEMA gregale_pipeline;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint,schema_hash text,schema_spec jsonb,schema_change text DEFAULT '',schema_phase text DEFAULT '',barrier pg_lsn DEFAULT '0/0',readable boolean DEFAULT false,retiring boolean DEFAULT false)`); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors(pipeline_id,generation,schema_hash,schema_spec) VALUES($1,$2,$3,$4)`, p.ID, p.Generation, plan.BeforeHash, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, `ALTER TABLE public.backfill_case ADD COLUMN status text,ADD COLUMN payload bytea,ADD COLUMN document json,ADD COLUMN issued timestamptz,ADD COLUMN token bigint`); err != nil {
		t.Fatal(err)
	}
	current := func(context.Context) error { return nil }
	encoded, _ = json.Marshal(after)
	if _, err = target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_spec=$2,schema_hash=$3,schema_change=$4,schema_phase='verifying' WHERE pipeline_id=$1`, p.ID, encoded, plan.AfterHash, plan.ID); err != nil {
		t.Fatal(err)
	}
	intent, spec := verificationID(p, after)
	storage, err := verificationSourceStorage(ctx, source, after)
	if err != nil {
		t.Fatal(err)
	}
	c, err := startVerification(ctx, p, target, after, physicalVerificationID(intent, storage), intent, spec, storage, 0, current)
	if err != nil {
		t.Fatal(err)
	}
	compare := func() {
		t.Helper()
		src, err := source.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = src.Rollback(ctx) }()
		dst, err := target.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = dst.Rollback(ctx) }()
		for _, tx := range []pgx.Tx{src, dst} {
			if err = verificationSetting(ctx, tx); err != nil {
				t.Fatal(err)
			}
		}
		moved, err := compareLockedVerificationRange(ctx, p, src, dst, after[0], c, 0, nil)
		if err != nil || !moved {
			t.Fatal("typed source backfill failed", moved, err, c.Progress)
		}
		if err = saveVerification(ctx, dst, p, c); err != nil {
			t.Fatal(err)
		}
		if err = dst.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	compare()
	if c.Progress.BackfilledRows != 2 || c.Table != 1 {
		t.Fatal("missing backfill progress", c.Progress)
	}
	assertBackfillPreservesExistingColumns(t, ctx, p, source, target, after[0], c)
	// Failed lease checks roll back both NOT NULL and the refresh receipt.
	if err = prepareSchemaRefresh(ctx, p, target, plan, func(context.Context) error { return ErrConflict }); !errors.Is(err, ErrConflict) {
		t.Fatal("stale constraint lease accepted", err)
	}
	var required bool
	if err = target.QueryRow(ctx, `SELECT attnotnull FROM pg_attribute WHERE attrelid='public.backfill_case'::regclass AND attname='status'`).Scan(&required); err != nil || required {
		t.Fatal("stale constraint transaction committed", err)
	}
	if err = prepareSchemaRefresh(ctx, p, target, plan, current); err != nil {
		t.Fatal(err)
	}
	if err = prepareSchemaRefresh(ctx, p, target, plan, current); err != nil {
		t.Fatal("refresh receipt replay failed", err)
	}
	var valid bool
	if err = target.QueryRow(ctx, `SELECT (SELECT bool_and(attnotnull) FROM pg_attribute WHERE attrelid='public.backfill_case'::regclass AND attname IN ('status','token'))
 AND NOT EXISTS(SELECT 1 FROM pg_attrdef WHERE adrelid='public.backfill_case'::regclass)
 AND (SELECT status='custom' AND token=101 AND payload IS NULL AND original='keep b' FROM public.backfill_case WHERE a='b')`).Scan(&valid); err != nil || !valid {
		t.Fatal("source values, required constraints or default isolation lost", err)
	}
	// A completed plan is read-only, including fields originally added by it.
	complete := plan
	complete.State = "complete"
	p.SchemaChange = &complete
	if cols, err := approvedBackfillColumns(p, after[0]); err != nil || len(cols) != 0 {
		t.Fatal("completed migration can still write", cols, err)
	}
	p.SchemaChange = &plan
	p.Generation++
	if _, err = approvedBackfillColumns(p, after[0]); !errors.Is(err, ErrConflict) {
		t.Fatal("approval survived generation change", err)
	}
}

func assertBackfillPreservesExistingColumns(t *testing.T, ctx context.Context, p Pipeline, source interface {
	Begin(context.Context) (pgx.Tx, error)
}, target *pgx.Conn, r relation, checkpoint *verificationCheckpoint) {
	t.Helper()
	src, err := source.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Rollback(ctx) }()
	dst, err := target.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Rollback(ctx) }()
	for _, tx := range []pgx.Tx{src, dst} {
		if err = verificationSetting(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = dst.Exec(ctx, `UPDATE public.backfill_case SET status='wrong new value',original='wrong original' WHERE a='a'`); err != nil {
		t.Fatal(err)
	}
	c := *checkpoint
	c.Table, c.Cursor = 0, nil
	moved, err := compareLockedVerificationRange(ctx, p, src, dst, r, &c, 0, nil)
	if err != nil || moved || c.Progress.State != "mismatch" {
		t.Fatal("backfill repaired an existing column", moved, err, c.Progress)
	}
	var valid bool
	if err = dst.QueryRow(ctx, `SELECT status='pending' AND original='wrong original' FROM public.backfill_case WHERE a='a'`).Scan(&valid); err != nil || !valid {
		t.Fatal("backfill exceeded approved column scope", err)
	}
	if _, err = dst.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET readable=true WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	columns, err := approvedBackfillColumns(p, r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backfillVerificationRows(ctx, p, dst, r, columns, []json.RawMessage{json.RawMessage(`{"a":"a","b":"2020-01-01T00:00Z","status":"not approved"}`)})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("backfill wrote through an open read gate", err)
	}
}
