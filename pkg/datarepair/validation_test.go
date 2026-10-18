package datarepair

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRepairValidationPreservesApprovalAndDetectsLaterCandidateDrift(t *testing.T) {
	e, scope, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, scope, source)
	first, err := e.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || !first.Valid() || !first.Matches || first.Candidates != 460 || first.Excluded != 40 || first.Checked != 460 || first.Unchanged != 460 {
		t.Fatalf("fresh approved plan: %+v %v", first, err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41; UPDATE products SET category='bad' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	before := validationState(t, e, p.ID)
	r, err := e.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Matches || r.Checked != 460 || r.Unchanged != 459 || r.Changed != 1 || r.Excluded != 40 || len(r.Issues) != 1 || r.Issues[0] != "candidate_drift" {
		t.Fatalf("later edit was not diagnosed: %+v %v", r, err)
	}
	if len(r.Rows) != 1 || r.Rows[0].KeyJSON != `{"id": 41}` || r.Rows[0].ExpectedVersion != "2" || r.Rows[0].ObservedVersion != "3" || r.Rows[0].Reason != ReasonVersionChanged {
		t.Fatalf("wrong changed candidate: %+v", r.Rows)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("validation wrote business data, journal, plan, or approval")
	}
	frozen, err := e.Get(ctx, scope, p.ID)
	if err != nil || frozen.Digest != p.Digest || frozen.ApprovedAt == nil || !frozen.ApprovedAt.Equal(*p.ApprovedAt) {
		t.Fatal("validation changed the original approval", err)
	}
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, repairID, "repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("a validation result bypassed the live guards", err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("stale apply committed any correction or receipt")
	}
	other := scope
	other.AppID = uuid.NewString()
	if _, err := e.Validate(ctx, other, p.ID, p.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatal("validation crossed scope", err)
	}
	if _, err := e.Validate(ctx, scope, p.ID, strings.Repeat("0", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("validation accepted a different digest", err)
	}
}

func validationState(t *testing.T, e *Engine, id string) string {
	t.Helper()
	var raw []byte
	err := e.Pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'plan',(SELECT to_jsonb(p) FROM gregale_repair.plans p WHERE id=$1),
 'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM gregale_repair.operations o),
 'changes',(SELECT jsonb_agg(to_jsonb(c) ORDER BY operation_id,row_identity) FROM gregale_repair.changes c),
 'products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p))`, id).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRepairValidationReportsReasonsWithoutCurrentValues(t *testing.T) {
	e, scope, source := fixture(t, 5)
	p := approved(t, e, scope, source)
	if _, err := e.Pool.Exec(t.Context(), `
UPDATE products SET category='human' WHERE id=1;
DELETE FROM products WHERE id=2;
INSERT INTO products(id,category) VALUES(2,'human');
BEGIN;
ALTER TABLE products DISABLE TRIGGER gregale_repair_guard;
UPDATE products SET category='human' WHERE id=3;
ALTER TABLE products ENABLE TRIGGER gregale_repair_guard;
COMMIT;
UPDATE products SET quantity=2 WHERE id=4;`); err != nil {
		t.Fatal(err)
	}
	r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Checked != 5 || r.Unchanged != 1 || r.Changed != 4 {
		t.Fatalf("candidate reasons: %+v %v", r, err)
	}
	want := map[string]string{`{"id": 1}`: ReasonVersionChanged, `{"id": 2}`: ReasonRecordMissing, `{"id": 3}`: ReasonValuesChanged, `{"id": 4}`: ReasonVersionChanged}
	for _, row := range r.Rows {
		if want[row.KeyJSON] != row.Reason {
			t.Fatalf("incorrect drift reason: %+v", row)
		}
		delete(want, row.KeyJSON)
	}
	if len(want) != 0 {
		t.Fatal("a changed original candidate was omitted")
	}
	raw, _ := json.Marshal(r)
	for _, value := range []string{"human", "quantity", "category", "SELECT"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("validation exposed SQL or business values", value)
		}
	}
}

func TestRepairValidationIsNotRehearsalAndDoesNotExecuteChecks(t *testing.T) {
	e, scope, source := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE SEQUENCE repair_validation_probe`); err != nil {
		t.Fatal(err)
	}
	e.Checks = []Check{{Name: "Unexecuted assertion", SQL: "SELECT nextval('public.repair_validation_probe')>0"}}
	p, err := e.Plan(t.Context(), scope, source)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Matches {
		t.Fatal("unapproved plan could not be inspected", err)
	}
	var executed bool
	if err := e.Pool.QueryRow(t.Context(), `SELECT is_called FROM repair_validation_probe`).Scan(&executed); err != nil || executed {
		t.Fatal("validation executed a business assertion", err)
	}
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("a matching diagnostic enabled approval", err)
	}
	e.Checks[0].Name = "Changed assertion"
	r, err = e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || r.Matches || len(r.Issues) != 1 || r.Issues[0] != "checks_changed" {
		t.Fatalf("changed policy was not reported: %+v %v", r, err)
	}
}

func TestRepairValidationReportsSchemaAndExpiryWithoutRefreshingPlan(t *testing.T) {
	for _, ddl := range []string{`ALTER TABLE products ADD COLUMN private_note text`, `ALTER TABLE products ENABLE ROW LEVEL SECURITY`, `DROP TABLE products`} {
		t.Run(ddl, func(t *testing.T) {
			e, scope, source := fixture(t, 1)
			p := approved(t, e, scope, source)
			if _, err := e.Pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
			if err != nil || !r.Valid() || r.Matches || r.Checked != 0 || len(r.Issues) != 1 || r.Issues[0] != "schema_changed" {
				t.Fatalf("schema drift did not stop row inspection: %+v %v", r, err)
			}
		})
	}
	e, scope, source := fixture(t, 1)
	p := approved(t, e, scope, source)
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Matches || r.Checked != 1 || len(r.Issues) != 1 || r.Issues[0] != "plan_expired" {
		t.Fatalf("expired plan: %+v %v", r, err)
	}
}

func TestRepairValidationReportsAppliedAndPurgedReceipts(t *testing.T) {
	e, scope, source := fixture(t, 1)
	p := approved(t, e, scope, source)
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "repair"); err != nil {
		t.Fatal(err)
	}
	r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Matches || r.Checked != 0 || len(r.Issues) != 1 || r.Issues[0] != "plan_applied" {
		t.Fatalf("committed repair was reclassified as fresh drift: %+v %v", r, err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '10 days',expires_at=clock_timestamp()-interval '9 days',applied_at=clock_timestamp()-interval '8 days' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	batch, err := e.PlanRetention(t.Context(), scope, time.Time{}, api.DataRepairPurgeMaxItems)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PurgeRetention(t.Context(), scope, batch.ID, batch.Digest, "maintenance"); err != nil {
		t.Fatal(err)
	}
	r, err = e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Matches || r.Checked != 0 || len(r.Issues) != 3 {
		t.Fatalf("purged receipt validation lost its diagnostic identity: %+v %v", r, err)
	}
}

func TestRepairValidationBoundsKeysAndPreservesNumericPrecision(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE validation_keys(id text PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('validation_keys',ARRAY['category']);
INSERT INTO validation_keys SELECT n::text||repeat('x',600),'original' FROM generate_series(1,1000) n;`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "validation_keys", `UPDATE validation_keys SET category='bad'`)
	p, err := e.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE validation_keys SET category='human'`); err != nil {
		t.Fatal(err)
	}
	r, err := e.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Changed != api.DataRepairMaxRows {
		t.Fatalf("bounded drift report: %+v %v", r, err)
	}
	keys, omitted := 0, 0
	for _, row := range r.Rows {
		keys += len(row.KeyJSON)
		if row.KeyOmitted {
			omitted++
		}
	}
	if keys > api.DataRepairMaxObservedPlanBytes || omitted == 0 || omitted == len(r.Rows) {
		t.Fatal("identifier budget lost or exceeded", keys, omitted)
	}
	e, scope, _ = fixture(t, 1)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO products(id,category) VALUES(9007199254740993,'original')`); err != nil {
		t.Fatal(err)
	}
	source = captureValidation(t, e, scope, "products", `UPDATE products SET category='bad' WHERE id=9007199254740993`)
	p, err = e.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=9007199254740993`); err != nil {
		t.Fatal(err)
	}
	r, err = e.Validate(ctx, scope, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Changed != 1 || r.Rows[0].KeyJSON != `{"id": 9007199254740993}` {
		t.Fatalf("large integer key lost precision: %+v %v", r, err)
	}
}

func captureValidation(t *testing.T, e *Engine, scope Scope, table, query string) string {
	t.Helper()
	source := uuid.NewString()
	tx, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := Capture(t.Context(), tx, scope, source, "validation-import", table, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), query)
		return err
	}); err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestRepairValidationOwnerUpgradeAndNoCandidates(t *testing.T) {
	e, scope, source := fixture(t, 1)
	p := approved(t, e, scope, source)
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.validate_plan(uuid,jsonb,text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Validate(t.Context(), scope, p.ID, p.Digest); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing owner upgrade was not refused", err)
	}
	before := validationState(t, e, p.ID)
	for range 2 {
		if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.Validate(t.Context(), scope, p.ID, p.Digest)
	if err != nil || !r.Matches || validationState(t, e, p.ID) != before {
		t.Fatal("owner upgrade changed enrollment, plan, receipt, or approval", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE products SET category='human'`); err != nil {
		t.Fatal(err)
	}
	empty, err := e.Plan(t.Context(), scope, source)
	if err != nil {
		t.Fatal(err)
	}
	r, err = e.Validate(t.Context(), scope, empty.ID, empty.Digest)
	if err != nil || !r.Valid() || r.Matches || r.Candidates != 0 || r.Excluded != 1 || len(r.Issues) != 1 || r.Issues[0] != "no_candidates" {
		t.Fatalf("empty candidate set was reported as usable: %+v %v", r, err)
	}
}
