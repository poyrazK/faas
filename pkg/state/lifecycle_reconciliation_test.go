package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreListInstancesForLifecycleReconciliation(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "lifecycle@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "lifecycle", NodeID: "node-a"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	other, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "other", NodeID: "node-b"})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	deleted := AppDeleted
	if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}

	deletedInstance, err := store.CreateInstance(ctx, app.ID, "dep-1", string(StateRunning), 128, "node-a", "")
	if err != nil {
		t.Fatalf("CreateInstance deleted app: %v", err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, "dep-2", string(StateParked), 128, "node-a", ""); err != nil {
		t.Fatalf("CreateInstance parked: %v", err)
	}
	if _, err := store.CreateInstance(ctx, other.ID, "dep-3", string(StateRunning), 128, "node-b", ""); err != nil {
		t.Fatalf("CreateInstance other: %v", err)
	}

	rows, err := store.ListInstancesForLifecycleReconciliation(ctx, "node-a", 10)
	if err != nil {
		t.Fatalf("ListInstancesForLifecycleReconciliation: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != deletedInstance.ID {
		t.Fatalf("rows = %+v, want only deleted app's running instance", rows)
	}
	if rows, err := store.ListInstancesForLifecycleReconciliation(ctx, "node-b", 10); err != nil {
		t.Fatalf("node-b query: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("node-b rows = %+v, want none", rows)
	}
	if rows, err := store.ListInstancesForLifecycleReconciliation(ctx, "", 0); err != nil {
		t.Fatalf("zero-limit query: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("zero-limit rows = %+v, want none", rows)
	}

	if err := store.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := store.UpdateInstanceState(ctx, deletedInstance.ID, string(StateEvictingAccountDeleting)); err != nil {
		t.Fatalf("UpdateInstanceState evicting: %v", err)
	}
	rows, err = store.ListInstancesForLifecycleReconciliation(ctx, "node-a", 10)
	if err != nil {
		t.Fatalf("pending account query: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != deletedInstance.ID {
		t.Fatalf("pending account rows = %+v, want evicting instance", rows)
	}
}
