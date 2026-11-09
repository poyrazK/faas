package datarepair

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Recreate the preceding journal producer in a private database, including its
// missing provenance marker. No production bypass is exposed by the package.
func installLegacyRows(t *testing.T, e *Engine) {
	t.Helper()
	parts := make([]string, 0, 7)
	for _, name := range []string{"fingerprint", "guard_row", "capture_row"} {
		parts = append(parts, strings.Replace(embeddedFunction(schema, name)[0], "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1))
	}
	parts = append(parts, strings.ReplaceAll(captureContract, "__CURRENT_ROW_CONTRACT__", "true"))
	begin := embeddedFunction(retentionContract, "begin_operation")[0]
	begin = strings.Replace(begin, ",gregale_repair.type_fingerprint(e.relation,true),4);", ",gregale_repair.type_fingerprint(e.relation,true),NULL);", 1)
	parts = append(parts, begin)
	for _, name := range []string{"create_plan", "apply_plan"} {
		part := embeddedFunction(retentionContract, name)[0]
		part = strings.Replace(part, "'value_contract_version',2,", "", 1)
		parts = append(parts, strings.NewReplacer("__UNPROVEN_OPERATION__", "false", "__UNPROVEN_PLAN__", "false").Replace(part))
	}
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(strings.Join(parts, "\n"))); err != nil {
		t.Fatal(err)
	}
}

func TestRepairRowUpgradePreservesSafeLegacyApproval(t *testing.T) {
	e, scope, _ := fixture(t, 2)
	ctx := t.Context()
	installLegacyRows(t, e)
	source := captureValidation(t, e, scope, "products", "UPDATE products SET category='original'")
	p := approved(t, e, scope, source)
	before := validationState(t, e, p.ID)
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if after := validationState(t, e, p.ID); after != before {
		t.Fatal("row upgrade changed retained source evidence or approval")
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("safe legacy approval lost its fingerprint: %+v %v", r, err)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "after-row-upgrade"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 2 {
		t.Fatalf("safe legacy correction did not verify: %+v %v", r, err)
	}
}

func TestRepairRowUpgradeDoesNotCertifyUnprovenFloatHistory(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installLegacyRows(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE precision_history(id int PRIMARY KEY,value double precision);
SELECT gregale_repair.enroll('precision_history',ARRAY['value']);
INSERT INTO precision_history(id,value) VALUES(1,1.2345678901234567)`); err != nil {
		t.Fatal(err)
	}
	writer := precisionEngine(t, e, 0)
	source := captureValidation(t, writer, scope, "precision_history", "UPDATE precision_history SET value=2")
	pending := approved(t, e, scope, source)
	committed := approved(t, e, scope, source)
	var err error
	committed, err = e.Apply(ctx, scope, committed.ID, committed.Digest, uuid.NewString(), "legacy-rounded-repair")
	if err != nil {
		t.Fatal(err)
	}
	var unproven, rounded bool
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT capture_contract_version IS NULL FROM gregale_repair.operations WHERE id=$1),value<>1.2345678901234567::float8 FROM precision_history`, source).Scan(&unproven, &rounded); err != nil || !unproven || !rounded {
		t.Fatalf("fixture did not reproduce the legacy loss: %v %v %v", unproven, rounded, err)
	}
	before := validationState(t, e, pending.ID)
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, e, pending.ID) != before {
		t.Fatal("upgrade synthesized proof or rewrote historical evidence")
	}
	if _, err := e.Plan(ctx, scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new plan trusted unproven float history: %v", err)
	}
	if _, err := e.Replace(ctx, scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement trusted unproven float history: %v", err)
	}
	if _, err := e.Apply(ctx, scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply trusted unproven float history: %v", err)
	}
	if r, err := e.Validate(ctx, scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("validation certified legacy float capture: %+v %v", r, err)
	}
	if r, err := e.Verify(ctx, scope, committed.ID, committed.Digest); err != nil || r.Available || r.Unchanged != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("verification certified legacy rounded restoration: %+v %v", r, err)
	}
	if receipt, err := e.Apply(ctx, scope, committed.ID, committed.Digest, *committed.RepairOperationID, "receipt-recovery"); err != nil || receipt.AppliedAt == nil || receipt.Digest != committed.Digest {
		t.Fatalf("read-only historical receipt recovery was lost: %+v %v", receipt, err)
	}
}

func TestRepairRowContractRejectsDowngradeAndCustomUpgrade(t *testing.T) {
	for _, mode := range []string{"legacy downgrade", "custom implementation", "changed settings"} {
		t.Run(mode, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			var statement string
			switch mode {
			case "legacy downgrade":
				statement = strings.Replace(schemaLimits().Replace(embeddedFunction(schema, "guard_row")[0]), "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
			case "custom implementation":
				statement = `CREATE OR REPLACE FUNCTION gregale_repair.guard_row() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$BEGIN RETURN NEW; END$$`
			case "changed settings":
				statement = `ALTER FUNCTION gregale_repair.guard_row() SET extra_float_digits='0'`
			}
			if _, err := e.Pool.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = Capture(ctx, tx, scope, uuid.NewString(), "changed-contract", "products", func(pgx.Tx) error { called = true; return nil })
			_ = tx.Rollback(ctx)
			if !errors.Is(err, ErrInvalid) || called {
				t.Fatalf("changed trigger could certify new capture: called=%v err=%v", called, err)
			}
			if mode == "legacy downgrade" {
				// This exact old version is deliberately upgradeable.
				if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
					t.Fatal(err)
				}
				return
			}
			conn, err := e.Pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			var before, after string
			if err := conn.QueryRow(ctx, `SELECT pg_get_functiondef('gregale_repair.guard_row()'::regprocedure)`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, UpgradeSchema()); err == nil || !strings.Contains(err.Error(), "repair_row_contract_upgrade_requires_review") {
				t.Fatalf("upgrade silently replaced an unknown contract: %v", err)
			}
			if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRow(ctx, `SELECT pg_get_functiondef('gregale_repair.guard_row()'::regprocedure)`).Scan(&after); err != nil || after != before {
				t.Fatalf("failed upgrade changed the installed function: %v", err)
			}
		})
	}
}
