package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A message in retry back-off or nacked with a delay counted toward worker
// demand and the oldest-age gauge although no worker could claim it before
// it was due (bug hunt 2026-10-28 B18).
func TestQueueStateCountsOnlyDueMessages(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "queue-due@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "queue-due", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: app.ID, AccountID: acct.ID, Source: state.InvocationQueue, DeploymentScope: "production",
		Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.QueueStateInScope(ctx, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Depth != 0 || !stats.OldestPendingAt.IsZero() {
		t.Fatalf("a message due in an hour: depth %d, oldest %v; want no demand", stats.Depth, stats.OldestPendingAt)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: app.ID, AccountID: acct.ID, Source: state.InvocationQueue, DeploymentScope: "production",
		Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if stats, err = store.QueueStateInScope(ctx, app.ID, "production"); err != nil || stats.Depth != 1 || stats.OldestPendingAt.IsZero() {
		t.Fatalf("a due message: depth %d, oldest %v, err %v; want one", stats.Depth, stats.OldestPendingAt, err)
	}
}
