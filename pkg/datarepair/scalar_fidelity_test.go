package datarepair

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rejectedScalarCapture struct{ name, setup, columns, value, assignment string }

// ADR-20261103205518: journal images must retain native scalar semantics.
func TestRepairCaptureRejectsNegativeZero(t *testing.T) {
	testRejectedScalarCaptures(t, []rejectedScalarCapture{
		{"float8 before", "", "id int PRIMARY KEY,value float8", "1,'-0'::float8", "value=1"},
		{"float4 after", "", "id int PRIMARY KEY,value float4", "1,1", "value='-0'::float4"},
		{"domain", "CREATE DOMAIN zero_value AS float8;", "id int PRIMARY KEY,value zero_value", "1,'-0'::float8", "value=1"},
		{"composite", "CREATE TYPE zero_value AS (nested float8);", "id int PRIMARY KEY,value zero_value", "1,ROW('-0'::float8)::zero_value", "value=ROW(1)::zero_value"},
		{"key", "", "id float8 PRIMARY KEY,value int", "'-0'::float8,1", "value=2"},
	})
}

func TestRepairCaptureRejectsJsonbNull(t *testing.T) {
	testRejectedScalarCaptures(t, []rejectedScalarCapture{
		{"jsonb before", "", "id int PRIMARY KEY,value jsonb", "1,'null'::jsonb", "value='{}'::jsonb"},
		{"jsonb after", "", "id int PRIMARY KEY,value jsonb", "1,'{}'::jsonb", "value='null'::jsonb"},
		{"composite jsonb", "CREATE TYPE null_value AS (nested jsonb);", "id int PRIMARY KEY,value null_value", "1,ROW('null'::jsonb)::null_value", "value=ROW('{}'::jsonb)::null_value"},
	})
}

func testRejectedScalarCaptures(t *testing.T, cases []rejectedScalarCapture) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, tc.setup+`CREATE TABLE zero_products(`+tc.columns+`); SELECT gregale_repair.enroll('zero_products',ARRAY['value']); INSERT INTO zero_products(id,value) VALUES(`+tc.value+`)`); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err := e.Pool.QueryRow(ctx, `SELECT ROW(z.*)::text FROM zero_products z`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			err = Capture(ctx, tx, scope, id, "negative-zero", "zero_products", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, "UPDATE zero_products SET "+tc.assignment)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "repair_capture_value_not_lossless") {
				_ = tx.Rollback(ctx)
				t.Fatalf("lossy value was captured: %v", err)
			}
			if err := tx.Commit(ctx); err == nil {
				t.Fatal("swallowed fidelity error allowed COMMIT")
			}
			if err := e.Pool.QueryRow(ctx, `SELECT ROW(z.*)::text FROM zero_products z`).Scan(&after); err != nil || after != before {
				t.Fatalf("rejected capture changed a native image: %q %q %v", before, after, err)
			}
			var retained int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.operations WHERE id=$1`, id).Scan(&retained); err != nil || retained != 0 {
				t.Fatalf("rejected capture retained authority: %d %v", retained, err)
			}
		})
	}
}

func TestRepairNumericScaleAndNativeObservations(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE scaled_products(id int PRIMARY KEY,value numeric,hidden float8);
SELECT gregale_repair.enroll('scaled_products',ARRAY['value']); INSERT INTO scaled_products(id,value,hidden) VALUES(1,1.00,'-0'::float8)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "scaled_products", "UPDATE scaled_products SET value=1.0")
	p := approved(t, e, scope, source)
	if p.Candidates != 1 || !strings.Contains(string(p.Items[0].Before), "1.00") {
		t.Fatalf("numeric scale change disappeared: %+v", p)
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("unchanged scale did not validate: %+v %v", r, err)
	}
	// Isolate the value guard from the independent row-version guard.
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE scaled_products DISABLE TRIGGER gregale_repair_guard; UPDATE scaled_products SET value=1.000; ALTER TABLE scaled_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || r.Matches || len(r.Rows) != 1 || r.Rows[0].Reason != ReasonValuesChanged {
		t.Fatalf("scale-only drift was hidden: %+v %v", r, err)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unapproved or stale plan was applied: %v", err)
	}
	child, err := e.Replace(ctx, scope, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.Candidates != 0 || child.Conflicts != 1 {
		t.Fatalf("replacement retained scale drift: %+v %v", child, err)
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE scaled_products DISABLE TRIGGER gregale_repair_guard; UPDATE scaled_products SET value=1.0; ALTER TABLE scaled_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	p = approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "scale-repair"); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err := e.Pool.QueryRow(ctx, `SELECT scale(value)=2 AND encode(float8send(hidden),'hex')='8000000000000000' FROM scaled_products`).Scan(&exact); err != nil || !exact {
		t.Fatalf("scale or unselected signed zero lost: %v %v", exact, err)
	}
	purgeScalarSource(t, e, scope, source)
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("current-contract verification lost after source compaction: %+v %v", r, err)
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE scaled_products DISABLE TRIGGER gregale_repair_guard; UPDATE scaled_products SET value=1.0; ALTER TABLE scaled_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 0 || r.Edited != 1 {
		t.Fatalf("verification missed scale-only drift: %+v %v", r, err)
	}
}

func TestRepairVerificationDetectsNegativeZero(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE zero_products(id int PRIMARY KEY,value float8); SELECT gregale_repair.enroll('zero_products',ARRAY['value']); INSERT INTO zero_products(id,value) VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "zero_products", "UPDATE zero_products SET value=1")
	p := approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "positive-zero"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE zero_products DISABLE TRIGGER gregale_repair_guard; UPDATE zero_products SET value='-0'::float8; ALTER TABLE zero_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 0 || r.Edited != 1 {
		t.Fatalf("verification certified a changed zero sign: %+v %v", r, err)
	}
}

func TestRepairVerificationDetectsJsonbNull(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE null_products(id int PRIMARY KEY,value jsonb); SELECT gregale_repair.enroll('null_products',ARRAY['value']); INSERT INTO null_products(id,value) VALUES(1,NULL)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "null_products", `UPDATE null_products SET value='{}'::jsonb`)
	p := approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "sql-null"); err != nil {
		t.Fatal(err)
	}
	var sqlNull bool
	if err := e.Pool.QueryRow(ctx, `SELECT value IS NULL FROM null_products`).Scan(&sqlNull); err != nil || !sqlNull {
		t.Fatalf("SQL NULL was not restored: %v %v", sqlNull, err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("SQL NULL did not verify: %+v %v", r, err)
	}
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE null_products DISABLE TRIGGER gregale_repair_guard; UPDATE null_products SET value='null'::jsonb; ALTER TABLE null_products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 0 || r.Edited != 1 {
		t.Fatalf("verification confused SQL NULL with JSONB null: %+v %v", r, err)
	}
}

func TestRepairNumericScaleRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE scaled_products(id int PRIMARY KEY,value numeric,hidden float8); SELECT gregale_repair.enroll('scaled_products',ARRAY['value']); INSERT INTO scaled_products(id,value,hidden) VALUES(1,1.00,'-0'::float8)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "scaled_products", "UPDATE scaled_products SET value=1.0")
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	e.CheckSQL = "SELECT scale(value)=2 FROM public.scaled_products"
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "scale-repair"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("rehearsed scale repair did not verify: %+v %v", r, err)
	}
}
