package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSoftDeleteRetiresQueueBindingsMem(t *testing.T) {
	softDeleteRetiresQueueBindingsSuite(t, state.NewMemStore())
}

func TestSoftDeleteRetiresQueueBindingsPG(t *testing.T) {
	store, _ := pgStore(t)
	softDeleteRetiresQueueBindingsSuite(t, store)
}

// Production 2026-11-09 kept a deleted app's pull binding enabled until purge.
// Soft delete retires the app's queue bindings in the same transaction as its
// crons, so a deleted app consumes nothing.
func softDeleteRetiresQueueBindingsSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@soft-delete-queues.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "softq-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "events", QueueName: "events",
		Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	history, ok := store.(interface {
		QueueBindingHistoryByID(context.Context, string, string, string) (state.QueueBinding, error)
	})
	if !ok {
		t.Fatal("store lacks queue binding history")
	}
	got, err := history.QueueBindingHistoryByID(ctx, account.ID, app.ID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.RetiredAt == nil {
		t.Fatalf("deleted app's queue binding still active: enabled=%t retired_at=%v", got.Enabled, got.RetiredAt)
	}
}
