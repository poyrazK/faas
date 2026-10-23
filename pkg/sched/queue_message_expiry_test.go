// adr: 20261022105723
package sched_test

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// One retention sweep expires a waiting backlog larger than one batch.
func TestInvocationsRetentionExpiresWaitingQueueMessages(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "queue-expiry@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-expiry", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	const messages = 1500
	ids := make([]string, 0, messages)
	for range messages {
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue,
			Payload: json.RawMessage(`{}`), DueAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inv.ID)
	}
	retention := sched.NewInvocationsRetention(store, slog.New(slog.DiscardHandler)).
		WithClock(func() time.Time { return time.Now().Add(25 * time.Hour) })
	if _, _, err := retention.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		inv, err := store.InvocationByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if inv.State != state.InvocationDeadLetter {
			t.Fatalf("message %s is %s after a sweep a day later; want dead_letter", id, inv.State)
		}
	}
}
