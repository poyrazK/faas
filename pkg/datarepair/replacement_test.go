package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRepairReplacement50040(t *testing.T) {
	e, s, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	parent := approved(t, e, s, source)
	var document string
	if err := e.Pool.QueryRow(ctx, `SELECT document::text FROM gregale_repair.plans WHERE id=$1`, parent.ID).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	child, err := e.Replace(ctx, s, parent.ID, parent.Digest, id)
	if err != nil {
		t.Fatal(err)
	}
	if child.Candidates != 459 || child.Conflicts != 41 || child.Replacement == nil || child.Replacement.RemovedCandidates != 1 ||
		child.Replacement.PreviousExcluded != 40 || !child.Replacement.Valid(child) || child.Digest == parent.Digest ||
		child.ApprovedAt != nil || child.RehearsedAt != nil || child.Rehearsal != nil || child.AppliedAt != nil {
		t.Fatalf("invalid replacement: %+v", child)
	}
	for _, item := range child.Items {
		if string(item.Key) == `{"id": 41}` && (item.PreviousStatus != "candidate" || item.Status != "conflict") {
			t.Fatalf("missing changed-candidate comparison: %+v", item)
		}
	}
	got, err := e.Get(ctx, s, parent.ID)
	if err != nil || got.ReplacementPlanID != id || got.Digest != parent.Digest || !reflect.DeepEqual(got.ApprovedAt, parent.ApprovedAt) ||
		!reflect.DeepEqual(got.ApprovedBy, parent.ApprovedBy) || !got.ExpiresAt.Equal(parent.ExpiresAt) {
		t.Fatalf("parent history changed: %+v %v", got, err)
	}
	var after string
	if err := e.Pool.QueryRow(ctx, `SELECT document::text FROM gregale_repair.plans WHERE id=$1`, parent.ID).Scan(&after); err != nil || after != document {
		t.Fatal("parent frozen document changed", err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("replacement inherited approval/rehearsal", err)
	}
	if _, err := e.Approve(ctx, s, parent.ID, parent.Digest, *parent.ApprovedBy); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced parent could be reapproved", err)
	}
	if _, err := e.Apply(ctx, s, parent.ID, parent.Digest, uuid.NewString(), "repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced parent could apply", err)
	}
	r, err := e.Validate(ctx, s, parent.ID, parent.Digest)
	if err != nil || r.Matches || r.Checked != 0 || !reflect.DeepEqual(r.Issues, []string{"plan_replaced"}) {
		t.Fatalf("parent freshness report: %+v %v", r, err)
	}
	replay, err := e.Replace(ctx, s, parent.ID, parent.Digest, id)
	if err != nil || !reflect.DeepEqual(child, replay) {
		t.Fatal("replacement replay changed selection or expiry", err)
	}
	if _, err := e.Replace(ctx, s, parent.ID, parent.Digest, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("parent admitted a second replacement", err)
	}
	child, err = e.Rehearse(ctx, s, child.ID, child.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, s, child.ID, parent.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("old digest approved replacement", err)
	}
	child, err = e.Approve(ctx, s, child.ID, child.Digest, "new-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	child, err = e.Apply(ctx, s, child.ID, child.Digest, child.ID, "replacement")
	if err != nil {
		t.Fatal(err)
	}
	var restored, human, changes int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human'),
	 (SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1) FROM products`, child.ID).Scan(&restored, &human, &changes); err != nil || restored != 459 || human != 41 || changes != 459 {
		t.Fatalf("replacement result: restored=%d human=%d changes=%d %v", restored, human, changes, err)
	}
	if replay, err = e.Replace(ctx, s, parent.ID, parent.Digest, id); err != nil || !reflect.DeepEqual(child, replay) {
		t.Fatal("replacement replay lost committed receipt", err)
	}
}

func TestRepairReplacementNeverPromotesExcludedAndFurtherDriftRollsBack(t *testing.T) {
	e, s, source := fixture(t, 3)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	parent := approved(t, e, s, source)
	// Simulate an owner bypass restoring exactly the captured version/values on
	// an excluded row. Replacement must not even reconsider that incarnation.
	if _, err := e.Pool.Exec(ctx, `ALTER TABLE products DISABLE TRIGGER gregale_repair_guard;
	 UPDATE products SET category='bad',gregale_row_version=2 WHERE id=1;
	 ALTER TABLE products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	child, err := e.Replace(ctx, s, parent.ID, parent.Digest, uuid.NewString())
	if err != nil || child.Candidates != 2 || child.Conflicts != 1 {
		t.Fatalf("excluded row was promoted: %+v %v", child, err)
	}
	child, err = e.Rehearse(ctx, s, child.ID, child.Digest)
	if err != nil {
		t.Fatal(err)
	}
	child, err = e.Approve(ctx, s, child.ID, child.Digest, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	replay, err := e.Replace(ctx, s, parent.ID, parent.Digest, child.ID)
	if err != nil || !reflect.DeepEqual(replay, child) {
		t.Fatal("replay refreshed candidates", err)
	}
	if _, err := e.Apply(ctx, s, child.ID, child.Digest, child.ID, "repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("later drift did not abort replacement", err)
	}
	var restored, receipts int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),
	 (SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products`, child.ID).Scan(&restored, &receipts); err != nil || restored != 0 || receipts != 0 {
		t.Fatalf("partial replacement: restored=%d receipts=%d %v", restored, receipts, err)
	}
	grandchild, err := e.Replace(ctx, s, child.ID, child.Digest, uuid.NewString())
	if err != nil || grandchild.Candidates != 1 || grandchild.Conflicts != 2 || grandchild.Replacement.PreviousExcluded != 1 {
		t.Fatalf("replacement chain expanded selection: %+v %v", grandchild, err)
	}
}

func TestRepairReplacementSerializesWithApplyCommit(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(map[bool]string{true: "commit", false: "rollback"}[commit], func(t *testing.T) {
			e, s, source := fixture(t, 2)
			p := approved(t, e, s, source)
			ctx := t.Context()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := readPlan(ctx, tx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,$3,$4::uuid,'repair')`, p.ID, scopeJSON(s), p.Digest, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			id := uuid.NewString()
			if _, err := e.Replace(wait, s, p.ID, p.Digest, id); err == nil {
				t.Fatal("replacement passed an uncommitted apply")
			}
			if commit {
				err = tx.Commit(ctx)
			} else {
				err = tx.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			child, err := e.Replace(ctx, s, p.ID, p.Digest, id)
			if commit && !errors.Is(err, ErrConflict) || !commit && (err != nil || child.Candidates != 2) {
				t.Fatalf("replacement after commit=%v: %+v %v", commit, child, err)
			}
		})
	}
}

func TestRepairReplacementConcurrentIdentityAndScope(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	other := s
	other.AppID = uuid.NewString()
	if _, err := e.Replace(t.Context(), other, p.ID, p.Digest, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatal("replacement crossed scope", err)
	}
	if _, err := e.Replace(t.Context(), s, p.ID, strings.Repeat("0", 64), uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong digest replaced plan", err)
	}
	id := uuid.NewString()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if child, err := e.Replace(t.Context(), s, p.ID, p.Digest, id); err != nil || child.ID != id {
				t.Errorf("same-ID concurrent replacement: %s %v", child.ID, err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gregale_repair.plans`).Scan(&count); err != nil || count != 2 {
		t.Fatal("duplicate replacement plans", count, err)
	}
	root, err := e.Plan(t.Context(), s, source)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := e.Replace(t.Context(), s, root.ID, root.Digest, uuid.NewString())
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("competing replacement forks", wins)
	}
}

func TestRepairReplacementRequiresExplicitOwnerUpgrade(t *testing.T) {
	e, s, source := fixture(t, 1)
	p := approved(t, e, s, source)
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.replace_plan(uuid,jsonb,text,uuid,text,jsonb);
	 ALTER TABLE gregale_repair.plans DROP COLUMN replacement_plan_id`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := e.Replace(t.Context(), s, p.ID, p.Digest, id); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing owner contract did not fail closed", err)
	}
	for range 2 {
		if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := e.Get(t.Context(), s, p.ID)
	if err != nil || !reflect.DeepEqual(p, parent) {
		t.Fatal("owner upgrade changed frozen plan or approval", err)
	}
	if child, err := e.Replace(t.Context(), s, p.ID, p.Digest, id); err != nil || child.Candidates != 1 || child.ApprovedAt != nil {
		t.Fatalf("upgraded replacement: %+v %v", child, err)
	}
}

func TestRepairReplacementExpiryPolicyAndOwnerUpgrade(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	e.Checks = []Check{{Name: "New declared rule", SQL: "SELECT true"}}
	child, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.ChecksSHA256 == p.ChecksSHA256 || len(child.Checks) != 1 || child.ApprovedAt != nil || child.RehearsedAt != nil {
		t.Fatalf("new policy/expiry replacement: %+v %v", child, err)
	}
	if b, err := json.Marshal(child.Replacement); err != nil || !json.Valid(b) {
		t.Fatal("invalid lineage", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE products ADD COLUMN changed_schema text`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Replace(t.Context(), s, child.ID, child.Digest, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("replacement bypassed schema qualification", err)
	}
}

func TestRepairReplacementBoundsFrozenAndNewObservations(t *testing.T) {
	e, s, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE replacement_values(id bigint PRIMARY KEY,category text NOT NULL,private_note text NOT NULL);
	 SELECT gregale_repair.enroll('replacement_values',ARRAY['category']);
	 INSERT INTO replacement_values SELECT generate_series(1,500),'original','unrelated private value'`); err != nil {
		t.Fatal(err)
	}
	source := captureValidation(t, e, s, "replacement_values", `UPDATE replacement_values SET category='bad'`)
	if _, err := e.Pool.Exec(ctx, `UPDATE replacement_values SET category=repeat('x',10000) WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	parent, err := e.Plan(ctx, s, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE replacement_values SET category=repeat('y',100000) WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	child, err := e.Replace(ctx, s, parent.ID, parent.Digest, uuid.NewString())
	if err != nil || child.Candidates != 459 || child.Conflicts != 41 {
		t.Fatalf("bounded replacement: candidates=%d conflicts=%d %v", child.Candidates, child.Conflicts, err)
	}
	bytes, omitted := 0, 0
	for _, item := range child.Items {
		if item.ObservationOmitted {
			omitted++
		} else if string(item.ObservedValues) != "null" {
			bytes += len(item.ObservedValues)
			if len(item.ObservedValues) > api.DataRepairMaxObservedRowBytes {
				t.Fatal("replacement exceeded per-record observation budget")
			}
		}
		if strings.Contains(item.ObservedValuesJSON, "private_note") {
			t.Fatal("replacement exposed unrelated values")
		}
		if item.KeyJSON == `{"id": 41}` && !item.ObservationOmitted {
			t.Fatal("large later edit was not omitted")
		}
	}
	if bytes > api.DataRepairMaxObservedPlanBytes || omitted == 0 {
		t.Fatal("replacement exceeded aggregate observation budget", bytes, omitted)
	}
}

func TestRepairReplacementMissingRowsAndValueDrift(t *testing.T) {
	e, s, source := fixture(t, 2)
	parent, err := e.Plan(t.Context(), s, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), `DELETE FROM products WHERE id=1;
	 INSERT INTO products(id,category) VALUES(1,'human');
	 ALTER TABLE products DISABLE TRIGGER gregale_repair_guard;
	 UPDATE products SET category='human' WHERE id=2;
	 ALTER TABLE products ENABLE TRIGGER gregale_repair_guard`); err != nil {
		t.Fatal(err)
	}
	child, err := e.Replace(t.Context(), s, parent.ID, parent.Digest, uuid.NewString())
	if err != nil || child.Candidates != 0 || child.Unavailable != 1 || child.Conflicts != 1 {
		t.Fatalf("missing/value drift replacement: %+v %v", child, err)
	}
	for _, item := range child.Items {
		if item.PreviousStatus != "candidate" || item.Reason != ReasonRecordMissing && item.Reason != ReasonValuesChanged {
			t.Fatal("replacement lost exclusion reason", item)
		}
	}
	if _, err := e.Rehearse(t.Context(), s, parent.ID, parent.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced parent could be rehearsed", err)
	}
	if _, err := e.Rehearse(t.Context(), s, child.ID, child.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("empty replacement could acquire rehearsal authority", err)
	}
}
