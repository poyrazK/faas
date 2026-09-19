package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreMatchingEventSubscriptionsPagesCandidates(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "event-candidates@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-candidates"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{
		{"orders", "order.created"},
		{"billing.*", "invoice.*"},
		{"*stripe", "*.paid"},
		{"*", "*"},
		{"support", "ticket.created"},
	} {
		if _, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, row[0], row[1], nil); err != nil {
			t.Fatal(err)
		}
	}

	var got []EventSubscription
	cursor := EventSubscriptionCursor{}
	for {
		page, err := store.ListMatchingEventSubscriptionsForAccount(ctx, accountID, "billing.stripe", "invoice.paid", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page...)
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		cursor = EventSubscriptionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(got) != 3 {
		t.Fatalf("candidate count = %d, want 3", len(got))
	}
	for _, row := range got {
		if row.Source == "support" {
			t.Fatalf("non-matching subscription returned: %+v", row)
		}
	}
}

func TestMemStoreEventSubscriptionDeadLetterOrigin(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "event-dlq@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	accountID := canonicalMemUUID(account.ID)
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: accountID, Slug: "event-dlq"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inv, err := store.EnqueueInvocation(ctx, Invocation{
		ID:          uuid.NewString(),
		AppID:       app.ID,
		AccountID:   accountID,
		Source:      InvocationAsyncInvoke,
		State:       InvocationDeadLetter,
		Payload:     json.RawMessage(`{"id":"evt-1"}`),
		Headers:     json.RawMessage(`{"x-gregale-event-id":"evt-1"}`),
		CreatedAt:   now,
		CompletedAt: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListDeadLetterEvents(ctx, app.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SourceID != inv.ID || events[0].Origin != "event_subscription" {
		t.Fatalf("dead-letter projection = %+v, want event_subscription origin", events)
	}
}
