package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func failReceiptInvocation(t *testing.T, store recipientClaimTestStore, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.ClaimInvocation(ctx, id, "replay-test", 30); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, id, "original handler failure", 0, 0); err != nil {
		t.Fatal(err)
	}
}

func TestEventReceiptReplayLineageAndPagination(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 3)
		for i, recipient := range work.RecipientSnapshot {
			ids[i] = state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", recipient.ID)
			if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: ids[i], AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			finishRecipient(t, store, claim, state.PublishedEventRecipientEnqueued)
		}
		if _, err := store.ClaimInvocation(ctx, ids[0], "replay-test", 30); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInvocation(ctx, ids[0], nil); err != nil {
			t.Fatal(err)
		}
		failReceiptInvocation(t, store, ids[1])
		sub := work.RecipientSnapshot[1].ID
		// Copied guest headers and a supplied root are not trusted lineage.
		forged, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayRootInvocationID: ids[1],
			Headers: []byte(`{"x-gregale-event-id":"evt-three-consumers","x-gregale-event-source":"orders","x-gregale-event-subscription-id":"` + sub + `"}`), DueAt: time.Now()})
		if err != nil || forged.ReplayRootInvocationID != "" {
			t.Fatalf("trusted forged lineage: %+v %v", forged, err)
		}
		createReplay := func(parent string) state.Invocation {
			t.Helper()
			inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: parent,
				ReplayRootInvocationID: ids[0], DueAt: time.Now()}) // a caller-supplied root cannot override the ledger
			if err != nil {
				t.Fatal(err)
			}
			if inv.ReplayRootInvocationID != ids[1] || inv.ReplayedFromInvocationID != parent || inv.ReplayRootCreatedAt == nil {
				t.Fatalf("lineage: %+v", inv)
			}
			read, err := store.InvocationByID(ctx, inv.ID)
			if err != nil || read.ReplayRootInvocationID != ids[1] || read.ReplayedFromInvocationID != parent {
				t.Fatalf("persisted lineage: %+v %v", read, err)
			}
			return inv
		}
		first := createReplay(ids[1])
		receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		entry := receipt.Recipients[1]
		if entry.Execution.State != "failed" || entry.Execution.LastError == "" || entry.Recovery.RetainedReplayCount != 1 || entry.Recovery.LatestReplay.State != "pending" || entry.HandlerReplayMode != "" {
			t.Fatalf("active recovery: %+v", entry)
		}
		failReceiptInvocation(t, store, first.ID)
		second := createReplay(first.ID)
		failReceiptInvocation(t, store, second.ID)
		page, err := store.EventReceiptReplays(ctx, account, "orders", "evt-three-consumers", sub, state.EventReceiptReplayCursor{}, 1)
		if err != nil || len(page.Replays) != 1 || page.Replays[0].InvocationID != second.ID || page.NextCursor.OutboxID == 0 {
			t.Fatalf("first page: %+v %v", page, err)
		}
		third := createReplay(second.ID)
		if _, err := store.ClaimInvocation(ctx, third.ID, "replay-test", 30); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInvocation(ctx, third.ID, nil); err != nil {
			t.Fatal(err)
		}
		older, err := store.EventReceiptReplays(ctx, account, "orders", "evt-three-consumers", sub, page.NextCursor, 1)
		if err != nil || len(older.Replays) != 1 || older.Replays[0].InvocationID != first.ID || older.NextCursor.OutboxID != 0 {
			t.Fatalf("page changed after newer replay: %+v %v", older, err)
		}
		receipt, err = store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
		if err != nil {
			t.Fatal(err)
		}
		entry = receipt.Recipients[1]
		if receipt.Recipients[0].Execution.State != "completed" || receipt.Recipients[0].Recovery != nil || entry.Execution.State != "failed" || entry.Recovery.LatestReplay.State != "completed" || entry.Recovery.RetainedReplayCount != 3 || entry.HandlerReplayMode != "" {
			t.Fatalf("recovered consumer: %+v", receipt)
		}
		for _, probe := range []struct{ account, source, sub string }{{uuid.NewString(), "orders", sub}, {account, "foreign", sub}, {account, "orders", "foreign"}} {
			if _, err := store.EventReceiptReplays(ctx, probe.account, probe.source, "evt-three-consumers", probe.sub, state.EventReceiptReplayCursor{}, 1); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign history: %v", err)
			}
		}
		stale := page.NextCursor
		stale.OutboxID++
		if _, err := store.EventReceiptReplays(ctx, account, "orders", "evt-three-consumers", sub, stale, 1); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale cursor: %v", err)
		}
	})
}

func TestEventReceiptReplayAdmissionRejectsForeignOrActiveParent(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, _ := seedRecipientClaims(t, store)
		parent, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		for _, inv := range []state.Invocation{
			{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: parent.ID},
			{AppID: app, AccountID: uuid.NewString(), Source: state.InvocationReplay, ReplayedFromInvocationID: parent.ID},
			{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: uuid.NewString()},
		} {
			if _, err := store.EnqueueInvocation(ctx, inv); err == nil {
				t.Fatalf("accepted invalid parent: %+v", inv)
			}
		}
		failReceiptInvocation(t, store, parent.ID)
		for _, change := range []func(*state.Invocation){func(i *state.Invocation) { i.Source = state.InvocationAsyncInvoke }, func(i *state.Invocation) { i.DeploymentScope = "foreign" }} {
			inv := state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: parent.ID}
			change(&inv)
			if _, err := store.EnqueueInvocation(ctx, inv); err == nil {
				t.Fatalf("accepted different namespace: %+v", inv)
			}
		}
	})
}

func TestEventReceiptReplaySurvivesExecutionRetentionPostgres(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, app, work := seedRecipientClaims(t, store)
	sub := work.RecipientSnapshot[0].ID
	root := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", sub)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	failReceiptInvocation(t, store, root)
	first, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: root, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	failReceiptInvocation(t, store, first.ID)
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET created_at=now()+interval '1 second' WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}
	newAcceptance, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1)
	if err != nil || newAcceptance.Recipients[0].Execution != nil || newAcceptance.Recipients[0].Recovery != nil || newAcceptance.Recipients[0].HandlerReplayMode != "" {
		t.Fatalf("old execution attached to new acceptance: %+v %v", newAcceptance, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET created_at=$1 WHERE id=$2", work.CreatedAt, work.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: first.ID, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.EventReceiptReplays(ctx, account, "orders", "evt-three-consumers", sub, state.EventReceiptReplayCursor{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM invocations WHERE id=ANY($1::uuid[])", []string{root, second.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: root, AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: first.ID, DueAt: time.Now()}); err == nil {
		t.Fatal("accepted a descendant whose ID equals its retained root")
	}
	older, err := store.EventReceiptReplays(ctx, account, "orders", "evt-three-consumers", sub, page.NextCursor, 10)
	if err != nil || len(older.Replays) != 1 || older.Replays[0].InvocationID != first.ID {
		t.Fatalf("expired cursor anchor: %+v %v", older, err)
	}
	third, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app, AccountID: account, Source: state.InvocationReplay, ReplayedFromInvocationID: first.ID, DueAt: time.Now()})
	if err != nil || third.ReplayRootInvocationID != root {
		t.Fatalf("root lost: %+v %v", third, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM invocations WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Execution != nil || receipt.Recipients[0].Recovery.LatestReplay.InvocationID != third.ID {
		t.Fatalf("expired execution: %+v %v", receipt, err)
	}
	// Identity reuse cannot attach an old root, even when its replay is newer.
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET created_at=now()+interval '1 second' WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err = store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 1)
	if err != nil || receipt.Recipients[0].Recovery != nil {
		t.Fatalf("old recovery attached to new acceptance: %+v %v", receipt, err)
	}
}

// adr: 601
func TestEventReceiptPlainReplayDeduplicationAndRetention(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		rootID := state.PublishedEventInvocationID(account, "orders", "evt-three-consumers", work.RecipientSnapshot[0].ID)
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: rootID, AccountID: account, AppID: app,
			Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		failReceiptInvocation(t, store, rootID)
		replayer := store.(state.PlainInvocationReplayStore)
		read := func() state.EventReceiptRecipient {
			t.Helper()
			receipt, err := store.EventReceipt(ctx, account, "orders", "evt-three-consumers", state.EventReceiptCursor{}, 200)
			if err != nil {
				t.Fatal(err)
			}
			return receipt.Recipients[0]
		}
		if read().HandlerReplayMode != "handler_replay" {
			t.Fatal("failed plain consumer has no recovery action")
		}
		child, err := replayer.ReplayPlainInvocation(ctx, account, rootID, state.PlainInvocationReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		failReceiptInvocation(t, store, child.ID)
		failed := read()
		if failed.Execution.State != "failed" || failed.Recovery == nil || failed.HandlerReplayMode != "handler_replay" || failed.HandlerReplayInvocationID != child.ID {
			t.Fatalf("latest failed child not recoverable: %+v", failed)
		}
		next, err := replayer.ReplayPlainInvocation(ctx, account, child.ID, state.PlainInvocationReplayOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DeleteInvocationsByIDs(ctx, []string{next.ID}); err != nil {
			t.Fatal(err)
		}
		if pruned := read(); pruned.Recovery.LatestReplay.InvocationID != child.ID || pruned.HandlerReplayMode != "" {
			t.Fatalf("latest retained parent offered redundant replay: %+v", pruned)
		}
		if _, err := store.DeleteInvocationsByIDs(ctx, []string{child.ID}); err != nil {
			t.Fatal(err)
		}
		if pruned := read(); pruned.Recovery != nil || pruned.Execution.State != "failed" || pruned.HandlerReplayMode != "" {
			t.Fatalf("retained original offered redundant replay: %+v", pruned)
		}
	})
}
