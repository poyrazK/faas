package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// ADR-607: receipts retain captured IDs but reveal no current-owner metadata
// when a target changes accounts. Pre-snapshot legacy data remains unknown.
func TestEventReceiptHistoricalEvidencePrivacy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "receipt-privacy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "old-owner", RAMMB: 512})
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
	id := state.PublishedEventInvocationID(state.CanonicalMemUUID(account.ID), "orders", "privacy", recipient.ID)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: id, AppID: app.ID, AccountID: account.ID, State: state.InvocationFailed, Source: state.InvocationAsyncInvoke}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationReplay, ReplayedFromInvocationID: id}); err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	moved := (*store.Apps())[app.ID]
	moved.AccountID = uuid.NewString()
	moved.Slug = "new-owner-private-name"
	(*store.Apps())[app.ID] = moved
	(*store.Mu()).Unlock()
	receipt, err := store.EventReceipt(ctx, account.ID, "orders", "privacy", state.EventReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	entry := receipt.Recipients[0]
	if entry.AppID != app.ID || entry.AppSlug != "" || entry.Execution != nil || entry.Recovery != nil || entry.HandlerReplayMode != "" || entry.RoutingReplayEligible {
		t.Fatalf("leaked moved target: %+v", entry)
	}
	if _, err := store.EventReceiptReplays(ctx, account.ID, "orders", "privacy", recipient.ID, state.EventReceiptReplayCursor{}, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign target replay history: %v", err)
	}
	(*store.Mu()).Lock()
	legacy := (*store.EventFanout())[state.CanonicalMemUUID(account.ID)+"\x00orders\x00privacy"]
	legacy.SnapshotCaptured = false
	legacy.RecipientSnapshot = nil
	(*store.Mu()).Unlock()
	receipt, err = store.EventReceipt(ctx, account.ID, "orders", "privacy", state.EventReceiptCursor{}, 10)
	if err != nil || receipt.SnapshotCaptured || len(receipt.Recipients) != 0 || receipt.RecipientCount != 0 || len(receipt.RoutingSummary) != 0 {
		t.Fatalf("invented legacy evidence: %+v %v", receipt, err)
	}
}

func TestEventReceiptReplayMemoryRetentionAndTies(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "receipt-memory-retention@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "memory-retention", RAMMB: 512})
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
	root := state.PublishedEventInvocationID(state.CanonicalMemUUID(account.ID), "orders", "retention", sub)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke, State: state.InvocationFailed}); err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationReplay, ReplayedFromInvocationID: root, State: state.InvocationFailed})
	if err != nil {
		t.Fatal(err)
	}
	*first.ReplayRootCreatedAt = time.Time{} // returned metadata must not alias the ledger
	second, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationReplay, ReplayedFromInvocationID: first.ID, State: state.InvocationFailed})
	if err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	delete((*store.Invocations()), root)
	delete((*store.Invocations()), first.ID)
	(*store.Mu()).Unlock()
	third, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationReplay, ReplayedFromInvocationID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	row := (*store.Invocations())[third.ID]
	row.CreatedAt = second.CreatedAt
	(*store.Invocations())[third.ID] = row
	(*store.Mu()).Unlock()
	page, err := store.EventReceiptReplays(ctx, account.ID, "orders", "retention", sub, state.EventReceiptReplayCursor{}, 1)
	if err != nil || len(page.Replays) != 1 || page.NextCursor.OutboxID == 0 {
		t.Fatalf("tied page: %+v %v", page, err)
	}
	older, err := store.EventReceiptReplays(ctx, account.ID, "orders", "retention", sub, page.NextCursor, 1)
	if err != nil || len(older.Replays) != 1 || older.Replays[0].InvocationID == page.Replays[0].InvocationID || older.NextCursor.OutboxID != 0 {
		t.Fatalf("tied continuation: %+v %v", older, err)
	}
	receipt, err := store.EventReceipt(ctx, account.ID, "orders", "retention", state.EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Execution != nil || receipt.Recipients[0].Recovery == nil || receipt.Recipients[0].Recovery.RetainedReplayCount != 2 {
		t.Fatalf("retained descendants: %+v %v", receipt, err)
	}
	(*store.Mu()).Lock()
	(*store.EventFanout())[state.CanonicalMemUUID(account.ID)+"\x00orders\x00retention"].CreatedAt = time.Now().Add(time.Second)
	(*store.Mu()).Unlock()
	receipt, err = store.EventReceipt(ctx, account.ID, "orders", "retention", state.EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Recovery != nil {
		t.Fatalf("old lineage attached to reused identity: %+v %v", receipt, err)
	}
}
