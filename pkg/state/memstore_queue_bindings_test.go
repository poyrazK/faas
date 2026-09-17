package state

import (
	"errors"
	"testing"
)

func TestMemStoreQueueBindingCRUDAndTenantScope(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "queue-bindings@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "worker", WorkloadClass: WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBinding(ctx, QueueBinding{
		AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders",
		Mode: "push", WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || string(created.RetryPolicyJSON) != "{}" {
		t.Fatalf("created binding defaults = %+v", created)
	}
	if _, err := store.CreateQueueBinding(ctx, QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "other", Mode: "pull", WorkloadClass: WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate binding err = %v, want ErrConflict", err)
	}
	updated, err := store.UpdateQueueBinding(ctx, acct.ID, app.ID, created.ID, UpdateQueueBindingParams{MaxConcurrency: intPtr(8)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxConcurrency != 8 {
		t.Fatalf("updated max_concurrency = %d, want 8", updated.MaxConcurrency)
	}
	if _, err := store.QueueBindingByID(ctx, "other-account", app.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account read err = %v, want ErrNotFound", err)
	}
	if err := store.DeleteQueueBinding(ctx, acct.ID, app.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueBindingByID(ctx, acct.ID, app.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted binding err = %v, want ErrNotFound", err)
	}
}

func intPtr(v int) *int { return &v }
