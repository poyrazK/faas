package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

// ADR-619: receipts retain captured IDs but reveal no current-owner metadata
// when a target changes accounts. Pre-snapshot legacy data remains unknown.
func TestEventReceiptHistoricalEvidencePrivacy(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "receipt-privacy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: account.ID, Slug: "old-owner", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "created", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, []byte(`{"id":"privacy","source":"orders","type":"created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recipient := work.RecipientSnapshot[0]
	id := PublishedEventInvocationID(canonicalMemUUID(account.ID), "orders", "privacy", recipient.ID)
	if _, err := store.EnqueueInvocation(ctx, Invocation{ID: id, AppID: app.ID, AccountID: account.ID, State: InvocationFailed, Source: InvocationAsyncInvoke}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationReplay, ReplayedFromInvocationID: id}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	moved := store.apps[app.ID]
	moved.AccountID = uuid.NewString()
	moved.Slug = "new-owner-private-name"
	store.apps[app.ID] = moved
	store.mu.Unlock()
	receipt, err := store.EventReceipt(ctx, account.ID, "orders", "privacy", EventReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	entry := receipt.Recipients[0]
	if entry.AppID != app.ID || entry.AppSlug != "" || entry.Execution != nil || entry.Recovery != nil || entry.HandlerReplayMode != "" || entry.RoutingReplayEligible {
		t.Fatalf("leaked moved target: %+v", entry)
	}
	if _, err := store.EventReceiptReplays(ctx, account.ID, "orders", "privacy", recipient.ID, EventReceiptReplayCursor{}, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target replay history: %v", err)
	}
	store.mu.Lock()
	legacy := store.eventFanout[canonicalMemUUID(account.ID)+"\x00orders\x00privacy"]
	legacy.SnapshotCaptured = false
	legacy.RecipientSnapshot = nil
	store.mu.Unlock()
	receipt, err = store.EventReceipt(ctx, account.ID, "orders", "privacy", EventReceiptCursor{}, 10)
	if err != nil || receipt.SnapshotCaptured || len(receipt.Recipients) != 0 || receipt.RecipientCount != 0 || len(receipt.RoutingSummary) != 0 {
		t.Fatalf("invented legacy evidence: %+v %v", receipt, err)
	}
}

func TestEventReceiptReplayMemoryRetentionAndTies(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "receipt-memory-retention@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: account.ID, Slug: "memory-retention", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "created", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, []byte(`{"id":"retention","source":"orders","type":"created","data":{}}`)); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sub := work.RecipientSnapshot[0].ID
	root := PublishedEventInvocationID(canonicalMemUUID(account.ID), "orders", "retention", sub)
	if _, err := store.EnqueueInvocation(ctx, Invocation{ID: root, AppID: app.ID, AccountID: account.ID, Source: InvocationAsyncInvoke, State: InvocationFailed}); err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationReplay, ReplayedFromInvocationID: root, State: InvocationFailed})
	if err != nil {
		t.Fatal(err)
	}
	*first.ReplayRootCreatedAt = time.Time{} // returned metadata must not alias the ledger
	second, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationReplay, ReplayedFromInvocationID: first.ID, State: InvocationFailed})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	delete(store.invocations, root)
	delete(store.invocations, first.ID)
	store.mu.Unlock()
	third, err := store.EnqueueInvocation(ctx, Invocation{AppID: app.ID, AccountID: account.ID, Source: InvocationReplay, ReplayedFromInvocationID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	row := store.invocations[third.ID]
	row.CreatedAt = second.CreatedAt
	store.invocations[third.ID] = row
	store.mu.Unlock()
	page, err := store.EventReceiptReplays(ctx, account.ID, "orders", "retention", sub, EventReceiptReplayCursor{}, 1)
	if err != nil || len(page.Replays) != 1 || page.NextCursor.OutboxID == 0 {
		t.Fatalf("tied page: %+v %v", page, err)
	}
	older, err := store.EventReceiptReplays(ctx, account.ID, "orders", "retention", sub, page.NextCursor, 1)
	if err != nil || len(older.Replays) != 1 || older.Replays[0].InvocationID == page.Replays[0].InvocationID || older.NextCursor.OutboxID != 0 {
		t.Fatalf("tied continuation: %+v %v", older, err)
	}
	receipt, err := store.EventReceipt(ctx, account.ID, "orders", "retention", EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Execution != nil || receipt.Recipients[0].Recovery == nil || receipt.Recipients[0].Recovery.RetainedReplayCount != 2 {
		t.Fatalf("retained descendants: %+v %v", receipt, err)
	}
	store.mu.Lock()
	store.eventFanout[canonicalMemUUID(account.ID)+"\x00orders\x00retention"].CreatedAt = time.Now().Add(time.Second)
	store.mu.Unlock()
	receipt, err = store.EventReceipt(ctx, account.ID, "orders", "retention", EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Recovery != nil {
		t.Fatalf("old lineage attached to reused identity: %+v %v", receipt, err)
	}
}
