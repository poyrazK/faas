package datarepair

import (
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const customCanonicalRange = `CREATE TYPE public.qualified_range;
CREATE FUNCTION public.qualified_canonical(public.qualified_range) RETURNS public.qualified_range LANGUAGE internal IMMUTABLE STRICT AS 'int4range_canonical';
CREATE TYPE public.qualified_range AS RANGE(subtype=int4,canonical=public.qualified_canonical,multirange_type_name=qualified_multirange);`

func TestRepairRangeQualificationEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name, setup, columns string
		allowed              bool
	}{
		{"builtin", ``, "id int PRIMARY KEY,value int4range", true},
		{"custom without callbacks", `CREATE TYPE qualified_range AS RANGE(subtype=int4);`, "id int PRIMARY KEY,value qualified_range", true},
		{"builtin subdiff", `CREATE TYPE qualified_range AS RANGE(subtype=int4,subtype_diff=pg_catalog.int4range_subdiff);`, "id int PRIMARY KEY,value qualified_range", true},
		{"canonical", customCanonicalRange, "id int PRIMARY KEY,value qualified_range", false},
		{"unselected array", customCanonicalRange, "id int PRIMARY KEY,value text,hidden qualified_range[]", false},
		{"nested multirange", customCanonicalRange + `CREATE TYPE qualified_pair AS (bounds qualified_multirange);`, "id int PRIMARY KEY,value qualified_pair", false},
		{"custom subdiff", `CREATE FUNCTION public.qualified_diff(a int,b int) RETURNS float8 LANGUAGE sql IMMUTABLE AS $$SELECT a::float8-b::float8$$; CREATE TYPE qualified_range AS RANGE(subtype=int4,subtype_diff=public.qualified_diff);`, "id int PRIMARY KEY,value qualified_range", false},
		{"subtype constraint", `CREATE FUNCTION public.qualified_bound_check(v int) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE EXCEPTION 'range callback executed'; END$$; CREATE DOMAIN qualified_bound AS int CHECK(public.qualified_bound_check(VALUE)); CREATE TYPE qualified_range AS RANGE(subtype=qualified_bound);`, "id int PRIMARY KEY,value qualified_range", false},
		{"unindexed subtype class", `CREATE OPERATOR CLASS public.qualified_int_ops FOR TYPE int USING btree AS OPERATOR 1 < (int,int),OPERATOR 2 <= (int,int),OPERATOR 3 = (int,int),OPERATOR 4 >= (int,int),OPERATOR 5 > (int,int),FUNCTION 1 pg_catalog.btint4cmp(int,int); CREATE TYPE qualified_range AS RANGE(subtype=int4,subtype_opclass=public.qualified_int_ops);`, "id int PRIMARY KEY,value qualified_range", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), tc.setup+`CREATE TABLE range_products(`+tc.columns+`)`); err != nil {
				t.Fatal(err)
			}
			_, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('range_products',ARRAY['value'])`)
			if tc.allowed && err != nil || !tc.allowed && (err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported")) {
				t.Fatalf("range allowed=%v: %v", tc.allowed, err)
			}
		})
	}
}

func TestRepairLegacyRangeCallbacksBlockEveryEntryPoint(t *testing.T) {
	e, writer, options, role := captureTestSetup(t)
	e.CheckSQL = "SELECT 1=1"
	// The old expression qualification omitted callbacks invoked by range input.
	old := strings.NewReplacer("__UNSUPPORTED_TARGET_EXPRESSIONS__", "false", "__UNSUPPORTED_ENROLLED_EXPRESSIONS__", "false").Replace(captureContract)
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(old)+customCanonicalRange+`CREATE TABLE range_products(id int PRIMARY KEY,value qualified_range); SELECT gregale_repair.enroll('range_products',ARRAY['value']); INSERT INTO range_products(id,value) VALUES(1,'[1,3)'); GRANT SELECT,UPDATE ON range_products TO `+role); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, options.Scope, "range_products", `UPDATE range_products SET value='[4,6)'`)
	pending := approved(t, e, options.Scope, source)
	committed := approved(t, e, options.Scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(t.Context(), options.Scope, committed.ID, committed.Digest, repairID, "before-callback-change"); err != nil {
		t.Fatal(err)
	}
	var before, after, localBefore, localAfter string
	if err := e.Pool.QueryRow(t.Context(), `SELECT gregale_repair.type_fingerprint('range_products',false),gregale_repair.type_fingerprint('range_products',true)`).Scan(&before, &localBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `CREATE OR REPLACE FUNCTION public.qualified_canonical(public.qualified_range) RETURNS public.qualified_range LANGUAGE plpgsql IMMUTABLE STRICT AS $$BEGIN PERFORM pg_advisory_lock(890251243); RETURN $1; END$$`); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(t.Context(), `SELECT gregale_repair.type_fingerprint('range_products',false),gregale_repair.type_fingerprint('range_products',true)`).Scan(&after, &localAfter); err != nil || before == after || localBefore == localAfter {
		t.Fatalf("callback definition escaped type signatures: %v", err)
	}
	// The owner can refresh a baseline for future captures; qualification must
	// still reject the callback before any saved value is reconstructed.
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	// A fresh backend must reject the new callback before loading its range codec.
	e = renderingEngine(t, e, "ISO, MDY", "postgres")
	frozen := validationState(t, e, pending.ID)
	options.Table = "public.range_products"
	if r, err := e.Doctor(t.Context(), options.Scope, options.Table); err != nil || r.Status != "blocked" {
		t.Fatalf("range doctor: %+v %v", r, err)
	}
	if r, err := writer.CheckCapture(t.Context(), options); err != nil || r.Status != "blocked" {
		t.Fatalf("range writer: %+v %v", r, err)
	}
	if r, err := e.Validate(t.Context(), options.Scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("range validation: %+v %v", r, err)
	}
	if r, err := e.Verify(t.Context(), options.Scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("range verification: %+v %v", r, err)
	}
	if _, err := e.Plan(t.Context(), options.Scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("range planning: %v", err)
	}
	if _, err := e.Replace(t.Context(), options.Scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("range replacement: %v", err)
	}
	if _, err := e.Apply(t.Context(), options.Scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("range apply: %v", err)
	}
	tx, err := writer.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = Capture(t.Context(), tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
	_ = tx.Rollback(t.Context())
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("range capture reached callback: called=%v %v", called, err)
	}
	if receipt, err := e.Apply(t.Context(), options.Scope, committed.ID, committed.Digest, repairID, "receipt-replay"); err != nil || receipt.AppliedAt == nil {
		t.Fatalf("range receipt recovery: %+v %v", receipt, err)
	}
	if validationState(t, e, pending.ID) != frozen {
		t.Fatal("blocked range calls changed frozen evidence")
	}
	var locks int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=890251243 AND granted`).Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("range callback escaped read-only inspection or rollback: %d %v", locks, err)
	}
}

func TestRepairRangeRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TYPE qualified_range AS RANGE(subtype=int4,subtype_diff=pg_catalog.int4range_subdiff); CREATE TABLE range_products(id int PRIMARY KEY,value qualified_range); SELECT gregale_repair.enroll('range_products',ARRAY['value']); INSERT INTO range_products(id,value) VALUES(1,'[1,3)')`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "range_products", `UPDATE range_products SET value='[4,6)'`)
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
	e.CheckSQL = "SELECT lower(value)=1 AND upper(value)=3 FROM public.range_products"
	e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "real-range"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("rehearsed range: %+v %v", r, err)
	}
}
