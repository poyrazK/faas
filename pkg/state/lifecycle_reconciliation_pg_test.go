package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListInstancesForLifecycleReconciliation runs the deletion sweep's SQL
// against real rows: a deleted app's live instances, an account deletion's
// live and evicting instances (warm and frozen VMs of the account's other
// apps included), the owner-node filter, and one row for an instance that
// matches both causes.
func TestPgListInstancesForLifecycleReconciliation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)

	account, app := seedPgAccountAndApp(t, s, ctx)
	_, other := seedPgAccountAndApp(t, s, ctx)
	// An app of the same account that is not deleted itself: only the
	// account branch of the sweep can find its VMs.
	live, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "s-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateApp live: %v", err)
	}
	for _, appID := range []string{app.ID, other.ID, live.ID} {
		if _, err := pool.Exec(ctx, `update apps set node_id = $2::uuid where id = $1`, appID, node); err != nil {
			t.Fatalf("own app: %v", err)
		}
	}
	running, err := s.CreateInstance(ctx, app.ID, seedPgDeployment(t, s, ctx, app).ID, string(state.StateRunning), 128, node, "")
	if err != nil {
		t.Fatalf("CreateInstance running: %v", err)
	}
	if _, err := s.CreateInstance(ctx, app.ID, seedPgDeployment(t, s, ctx, app).ID, string(state.StateParked), 128, node, ""); err != nil {
		t.Fatalf("CreateInstance parked: %v", err)
	}
	if _, err := s.CreateInstance(ctx, other.ID, seedPgDeployment(t, s, ctx, other).ID, string(state.StateRunning), 128, node, ""); err != nil {
		t.Fatalf("CreateInstance other: %v", err)
	}
	// Warm and frozen VMs hold memory like running ones (MemStore lists
	// every IsLive state for a deleting account).
	accountWide := map[string]bool{running.ID: true}
	for _, st := range []state.State{state.StateWarm, state.StateFrozen} {
		ins, err := s.CreateInstance(ctx, live.ID, seedPgDeployment(t, s, ctx, live).ID, string(st), 128, node, "")
		if err != nil {
			t.Fatalf("CreateInstance %s: %v", st, err)
		}
		accountWide[ins.ID] = true
	}
	ids := func(nodeID string, limit int) []string {
		t.Helper()
		rows, err := s.ListInstancesForLifecycleReconciliation(ctx, nodeID, limit)
		if err != nil {
			t.Fatalf("ListInstancesForLifecycleReconciliation(%q): %v", nodeID, err)
		}
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}
	accountRows := func(label string) {
		t.Helper()
		got := ids(node, 10)
		if len(got) != len(accountWide) {
			t.Fatalf("%s = %v, want %v", label, got, accountWide)
		}
		for _, id := range got {
			if !accountWide[id] {
				t.Fatalf("%s = %v, want %v", label, got, accountWide)
			}
		}
	}
	if got := ids(node, 10); len(got) != 0 {
		t.Fatalf("before any deletion = %v, want none", got)
	}

	deleted := state.AppDeleted
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}
	for _, nodeID := range []string{node, ""} {
		if got := ids(nodeID, 10); len(got) != 1 || got[0] != running.ID {
			t.Fatalf("deleted app (node %q) = %v, want only %s", nodeID, got, running.ID)
		}
	}
	if got := ids("00000000-0000-0000-0000-00000000beef", 10); len(got) != 0 {
		t.Fatalf("another node = %v, want none", got)
	}
	if got := ids(node, 0); len(got) != 0 {
		t.Fatalf("zero limit = %v, want none", got)
	}

	if err := s.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	accountRows("deleting account")
	if err := s.UpdateInstanceState(ctx, running.ID, string(state.StateEvictingAccountDeleting)); err != nil {
		t.Fatalf("UpdateInstanceState evicting: %v", err)
	}
	accountRows("deleting account with an evicting instance")
}
