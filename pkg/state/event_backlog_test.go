package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 617
func TestEventBacklogLifecyclePaginationAndReplay(t *testing.T) {
	forRoutingAdmissionStores(t, func(t *testing.T, store routingAdmissionTestStore, pool *pgxpool.Pool, adopted bool) {
		ctx, account, app, work := seedRecipientClaims(t, store)
		if adopted {
			if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		backlog := store.(state.EventBacklogStore)
		q := state.EventBacklogQuery{Limit: 1, ConsumerLimit: 1, WindowAt: time.Now().UTC()}
		first, err := backlog.EventBacklog(ctx, account, q)
		if err != nil || len(first.Recipients) != 1 || len(first.Consumers) != 1 || first.Next.OutboxID != work.ID || first.NextConsumer.AppID != app || first.Consumers[0].WaitingRecipients != 1 {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		victim := first.Recipients[0].SubscriptionID
		for _, filter := range []state.EventBacklogQuery{{AppID: uuid.NewString()}, {Filters: api.EventBacklogFilters{SubscriptionID: "unknown"}}, {Filters: api.EventBacklogFilters{CapacityScope: "account"}}, {Filters: api.EventBacklogFilters{MinAgeSeconds: 3600}}} {
			r, err := backlog.EventBacklog(ctx, account, filter)
			if err != nil || len(r.Recipients) != 0 || len(r.Consumers) != 0 {
				t.Fatalf("filter=%+v result=%+v err=%v", filter, r, err)
			}
		}
		foreign, err := backlog.EventBacklog(ctx, uuid.NewString(), q)
		if err != nil || len(foreign.Recipients) != 0 || len(foreign.Consumers) != 0 {
			t.Fatalf("foreign=%+v %v", foreign, err)
		}
		claims := map[string]*state.PublishedEventRecipientWork{}
		if adopted {
			for range 3 {
				claim, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				claims[claim.Recipient.ID] = claim
			}
		}
		finish := func(id, status string) {
			p := state.PublishedEventRecipientProgress{State: status, Attempts: 1, UpdatedAt: time.Now(), Retryable: status == "failed", FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed}
			var err error
			if adopted {
				err = store.FinishPublishedEventRecipient(ctx, claims[id], p, time.Now())
			} else {
				err = store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, id, p)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		finish(victim, "failed")
		q.After = first.Next
		q.ConsumersAfter = first.NextConsumer
		q.Limit = 10
		q.ConsumerLimit = 10
		second, err := backlog.EventBacklog(ctx, account, q)
		if err != nil || len(second.Recipients) != 2 || len(second.Consumers) != 2 || second.Next.OutboxID != 0 || second.NextConsumer.AppID != "" {
			t.Fatalf("removed cursor=%+v %v", second, err)
		}
		for _, r := range second.Recipients {
			if r.SubscriptionID == victim {
				t.Fatal("terminal recipient still in backlog")
			}
			if adopted && (r.State != "processing" || r.WaitingReason != "routing_in_progress" || r.CapacityScope != "") {
				t.Fatalf("lease=%+v", r)
			}
			finish(r.SubscriptionID, "enqueued")
		}
		if !adopted {
			if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
		}
		empty, err := backlog.EventBacklog(ctx, account, state.EventBacklogQuery{})
		if err != nil || len(empty.Recipients) != 0 || len(empty.Consumers) != 0 {
			t.Fatalf("settled=%+v %v", empty, err)
		}
		if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, account, app, "orders", "evt-three-consumers", victim); err != nil {
			t.Fatal(err)
		}
		replayed, err := backlog.EventBacklog(ctx, account, state.EventBacklogQuery{})
		if err != nil || len(replayed.Recipients) != 1 || replayed.Recipients[0].SubscriptionID != victim || replayed.Recipients[0].State != "pending" || len(replayed.Consumers) != 1 {
			t.Fatalf("replay=%+v %v", replayed, err)
		}
		if pool != nil {
			// Root deletion cascades the projection, including adopted child triggers.
			if _, err := pool.Exec(context.Background(), "DELETE FROM event_fanout_outbox WHERE id=$1", work.ID); err != nil {
				t.Fatal(err)
			}
			r, err := backlog.EventBacklog(ctx, account, state.EventBacklogQuery{})
			if err != nil || len(r.Recipients) != 0 {
				t.Fatalf("cascade=%+v %v", r, err)
			}
		}
	})
}

// adr: 617
func TestPgEventBacklogBackfillUnattributedAndForeignTarget(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, app, work := seedRecipientClaims(t, store)
	source, err := migrations.FS.ReadFile("20261005190741382_event_routing_backlog.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	// An old writer updates the checkpoint before the projection exists.
	first := work.RecipientSnapshot[0].ID
	p := state.PublishedEventRecipientProgress{State: "pending", CapacityScope: "app", CapacityDeferrals: 4, Attempts: 4, UpdatedAt: time.Now()}
	if err := store.RecordPublishedEventRecipientProgress(ctx, work.ID, work.ClaimToken, first, p); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	r, err := store.EventBacklog(ctx, account, state.EventBacklogQuery{Filters: api.EventBacklogFilters{CapacityScope: "app"}})
	if err != nil || len(r.Recipients) != 1 || r.Recipients[0].CapacityDeferrals != 4 || r.Recipients[0].SubscriptionID != first {
		t.Fatalf("backfill=%+v %v", r, err)
	}
	other, err := store.CreateAccount(ctx, "backlog-transfer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyForeignCreatingAccount(t, ctx, pool, app, other.ID)
	if _, err := pool.Exec(ctx, "UPDATE apps SET slug='private-new-owner' WHERE id=$1", app); err != nil {
		t.Fatal(err)
	}
	r, err = store.EventBacklog(ctx, account, state.EventBacklogQuery{})
	if err != nil || len(r.Recipients) != 3 || len(r.Consumers) != 3 {
		t.Fatalf("transfer=%+v %v", r, err)
	}
	for _, e := range r.Recipients {
		if e.AppSlug != "" || e.TargetAvailable {
			t.Fatalf("foreign label leak=%+v", e)
		}
	}
	for _, e := range r.Consumers {
		if e.AppSlug != "" || e.TargetAvailable {
			t.Fatalf("foreign consumer leak=%+v", e)
		}
	}
	foreign, err := store.EventBacklog(ctx, other.ID, state.EventBacklogQuery{})
	if err != nil || len(foreign.Recipients) != 0 {
		t.Fatalf("new owner receipt leak=%+v %v", foreign, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE event_fanout_outbox SET recipient_snapshot=NULL WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}
	r, err = store.EventBacklog(ctx, account, state.EventBacklogQuery{AppID: uuid.NewString(), Filters: api.EventBacklogFilters{CapacityScope: "consumer"}})
	if err != nil || len(r.Recipients) != 0 || len(r.Consumers) != 0 || r.UnattributedReceipts != 1 {
		t.Fatalf("legacy=%+v %v", r, err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	r, err = store.EventBacklog(ctx, account, state.EventBacklogQuery{})
	if err != nil || r.UnattributedReceipts != 0 {
		t.Fatalf("settled legacy=%+v %v", r, err)
	}
}

// adr: 617
func TestPgEventBacklogProjectionRollbackAndClaimLockOrder(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, _, work := seedRecipientClaims(t, store)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "UPDATE event_fanout_outbox SET state='delivered' WHERE id=$1", work.ID); err != nil {
		t.Fatal(err)
	}
	// Inspection sees committed state without waiting for the writer's locks.
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	r, err := store.EventBacklog(readCtx, account, state.EventBacklogQuery{})
	cancel()
	if err != nil || len(r.Recipients) != 3 {
		t.Fatalf("uncommitted state leaked or blocked = %+v %v", r, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = store.EventBacklog(ctx, account, state.EventBacklogQuery{})
	if err != nil || len(r.Recipients) != 3 {
		t.Fatalf("rollback lost projection = %+v %v", r, err)
	}
	if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	if err := tx.QueryRow(ctx, "SELECT id FROM event_fanout_outbox WHERE id=$1 FOR UPDATE", work.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, time.Second)
	claim, err := store.ClaimDuePublishedEventRecipient(claimCtx, time.Now())
	cancel()
	if err != nil {
		t.Fatalf("child projection acquired parent lock: %v", err)
	}
	r, err = store.EventBacklog(ctx, account, state.EventBacklogQuery{Filters: api.EventBacklogFilters{State: "processing"}})
	if err != nil || len(r.Recipients) != 1 || r.Recipients[0].SubscriptionID != claim.Recipient.ID {
		t.Fatalf("claim projection=%+v %v", r, err)
	}
}
