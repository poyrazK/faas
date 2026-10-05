package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// ADR-596: pending recipients exist before invocations, summary counts cover
// the whole snapshot, and mutable routing progress cannot reorder pages.
func TestEventReceiptRoutingPaginationAndRecovery(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		first, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !first.SnapshotCaptured || first.RecipientCount != 3 || first.RoutingSummary["pending"] != 3 || len(first.Recipients) != 1 || first.NextPosition != 1 {
			t.Fatalf("pending receipt: %+v", first)
		}
		if first.Recipients[0].Execution != nil || first.Recipients[0].ExecutionUnavailable != "not_enqueued" {
			t.Fatalf("pending execution: %+v", first.Recipients[0])
		}
		accepted, err := store.EventReceiptAcceptedAt(ctx, account, "orders", "evt-three-consumers")
		if err != nil || !accepted.Equal(first.AcceptedAt) {
			t.Fatalf("acceptance = %v, %v", accepted, err)
		}
		if err := store.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		claims := make(map[string]*state.PublishedEventRecipientWork)
		for range 3 {
			claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			claims[claim.Recipient.ID] = claim
		}
		// Complete one handler, leave a second route retrying, fail a third route.
		successful, pending, failed := work.RecipientSnapshot[0].ID, work.RecipientSnapshot[1].ID, work.RecipientSnapshot[2].ID
		invocationID := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", successful)
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: invocationID, AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/events", DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimInvocation(ctx, invocationID, "receipt-instance", 30); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInvocation(ctx, invocationID, nil); err != nil {
			t.Fatal(err)
		}
		finishRecipient(t, store, claims[successful], state.PublishedEventRecipientEnqueued)
		finishRecipient(t, store, claims[pending], state.PublishedEventRecipientPending)
		finishRecipient(t, store, claims[failed], state.PublishedEventRecipientFailed)
		receipt, err := store.EventReceipt(ctx, strings.ReplaceAll(account, "-", ""), "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.RoutingSummary["enqueued"] != 1 || receipt.RoutingSummary["pending"] != 1 || receipt.RoutingSummary["failed"] != 1 || receipt.RoutingSettledAt != nil || receipt.RetainUntil != nil {
			t.Fatalf("routing summary: %+v", receipt)
		}
		execution := receipt.Recipients[0].Execution
		if execution == nil || execution.InvocationID != invocationID || execution.State != "completed" || execution.Attempts != 1 || execution.CompletedAt == nil {
			t.Fatalf("execution: %+v", execution)
		}
		if receipt.Recipients[1].Routing.NextAttemptAt == nil || *receipt.Recipients[1].Routing.GenerationAttempts != 1 {
			t.Fatalf("retry: %+v", receipt.Recipients[1])
		}
		if !receipt.Recipients[2].RoutingReplayEligible || receipt.Recipients[2].Routing.LastError == "" {
			t.Fatalf("failed route: %+v", receipt.Recipients[2])
		}
		if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, account, app, "orders", "evt-three-consumers", failed); err != nil {
			t.Fatal(err)
		}
		page, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{OutboxID: first.OutboxID, Position: first.NextPosition}, 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Recipients) != 2 || page.Recipients[0].SubscriptionID != pending || page.Recipients[1].SubscriptionID != failed || page.Recipients[1].Routing.ReplayCount != 1 || *page.Recipients[1].Routing.Generation != 2 || *page.Recipients[1].Routing.GenerationAttempts != 0 {
			t.Fatalf("replay page: %+v", page)
		}
		if _, err := store.EventReceipt(ctx, uuid.NewString(), "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign account: %v", err)
		}
		if _, err := store.EventReceipt(ctx, account, "different-source", "evt-three-consumers", state.EventReceiptCursor{}, 1); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign source: %v", err)
		}
		if _, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{OutboxID: first.OutboxID + 1, Position: 1}, 1); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale cursor: %v", err)
		}
	})
}

// adr: 596
// Recovery actions use the retained unified DLQ identity for original and
// replayed executions; purged records must not advertise a broken action.
func TestEventReceiptDeadLetterRecoveryIdentity(t *testing.T) {
	for _, lineage := range []string{"original", "replay"} {
		t.Run(lineage, func(t *testing.T) {
			forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
				ctx, account, app, work := seedRecipientClaims(t, store)
				root := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", work.RecipientSnapshot[0].ID)
				inv, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
				if err != nil {
					t.Fatal(err)
				}
				if lineage == "replay" {
					failReceiptInvocation(t, store, root)
					inv, err = store.(state.PlainInvocationReplayStore).ReplayPlainInvocation(ctx, account, root, state.PlainInvocationReplayOptions{})
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.ClaimInvocation(ctx, inv.ID, "receipt-dead-letter", 30); err != nil {
					t.Fatal(err)
				}
				if err := store.FailInvocation(ctx, inv.ID, "handler exhausted", 0, 1); err != nil {
					t.Fatal(err)
				}
				deadLetters, err := store.ListDeadLetterEvents(ctx, app, 10, "")
				if err != nil || len(deadLetters) != 1 || deadLetters[0].SourceID != inv.ID {
					t.Fatalf("dead-letter projection: %+v %v", deadLetters, err)
				}
				receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
				if err != nil {
					t.Fatal(err)
				}
				entry := receipt.Recipients[0]
				if entry.HandlerReplayMode != "dead_letter_replay" || entry.HandlerReplayDeadLetterID != deadLetters[0].ID || lineage == "replay" && entry.HandlerReplayInvocationID != inv.ID {
					t.Fatalf("dead-letter recovery identity: %+v", entry)
				}
				if err := store.DeleteDeadLetterEvent(ctx, account, app, deadLetters[0].ID); err != nil {
					t.Fatal(err)
				}
				receipt, err = store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
				if err != nil || receipt.Recipients[0].HandlerReplayMode != "" {
					t.Fatalf("purged dead letter advertised recovery: %+v %v", receipt, err)
				}
			})
		})
	}
}

// ADR-596: keyed work outcomes and cancellation receipts are actual delivery
// evidence; an enqueued route alone cannot assert a successful execution.
func TestEventReceiptWorkOutcomesAndRetention(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		policy := workpolicy.Policy{Name: "receipt-latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
		ids := make([]string, 3)
		for i, recipient := range work.RecipientSnapshot {
			ids[i] = state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", recipient.ID)
		}
		for _, id := range ids[:2] {
			if _, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: id, AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now().Add(time.Hour)}, policy, "s:same-key"); err != nil {
				t.Fatal(err)
			}
		}
		cancellation, err := store.CancelPendingKeyedInvocations(ctx, app, policy.Name, "s:same-key", ids[2])
		if err != nil {
			t.Fatal(err)
		}
		if cancellation.CancelledCount != 1 {
			t.Fatalf("cancelled count: %+v", cancellation)
		}
		for range 3 {
			claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			finishRecipient(t, store, claim, state.PublishedEventRecipientEnqueued)
		}
		receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.RoutingSettledAt == nil || receipt.RetainUntil == nil || receipt.RetainUntil.Sub(*receipt.RoutingSettledAt) != state.PublishedEventIdentityRetention {
			t.Fatalf("settled retention: %+v", receipt)
		}
		if receipt.Recipients[0].Execution.State != "superseded" || receipt.Recipients[1].Execution.State != "cancelled" || receipt.Recipients[2].Cancellation == nil || receipt.Recipients[2].Execution != nil || receipt.Recipients[2].ExecutionUnavailable != "cancel_pending" {
			t.Fatalf("work outcomes: %+v", receipt.Recipients)
		}
		// Caller mutation must not change the ledger through a returned pointer.
		*receipt.Recipients[0].Execution.CompletedAt = time.Time{}
		fresh, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1)
		if err != nil || fresh.Recipients[0].Execution.CompletedAt.IsZero() {
			t.Fatalf("aliased execution: %+v %v", fresh, err)
		}
		pruned, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(state.PublishedEventIdentityRetention+time.Hour), 1)
		if err != nil || pruned != 1 {
			t.Fatalf("prune: %d %v", pruned, err)
		}
		if _, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("pruned receipt: %v", err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account, work.Payload); err != nil {
			t.Fatal(err)
		}
		if _, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{OutboxID: receipt.OutboxID, Position: 1}, 1); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("reused identity cursor: %v", err)
		}
	})
}

// ADR-596: a historical receipt cannot join current metadata across tenants,
// and old NULL snapshots must not be presented as a known empty recipient set.
func TestEventReceiptLegacyAndForeignTargetPostgres(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, app, work := seedRecipientClaims(t, store)
	id := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", work.RecipientSnapshot[0].ID)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: id, AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "receipt-other-owner@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE apps SET account_id=$1, slug='new-owner-private-name' WHERE id=$2", other.ID, app); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range receipt.Recipients {
		if entry.AppID != app || entry.AppSlug != "" || entry.Execution != nil || entry.RoutingReplayEligible || entry.HandlerReplayMode != "" {
			t.Fatalf("foreign target leak: %+v", entry)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET recipient_snapshot=NULL WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err = store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 10)
	if err != nil || receipt.SnapshotCaptured || receipt.RecipientCount != 0 || len(receipt.Recipients) != 0 || len(receipt.RoutingSummary) != 0 {
		t.Fatalf("invented legacy membership: %+v %v", receipt, err)
	}
}

// ADR-596: active leases and filtered candidates remain inspectable; missing
// original invocation evidence after a handled route is explicitly unavailable.
func TestEventReceiptActiveFilteredAndMissingExecution(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, _, work := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, work, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		claims := make([]*state.PublishedEventRecipientWork, 0, 3)
		for range 3 {
			claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			claims = append(claims, claim)
		}
		active, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		if active.RoutingSummary["processing"] != 3 || active.RoutingSettledAt != nil {
			t.Fatalf("active receipt: %+v", active)
		}
		for _, entry := range active.Recipients {
			if entry.Routing.LeaseUntil == nil || entry.Routing.NextAttemptAt != nil || entry.Execution != nil {
				t.Fatalf("leased recipient: %+v", entry)
			}
		}
		for i, claim := range claims {
			status := state.PublishedEventRecipientFiltered
			if i == 0 {
				status = state.PublishedEventRecipientEnqueued
			}
			finishRecipient(t, store, claim, status)
		}
		settled, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		if settled.RoutingSummary["enqueued"] != 1 || settled.RoutingSummary["filtered"] != 2 || settled.RoutingSettledAt == nil {
			t.Fatalf("settled receipt: %+v", settled)
		}
		for _, entry := range settled.Recipients {
			want := "not_enqueued"
			if entry.Routing.State == state.PublishedEventRecipientEnqueued {
				want = "record_unavailable"
			}
			if entry.ExecutionUnavailable != want || entry.Execution != nil || entry.HandlerReplayMode != "" {
				t.Fatalf("invented execution result: %+v", entry)
			}
		}
	})
}
