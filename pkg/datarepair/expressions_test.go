package datarepair

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSelectiveRepairCustomIndexEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name, index string
		allowed     bool
	}{
		{"custom expression", "CREATE INDEX ON indexed_products(public.index_value(category))", false},
		{"custom predicate", "CREATE INDEX ON indexed_products(id) WHERE public.index_value(category)='original'", false},
		{"builtin expression", "CREATE INDEX ON indexed_products(lower(category))", true},
		{"ordinary index", "CREATE INDEX ON indexed_products(category)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, `CREATE TABLE indexed_products(id bigint PRIMARY KEY,category text);
CREATE FUNCTION public.index_value(v text) RETURNS text LANGUAGE sql IMMUTABLE AS $$SELECT lower(v)$$;`+tc.index); err != nil {
				t.Fatal(err)
			}
			_, err := e.Pool.Exec(ctx, `SELECT gregale_repair.enroll('indexed_products',ARRAY['category'])`)
			if tc.allowed && err != nil || !tc.allowed && (err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported")) {
				t.Fatalf("enrollment: allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
}

func TestSelectiveRepairLegacyCustomIndexBlocked(t *testing.T) {
	for _, index := range []string{
		"CREATE INDEX ON products(public.index_value(category))",
		"CREATE INDEX ON products(id) WHERE public.index_value(category)='original'",
	} {
		t.Run(index, func(t *testing.T) {
			owner, writer, options, _ := captureTestSetup(t)
			owner.CheckSQL = "SELECT 1=1"
			ctx := t.Context()
			var source string
			if err := owner.Pool.QueryRow(ctx, `SELECT id::text FROM gregale_repair.operations LIMIT 1`).Scan(&source); err != nil {
				t.Fatal(err)
			}
			pending := approved(t, owner, options.Scope, source)
			committed := approved(t, owner, options.Scope, source)
			if _, err := owner.Apply(ctx, options.Scope, committed.ID, committed.Digest, uuid.NewString(), "before-index"); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Pool.Exec(ctx, `CREATE FUNCTION public.index_value(v text) RETURNS text LANGUAGE sql IMMUTABLE AS $$SELECT lower(v)$$;`+index+`;
CREATE OR REPLACE FUNCTION public.index_value(v text) RETURNS text LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE EXCEPTION 'application expression executed'; END$$`); err != nil {
				t.Fatal(err)
			}
			// Index expressions were never part of the saved fingerprint. Owner
			// upgrade must block those legacy enrollments without rewriting it.
			before := validationState(t, owner, pending.ID)
			if _, err := owner.Pool.Exec(ctx, UpgradeSchema()); err != nil {
				t.Fatal(err)
			}
			if validationState(t, owner, pending.ID) != before {
				t.Fatal("upgrade rewrote existing evidence")
			}
			owner.CheckSQL = "SELECT 1=1"
			if r, err := owner.Doctor(ctx, options.Scope, options.Table); err != nil || r.Status != "blocked" {
				t.Fatalf("executor accepted a custom index: %+v %v", r, err)
			}
			if r, err := writer.CheckCapture(ctx, options); err != nil || r.Status != "blocked" {
				t.Fatalf("writer accepted a custom index: %+v %v", r, err)
			}
			if r, err := owner.Validate(ctx, options.Scope, pending.ID, pending.Digest); err != nil || r.Matches || !slices.Contains(r.Issues, "schema_changed") || r.Checked != 0 {
				t.Fatalf("freshness accepted a custom index: %+v %v", r, err)
			}
			if r, err := owner.Verify(ctx, options.Scope, committed.ID, committed.Digest); err != nil || r.Available || !slices.Contains(r.Issues, "schema_changed") || r.Checked != 0 {
				t.Fatalf("verification evaluated an unsupported table: %+v %v", r, err)
			}
			if _, err := owner.Plan(ctx, options.Scope, source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("planning accepted a custom index: %v", err)
			}
			if _, err := owner.Replace(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("replacement accepted a custom index: %v", err)
			}
			if _, err := owner.Apply(ctx, options.Scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("apply accepted a custom index: %v", err)
			}
			tx, err := writer.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			called := false
			err = Capture(ctx, tx, options.Scope, uuid.NewString(), options.Release, options.Table, func(pgx.Tx) error { called = true; return nil })
			if !errors.Is(err, ErrInvalid) || called {
				t.Fatalf("unsupported capture reached business code: called=%v err=%v", called, err)
			}
		})
	}
}
