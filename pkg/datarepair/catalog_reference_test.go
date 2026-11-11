package datarepair

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRepairRejectsCatalogReferenceTypes(t *testing.T) {
	e, _, _ := fixture(t, 1)
	for _, datatype := range []string{"regclass", "regcollation", "regconfig", "regdictionary", "regnamespace", "regoper", "regoperator", "regproc", "regprocedure", "regrole", "regtype"} {
		for _, key := range []bool{false, true} {
			name := datatype + "_field"
			columns := "id int PRIMARY KEY,value " + datatype
			if key {
				name = datatype + "_key"
				columns = "id " + datatype + " PRIMARY KEY,value text"
			}
			t.Run(name, func(t *testing.T) {
				table := "catalog_" + name
				if _, err := e.Pool.Exec(t.Context(), "CREATE TABLE "+table+"("+columns+")"); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(t.Context(), "SELECT gregale_repair.enroll('"+table+"',ARRAY['value'])"); err == nil || !strings.Contains(err.Error(), "repair_lossy_field_types_unsupported") {
					t.Fatalf("catalog reference enrollment accepted: %v", err)
				}
			})
		}
	}
	for _, tc := range []struct{ name, setup, datatype string }{
		{"domain", "CREATE DOMAIN catalog_domain AS regclass;", "catalog_domain"},
		{"nested_composite", "CREATE TYPE catalog_member AS (reference regclass); CREATE TYPE catalog_wrapper AS (member catalog_member);", "catalog_wrapper"},
		{"range", "CREATE TYPE catalog_range AS RANGE(subtype=regclass,multirange_type_name=catalog_multirange);", "catalog_range"},
		{"multirange", "CREATE TYPE catalog_multi_range AS RANGE(subtype=regclass,multirange_type_name=catalog_multi_value);", "catalog_multi_value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := "catalog_nested_" + tc.name
			if _, err := e.Pool.Exec(t.Context(), tc.setup+"CREATE TABLE "+table+"(id int PRIMARY KEY,value "+tc.datatype+")"); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(t.Context(), "SELECT gregale_repair.enroll('"+table+"',ARRAY['value'])"); err == nil || !strings.Contains(err.Error(), "repair_lossy_field_types_unsupported") {
				t.Fatalf("nested catalog reference enrollment accepted: %v", err)
			}
		})
	}
}

func TestRepairCatalogReferenceUpgradeBlocksRetargeting(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "pending"
		if committed {
			name = "committed"
		}
		t.Run(name, func(t *testing.T) {
			e, writer, options, role := captureTestSetup(t)
			e.CheckSQL = "SELECT 1=1"
			// Model the preceding qualification policy without changing the journal
			// producer, fingerprints, or any historical proof/version marker.
			legacy := strings.NewReplacer("__LOSSY_TARGET_TYPES__", "false", "__LOSSY_ENROLLED_TYPES__", "false").Replace(captureContract)
			if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(legacy)); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE referenced_original(id int); CREATE TABLE referenced_after(id int);
CREATE TABLE reference_rows(id int PRIMARY KEY,value regclass);
SELECT gregale_repair.enroll('reference_rows',ARRAY['value']);
INSERT INTO reference_rows(id,value) VALUES(1,'public.referenced_original'); GRANT SELECT,UPDATE ON reference_rows TO `+role); err != nil {
				t.Fatal(err)
			}
			var original uint32
			if err := e.Pool.QueryRow(t.Context(), `SELECT value::oid FROM reference_rows`).Scan(&original); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, options.Scope, "reference_rows", `UPDATE reference_rows SET value='public.referenced_after'`)
			p := approved(t, e, options.Scope, source)
			if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE referenced_original RENAME TO referenced_renamed; CREATE TABLE referenced_original(id int)`); err != nil {
				t.Fatal(err)
			}
			repairID := uuid.NewString()
			if committed {
				var err error
				p, err = e.Apply(t.Context(), options.Scope, p.ID, p.Digest, repairID, "old-reference-repair")
				if err != nil {
					t.Fatal(err)
				}
				var actual uint32
				if err := e.Pool.QueryRow(t.Context(), `SELECT value::oid FROM reference_rows`).Scan(&actual); err != nil || actual == original {
					t.Fatalf("fixture did not reproduce name retargeting: %d/%d %v", actual, original, err)
				}
			}
			before := validationState(t, e, p.ID)
			for range 2 {
				if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
					t.Fatal(err)
				}
			}
			if validationState(t, e, p.ID) != before {
				t.Fatal("upgrade rewrote historical evidence or approval")
			}
			options.Table = "public.reference_rows"
			if r, err := e.Doctor(t.Context(), options.Scope, options.Table); err != nil || r.Status != "blocked" {
				t.Fatalf("executor accepted catalog references: %+v %v", r, err)
			}
			if r, err := writer.CheckCapture(t.Context(), options); err != nil || r.Status != "blocked" {
				t.Fatalf("writer accepted catalog references: %+v %v", r, err)
			}
			tx, err := writer.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = Capture(t.Context(), tx, options.Scope, uuid.NewString(), "must-block", options.Table, func(pgx.Tx) error { called = true; return nil })
			_ = tx.Rollback(t.Context())
			if !errors.Is(err, ErrInvalid) || called {
				t.Fatalf("capture reached callback: called=%v %v", called, err)
			}
			if _, err := e.Plan(t.Context(), options.Scope, source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("new selection trusted catalog names: %v", err)
			}
			if committed {
				if r, err := e.Verify(t.Context(), options.Scope, p.ID, p.Digest); err != nil || r.Available || r.Unchanged != 0 || !slices.Contains(r.Issues, "schema_changed") {
					t.Fatalf("verification certified retargeted reference: %+v %v", r, err)
				}
				receipt, err := e.Apply(t.Context(), options.Scope, p.ID, p.Digest, repairID, "receipt-replay")
				if err != nil || receipt.AppliedAt == nil || receipt.Digest != p.Digest {
					t.Fatalf("receipt recovery was blocked: %+v %v", receipt, err)
				}
			} else {
				if r, err := e.Validate(t.Context(), options.Scope, p.ID, p.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
					t.Fatalf("validation trusted catalog names: %+v %v", r, err)
				}
				if _, err := e.Replace(t.Context(), options.Scope, p.ID, p.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
					t.Fatalf("replacement trusted catalog names: %v", err)
				}
				if _, err := e.Apply(t.Context(), options.Scope, p.ID, p.Digest, repairID, "must-block"); !errors.Is(err, ErrInvalid) {
					t.Fatalf("apply trusted catalog names: %v", err)
				}
			}
			if validationState(t, e, p.ID) != before {
				t.Fatal("blocked actions or replay changed historical evidence")
			}
		})
	}
}

func TestRepairPreservesUnselectedCatalogReferences(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE referenced_original(id int);
CREATE TABLE reference_rows(id int PRIMARY KEY,category text,reference regclass);
SELECT gregale_repair.enroll('reference_rows',ARRAY['category']);
INSERT INTO reference_rows(id,category,reference) VALUES(1,'original','public.referenced_original')`); err != nil {
		t.Fatal(err)
	}
	var original uint32
	if err := e.Pool.QueryRow(t.Context(), `SELECT reference::oid FROM reference_rows`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "reference_rows", `UPDATE reference_rows SET category='bad'`)
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE referenced_original RENAME TO referenced_renamed; CREATE TABLE referenced_original(id int)`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "unselected-reference"); err != nil {
		t.Fatal(err)
	}
	var actual uint32
	var category string
	if err := e.Pool.QueryRow(t.Context(), `SELECT reference::oid,category FROM reference_rows`).Scan(&actual, &category); err != nil || actual != original || category != "original" {
		t.Fatalf("unselected native reference changed: oid=%d/%d category=%s %v", actual, original, category, err)
	}
	if r, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("safe text repair failed verification: %+v %v", r, err)
	}
}
