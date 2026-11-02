package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListInstancesForLifecycleReconciliation runs the deletion sweep's SQL
// against real rows: a deleted app's live instances, an account deletion's
// live and evicting instances, the owner-node filter, and one row for an
// instance that matches both causes.
func TestPgListInstancesForLifecycleReconciliation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)

	account, app := seedPgAccountAndApp(t, s, ctx)
	_, other := seedPgAccountAndApp(t, s, ctx)
	for _, appID := range []string{app.ID, other.ID} {
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
	if got := ids(node, 10); len(got) != 1 || got[0] != running.ID {
		t.Fatalf("deleted app of a deleting account = %v, want %s once", got, running.ID)
	}
	if err := s.UpdateInstanceState(ctx, running.ID, string(state.StateEvictingAccountDeleting)); err != nil {
		t.Fatalf("UpdateInstanceState evicting: %v", err)
	}
	if got := ids(node, 10); len(got) != 1 || got[0] != running.ID {
		t.Fatalf("evicting instance = %v, want %s", got, running.ID)
	}
}
