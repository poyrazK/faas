package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestMemStore_InsertTriggerRecord_OnFreshStore is the regression pin for a
// nil-map panic.
//
// MemStore.InsertTriggerRecord writes to m.records, which NewMemStore did not
// initialize and no lazy guard covered. Every call on a freshly constructed
// store panicked with "assignment to entry in nil map".
//
// It went unnoticed because no MemStore test had ever called the method — the
// only coverage was through PgStore — so the panic sat in a Store method that
// looked exercised. This test needs no database, so the cheap path now covers
// it too.
//
// The conformance case that first hit this lives in
// pkg/state/conformance/claim_cases.go, which is not a _test.go file; a
// direct unit test is what makes the defect legible on its own.
func TestMemStore_InsertTriggerRecord_OnFreshStore(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()

	// No seeding of any kind: a brand-new store is the failing condition.
	id, err := store.InsertTriggerRecord(ctx, "00000000-0000-0000-0000-000000000001",
		"item-1", []byte(`{"payload":1}`), nil, nil)
	if err != nil {
		t.Fatalf("InsertTriggerRecord on a fresh MemStore: %v", err)
	}
	if id == "" {
		t.Fatal("InsertTriggerRecord returned an empty record ID")
	}

	// The dedupe probe mirrors ON CONFLICT DO NOTHING on
	// (trigger_id, item_identifier): the same item yields the same row.
	again, err := store.InsertTriggerRecord(ctx, "00000000-0000-0000-0000-000000000001",
		"item-1", []byte(`{"payload":2}`), nil, nil)
	if err != nil {
		t.Fatalf("duplicate InsertTriggerRecord: %v", err)
	}
	if again != id {
		t.Fatalf("duplicate insert produced a new record %s, want the existing %s", again, id)
	}
}
