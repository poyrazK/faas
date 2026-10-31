package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// slotDrivenDeliveryCounts is the delivery count before slots said whether
// their delivery is live: every slot of the account, filtered on the
// invocation's state.
const slotDrivenDeliveryCounts = `SELECT count(*), count(*) FILTER (WHERE s.app_id=$2),
 count(*) FILTER (WHERE s.app_id=$2 AND s.subscription_id=$3)
FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id=$1 AND i.state IN ('pending','dispatching')`

// staleLiveSlots counts slots whose live flag disagrees with their invocation.
const staleLiveSlots = `SELECT count(*) FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id = ANY ($1::uuid[]) AND s.live IS DISTINCT FROM (i.state IN ('pending','dispatching'))`

// liveSlotsFixture fills two apps of one account and a second account with
// deliveries, then fails, cancels, retries and replays some of them.
func liveSlotsFixture(t *testing.T, store *state.PgStore) []capacityFixture {
	t.Helper()
	ctx := context.Background()
	a := newCapacityFixture(t, store, true)
	ids := a.fill(t, 7)
	if err := store.FailInvocation(ctx, ids[0], "handler failed", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelInvocation(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, ids[2], "handler failed", 0, 0); err != nil {
		t.Fatal(err)
	}
	// A retry stays live.
	if err := store.FailInvocation(ctx, ids[3], "handler failed", time.Minute, 5); err != nil {
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
	// Its own source keeps the sibling's events from also matching app A.
	sibling, _, err := store.UpsertEventSubscription(ctx, a.account, other.ID, "orders.sibling", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	siblingFixture := capacityFixture{store: store, account: a.account, app: other.ID, sub: sibling.ID, adopted: true, source: "orders.sibling"}
	siblingFixture.fill(t, 3)
	b := newCapacityFixture(t, store, true)
	b.fill(t, 2)
	return []capacityFixture{a, siblingFixture, b}
}

func requireLiveSlotsAgree(t *testing.T, pool *pgxpool.Pool, fixtures []capacityFixture) {
	t.Helper()
	ctx := context.Background()
	accounts := []uuid.UUID{}
	for _, f := range fixtures {
		accounts = append(accounts, uuid.MustParse(f.account))
	}
	var stale int
	if err := pool.QueryRow(ctx, staleLiveSlots, accounts).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("%d slots disagree with their invocation's state", stale)
	}
	for _, f := range fixtures {
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
			t.Fatalf("live-slot counts %+v, slot-driven %d/%d/%d", counts, account, app, consumer)
		}
	}
}

// adr: 614 — a slot is live exactly while its delivery is pending or
// dispatching, through failure, cancellation, retry and replay, and counting
// live slots gives the slot-driven counts: other apps of the account and
// other accounts included.
func TestPgEventDeliveryCountsReadLiveSlots(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	fixtures := liveSlotsFixture(t, store)
	requireLiveSlotsAgree(t, pool, fixtures)
	a := fixtures[0]
	counts, err := sqlc.New().EventDeliveryCounts(ctx, pool, sqlc.EventDeliveryCountsParams{
		AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.account), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(a.app), Valid: true},
		SubscriptionID: a.sub,
	})
	if err != nil || counts.AccountCount != 8 || counts.AppCount != 5 || counts.ConsumerCount != 5 {
		t.Fatalf("counts = %+v, %v; want 8 live in the account, 5 in the app and consumer", counts, err)
	}
}

// adr: 614 — the backfill marks exactly the slots of finished deliveries dead,
// whatever the slots said before it ran.
func TestPgEventDeliveryLiveSlotsBackfill(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	fixtures := liveSlotsFixture(t, store)
	source, err := migrations.FS.ReadFile("20261023110719517_event_delivery_live_slots_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "DO $backfill$")
	end := strings.Index(string(source), "END $backfill$;")
	if start < 0 || end < start {
		t.Fatal("backfill block not found")
	}
	backfill := string(source)[start : end+len("END $backfill$;")]
	// Slots written before the live column all said live.
	if _, err := pool.Exec(ctx, `UPDATE event_delivery_slots SET live = true`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, backfill); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	requireLiveSlotsAgree(t, pool, fixtures)
}
