package datarepair

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ADR-20261103205518: recreate the preceding producer and weaker inverse
// selector in a private database. No historical evidence is upgraded in place.
func installV1ScalarContract(t *testing.T, e *Engine) {
	t.Helper()
	definitions := timezoneRowContract(previousRowContractSchema()) + "\n" + strings.ReplaceAll(captureContract, "__CURRENT_ROW_CONTRACT__", "true")
	begin := strings.Replace(embeddedFunction(retentionContract, "begin_operation")[0], ",gregale_repair.type_fingerprint(e.relation,true),3);", ",gregale_repair.type_fingerprint(e.relation,true),1);", 1)
	definitions += "\n" + begin
	for _, name := range []string{"create_plan", "apply_plan"} {
		part := embeddedFunction(retentionContract, name)[0]
		part = strings.Replace(part, "v::text IS DISTINCT FROM (c.after_values->k)::text", "v IS DISTINCT FROM c.after_values->k", 1)
		part = strings.Replace(part, "'value_contract_version',2,", "", 1)
		definitions += "\n" + strings.NewReplacer("__UNPROVEN_OPERATION__", "false", "__UNPROVEN_PLAN__", "false").Replace(part)
	}
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(definitions)); err != nil {
		t.Fatal(err)
	}
}

func TestRepairScalarUpgradeRejectsAmbiguousZeroAndPreservesReceipts(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installV1ScalarContract(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE zero_history(id int PRIMARY KEY,value float8); SELECT gregale_repair.enroll('zero_history',ARRAY['value']); INSERT INTO zero_history(id,value) VALUES(1,'-0'::float8)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "zero_history", "UPDATE zero_history SET value=1")
	pending := approved(t, e, scope, source)
	committed := approved(t, e, scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, scope, committed.ID, committed.Digest, repairID, "old-zero-repair"); err != nil {
		t.Fatal(err)
	}
	before := validationState(t, e, pending.ID)
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if validationState(t, e, pending.ID) != before {
		t.Fatal("upgrade rewrote ambiguous history or approval")
	}
	if _, err := e.Plan(ctx, scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new plan trusted an ambiguous zero: %v", err)
	}
	if _, err := e.Replace(ctx, scope, pending.ID, pending.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement trusted an ambiguous zero: %v", err)
	}
	if _, err := e.Apply(ctx, scope, pending.ID, pending.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply trusted an ambiguous zero: %v", err)
	}
	if r, err := e.Validate(ctx, scope, pending.ID, pending.Digest); err != nil || r.Matches || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("validation certified unproven history: %+v %v", r, err)
	}
	if r, err := e.Verify(ctx, scope, committed.ID, committed.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("verification certified an unproven restoration: %+v %v", r, err)
	}
	if p, err := e.Apply(ctx, scope, committed.ID, committed.Digest, repairID, "recover-receipt"); err != nil || p.AppliedAt == nil {
		t.Fatalf("historical receipt recovery lost: %+v %v", p, err)
	}
}

func TestRepairScalarUpgradePreservesProvenFloatApproval(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installV1ScalarContract(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE float_history(id int PRIMARY KEY,value float8); SELECT gregale_repair.enroll('float_history',ARRAY['value']); INSERT INTO float_history(id,value) VALUES(1,1.25)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "float_history", "UPDATE float_history SET value=2.5")
	p := approved(t, e, scope, source)
	before := validationState(t, e, p.ID)
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("upgrade rewrote a safe float approval")
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || !r.Matches {
		t.Fatalf("safe v1 float history lost: %+v %v", r, err)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "safe-v1-float"); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("safe v1 float correction failed verification: %+v %v", r, err)
	}
}

func TestRepairScalarUpgradeReplansOmittedScale(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installV1ScalarContract(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE scale_history(id int PRIMARY KEY,category text,value numeric); SELECT gregale_repair.enroll('scale_history',ARRAY['category','value']); INSERT INTO scale_history(id,category,value) VALUES(1,'original',1.00)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "scale_history", "UPDATE scale_history SET category='bad',value=1.0")
	old := approved(t, e, scope, source)
	if strings.Contains(string(old.Items[0].Before), "value") {
		t.Fatal("fixture did not recreate the omitted inverse")
	}
	before := validationState(t, e, old.ID)
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if validationState(t, e, old.ID) != before {
		t.Fatal("upgrade expanded an old selection")
	}
	if _, err := e.Apply(ctx, scope, old.ID, old.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("incomplete inverse was applied: %v", err)
	}
	if _, err := e.Replace(ctx, scope, old.ID, old.Digest, uuid.NewString()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replacement copied an incomplete inverse: %v", err)
	}
	p := approved(t, e, scope, source)
	if p.Candidates != 1 || !strings.Contains(string(p.Items[0].Before), "1.00") {
		t.Fatalf("fresh planning did not recover the retained scale: %+v", p)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "complete-scale-repair"); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err := e.Pool.QueryRow(ctx, `SELECT category='original' AND scale(value)=2 FROM scale_history`).Scan(&exact); err != nil || !exact {
		t.Fatalf("replanned inverse failed to restore all fields: %v %v", exact, err)
	}
}

func TestRepairScalarUpgradeDoesNotCertifyIncompleteScaleReceipt(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installV1ScalarContract(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE scale_history(id int PRIMARY KEY,category text,value numeric); SELECT gregale_repair.enroll('scale_history',ARRAY['category','value']); INSERT INTO scale_history(id,category,value) VALUES(1,'original',1.00)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "scale_history", "UPDATE scale_history SET category='bad',value=1.0")
	p := approved(t, e, scope, source)
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, repairID, "old-partial-inverse"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || r.Available || r.Checked != 0 || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("incomplete historical inverse was certified: %+v %v", r, err)
	}
	if receipt, err := e.Apply(ctx, scope, p.ID, p.Digest, repairID, "receipt-recovery"); err != nil || receipt.AppliedAt == nil {
		t.Fatalf("historical receipt recovery lost: %+v %v", receipt, err)
	}
	// Compaction must not erase the reason that this historical inverse cannot
	// be certified. Remove the source first and leave its repair journal intact.
	purgeScalarSource(t, e, scope, source)
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || r.Available || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("compaction recertified an incomplete inverse: %+v %v", r, err)
	}
}

func purgeScalarSource(t *testing.T, e *Engine, scope Scope, source string) {
	t.Helper()
	ctx := t.Context()
	ageRetentionFixture(t, e)
	if _, err := e.Pool.Exec(ctx, `BEGIN; ALTER TABLE gregale_repair.operations DISABLE TRIGGER gregale_repair_protect_operation;
UPDATE gregale_repair.operations SET created_at=clock_timestamp()-interval '11 days' WHERE repair_of IS NULL;
ALTER TABLE gregale_repair.operations ENABLE TRIGGER gregale_repair_protect_operation; COMMIT`); err != nil {
		t.Fatal(err)
	}
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, 10))
	preview := retentionPreview(t, e, scope, 1)
	if len(preview.Entries) != 1 || preview.Entries[0].ID != source {
		t.Fatalf("expected source compaction first: %+v", preview)
	}
	retentionPurge(t, e, scope, preview)
}

func TestRepairScalarUpgradePurgedFloatProofStaysUnavailable(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installV1ScalarContract(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE float_history(id int PRIMARY KEY,value float8); SELECT gregale_repair.enroll('float_history',ARRAY['value']); INSERT INTO float_history(id,value) VALUES(1,1.25)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "float_history", "UPDATE float_history SET value=2.5")
	p := approved(t, e, scope, source)
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "old-proven-float"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !r.Available || r.Unchanged != 1 {
		t.Fatalf("retained nonzero float proof was lost: %+v %v", r, err)
	}
	purgeScalarSource(t, e, scope, source)
	if r, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || r.Available || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("missing float proof was synthesized: %+v %v", r, err)
	}
}

func TestRepairScalarUpgradeChecksTypedImages(t *testing.T) {
	for _, tc := range []struct {
		name, setup, datatype, before, after string
		allowed                              bool
	}{
		{"float composite with integer zero", "CREATE TYPE history_value AS (f float8,n int);", "history_value", "ROW(1.25,0)::history_value", "ROW(2.5,0)::history_value", true},
		{"jsonb object with nested null", "", "jsonb", `'{"n":null}'::jsonb`, `'{"n":1}'::jsonb`, true},
		{"ambiguous jsonb null", "", "jsonb", `'null'::jsonb`, `'{}'::jsonb`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			installV1ScalarContract(t, e)
			if _, err := e.Pool.Exec(ctx, tc.setup+`CREATE TABLE typed_history(id int PRIMARY KEY,value `+tc.datatype+`); SELECT gregale_repair.enroll('typed_history',ARRAY['value']); INSERT INTO typed_history(id,value) VALUES(1,`+tc.before+`)`); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, scope, "typed_history", "UPDATE typed_history SET value="+tc.after)
			p := approved(t, e, scope, source)
			if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
				t.Fatal(err)
			}
			r, err := e.Validate(ctx, scope, p.ID, p.Digest)
			if err != nil || r.Matches != tc.allowed {
				t.Fatalf("typed history qualification: allowed=%v %+v %v", tc.allowed, r, err)
			}
			_, err = e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "typed-history")
			if !tc.allowed {
				if !errors.Is(err, ErrInvalid) || !slices.Contains(r.Issues, "schema_changed") {
					t.Fatalf("ambiguous null received authority: %+v %v", r, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if report, err := e.Verify(ctx, scope, p.ID, p.Digest); err != nil || !report.Available || report.Unchanged != 1 {
				t.Fatalf("safe typed correction failed verification: %+v %v", report, err)
			}
		})
	}
}

func TestRepairScalarUpgradeRejectsUnprovenNumericCompleteness(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	installLegacyRows(t, e)
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE hidden_scale(id int PRIMARY KEY,category text,value numeric); SELECT gregale_repair.enroll('hidden_scale',ARRAY['category']); INSERT INTO hidden_scale(id,category,value) VALUES(1,'original',1.00)`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, scope, "hidden_scale", "UPDATE hidden_scale SET category='bad',value=1.0")
	p := approved(t, e, scope, source)
	before := validationState(t, e, p.ID)
	if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("upgrade rewrote incomplete history")
	}
	if _, err := e.Plan(ctx, scope, source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new plan trusted undeclared numeric changes: %v", err)
	}
	if _, err := e.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "must-block"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("old approval trusted incomplete numeric capture: %v", err)
	}
	if r, err := e.Validate(ctx, scope, p.ID, p.Digest); err != nil || r.Matches || !slices.Contains(r.Issues, "schema_changed") {
		t.Fatalf("incomplete capture was certified: %+v %v", r, err)
	}
}
