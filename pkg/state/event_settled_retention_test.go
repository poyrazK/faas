package state_test

// adr: 20261023215834

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// settleStoragePublishes claims and settles every due receipt.
func settleStoragePublishes(t *testing.T, store eventStorageTestStore, n int) {
	t.Helper()
	ctx := context.Background()
	for range n {
		work, err := store.ClaimDuePublishedEvent(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSettledReceiptsPruneAtTheirPlanWindow(t *testing.T) {
	forEventStorageStores(t, func(t *testing.T, store eventStorageTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		free, err := store.CreateAccount(ctx, "settled-free-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		pro, err := store.CreateAccount(ctx, "settled-pro-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		for _, account := range []state.Account{free, pro} {
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("settled", "orders", "{}")); err != nil {
				t.Fatal(err)
			}
		}
		settleStoragePublishes(t, store, 2)
		settled := time.Now()
		day, week := 24*time.Hour, 7*24*time.Hour

		receipt, err := store.EventReceipt(ctx, free.ID, "orders", "settled", state.EventReceiptCursor{}, 10)
		if err != nil || receipt.RetainUntil == nil || receipt.RetainUntil.Sub(*receipt.RoutingSettledAt) != day {
			t.Fatalf("free receipt retention: %+v %v", receipt, err)
		}
		usage, err := store.EventStorageUsage(ctx, pro.ID)
		if err != nil || usage.Limits.SettledRetention() != week || usage.RetainedEvents != 1 {
			t.Fatalf("pro usage: %+v %v", usage, err)
		}
		for _, step := range []struct {
			at     time.Time
			pruned int64
		}{
			{settled.Add(day - time.Minute), 0},
			{settled.Add(day + time.Minute), 1},
			{settled.Add(week - time.Minute), 0},
			{settled.Add(week + time.Minute), 1},
		} {
			if n, err := store.PruneSettledPublishedEvents(ctx, step.at, 100); err != nil || n != step.pruned {
				t.Fatalf("prune at %s: %d %v, want %d", step.at.Sub(settled), n, err, step.pruned)
			}
		}
		for _, account := range []state.Account{free, pro} {
			usage, err := store.EventStorageUsage(ctx, account.ID)
			if err != nil || usage.RetainedEvents != 0 || usage.RetainedBytes != 0 {
				t.Fatalf("usage after prune: %+v %v", usage, err)
			}
			// The identity is gone with the receipt, so it is accepted again.
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("settled", "orders", `{"again":true}`)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// The counters must equal what admission used to count, through inserts,
// snapshot rewrites, platform receipts and multi-owner deletes.
func TestEventReceiptUsageMatchesRetainedReceipts(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	owners := make([]string, 2)
	for i := range owners {
		account, err := store.CreateAccount(ctx, "receipt-usage-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		owners[i] = account.ID
		for _, id := range []string{"one", "two", "three"} {
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(id, "orders", `{"n":1}`)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.AppendEvent(ctx, "sched", "event.published", &account.ID, storagePayload("platform", "gregale.apps", "{}")); err != nil {
			t.Fatal(err)
		}
	}
	matches := func(stage string) {
		t.Helper()
		for _, owner := range owners {
			var events, bytes int64
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE customer_storage_bytes>0), coalesce(sum(customer_storage_bytes),0)
				FROM event_fanout_outbox WHERE account_id=$1`, owner).Scan(&events, &bytes); err != nil {
				t.Fatal(err)
			}
			usage, err := store.EventStorageUsage(ctx, owner)
			if err != nil || usage.RetainedEvents != events || usage.RetainedBytes != bytes {
				t.Fatalf("%s: usage %+v, receipts hold %d events %d bytes (%v)", stage, usage, events, bytes, err)
			}
		}
	}
	matches("published")
	if _, err := pool.Exec(ctx, `UPDATE event_fanout_outbox SET recipient_snapshot=coalesce(recipient_snapshot,'[]'::jsonb)||'[{"id":"rewrite"}]'::jsonb
		WHERE account_id=$1 AND event_id='two'`, owners[0]); err != nil {
		t.Fatal(err)
	}
	matches("snapshot rewritten")
	if _, err := pool.Exec(ctx, `DELETE FROM event_fanout_outbox WHERE event_id IN ('one','platform')`); err != nil {
		t.Fatal(err)
	}
	matches("deleted across owners")
}

func TestEventReplayBackfillRangeFollowsPlanWindow(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, app := seedReplayPreviewApp(t, store)
	sub, _, err := store.UpsertEventSubscription(ctx, account, app, "orders", "invoice.paid", nil)
	if err != nil {
		t.Fatal(err)
	}
	week := api.MustLimitsFor(api.PlanPro).EventStorage.SettledRetention()
	now := time.Now()
	_, err = store.CreateEventReplayBackfill(ctx, account, state.EventReplayBackfillQuery{
		AppID: app, SubscriptionID: sub.ID, EventReplayBackfillRequest: api.EventReplayBackfillRequest{From: now.Add(-week - time.Hour), Until: now},
	})
	var rangeErr *state.EventReplayBackfillRangeError
	if !errors.As(err, &rangeErr) || !errors.Is(err, state.ErrEventReplayBackfillRange) || rangeErr.Limit != week || rangeErr.Range <= week {
		t.Fatalf("range over the window: %v", err)
	}
	if _, err := store.CreateEventReplayBackfill(ctx, account, state.EventReplayBackfillQuery{
		AppID: app, SubscriptionID: sub.ID, EventReplayBackfillRequest: api.EventReplayBackfillRequest{From: now.Add(-week + time.Hour), Until: now},
	}); err != nil {
		t.Fatalf("range inside the window: %v", err)
	}
}
