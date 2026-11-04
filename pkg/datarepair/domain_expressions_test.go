package datarepair

// ADR-20261103201136: inspect domain dependencies before typed observations.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRepairDomainExpressionEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name, definition, fields string
		allowed                  bool
	}{
		{"builtin", "value public.builtin_value", "value", true},
		{"direct", "value public.custom_value", "value", false},
		{"nested domain", "value public.outer_value", "value", false},
		{"composite", "value public.wrapped_value", "value", false},
		{"unselected array", "value text,extra public.custom_value[]", "value", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			_, err := e.Pool.Exec(t.Context(), `CREATE FUNCTION public.domain_allowed(v int) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN RAISE EXCEPTION 'application expression executed'; END$$;
CREATE DOMAIN public.builtin_value AS int CHECK(VALUE>0);
CREATE DOMAIN public.custom_value AS int CHECK(public.domain_allowed(VALUE));
CREATE DOMAIN public.outer_value AS public.custom_value;
CREATE TYPE public.wrapped_value AS (nested public.outer_value);
CREATE TABLE domain_products(id int PRIMARY KEY,`+tc.definition+`)`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('domain_products',ARRAY[$1])`, tc.fields)
			if tc.allowed && err != nil || !tc.allowed && (err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported")) {
				t.Fatalf("domain qualification allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
}

func TestRepairLegacyDomainExpressionsBlockedBeforeEvaluation(t *testing.T) {
	testLegacyDomainExpressionBlocked(t, false)
}

func TestRepairLegacyOperatorExpressionsBlockedBeforeEvaluation(t *testing.T) {
	testLegacyDomainExpressionBlocked(t, true)
}

func testLegacyDomainExpressionBlocked(t *testing.T, operator bool) {
	t.Helper()
	e, writer, options, role := captureTestSetup(t)
	e.CheckSQL = "SELECT 1=1"
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE DOMAIN public.checked_value AS int CHECK(VALUE>0);
CREATE TABLE domain_products(id int PRIMARY KEY,value public.checked_value);
SELECT gregale_repair.enroll('domain_products',ARRAY['value']);
INSERT INTO domain_products(id,value) VALUES(1,10);
GRANT SELECT,UPDATE ON domain_products TO `+role); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, options.Scope, "domain_products", "UPDATE domain_products SET value=20")
	pending := approved(t, e, options.Scope, source)
	committed := approved(t, e, options.Scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, options.Scope, committed.ID, committed.Digest, repairID, "before-domain-change"); err != nil {
		t.Fatal(err)
	}
	// NOT VALID adds catalog evidence without evaluating existing values. An
	// earlier qualifier would accept this domain and execute the function when
	// values_match reconstructs a selected value during observation.
	definition := `CREATE FUNCTION public.domain_allowed(v int) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN RAISE EXCEPTION 'application expression executed'; END$$;`
	expression := "public.domain_allowed(VALUE)"
	if operator {
		definition = `CREATE FUNCTION public.operator_allowed(a int,b int) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE EXCEPTION 'application expression executed'; END$$;
CREATE OPERATOR public.## (LEFTARG=int,RIGHTARG=int,FUNCTION=public.operator_allowed);`
		expression = "VALUE OPERATOR(public.##) 0"
	}
	if _, err := e.Pool.Exec(ctx, definition+`ALTER DOMAIN public.checked_value ADD CONSTRAINT custom_check CHECK(`+expression+`) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	before := validationState(t, e, pending.ID)
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Doctor(ctx, options.Scope, "public.domain_products"); err != nil || r.Status != "blocked" {
		t.Fatalf("doctor accepted a custom domain: %+v %v", r, err)
	}
	options.Table = "public.domain_products"
	if r, err := writer.CheckCapture(ctx, options); err != nil || r.Status != "blocked" {
		t.Fatalf("writer accepted a custom domain: %+v %v", r, err)
	}
	if r, err := e.Validate(ctx, options.Scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("validation evaluated a custom domain: %+v %v", r, err)
	}
	if r, err := e.Verify(ctx, options.Scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("verification evaluated a custom domain: %+v %v", r, err)
	}
	if _, err := e.Plan(ctx, options.Scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("planning accepted a custom domain: %v", err)
	}
	if _, err := e.Replace(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement accepted a custom domain: %v", err)
	}
	if _, err := e.Apply(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply accepted a custom domain: %v", err)
	}
	tx, err := writer.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = Capture(ctx, tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
	_ = tx.Rollback(ctx)
	if !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("unsupported capture reached business code: called=%v err=%v", called, err)
	}
	if validationState(t, e, pending.ID) != before {
		t.Fatal("blocked domain qualification changed saved evidence")
	}
	if _, err := e.Apply(ctx, options.Scope, committed.ID, committed.Digest, repairID, "receipt-recovery"); err != nil {
		t.Fatal(err)
	}
}
