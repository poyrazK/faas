package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

// ADR-582: receipts retain captured IDs but reveal no current-owner metadata
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
	if entry.AppID != app.ID || entry.AppSlug != "" || entry.Execution != nil || entry.HandlerReplayMode != "" || entry.RoutingReplayEligible {
		t.Fatalf("leaked moved target: %+v", entry)
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
