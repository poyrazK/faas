package datarepair

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func retentionHoldCount(p RetentionPlan, kind, reason string) int64 {
	for _, h := range p.Holds {
		if h.Kind == kind && h.Reason == reason {
			return h.Count
		}
	}
	return 0
}

// Backdate only private fixture metadata to exercise the seven-day boundary
// without waiting. The child's fixed expiry, row identities and versions stay
// unchanged; production never extends an expired replacement's authority.
func ageReplacementFence(t *testing.T, e *Engine, id string) {
	t.Helper()
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET created_at=clock_timestamp()-interval '8 days' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestRepairRetentionSupersededRecovery50040(t *testing.T) {
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
		t.Fatal("stale apply did not roll back", err)
	}
	closed, err := e.Resolve(ctx, s, p.ID, p.Digest, failedID, "resolver")
	if err != nil || closed.ApplyResolution == nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 0 || batch.HeldApprovedPlans != 1 || retentionHoldCount(batch, "plan", "closed_without_replacement") != 1 {
		t.Fatalf("closure alone released payload: %+v", batch)
	}
	child, err := e.Replace(ctx, s, p.ID, p.Digest, uuid.NewString())
	if err != nil || child.Candidates != 459 || child.Conflicts != 41 {
		t.Fatal("replacement lost the 459/41 selection", err)
	}
	batch = retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 0 || retentionHoldCount(batch, "plan", "replacement_retention_minimum") != 1 {
		t.Fatalf("old approval bypassed fresh fence age: %+v", batch)
	}
	ageReplacementFence(t, e, child.ID)
	before, err := e.Get(ctx, s, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch = retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 1 || batch.HeldApprovedPlans != 0 || retentionHoldCount(batch, "operation", "plan_payload_required") != 1 {
		t.Fatalf("replacement did not retain its journal: %+v", batch)
	}
	entry := batch.Entries[0]
	if entry.Kind != "plan" || entry.ID != p.ID || entry.Reason != "superseded_plan" || entry.ReplacementPlanID != child.ID ||
		entry.ReplacementPlanDigest != child.Digest || entry.SupersededAt == nil || entry.SupersededAt.After(batch.Before) || entry.ChangeRows != 500 {
		t.Fatalf("missing reviewed fence identity: %+v", entry)
	}
	receipt := retentionPurge(t, e, s, batch)
	for range 2 {
		if _, err := e.Pool.Exec(ctx, UpgradeSchema()); err != nil {
			t.Fatal(err)
		}
	}
	retained, err := e.Resolve(ctx, s, p.ID, p.Digest, failedID, "recovery-operator")
	if err != nil || retained.PayloadPurgedAt == nil || len(retained.Items) != 0 || !retained.ResolvedApply(p.ID, p.Digest, failedID) {
		t.Fatal("compacted closure could not be recovered", err)
	}
	before.Items, before.PayloadPurgedAt = retained.Items, retained.PayloadPurgedAt
	if !reflect.DeepEqual(before, retained) {
		t.Fatal("compaction changed history, approval, closure or replacement linkage")
	}
	recovered, err := e.Replace(ctx, s, p.ID, p.Digest, child.ID)
	if err != nil || !reflect.DeepEqual(child, recovered) {
		t.Fatal("lost replacement response could not be recovered after parent purge", err)
	}
	if _, err := e.Replace(ctx, s, p.ID, p.Digest, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatal("compacted parent accepted another replacement", err)
	}
	for _, id := range []string{failedID, uuid.NewString()} {
		if _, err := e.Apply(ctx, s, p.ID, p.Digest, id, "delayed-apply"); !errors.Is(err, ErrConflict) {
			t.Fatal("old apply escaped retained source fences", err)
		}
	}
	if _, err := e.Approve(ctx, s, p.ID, p.Digest, "old-reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("compacted parent could be reapproved", err)
	}
	if _, err := e.RecordRehearsal(ctx, s, p.ID, p.Digest, *p.Rehearsal); !errors.Is(err, ErrConflict) {
		t.Fatal("delayed evidence changed compacted parent", err)
	}
	next := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(next.Entries) != 0 || retentionHoldCount(next, "operation", "plan_payload_required") != 1 {
		t.Fatalf("active replacement lost source protection: %+v", next)
	}
	var rows, original, human int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM gregale_repair.changes),count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&rows, &original, &human); err != nil || rows != 500 || original != 0 || human != 41 {
		t.Fatal("compaction changed business rows or pinned capture", rows, original, human, err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "new-reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatal("replacement inherited parent rehearsal", err)
	}
	if _, err := e.Rehearse(ctx, s, child.ID, child.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(ctx, s, child.ID, child.Digest, "new-reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, s, child.ID, child.Digest, child.ID, "replacement-repair"); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&original, &human); err != nil || original != 459 || human != 41 {
		t.Fatal("replacement overwrote valid work after compaction", original, human, err)
	}
	replay, err := e.PurgeRetention(ctx, s, batch.ID, batch.Digest, "another-operator")
	if err != nil || !reflect.DeepEqual(receipt, replay) {
		t.Fatal("lost purge response changed its original receipt", err)
	}
}

func TestRepairRetentionReplacementFenceRequired(t *testing.T) {
	for _, approvedParent := range []bool{false, true} {
		for _, closeParent := range []bool{false, true} {
			name := map[bool]string{false: "unapproved", true: "approved"}[approvedParent] + "/" + map[bool]string{false: "direct", true: "closed"}[closeParent]
			t.Run(name, func(t *testing.T) {
				e, s, source := fixture(t, 2)
				var p Plan
				if approvedParent {
					p = approved(t, e, s, source)
				} else {
					var err error
					p, err = e.Plan(t.Context(), s, source)
					if err != nil {
						t.Fatal(err)
					}
				}
				if closeParent {
					if _, err := e.Resolve(t.Context(), s, p.ID, p.Digest, uuid.NewString(), "resolver"); err != nil {
						t.Fatal(err)
					}
				}
				ageRetentionFixture(t, e)
				if closeParent {
					batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
					if retentionHoldCount(batch, "plan", "closed_without_replacement") != 1 {
						t.Fatal("closure without child was not held", batch)
					}
				}
				child, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
				for _, item := range batch.Entries {
					if item.ID == p.ID {
						t.Fatal("expired unapproved branch bypassed fresh replacement fence")
					}
				}
				if retentionHoldCount(batch, "plan", "replacement_retention_minimum") != 1 {
					t.Fatal("fresh replacement hold missing", batch)
				}
				ageReplacementFence(t, e, child.ID)
				batch = retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
				found := false
				for _, item := range batch.Entries {
					found = found || item.ID == p.ID && item.Reason == "superseded_plan"
				}
				if !found {
					t.Fatal("verified old source fence did not release parent", batch)
				}
				retentionPurge(t, e, s, batch)
				if _, err := e.Replace(t.Context(), s, p.ID, p.Digest, child.ID); err != nil {
					t.Fatal("compaction lost replacement replay", err)
				}
			})
		}
	}
}

func TestRepairRetentionRechecksReplacementProof(t *testing.T) {
	for _, tc := range []struct {
		name, mutation, hold string
	}{
		{"fence_age", `UPDATE gregale_repair.plans SET created_at=clock_timestamp() WHERE id=$1`, "replacement_retention_minimum"},
		{"scope", `UPDATE gregale_repair.plans SET scope=jsonb_set(scope,'{environment}','"other"') WHERE id=$1`, "replacement_unverified"},
		{"corrupt_child", `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{candidates}','999') WHERE id=$1`, ""},
		{"wrong_parent", `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{replacement,parent_plan_id}','"00000000-0000-0000-0000-000000000001"'),digest=encode(sha256(convert_to(jsonb_set(document,'{replacement,parent_plan_id}','"00000000-0000-0000-0000-000000000001"')::text,'UTF8')),'hex') WHERE id=$1`, ""},
		{"invalid_comparison", `UPDATE gregale_repair.plans SET document=jsonb_set(document,'{replacement,removed_candidates}','99'),digest=encode(sha256(convert_to(jsonb_set(document,'{replacement,removed_candidates}','99')::text,'UTF8')),'hex') WHERE id=$1`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, source := fixture(t, 2)
			p := approved(t, e, s, source)
			ageRetentionFixture(t, e)
			child, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			ageReplacementFence(t, e, child.ID)
			batch := retentionPreview(t, e, s, 1)
			if len(batch.Entries) != 1 || batch.Entries[0].ID != p.ID {
				t.Fatal("parent not selected", batch)
			}
			if _, err := e.Pool.Exec(t.Context(), tc.mutation, child.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.PurgeRetention(t.Context(), s, batch.ID, batch.Digest, "operator"); !errors.Is(err, ErrConflict) {
				t.Fatal("reviewed batch accepted changed replacement proof", err)
			}
			got, err := e.Get(t.Context(), s, p.ID)
			if err != nil || got.PayloadPurgedAt != nil || len(got.Items) != 2 {
				t.Fatal("failed revalidation partially compacted parent", err)
			}
			fresh, err := e.PlanRetention(t.Context(), s, time.Time{}, api.DataRepairPurgeMaxItems)
			if tc.hold == "" {
				if !errors.Is(err, ErrConflict) {
					t.Fatal("fresh digest legitimized broken replacement evidence", err)
				}
			} else if err != nil || len(fresh.Entries) != 0 || fresh.HeldApprovedPlans != 1 || retentionHoldCount(fresh, "plan", tc.hold) != 1 {
				t.Fatal("replacement proof hold missing", fresh, err)
			}
		})
	}
}

func TestRepairRetentionSupersededChainReplay(t *testing.T) {
	e, s, source := fixture(t, 3)
	p := approved(t, e, s, source)
	child, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	last, err := e.Replace(t.Context(), s, child.ID, child.Digest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rehearse(t.Context(), s, last.ID, last.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(t.Context(), s, last.ID, last.Digest, "last-reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(t.Context(), s, last.ID, last.Digest, last.ID, "last-repair"); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	batch := retentionPreview(t, e, s, api.DataRepairPurgeMaxItems)
	if len(batch.Entries) != 3 || batch.HeldApprovedPlans != 0 {
		t.Fatal("chain or receipt not eligible", batch)
	}
	retentionPurge(t, e, s, batch)
	retentionPurge(t, e, s, retentionPreview(t, e, s, api.DataRepairPurgeMaxItems))
	for _, pair := range [][2]Plan{{p, child}, {child, last}} {
		recovered, err := e.Replace(t.Context(), s, pair[0].ID, pair[0].Digest, pair[1].ID)
		if err != nil || recovered.PayloadPurgedAt == nil || recovered.Replacement == nil || !recovered.Replacement.Valid(recovered) {
			t.Fatal("compacted chain lost lineage replay", err)
		}
	}
	if _, err := e.Apply(t.Context(), s, last.ID, last.Digest, last.ID, "receipt-replay"); err != nil {
		t.Fatal("compacted last receipt could not be recovered", err)
	}
	if _, err := e.Replace(t.Context(), s, p.ID, strings.Repeat("0", 64), child.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("compacted lineage accepted a changed digest", err)
	}
}

func TestRepairRetentionSupersededRetainedChild(t *testing.T) {
	e, s, source := fixture(t, 2)
	p := approved(t, e, s, source)
	child, err := e.Replace(t.Context(), s, p.ID, p.Digest, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	// Give the fixture equal old creation timestamps so the bounded inventory's
	// UUID tie-break selects the expired child before its approved parent.
	if _, err := e.Pool.Exec(t.Context(), `UPDATE gregale_repair.plans SET created_at=transaction_timestamp()-interval '10 days'`); err != nil {
		t.Fatal(err)
	}
	first := retentionPreview(t, e, s, 1)
	if len(first.Entries) != 1 || first.Entries[0].ID != child.ID {
		t.Fatal("expected child to compact first", first)
	}
	retentionPurge(t, e, s, first)
	next := retentionPreview(t, e, s, 1)
	if len(next.Entries) != 1 || next.Entries[0].ID != p.ID || next.Entries[0].ReplacementPlanDigest != child.Digest {
		t.Fatal("retained child could no longer prove parent fence", next)
	}
	retentionPurge(t, e, s, next)
	replay, err := e.Replace(t.Context(), s, p.ID, p.Digest, child.ID)
	if err != nil || replay.PayloadPurgedAt == nil || replay.Digest != child.Digest {
		t.Fatal("compacted child identity could not be recovered", err)
	}
	if _, err := e.Apply(t.Context(), s, child.ID, child.Digest, child.ID, "expired-child"); !errors.Is(err, ErrPurged) {
		t.Fatal("compaction restored authority to expired unapproved child", err)
	}
}

func TestRepairRetentionSupersededWaitsForChildApply(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, s, source := fixture(t, 2)
			p := approved(t, e, s, source)
			ageRetentionFixture(t, e)
			child, err := e.Replace(t.Context(), s, p.ID, p.Digest, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			ageReplacementFence(t, e, child.ID)
			batch := retentionPreview(t, e, s, 1)
			// A later child approval/receipt does not change the immutable fence
			// reviewed above. Its transaction must still finish before purge.
			if _, err := e.Rehearse(t.Context(), s, child.ID, child.Digest); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Approve(t.Context(), s, child.ID, child.Digest, "new-reviewer"); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			if _, err := readPlan(t.Context(), tx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,$3,$4::uuid,'replacement')`, child.ID, scopeJSON(s), child.Digest, child.ID); err != nil {
				t.Fatal(err)
			}
			wait, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			if _, err := e.PurgeRetention(wait, s, batch.ID, batch.Digest, "operator"); err == nil {
				t.Fatal("purge raced an open replacement apply")
			}
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			retentionPurge(t, e, s, batch)
			if _, err := e.Apply(t.Context(), s, child.ID, child.Digest, child.ID, "replacement-replay"); err != nil {
				t.Fatal("child apply or receipt recovery failed after parent purge", err)
			}
		})
	}
}

func TestRepairRetentionYoungReceiptHold(t *testing.T) {
	e, s, source := fixture(t, 1)
	p := approved(t, e, s, source)
	if _, err := e.Apply(t.Context(), s, p.ID, p.Digest, uuid.NewString(), "repair"); err != nil {
		t.Fatal(err)
	}
	batch := retentionPreview(t, e, s, 1)
	if len(batch.Entries) != 0 || batch.HeldApprovedPlans != 0 || retentionHoldCount(batch, "plan", "receipt_retention_minimum") != 1 {
		t.Fatal("young receipt lost its hold or was counted as an unknown apply", batch)
	}
}
