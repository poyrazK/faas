package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// slotDrivenDeliveryCounts is the delivery count as it was first written: from
// every slot of the account. Slots outlive their deliveries, so this reads the
// account's whole retained history.
const slotDrivenDeliveryCounts = `SELECT count(*), count(*) FILTER (WHERE s.app_id=$2),
 count(*) FILTER (WHERE s.app_id=$2 AND s.subscription_id=$3)
FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id=$1 AND i.state IN ('pending','dispatching')`

// adr: 614 — counting live deliveries from the account's live invocations
// gives exactly the slot-driven counts: finished, cancelled and replayed
// deliveries, other apps of the account and other accounts included.
func TestPgEventDeliveryCountsMatchSlotDrivenCounts(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	a := newCapacityFixture(t, store, true)
	ids := a.fill(t, 6)
	if err := store.FailInvocation(ctx, ids[0], "handler failed", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelInvocation(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, ids[2], "handler failed", 0, 0); err != nil {
		t.Fatal(err)
	}
	// A replay child inherits the failed delivery's slot and is live again.
	if _, err := store.ReplayPlainInvocation(ctx, a.account, ids[2], state.PlainInvocationReplayOptions{}); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: a.account, Slug: "counts-" + uuid.NewString(),
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	sibling, _, err := store.UpsertEventSubscription(ctx, a.account, other.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	siblingFixture := capacityFixture{store: store, account: a.account, app: other.ID, sub: sibling.ID, adopted: true, source: "orders"}
	siblingFixture.fill(t, 3)
	b := newCapacityFixture(t, store, true)
	b.fill(t, 2)

	for _, f := range []capacityFixture{a, siblingFixture, b} {
		var account, app, consumer int64
		if err := pool.QueryRow(ctx, slotDrivenDeliveryCounts, uuid.MustParse(f.account), uuid.MustParse(f.app), f.sub).Scan(&account, &app, &consumer); err != nil {
			t.Fatal(err)
		}
		counts, err := sqlc.New().EventDeliveryCounts(ctx, pool, sqlc.EventDeliveryCountsParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(f.account), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(f.app), Valid: true},
			SubscriptionID: f.sub,
		})
		if err != nil {
			t.Fatal(err)
		}
		if counts.AccountCount != account || counts.AppCount != app || counts.ConsumerCount != consumer {
			t.Fatalf("live-driven counts %+v, slot-driven %d/%d/%d", counts, account, app, consumer)
		}
	}
	counts, err := sqlc.New().EventDeliveryCounts(ctx, pool, sqlc.EventDeliveryCountsParams{
		AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.account), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(a.app), Valid: true},
		SubscriptionID: a.sub,
	})
	if err != nil || counts.AccountCount != 7 || counts.AppCount != 4 || counts.ConsumerCount != 4 {
		t.Fatalf("counts = %+v, %v; want 7 live in the account, 4 in the app and consumer", counts, err)
	}
}
