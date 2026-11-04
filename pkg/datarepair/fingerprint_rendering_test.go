package datarepair

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const renderingDefaultsTable = `CREATE TABLE render_products(id int PRIMARY KEY,value text,
 stamp date DEFAULT DATE '2026-11-03',duration interval DEFAULT '-1 days -02:00'::interval);
SELECT gregale_repair.enroll('render_products',ARRAY['value']); INSERT INTO render_products(id,value) VALUES(1,'original')`

func TestRepairFingerprintRenderingAcrossConnections(t *testing.T) {
	e, writer, options, role := captureTestSetup(t)
	e.CheckSQL = "SELECT 1=1"
	sourceWriter := renderingEngine(t, e, "SQL, DMY", "sql_standard")
	if _, err := sourceWriter.Pool.Exec(t.Context(), renderingDefaultsTable+`; GRANT SELECT,UPDATE ON render_products TO `+role); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, sourceWriter, options.Scope, "render_products", `UPDATE render_products SET value='bad'`)
	p := approved(t, e, options.Scope, source)
	for _, tc := range []struct{ date, interval string }{
		{"ISO, MDY", "postgres"}, {"SQL, DMY", "sql_standard"}, {"German, DMY", "postgres_verbose"}, {"Postgres, MDY", "iso_8601"},
	} {
		observer := renderingEngine(t, e, tc.date, tc.interval)
		writerObserver := renderingEngine(t, writer, tc.date, tc.interval)
		options.Table = "public.render_products"
		if r, err := observer.Doctor(t.Context(), options.Scope, options.Table); err != nil || r.Status != "ready" {
			t.Fatalf("%s doctor: %+v %v", tc.date, r, err)
		}
		if r, err := writerObserver.CheckCapture(t.Context(), options); err != nil || r.Status != "ready" {
			t.Fatalf("%s writer: %+v %v", tc.date, r, err)
		}
		if r, err := observer.Validate(t.Context(), options.Scope, p.ID, p.Digest); err != nil || !r.Matches {
			t.Fatalf("%s validation: %+v %v", tc.date, r, err)
		}
		assertRepairRendering(t, observer, tc.date, tc.interval)
		assertRepairRendering(t, writerObserver, tc.date, tc.interval)
	}
	executor := renderingEngine(t, e, "SQL, DMY", "sql_standard")
	if _, err := executor.Apply(t.Context(), options.Scope, p.ID, p.Digest, uuid.NewString(), "render-independent"); err != nil {
		t.Fatal(err)
	}
	if r, err := executor.Verify(t.Context(), options.Scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("cross-style receipt: %+v %v", r, err)
	}
	assertRepairRendering(t, executor, "SQL, DMY", "sql_standard")
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE render_products ALTER COLUMN stamp SET DEFAULT DATE '2026-11-04'`); err != nil {
		t.Fatal(err)
	}
	if r, err := executor.Doctor(t.Context(), options.Scope, options.Table); err != nil || r.Status != "blocked" {
		t.Fatalf("real default drift accepted: %+v %v", r, err)
	}
}

func TestRepairFingerprintRenderingUpgradeRequiresMatchingProof(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	e.CheckSQL = "SELECT 1=1"
	installV2RenderingContract(t, e)
	legacy := renderingEngine(t, e, "SQL, DMY", "sql_standard")
	if _, err := legacy.Pool.Exec(t.Context(), renderingDefaultsTable); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, legacy, scope, "render_products", `UPDATE render_products SET value='bad'`)
	p := approved(t, legacy, scope, source)
	before := validationState(t, e, p.ID)
	var fingerprint string
	if err := legacy.Pool.QueryRow(t.Context(), `SELECT fingerprint FROM gregale_repair.enrollments WHERE relation='render_products'::regclass`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE gregale_repair.enrollments DROP COLUMN fingerprint_datestyle,DROP COLUMN fingerprint_intervalstyle`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	var unknown bool
	if err := e.Pool.QueryRow(t.Context(), `SELECT fingerprint_datestyle IS NULL AND fingerprint_intervalstyle IS NULL FROM gregale_repair.enrollments WHERE relation='render_products'::regclass`).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("unmatched rendering was certified: %v %v", unknown, err)
	}
	if _, err := e.Plan(t.Context(), scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown rendering authorized a plan: %v", err)
	}
	if r, err := e.Doctor(t.Context(), scope, "public.render_products"); err != nil || r.Status != "blocked" {
		t.Fatalf("unknown rendering reported ready: %+v %v", r, err)
	}
	for range 2 {
		if _, err := legacy.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("rendering upgrade rewrote evidence or approval")
	}
	var saved, dateStyle, intervalStyle string
	if err := e.Pool.QueryRow(t.Context(), `SELECT fingerprint,fingerprint_datestyle,fingerprint_intervalstyle FROM gregale_repair.enrollments WHERE relation='render_products'::regclass`).Scan(&saved, &dateStyle, &intervalStyle); err != nil || saved != fingerprint || dateStyle != "SQL, DMY" || intervalStyle != "sql_standard" {
		t.Fatalf("wrong legacy proof: %q %q %v", dateStyle, intervalStyle, err)
	}
	if r, err := e.Doctor(t.Context(), scope, "public.render_products"); err != nil || r.Status != "ready" {
		t.Fatalf("proven legacy rendering blocked: %+v %v", r, err)
	}
	if r, err := e.Validate(t.Context(), scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("legacy approval was invalidated: %+v %v", r, err)
	}
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "preserved-rendering"); err != nil {
		t.Fatal(err)
	}
	assertRepairRendering(t, legacy, "SQL, DMY", "sql_standard")
}

func TestRepairRenderingUpgradeRejectsUnreviewedSettings(t *testing.T) {
	e, _, _ := fixture(t, 1)
	definition := embeddedFunction(rowContractSchema(), "fingerprint")[0]
	definition = strings.Replace(definition, "SET IntervalStyle = 'postgres'", "SET IntervalStyle = 'sql_standard'", 1)
	if _, err := e.Pool.Exec(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err == nil || !strings.Contains(err.Error(), "repair_row_contract_upgrade_requires_review") {
		t.Fatalf("unreviewed rendering settings upgraded: %v", err)
	}
}
