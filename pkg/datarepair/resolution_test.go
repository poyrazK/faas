package datarepair

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRepairResolveConflictThenReplacement50040(t *testing.T) {
	e, s, source := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, s, source)
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("expected row conflict", err)
	}
	closed, err := e.Resolve(ctx, s, p.ID, p.Digest, repairID, "resolver")
	if err != nil || !closed.ResolvedApply(p.ID, p.Digest, repairID) || closed.ApplyResolution == nil ||
		!reflect.DeepEqual(p.Items, closed.Items) || !reflect.DeepEqual(p.ApprovedAt, closed.ApprovedAt) ||
		!reflect.DeepEqual(p.Rehearsal, closed.Rehearsal) || !p.ExpiresAt.Equal(closed.ExpiresAt) {
		t.Fatalf("closure changed frozen authority/history: %+v %v", closed.ApplyResolution, err)
	}
	assertClosedRepair(t, e, s, closed, repairID)
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := e.Resolve(ctx, s, p.ID, p.Digest, repairID, "another-authorized-resolver")
	if err != nil || !reflect.DeepEqual(closed.ApplyResolution, replay.ApplyResolution) {
		t.Fatal("lost response replay changed durable closure", err)
	}
	child, err := e.Replace(ctx, s, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.Candidates != 459 || child.Conflicts != 41 || child.ApplyResolution != nil || child.ApprovedAt != nil {
		t.Fatal("closure did not allow a fresh narrowed replacement", err)
	}
	if _, err := e.Apply(ctx, s, child.ID, child.Digest, uuid.NewString(), "repair"); !errors.Is(err, ErrConflict) {
		t.Fatal("child inherited old approval", err)
	}
	if _, err := e.Rehearse(ctx, s, child.ID, child.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "new-reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, s, child.ID, child.Digest, uuid.NewString(), "replacement-repair"); err != nil {
		t.Fatal(err)
	}
	var original, human int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&original, &human); err != nil || original != 459 || human != 41 {
		t.Fatal("replacement overwrote valid work", original, human, err)
	}
	if replay, err := e.Resolve(ctx, s, p.ID, p.Digest, repairID, "resolver"); err != nil || !reflect.DeepEqual(closed.ApplyResolution, replay.ApplyResolution) {
		t.Fatal("replacement invalidated closure replay", err)
	}
}

func assertClosedRepair(t *testing.T, e *Engine, s Scope, p Plan, repairID string) {
	t.Helper()
	for _, id := range []string{repairID, uuid.NewString()} {
		if _, err := e.Apply(t.Context(), s, p.ID, p.Digest, id, "delayed-apply"); !errors.Is(err, ErrConflict) {
			t.Fatal("closed plan acquired apply authority", err)
		}
	}
	if _, err := e.Approve(t.Context(), s, p.ID, p.Digest, *p.ApprovedBy); !errors.Is(err, ErrConflict) {
		t.Fatal("closed plan could be approved", err)
	}
	if _, err := e.Rehearse(t.Context(), s, p.ID, p.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("closed plan could be rehearsed", err)
	}
	if _, err := e.RecordRehearsal(t.Context(), s, p.ID, p.Digest, *p.Rehearsal); !errors.Is(err, ErrConflict) {
		t.Fatal("delayed evidence changed closed plan", err)
	}
	r, err := e.Validate(t.Context(), s, p.ID, p.Digest)
	if err != nil || r.Matches || r.Checked != 0 || len(r.Issues) != 1 || r.Issues[0] != "apply_closed" {
		t.Fatal("closed plan advertised usable candidates", r, err)
	}
}

func TestRepairResolveNamedCheckFailure(t *testing.T) {
	e, s, source := namedCheckFixture(t, 2)
	p := approved(t, e, s, source)
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	var failed *CheckError
	if _, err := e.Apply(t.Context(), s, p.ID, p.Digest, id, "repair"); !errors.As(err, &failed) {
		t.Fatal("business check did not roll back", err)
	}
	closed, err := e.Resolve(t.Context(), s, p.ID, p.Digest, id, "resolver")
	if err != nil || closed.CheckFailure == nil || closed.ApplyResolution == nil {
		t.Fatal("check failure could not be closed or lost diagnostic history", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `UPDATE repair_policy SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	assertClosedRepair(t, e, s, closed, id)
	var original, receipts int
	if err := e.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM products WHERE category='original'),(SELECT count(*) FROM gregale_repair.operations WHERE id=$1)`, id).Scan(&original, &receipts); err != nil || original != 0 || receipts != 0 {
		t.Fatal("closure created a receipt or business write", original, receipts, err)
	}
}

func TestRepairResolveWaitsForApplyOutcome(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "committed-response-lost"}[commit], func(t *testing.T) {
			e, s, source := fixture(t, 2)
			p := approved(t, e, s, source)
			id := uuid.NewString()
			tx, err := e.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := readPlan(t.Context(), tx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,$3,$4::uuid,'repair')`, p.ID, scopeJSON(s), p.Digest, id); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Resolve(t.Context(), s, p.ID, p.Digest, id, "resolver"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("in-flight apply was treated as rolled back", err)
			}
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Resolve(t.Context(), s, p.ID, p.Digest, id, "resolver")
			if err != nil || !got.ResolvedApply(p.ID, p.Digest, id) || (got.AppliedAt != nil) != commit || (got.ApplyResolution == nil) != commit {
				t.Fatalf("resolved wrong outcome after commit=%v: %+v %v", commit, got.ApplyResolution, err)
			}
			if commit {
				if _, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString()); !errors.Is(err, ErrConflict) {
					t.Fatal("committed plan could be replaced", err)
				}
			}
		})
	}
}

func TestRepairResolveFenceWinsDelayedApply(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	id := uuid.NewString()
	tx, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := readPlan(t.Context(), tx, `SELECT gregale_repair.resolve_apply($1::uuid,$2::jsonb,$3,$4::uuid,'resolver')`, p.ID, scopeJSON(s), p.Digest, id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.Apply(t.Context(), s, p.ID, p.Digest, id, "delayed")
		done <- err
	}()
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatal("delayed apply escaped closure", err)
	}
}

func TestRepairResolveIdentityAndExplicitUpgrade(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	id := uuid.NewString()
	other := s
	other.Environment = "another"
	if _, err := e.Resolve(t.Context(), other, p.ID, p.Digest, id, "resolver"); !errors.Is(err, ErrNotFound) {
		t.Fatal("resolution crossed scope", err)
	}
	if _, err := e.Resolve(t.Context(), s, p.ID, strings.Repeat("0", 64), id, "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong digest closed plan", err)
	}
	if _, err := e.Pool.Exec(t.Context(), `DROP FUNCTION gregale_repair.resolve_apply(uuid,jsonb,text,uuid,text); ALTER TABLE gregale_repair.plans DROP COLUMN apply_resolution`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resolve(t.Context(), s, p.ID, p.Digest, id, "resolver"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing source contract did not fail closed", err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	closed, err := e.Resolve(t.Context(), s, p.ID, p.Digest, id, "resolver")
	if err != nil || closed.ApplyResolution == nil {
		t.Fatal(err)
	}
	if _, err := e.Resolve(t.Context(), s, p.ID, p.Digest, uuid.NewString(), "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatal("closure replay accepted another repair identity", err)
	}
}
