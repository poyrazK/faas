package datarepair

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRepairRejectsLossyValueTypes(t *testing.T) {
	for _, tc := range []struct {
		name, setup, columns string
		allowed              bool
	}{
		{"plain json", "", "id int PRIMARY KEY,value json", false},
		{"array", "", "id int PRIMARY KEY,value int[]", false},
		{"timestamp array", "", "id int PRIMARY KEY,value timestamptz[]", false},
		{"domain", "CREATE DOMAIN array_value AS int[];", "id int PRIMARY KEY,value array_value", false},
		{"nested composite", "CREATE TYPE json_value AS (payload json); CREATE TYPE wrapped_value AS (nested json_value);", "id int PRIMARY KEY,value wrapped_value", false},
		{"array key", "", "id int[] PRIMARY KEY,value text", false},
		{"jsonb", "", "id int PRIMARY KEY,value jsonb", true},
		{"scalar domain", "CREATE DOMAIN scalar_value AS int NOT NULL CHECK(VALUE>0);", "id int PRIMARY KEY,value scalar_value", true},
		{"unselected lossy values", "", "id int PRIMARY KEY,value text,notes json,tags int[]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), tc.setup+"CREATE TABLE typed_products("+tc.columns+")"); err != nil {
				t.Fatal(err)
			}
			_, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('typed_products',ARRAY['value'])`)
			if tc.allowed && err != nil || !tc.allowed && (err == nil || !strings.Contains(err.Error(), "repair_lossy_field_types_unsupported")) {
				t.Fatalf("enrollment allowed=%v: %v", tc.allowed, err)
			}
		})
	}
}

// ADR-20261103205518: the old producer must not acquire new capture proof.
func TestRepairLegacyLossyEvidenceCannotAuthorizeWrites(t *testing.T) {
	for _, tc := range []struct{ name, datatype, before, after string }{
		{"array bounds", "int[]", "'[0:1]={10,20}'::int[]", "'[0:1]={30,40}'::int[]"},
		{"json duplicate keys", "json", `'{"a":1,"a":2}'::json`, `'{"a":3,"a":4}'::json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, writer, options, role := captureTestSetup(t)
			e.CheckSQL = "SELECT 1=1"
			ctx := t.Context()
			// Recreate the older producer and JSONB comparisons, which admitted
			// images that the current round-trip guard correctly rejects.
			legacy := schemaLimits().Replace(rowContractV1) + "\n" + strings.NewReplacer("__CURRENT_ROW_CONTRACT__", "true", "__LOSSY_TARGET_TYPES__", "false", "__LOSSY_ENROLLED_TYPES__", "false").Replace(captureContract)
			legacy += "\n" + strings.Replace(embeddedFunction(retentionContract, "begin_operation")[0], ",gregale_repair.type_fingerprint(e.relation,true),3);", ",gregale_repair.type_fingerprint(e.relation,true),1);", 1)
			legacy += `
CREATE OR REPLACE FUNCTION gregale_repair.values_match(observed anyelement, recorded jsonb) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog SET extra_float_digits = '3' AS $$SELECT to_jsonb(observed) @> recorded$$;`
			if _, err := e.Pool.Exec(ctx, schemaBounds().Replace(legacy)); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, "CREATE TABLE typed_products(id int PRIMARY KEY,value "+tc.datatype+"); SELECT gregale_repair.enroll('typed_products',ARRAY['value']); INSERT INTO typed_products(id,value) VALUES(1,"+tc.before+"); GRANT SELECT,UPDATE ON typed_products TO "+role); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, options.Scope, "typed_products", "UPDATE typed_products SET value="+tc.after)
			pending := approved(t, e, options.Scope, source)
			committed := approved(t, e, options.Scope, source)
			if _, err := e.Apply(ctx, options.Scope, committed.ID, committed.Digest, uuid.NewString(), "legacy-repair"); err != nil {
				t.Fatal(err)
			}
			before := validationState(t, e, pending.ID)
			for range 2 {
				if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
					t.Fatal(err)
				}
			}
			if validationState(t, e, pending.ID) != before {
				t.Fatal("upgrade rewrote frozen evidence or approvals")
			}
			options.Table = "public.typed_products"
			if r, err := e.Doctor(ctx, options.Scope, options.Table); err != nil || r.Status != "blocked" {
				t.Fatalf("executor trusted lossy evidence: %+v %v", r, err)
			}
			if r, err := writer.CheckCapture(ctx, options); err != nil || r.Status != "blocked" {
				t.Fatalf("writer trusted lossy enrollment: %+v %v", r, err)
			}
			if r, err := e.Validate(ctx, options.Scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("freshness trusted lossy evidence: %+v %v", r, err)
			}
			if r, err := e.Verify(ctx, options.Scope, committed.ID, committed.Digest); err != nil || r.Available || r.Unchanged != 0 || !slices.Contains(r.Issues, "schema_changed") {
				t.Fatalf("verification claimed lossy restoration: %+v %v", r, err)
			}
			if _, err := e.Plan(ctx, options.Scope, source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("new plan trusted lossy capture: %v", err)
			}
			if _, err := e.Replace(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("replacement trusted lossy capture: %v", err)
			}
			if _, err := e.Apply(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("apply trusted lossy capture: %v", err)
			}
			tx, err := writer.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			called := false
			err = Capture(ctx, tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
			if !errors.Is(err, ErrInvalid) || called {
				t.Fatalf("capture reached callback: called=%v err=%v", called, err)
			}
		})
	}
}

func TestRepairPreservesUnselectedDomainsAndLossyValues(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE DOMAIN positive_stock AS int NOT NULL CHECK(VALUE>0);
CREATE DOMAIN category_value AS text NOT NULL CHECK(VALUE IN ('original','bad'));
CREATE TABLE domain_products(id int PRIMARY KEY,category category_value,stock positive_stock,tags int[],document json);
SELECT gregale_repair.enroll('domain_products',ARRAY['category']);
INSERT INTO domain_products(id,category,stock,tags,document) VALUES(1,'original',10,'[0:1]={10,20}','{"a":1,"a":2}')`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "domain_products", "UPDATE domain_products SET category='bad'")
	p := approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "domain-repair"); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err := e.Pool.QueryRow(ctx, `SELECT category='original' AND stock=10 AND array_dims(tags)='[0:1]' AND tags[0]=10 AND (SELECT count(*) FROM json_each(document))=2 AND gregale_row_version=3 FROM domain_products`).Scan(&restored); err != nil || !restored {
		t.Fatalf("repair changed an unrelated native value: %v %v", restored, err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("domain repair did not verify: %+v %v", r, err)
	}
}
