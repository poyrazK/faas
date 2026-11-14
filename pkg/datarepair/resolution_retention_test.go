package datarepair

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A lost first source lookup can leave the hosted apply uncertain even though
// no source approval was sent. Normal retention may then compact its payload.
func TestRepairResolvePurgedUnappliedPlan(t *testing.T) {
	e, scope, source := fixture(t, 1)
	p, err := e.Plan(t.Context(), scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rehearse(t.Context(), scope, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	purged, err := e.Get(t.Context(), scope, p.ID)
	if err != nil || purged.PayloadPurgedAt == nil || purged.ApprovedAt != nil || purged.AppliedAt != nil || len(purged.Items) != 0 {
		t.Fatalf("wrong retention fixture: %+v %v", purged, err)
	}
	repairID := uuid.NewString()
	if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, repairID, "must-remain-purged"); !errors.Is(err, ErrPurged) {
		t.Fatalf("purged plan regained apply authority: %v", err)
	}
	other := scope
	other.Environment = "staging"
	if _, err := e.Resolve(t.Context(), other, p.ID, p.Digest, repairID, "resolver"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolution crossed scope: %v", err)
	}
	if _, err := e.Resolve(t.Context(), scope, p.ID, strings.Repeat("0", 64), repairID, "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatalf("resolution accepted a different digest: %v", err)
	}
	if _, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, source, "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatalf("source identity became repair authority: %v", err)
	}
	tx, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT id FROM gregale_repair.plans WHERE id=$1 FOR UPDATE`, p.ID); err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatal(err)
	}
	_, err = e.Resolve(t.Context(), scope, p.ID, p.Digest, repairID, "resolver")
	_ = tx.Rollback(t.Context())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("busy purged plan was treated as closed: %v", err)
	}
	snapshot := func() string {
		t.Helper()
		var result string
		err := e.Pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'plan',(SELECT to_jsonb(p)-'apply_resolution' FROM gregale_repair.plans p WHERE id=$1),
 'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM gregale_repair.operations o),
 'changes',(SELECT jsonb_agg(to_jsonb(c) ORDER BY operation_id,row_identity) FROM gregale_repair.changes c),
 'products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p))::text`, p.ID).Scan(&result)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	e.CheckSQL = "SELECT false" // Closure must not run business checks.
	closed, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, repairID, "resolver")
	if err != nil || !closed.ResolvedApply(p.ID, p.Digest, repairID) || closed.ApplyResolution == nil || !reflect.DeepEqual(closed.PayloadPurgedAt, purged.PayloadPurgedAt) {
		t.Fatalf("purged plan could not record durable closure: %+v %v", closed, err)
	}
	if snapshot() != before {
		t.Fatal("closure changed retained evidence, receipt, payload or business rows")
	}
	for range 2 {
		replay, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, repairID, "another-resolver")
		if err != nil || !reflect.DeepEqual(replay.ApplyResolution, closed.ApplyResolution) {
			t.Fatalf("closure replay changed its receipt: %+v %v", replay.ApplyResolution, err)
		}
	}
	if _, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatalf("closure accepted another repair identity: %v", err)
	}
	for _, id := range []string{repairID, uuid.NewString()} {
		if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "delayed-apply"); !errors.Is(err, ErrConflict) {
			t.Fatalf("closed plan acquired apply authority: %v", err)
		}
	}
	if _, err := e.Approve(t.Context(), scope, p.ID, p.Digest, "reviewer"); !errors.Is(err, ErrConflict) {
		t.Fatalf("purged closure acquired approval: %v", err)
	}
	if _, err := e.Replace(t.Context(), scope, p.ID, p.Digest, uuid.NewString()); !errors.Is(err, ErrPurged) {
		t.Fatalf("closure reconstructed a purged payload: %v", err)
	}
	if snapshot() != before {
		t.Fatal("failed replays changed retained evidence or business data")
	}
}

func TestRepairResolvePurgedCommittedReceipt(t *testing.T) {
	e, scope, source := fixture(t, 1)
	p := approved(t, e, scope, source)
	id := uuid.NewString()
	committed, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "committed")
	if err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	retentionPurge(t, e, scope, retentionPreview(t, e, scope, api.DataRepairPurgeMaxItems))
	before := validationState(t, e, p.ID)
	receipt, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, id, "resolver")
	if err != nil || receipt.ApplyResolution != nil || receipt.AppliedAt == nil || receipt.RepairOperationID == nil || *receipt.RepairOperationID != *committed.RepairOperationID {
		t.Fatalf("compaction lost the committed receipt: %+v %v", receipt, err)
	}
	if _, err := e.Resolve(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "resolver"); !errors.Is(err, ErrConflict) {
		t.Fatalf("committed plan closed with another identity: %v", err)
	}
	if validationState(t, e, p.ID) != before {
		t.Fatal("receipt lookup rewrote a compacted plan")
	}
}
