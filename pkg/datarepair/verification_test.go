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

func applyVerificationFixture(t *testing.T, e *Engine, s Scope, p Plan) Plan {
	t.Helper()
	applied, err := e.Apply(t.Context(), s, p.ID, p.Digest, uuid.NewString(), "verified-repair")
	if err != nil {
		t.Fatal(err)
	}
	return applied
}

func TestRepairVerificationReplacement45941(t *testing.T) {
	e, s, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, s, source)
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	failedID := uuid.NewString()
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, failedID, "failed-repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale apply unexpectedly succeeded", err)
	}
	if _, err := e.Resolve(ctx, s, p.ID, p.Digest, failedID, "resolver"); err != nil {
		t.Fatal(err)
	}
	child, err := e.Replace(ctx, s, p.ID, p.Digest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rehearse(ctx, s, child.ID, child.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "new-reviewer"); err != nil {
		t.Fatal(err)
	}
	committed := applyVerificationFixture(t, e, s, child)
	r, err := e.Verify(ctx, s, child.ID, child.Digest)
	if err != nil || !r.MatchesPlan(committed) || !r.Available || r.Restored != 459 || r.Excluded != 41 || r.Unchanged != 459 {
		t.Fatalf("committed replacement observations: %+v %v", r, err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=42; UPDATE products SET category='bad' WHERE id=1; CREATE SEQUENCE repair_verification_probe`); err != nil {
		t.Fatal(err)
	}
	// Current policy changes cannot invalidate a historical commit or run SQL
	// as a side effect of observation. Original exclusions are never reread.
	e.CheckSQL = "SELECT nextval('public.repair_verification_probe')>0"
	before := validationState(t, e, child.ID)
	r, err = e.Verify(ctx, s, child.ID, child.Digest)
	if err != nil || !r.Valid() || !r.Available || r.Restored != 459 || r.Excluded != 41 || r.Checked != 459 || r.Unchanged != 458 || r.Edited != 1 || r.Missing != 0 || len(r.Rows) != 1 || r.Rows[0].KeyJSON != `{"id": 42}` {
		t.Fatalf("later edit changed the commit result or exclusions: %+v %v", r, err)
	}
	if before != validationState(t, e, child.ID) {
		t.Fatal("verification wrote business rows, journals or plan/approval history")
	}
	var called bool
	if err := e.Pool.QueryRow(ctx, `SELECT is_called FROM repair_verification_probe`).Scan(&called); err != nil || called {
		t.Fatal("verification ran a business assertion", err)
	}
	if _, err := e.Verify(ctx, s, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("closed parent was mistaken for a committed repair", err)
	}
}

func TestRepairVerificationReasonsAndRowIncarnations(t *testing.T) {
	e, s, source := fixture(t, 5)
	p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
	if _, err := e.Pool.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=1;
UPDATE products SET category='original' WHERE id=1;
DELETE FROM products WHERE id=2;
INSERT INTO products(id,category) VALUES(2,'original');
UPDATE products SET quantity=2 WHERE id=3;
BEGIN;
ALTER TABLE products DISABLE TRIGGER gregale_repair_guard;
UPDATE products SET category='human' WHERE id=4;
ALTER TABLE products ENABLE TRIGGER gregale_repair_guard;
COMMIT;`); err != nil {
		t.Fatal(err)
	}
	r, err := e.Verify(t.Context(), s, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Checked != 5 || r.Unchanged != 1 || r.Edited != 3 || r.Missing != 1 {
		t.Fatalf("repair observation reasons: %+v %v", r, err)
	}
	want := map[string]string{`{"id": 1}`: ReasonVersionChanged, `{"id": 2}`: ReasonRecordMissing, `{"id": 3}`: ReasonVersionChanged, `{"id": 4}`: ReasonValuesChanged}
	for _, row := range r.Rows {
		if want[row.KeyJSON] != row.Reason || row.ExpectedVersion != "3" {
			t.Fatalf("wrong repair row/version: %+v", row)
		}
		delete(want, row.KeyJSON)
	}
	if len(want) != 0 {
		t.Fatal("changed repair row omitted")
	}
	raw, _ := json.Marshal(r)
	for _, secret := range []string{"human", "category", "quantity", "SELECT", "before_values", "after_values"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("verification exposed SQL or business field values", secret)
		}
	}
}

func TestRepairVerificationCompactionAndReceiptRecovery(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
	ageRetentionFixture(t, e)
	retentionPurge(t, e, s, retentionPreview(t, e, s, api.DataRepairPurgeMaxItems))
	compacted, err := e.Get(t.Context(), s, p.ID)
	if err != nil || compacted.PayloadPurgedAt == nil {
		t.Fatal(err)
	}
	r, err := e.Verify(t.Context(), s, p.ID, p.Digest)
	if err != nil || !r.Available || r.Unchanged != 2 || !r.MatchesPlan(compacted) {
		t.Fatal("plan compaction prevented journal-based verification", r, err)
	}
	retentionPurge(t, e, s, retentionPreview(t, e, s, api.DataRepairPurgeMaxItems))
	before := validationState(t, e, p.ID)
	r, err = e.Verify(t.Context(), s, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Available || r.Restored != 2 || r.Checked != 0 || len(r.Rows) != 0 || len(r.Issues) != 1 || r.Issues[0] != "journal_purged" || !r.MatchesPlan(compacted) {
		t.Fatalf("purged journal was presented as a current observation: %+v %v", r, err)
	}
	if _, err := e.Apply(t.Context(), s, p.ID, p.Digest, *p.RepairOperationID, "receipt-replay"); err != nil || before != validationState(t, e, p.ID) {
		t.Fatal("receipt replay or unavailable verification changed historical data", err)
	}
}

func TestRepairVerificationRequiresReceiptAndExactScope(t *testing.T) {
	e, s, source := fixture(t, 1)
	p := approved(t, e, s, source)
	if _, err := e.Verify(t.Context(), s, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("no receipt was interpreted as a settled outcome", err)
	}
	p = applyVerificationFixture(t, e, s, p)
	other := s
	other.PlatformTenantID = uuid.NewString()
	if _, err := e.Verify(t.Context(), other, p.ID, p.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatal("verification crossed scope", err)
	}
	if _, err := e.Verify(t.Context(), s, p.ID, strings.Repeat("0", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong receipt digest accepted", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(t.Context(), s, p.ID, p.Digest); err != nil || !r.Available {
		t.Fatal("plan expiry invalidated historical verification", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{candidates}','999') WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Verify(t.Context(), s, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("tampered receipt accepted", err)
	}
}

func TestRepairVerificationSchemaUnavailable(t *testing.T) {
	for _, ddl := range []string{`ALTER TABLE products ADD COLUMN private_note text`, `ALTER TABLE products ENABLE ROW LEVEL SECURITY`, `DROP TABLE products`} {
		t.Run(ddl, func(t *testing.T) {
			e, s, source := fixture(t, 1)
			p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
			if _, err := e.Pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			r, err := e.Verify(t.Context(), s, p.ID, p.Digest)
			if err != nil || !r.Valid() || r.Available || r.Checked != 0 || len(r.Issues) != 1 || r.Issues[0] != "schema_changed" {
				t.Fatal("schema change did not stop observation", r, err)
			}
		})
	}
}

func TestRepairVerificationOwnerUpgradePreservesHistory(t *testing.T) {
	e, s, source := fixture(t, 1)
	p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.verify_plan(uuid,jsonb,text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Verify(t.Context(), s, p.ID, p.Digest); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing source upgrade did not fail closed", err)
	}
	before := validationState(t, e, p.ID)
	for range 2 {
		if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := e.Verify(t.Context(), s, p.ID, p.Digest); err != nil || !r.Available || before != validationState(t, e, p.ID) {
		t.Fatal("upgrade changed historical rows or approval", r, err)
	}
}

func TestRepairVerificationReportRejectsMalformedObservations(t *testing.T) {
	for _, mutate := range []func(*VerificationReport){
		func(r *VerificationReport) { r.Available = false },
		func(r *VerificationReport) { r.Checked = 0 },
		func(r *VerificationReport) { r.Missing = 1 },
		func(r *VerificationReport) { r.Rows = nil },
		func(r *VerificationReport) { r.Issues = nil },
		func(r *VerificationReport) { r.Issues = []string{"candidate_drift"} },
		func(r *VerificationReport) { r.Restored = api.DataRepairMaxRows + 1 },
		func(r *VerificationReport) { r.RepairOperationID = r.SourceOperationID },
		func(r *VerificationReport) { r.AppliedAt = time.Time{} },
	} {
		r := VerificationReport{PlanID: uuid.NewString(), PlanDigest: strings.Repeat("a", 64), Scope: Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"},
			SourceOperationID: uuid.NewString(), RepairOperationID: uuid.NewString(), AppliedAt: time.Now(), CheckedAt: time.Now(), Restored: 1, Available: true, Checked: 1, Unchanged: 1,
			Rows: []ValidationRow{}, Issues: []string{}}
		mutate(&r)
		if r.Valid() {
			t.Fatal("malformed verification report accepted", r)
		}
	}
}

func TestRepairVerificationConcurrentApplyNeedsCommittedReceipt(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, s, source := fixture(t, 2)
			p := approved(t, e, s, source)
			tx, err := e.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err := readPlan(t.Context(), tx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,$3,$4::uuid,'repair')`, p.ID, scopeJSON(s), p.Digest, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Verify(t.Context(), s, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
				t.Fatal("uncommitted apply was presented as a committed repair", err)
			}
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := e.Verify(t.Context(), s, p.ID, p.Digest)
			if commit && (err != nil || !r.Available || r.Unchanged != 2) || !commit && !errors.Is(err, ErrConflict) {
				t.Fatal("verification confused rollback and committed receipt", r, err)
			}
		})
	}
}

func TestRepairVerificationUsesOneReadOnlySnapshot(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
	tx, err := e.Pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	read := func() VerificationReport {
		t.Helper()
		var raw []byte
		var r VerificationReport
		if err := tx.QueryRow(t.Context(), `SELECT gregale_repair.verify_plan($1::uuid,$2::jsonb,$3)`, p.ID, scopeJSON(s), p.Digest).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(raw, &r) != nil || !r.Valid() {
			t.Fatal("invalid snapshot report")
		}
		return r
	}
	if read().Unchanged != 2 {
		t.Fatal("initial snapshot did not match committed repair")
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if read().Unchanged != 2 {
		t.Fatal("concurrent edit mixed snapshots inside verification")
	}
	if r, err := e.Verify(t.Context(), s, p.ID, p.Digest); err != nil || r.Edited != 1 {
		t.Fatal("new verification did not observe later commit", r, err)
	}
}

func TestRepairVerificationBoundsKeysAndKeepsBigIntegers(t *testing.T) {
	e, s, _ := fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `CREATE TABLE verification_keys(id text PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('verification_keys',ARRAY['category']);
INSERT INTO verification_keys SELECT n::text||repeat('x',600),'original' FROM generate_series(1,1000) n;`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, s, "verification_keys", `UPDATE verification_keys SET category='bad'`)
	p := applyVerificationFixture(t, e, s, approved(t, e, s, source))
	if _, err := e.Pool.Exec(t.Context(), `UPDATE verification_keys SET category='human'`); err != nil {
		t.Fatal(err)
	}
	r, err := e.Verify(t.Context(), s, p.ID, p.Digest)
	if err != nil || !r.Valid() || r.Edited != api.DataRepairMaxRows {
		t.Fatal("bounded verification report", r, err)
	}
	keys, omitted := 0, 0
	for _, row := range r.Rows {
		keys += len(row.KeyJSON)
		if row.KeyOmitted {
			omitted++
		}
	}
	if keys > api.DataRepairMaxObservedPlanBytes || omitted == 0 || omitted == len(r.Rows) {
		t.Fatal("verification key budget lost or exceeded", keys, omitted)
	}
	e, s, _ = fixture(t, 1)
	if _, err := e.Pool.Exec(t.Context(), `INSERT INTO products(id,category) VALUES(9007199254740993,'original')`); err != nil {
		t.Fatal(err)
	}
	source = captureValidation(t, e, s, "products", `UPDATE products SET category='bad' WHERE id=9007199254740993`)
	p = applyVerificationFixture(t, e, s, approved(t, e, s, source))
	if _, err := e.Pool.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=9007199254740993`); err != nil {
		t.Fatal(err)
	}
	r, err = e.Verify(t.Context(), s, p.ID, p.Digest)
	if err != nil || r.Edited != 1 || r.Rows[0].KeyJSON != `{"id": 9007199254740993}` {
		t.Fatal("verification lost key precision", r, err)
	}
}
