package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func routingSummary(t *testing.T, store recipientClaimTestStore, app, sub string) state.EventFanoutHistorySummary {
	t.Helper()
	rows, err := store.(state.EventFanoutHistorySummaryStore).ListEventFanoutHistorySummariesForApp(context.Background(), app, "orders", "evt-three-consumers", sub)
	if err != nil || len(rows) != 1 {
		t.Fatalf("summaries=%+v,%v", rows, err)
	}
	return rows[0]
}

func TestEventRoutingHistoryCoalescesCapacityWithoutChangingCheckpoint(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, _, app, receipt := seedRecipientClaims(t, store)
		sub := receipt.RecipientSnapshot[0].ID
		first := time.Now().UTC()
		const waits = 20000
		for i := 1; i <= waits; i++ {
			p := state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientPending, Attempts: i, CapacityDeferrals: i, CapacityScope: "consumer", UpdatedAt: first.Add(time.Duration(i) * time.Microsecond)}
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sub, p); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := store.ListEventFanoutAttemptsForApp(ctx, app, 200, state.EventFanoutAttemptCursor{}, "orders", "evt-three-consumers", sub)
		summary := routingSummary(t, store, app, sub)
		if err != nil || len(rows) != 1 || summary.CapacityDeferrals != waits || summary.ObservedOutcomes != waits || summary.CoalescedOutcomes != waits-1 || summary.RetainedRecords != 1 || summary.FirstCapacityWaitAt == nil || summary.LastCapacityWaitAt == nil {
			t.Fatalf("history=%+v summary=%+v err=%v", rows, summary, err)
		}
		if rows[0].CapacityDeferrals != 1 || rows[0].CapacityScope != "consumer" {
			t.Fatalf("immutable first wait=%+v", rows[0])
		}
		// A stale claim must not increment counters, even if it carries a larger checkpoint.
		err = store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, "11111111-1111-1111-1111-111111111111", sub, state.PublishedEventRecipientProgress{State: "pending", Attempts: waits + 1, CapacityDeferrals: waits + 1, CapacityScope: "consumer", UpdatedAt: time.Now()})
		if !errors.Is(err, state.ErrConflict) || routingSummary(t, store, app, sub).ObservedOutcomes != waits {
			t.Fatalf("stale write=%v", err)
		}
		// A healthy sibling progresses while the first consumer is still pending.
		sibling := receipt.RecipientSnapshot[1].ID
		if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sibling, state.PublishedEventRecipientProgress{State: "enqueued", Attempts: 1, UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		p := state.PublishedEventRecipientProgress{State: "enqueued", Attempts: waits + 1, CapacityDeferrals: waits, UpdatedAt: time.Now()}
		if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sub, p); err != nil {
			t.Fatal(err)
		}
		rows, err = store.ListEventFanoutAttemptsForApp(ctx, app, 200, state.EventFanoutAttemptCursor{}, "orders", "evt-three-consumers", sub)
		if err != nil || len(rows) != 2 || rows[0].State != "enqueued" || routingSummary(t, store, app, sub).CapacityDeferrals != waits {
			t.Fatalf("recovery=%+v,%v", rows, err)
		}
	})
}

func TestEventRoutingHistoryBoundsReplayCyclesAndRetainsEvidence(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx, account, app, receipt := seedRecipientClaims(t, store)
		sub := receipt.RecipientSnapshot[0].ID
		for _, r := range receipt.RecipientSnapshot[1:] {
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, r.ID, state.PublishedEventRecipientProgress{State: "enqueued", Attempts: 1, UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		const cycles = 200
		for i := 0; i < cycles; i++ {
			p := state.PublishedEventRecipientProgress{State: "failed", Attempts: i + 7, CapacityDeferrals: 7, FailureCode: state.EventFanoutFailureCodeInvocationEnqueueFailed, Retryable: true, LastError: strings.Repeat("界", 1000), UpdatedAt: time.Now()}
			if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sub, p); err != nil {
				t.Fatal(err)
			}
			if err := store.FinishPublishedEvent(ctx, receipt.ID, receipt.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
			if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, account, app, "orders", "evt-three-consumers", sub); err != nil {
				t.Fatal(err)
			}
			var err error
			receipt, err = store.ClaimDuePublishedEvent(ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if receipt.RecipientProgress[sub].CapacityDeferrals != 7 {
				t.Fatalf("replay reset deferrals=%+v", receipt.RecipientProgress[sub])
			}
		}
		summary := routingSummary(t, store, app, sub)
		if summary.RetainedRecords > api.EventRoutingHistoryMaxRows || summary.RetainedBytes > api.EventRoutingHistoryMaxBytes || summary.CompactedOutcomes == 0 || summary.CompactedThroughAt == nil {
			t.Fatalf("unbounded summary=%+v", summary)
		}
		rows, err := store.ListEventFanoutAttemptsForApp(ctx, app, 200, state.EventFanoutAttemptCursor{}, "orders", "evt-three-consumers", sub)
		if err != nil || len(rows) < 2 || rows[0].Action != state.EventFanoutAttemptActionReplay || rows[1].State != "failed" || !rows[1].DetailsTruncated || !utf8.ValidString(rows[1].LastError) || len(rows[1].LastError) > api.EventRoutingHistoryErrorMaxBytes {
			t.Fatalf("recent evidence=%+v,%v", rows, err)
		}
		cursor := state.EventFanoutAttemptCursor{ID: rows[0].ID}
		// The receipt is pending, so normal receipt retention cannot delete it.
		retention := store.(state.EventFanoutHistoryRetentionStore)
		n, err := retention.PruneEventFanoutHistory(ctx, time.Now().Add(31*24*time.Hour), api.EventRoutingHistoryPruneBatch)
		if err != nil || n != 1 {
			t.Fatalf("independent compaction=%d,%v", n, err)
		}
		page, err := store.ListEventFanoutAttemptsForApp(ctx, app, 200, cursor, "orders", "evt-three-consumers", sub)
		if err != nil || len(page) != 1 || page[0].State != "failed" || page[0].ID >= cursor.ID {
			t.Fatalf("cursor after prune=%+v,%v", page, err)
		}
		summary = routingSummary(t, store, app, sub)
		if summary.RetainedRecords != 2 || summary.CapacityDeferrals != 7 || summary.ObservedOutcomes != cycles*2 {
			t.Fatalf("protected evidence=%+v", summary)
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(60*24*time.Hour), 100); err != nil || n != 0 {
			t.Fatalf("pending receipt pruned=%d,%v", n, err)
		}
		// Recovery still uses the durable checkpoint after detail compaction.
		if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sub, state.PublishedEventRecipientProgress{State: "enqueued", Attempts: cycles + 8, CapacityDeferrals: 7, UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishPublishedEvent(ctx, receipt.ID, receipt.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(60*24*time.Hour), 100); err != nil || n != 1 {
			t.Fatalf("settled prune=%d,%v", n, err)
		}
		summaries, err := store.(state.EventFanoutHistorySummaryStore).ListEventFanoutHistorySummariesForApp(ctx, app, "orders", "evt-three-consumers", sub)
		if err != nil || len(summaries) != 0 {
			t.Fatalf("summary leaked=%+v,%v", summaries, err)
		}
	})
}

func TestPgEventRoutingHistoryPruneSkipsActiveReceipt(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, _, app, receipt := seedRecipientClaims(t, store)
	sub := receipt.RecipientSnapshot[0].ID
	for i := 1; i <= 3; i++ {
		if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, sub, state.PublishedEventRecipientProgress{State: "pending", Attempts: i, FailureCode: "target_lookup_failed", UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM event_fanout_outbox WHERE id=$1 FOR UPDATE", receipt.ID); err != nil {
		t.Fatal(err)
	}
	timed, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if n, err := store.PruneEventFanoutHistory(timed, time.Now().Add(31*24*time.Hour), 50); err != nil || n != 0 {
		t.Fatalf("locked prune=%d,%v", n, err)
	}
	if routingSummary(t, store, app, sub).RetainedRecords != 3 {
		t.Fatal("active receipt detail changed")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneEventFanoutHistory(ctx, time.Now().Add(31*24*time.Hour), 50); err != nil || n != 1 {
		t.Fatalf("unlocked prune=%d,%v", n, err)
	}
}

// Reapply only this additive migration around real retained legacy observations.
// Older terminal failures can have no classification code and must stay useful.
func TestPgEventRoutingHistoryBackfillsUnknownFailureAndDeferrals(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, account, app, receipt := seedRecipientClaims(t, store)
	sub := receipt.RecipientSnapshot[0].ID
	for _, r := range receipt.RecipientSnapshot {
		p := state.PublishedEventRecipientProgress{State: "enqueued", Attempts: 1, UpdatedAt: time.Now()}
		if r.ID == sub {
			p.State = "failed"
			p.Attempts = 6
			p.CapacityDeferrals = 5
			p.LastError = "legacy unclassified failure"
		}
		if err := store.RecordPublishedEventRecipientProgress(ctx, receipt.ID, receipt.ClaimToken, r.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FinishPublishedEvent(ctx, receipt.ID, receipt.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplayFailedPublishedEventRecipientForApp(ctx, account, app, "orders", "evt-three-consumers", sub); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.FS.ReadFile("20261005181424759_bounded_event_routing_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	summary := routingSummary(t, store, app, sub)
	if summary.CapacityDeferrals != 5 || summary.ObservedOutcomes != 2 || summary.FirstCapacityWaitAt != nil {
		t.Fatalf("legacy summary=%+v", summary)
	}
	if _, err := store.PruneEventFanoutHistory(ctx, time.Now().Add(31*24*time.Hour), 50); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListEventFanoutAttemptsForApp(ctx, app, 200, state.EventFanoutAttemptCursor{}, "orders", "evt-three-consumers", sub)
	if err != nil || len(rows) != 2 || rows[1].State != "failed" || rows[1].LastError != "legacy unclassified failure" {
		t.Fatalf("legacy evidence=%+v,%v", rows, err)
	}
}
