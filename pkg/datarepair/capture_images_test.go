package datarepair

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func precisionEngine(t *testing.T, e *Engine, digits int) *Engine {
	t.Helper()
	cfg := e.Pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["extra_float_digits"] = strconv.Itoa(digits)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Engine{Pool: pool, CheckSQL: e.CheckSQL, Checks: e.Checks, Rehearser: e.Rehearser}
}

func imageFixture(t *testing.T) (*Engine, Scope, string) {
	t.Helper()
	e, scope, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE image_products("id$1" int PRIMARY KEY,t text,r double precision,document json,tags int[]);
INSERT INTO image_products VALUES(1,'original',1.2345678901234567,'{"value":"\u0000"}','[0:1]={10,20}');
SELECT gregale_repair.enroll('image_products',ARRAY['t','r']);
UPDATE image_products SET t=t`); err != nil {
		t.Fatalf("enrollment broke an ordinary update with valid unselected json: %v", err)
	}
	writer := precisionEngine(t, e, 0)
	source := captureValidation(t, writer, scope, "image_products", "UPDATE image_products SET t='bad',r=2.3456789012345678")
	return e, scope, source
}

func checkImageRestored(t *testing.T, e *Engine, scope Scope, p Plan) {
	t.Helper()
	var restored bool
	if err := e.Pool.QueryRow(t.Context(), `SELECT t='original' AND r=1.2345678901234567::float8 AND array_dims(tags)='[0:1]' AND document::text='{"value":"\u0000"}' AND gregale_row_version=4 FROM image_products`).Scan(&restored); err != nil || !restored {
		t.Fatalf("repair changed a native value or lost float precision: %v %v", restored, err)
	}
	if report, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !report.Available || report.Unchanged != 1 {
		t.Fatalf("exact repair did not verify: %+v %v", report, err)
	}
	var digits string
	if err := e.Pool.QueryRow(t.Context(), `SHOW extra_float_digits`).Scan(&digits); err != nil || digits != "-15" {
		t.Fatalf("function-local precision escaped into the executor session: %s %v", digits, err)
	}
}

func TestRepairNativeImagesAndAliasColumns(t *testing.T) {
	e, scope, source := imageFixture(t)
	ctx := t.Context()
	e = precisionEngine(t, e, -15)
	p, err := e.Plan(ctx, scope, source)
	if err != nil || p.Candidates != 1 || p.Conflicts != 0 || !strings.Contains(string(p.Items[0].Before), "1.2345678901234567") {
		t.Fatalf("alias columns or writer precision changed the capture: %+v %v", p, err)
	}
	for _, digits := range []int{-15, 0, 3} {
		observer := precisionEngine(t, e, digits)
		if report, err := observer.Validate(ctx, scope, p.ID, p.Digest); err != nil || !report.Matches || report.Unchanged != 1 {
			t.Fatalf("observer precision %d changed eligibility: %+v %v", digits, report, err)
		}
	}
	child, err := e.Replace(ctx, scope, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.Candidates != 1 || child.Conflicts != 0 {
		t.Fatalf("replacement could not read alias columns: %+v %v", child, err)
	}
	child, err = e.Rehearse(ctx, scope, child.ID, child.Digest)
	if err != nil {
		t.Fatal(err)
	}
	child, err = e.Approve(ctx, scope, child.ID, child.Digest, "image-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	child, err = e.Apply(ctx, scope, child.ID, child.Digest, uuid.NewString(), "image-repair")
	if err != nil {
		t.Fatal(err)
	}
	checkImageRestored(t, e, scope, child)
}

func TestRepairRejectsUndeclaredNativeChanges(t *testing.T) {
	for _, tc := range []struct{ name, assignment string }{
		{"array bounds", "tags='[1:2]={10,20}'"},
		{"duplicate json keys", `document='{"a":2}'`},
		{"raw json spelling", `document='{"a":1, "a":2}'`},
		{"float precision", "hidden=1.234567890123457"},
		{"quoted identifier", `"name$1"='changed'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, `CREATE TABLE scoped_values(id int PRIMARY KEY,category text,tags int[],document json,hidden double precision,"name$1" text);
INSERT INTO scoped_values VALUES(1,'original','[0:1]={10,20}','{"a":1,"a":2}',1.2345678901234567,'original');
SELECT gregale_repair.enroll('scoped_values',ARRAY['category'])`); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err := e.Pool.QueryRow(ctx, `SELECT ROW(s.*)::text FROM scoped_values s`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			writer := precisionEngine(t, e, 0)
			tx, err := writer.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			err = Capture(ctx, tx, scope, id, "undeclared", "scoped_values", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, "UPDATE scoped_values SET category='bad',"+tc.assignment)
				return err
			})
			var rejected *pgconn.PgError
			if !errors.As(err, &rejected) || rejected.Code != "22023" || rejected.Message != "repair_capture_undeclared_field" {
				_ = tx.Rollback(ctx)
				t.Fatalf("undeclared native change was accepted: %v", err)
			}
			if err := tx.Commit(ctx); err == nil {
				t.Fatal("swallowing the capture error allowed COMMIT")
			}
			if err := e.Pool.QueryRow(ctx, `SELECT ROW(s.*)::text FROM scoped_values s`).Scan(&after); err != nil || after != before {
				t.Fatalf("rejection changed the row: %s %s %v", before, after, err)
			}
			var operations int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM gregale_repair.operations WHERE id=$1`, id).Scan(&operations); err != nil || operations != 0 {
				t.Fatalf("rejection retained a false capture: %d %v", operations, err)
			}
		})
	}
}

func TestRepairNativeImagesRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, source := imageFixture(t)
	cfg := e.Pool.Config().ConnConfig
	dsn := cfg.ConnString()
	if at := strings.LastIndex(dsn, "/"); at >= 0 {
		dsn = dsn[:at+1] + cfg.Database + "?sslmode=disable"
	}
	e = precisionEngine(t, e, -15)
	e.Rehearser = DockerRehearser{SourceURL: dsn, DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	p, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "isolated-image-repair")
	if err != nil {
		t.Fatal(err)
	}
	checkImageRestored(t, e, scope, p)
}
