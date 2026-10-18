package datarepair

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRepairConflictDiagnosticsAreFrozenAndScopedToChangedFields(t *testing.T) {
	e, scope, source := fixture(t, 5)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `
UPDATE products SET category='human' WHERE id=1;
UPDATE products SET quantity=2 WHERE id=2;
DELETE FROM products WHERE id=3;
INSERT INTO products(id,category) VALUES(3,'human');
BEGIN;
ALTER TABLE products DISABLE TRIGGER gregale_repair_guard;
UPDATE products SET category='human' WHERE id=4;
ALTER TABLE products ENABLE TRIGGER gregale_repair_guard;
COMMIT;`); err != nil {
		t.Fatal(err)
	}
	p, err := e.Prepare(ctx, scope, source, uuid.NewString())
	if err != nil || p.Candidates != 1 || p.Conflicts != 3 || p.Unavailable != 1 {
		t.Fatalf("classification: %+v %v", p, err)
	}
	want := map[int]struct {
		status, reason, version, values string
	}{
		1: {"conflict", ReasonVersionChanged, "3", `{"category": "human"}`},
		2: {"conflict", ReasonVersionChanged, "3", `{"category": "bad"}`},
		3: {"unavailable", ReasonRecordMissing, "", `null`},
		4: {"conflict", ReasonValuesChanged, "2", `{"category": "human"}`},
		5: {"candidate", ReasonMatchesRecordedWrite, "2", `{"category": "bad"}`},
	}
	for _, item := range p.Items {
		var key struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(item.Key, &key); err != nil {
			t.Fatal(err)
		}
		expect, ok := want[key.ID]
		if !ok || item.Status != expect.status || item.Reason != expect.reason || item.ObservedVersion != expect.version || item.ObservedValuesJSON != expect.values {
			t.Fatalf("record %d diagnostics: %+v", key.ID, item)
		}
		if strings.Contains(item.ObservedValuesJSON, "quantity") {
			t.Fatal("unrelated column entered the review payload")
		}
		delete(want, key.ID)
	}
	if len(want) != 0 {
		t.Fatal("missing record diagnostics")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='original' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func() (Plan, error){
		func() (Plan, error) { return e.Get(ctx, scope, p.ID) },
		func() (Plan, error) { return e.Prepare(ctx, scope, source, p.ID) },
	} {
		frozen, err := load()
		if err != nil || frozen.Digest != p.Digest {
			t.Fatalf("saved plan changed: %+v %v", frozen, err)
		}
		before, _ := json.Marshal(p.Items)
		after, _ := json.Marshal(frozen.Items)
		if string(before) != string(after) {
			t.Fatal("reading a saved plan silently refreshed its observations")
		}
	}
	fresh, err := e.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range fresh.Items {
		if item.KeyJSON == `{"id": 1}` {
			found = item.ObservedVersion == "4" && item.ObservedValuesJSON == `{"category": "original"}` && item.Status == "conflict"
		}
	}
	if !found {
		t.Fatal("a new plan did not observe the later edit")
	}
}

func TestRepairObservedNumbersRetainPrecision(t *testing.T) {
	e, scope, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE measurements(id bigint PRIMARY KEY,amount numeric(30,10) NOT NULL,private_note text NOT NULL);
SELECT gregale_repair.enroll('measurements',ARRAY['amount']);
INSERT INTO measurements VALUES(9007199254740993,9007199254740993.1234567890,'not a repair field');`); err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Capture(ctx, tx, scope, source, "numeric-import", "measurements", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE measurements SET amount=9007199254740994.1234567890`)
		return err
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE measurements SET amount=9007199254740995.1234567890`); err != nil {
		t.Fatal(err)
	}
	p, err := e.Plan(ctx, scope, source)
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("numeric plan: %+v %v", p, err)
	}
	i := p.Items[0]
	if i.ObservedValuesJSON != `{"amount": 9007199254740995.1234567890}` || i.KeyJSON != `{"id": 9007199254740993}` || i.Reason != ReasonVersionChanged {
		t.Fatalf("numeric observation lost precision: %+v", i)
	}
	if strings.Contains(i.ObservedValuesJSON, "private_note") {
		t.Fatal("unrelated private field leaked")
	}
}

func TestRepairUpgradePreservesLegacyPlanApprovalAndApply(t *testing.T) {
	e, scope, source := fixture(t, 3)
	ctx := t.Context()
	p, err := e.Plan(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	// Construct the original plan format in this private fixture. Its digest
	// covers that original document; upgrade must preserve that exact identity.
	if _, err := e.Pool.Exec(ctx, `WITH legacy AS (
 SELECT id,jsonb_set(document,'{items}',(
  SELECT jsonb_agg(item-ARRAY['reason','observed_version','observed_values','observation_omitted'] ORDER BY ordinal)
  FROM jsonb_array_elements(document->'items') WITH ORDINALITY AS rows(item,ordinal))) AS document
 FROM gregale_repair.plans WHERE id=$1)
UPDATE gregale_repair.plans p SET document=legacy.document,digest=encode(sha256(convert_to(legacy.document::text,'UTF8')),'hex')
FROM legacy WHERE p.id=legacy.id`, p.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := e.Get(ctx, scope, p.ID)
	if err != nil || legacy.Items[0].Reason != "" || len(legacy.Items[0].ObservedValues) != 0 {
		t.Fatalf("legacy format: %+v %v", legacy, err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Rehearse(ctx, scope, legacy.ID, legacy.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, scope, legacy.ID, legacy.Digest, "legacy-reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, scope, legacy.ID, legacy.Digest, uuid.NewString(), "repair-v1"); err != nil {
		t.Fatal(err)
	}
	retained, err := e.Get(ctx, scope, legacy.ID)
	if err != nil || retained.Digest != legacy.Digest || retained.Items[0].Reason != "" || retained.AppliedAt == nil {
		t.Fatalf("upgrade changed the original plan or receipt: %+v %v", retained, err)
	}
	fresh, err := e.Plan(ctx, scope, source)
	if err != nil || fresh.Conflicts != 3 || fresh.Items[0].Reason != ReasonVersionChanged {
		t.Fatalf("new plan lacks upgraded diagnostics: %+v %v", fresh, err)
	}
}

func TestRepairDiagnosticObservationLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows, bytes int
	}{
		{"one_large_later_edit", 1, api.DataRepairMaxObservedRowBytes + 1},
		{"aggregate_later_edits", 100, api.DataRepairMaxObservedRowBytes / 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			ctx := t.Context()
			if _, err := e.Pool.Exec(ctx, `CREATE TABLE diagnostic_payloads(id int PRIMARY KEY,value text NOT NULL);
SELECT gregale_repair.enroll('diagnostic_payloads',ARRAY['value']);`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `INSERT INTO diagnostic_payloads SELECT generate_series(1,$1),'original'`, tc.rows); err != nil {
				t.Fatal(err)
			}
			source := uuid.NewString()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := Capture(ctx, tx, scope, source, "small-source", "diagnostic_payloads", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE diagnostic_payloads SET value='bad'`)
				return err
			}); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE diagnostic_payloads SET value=repeat('x',$1)`, tc.bytes); err != nil {
				t.Fatal(err)
			}
			p, err := e.Plan(ctx, scope, source)
			if err != nil || p.Conflicts != tc.rows || p.Candidates != 0 {
				t.Fatalf("bounded observation plan: %+v %v", p, err)
			}
			total, omitted := 0, 0
			for _, item := range p.Items {
				if item.ObservedVersion != "3" || item.Reason != ReasonVersionChanged {
					t.Fatal("observation limits changed the conflict result")
				}
				if item.ObservationOmitted {
					omitted++
					if string(item.ObservedValues) != "null" {
						t.Fatal("omitted observation still contains values")
					}
					continue
				}
				if len(item.ObservedValues) > api.DataRepairMaxObservedRowBytes {
					t.Fatal("row observation exceeded its bound")
				}
				total += len(item.ObservedValues)
			}
			if omitted == 0 || total > api.DataRepairMaxObservedPlanBytes {
				t.Fatalf("unbounded observations: omitted=%d bytes=%d", omitted, total)
			}
		})
	}
}
