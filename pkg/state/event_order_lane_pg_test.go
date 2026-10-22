package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// laneLookupMigration replaced the ordering blocker's account scan with a
// lookup by lane; its Down section holds the scan it replaced.
const laneLookupMigration = "20261021223015891_event_order_lane_lookup.sql"

// adr: 795 — looking earlier deliveries up by ordering lane finds exactly the
// blockers the account scan found: adopted and unadopted receipts, settled
// and retrying deliveries, an unordered sibling on the same policy, a
// consumer without a policy and an event without a key, for the target alone
// and for its earlier siblings in the same receipt.
func TestPgEventOrderLaneLookupMatchesAccountScan(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	source, err := migrations.FS.ReadFile(laneLookupMigration)
	if err != nil {
		t.Fatal(err)
	}
	down := string(source)[strings.Index(string(source), "-- +goose Down"):]
	start := strings.Index(down, "CREATE OR REPLACE FUNCTION event_recipient_order_blocker(")
	end := strings.Index(down[start:], "\n$$;") + start + len("\n$$;")
	scan := strings.Replace(down[start:end], "CREATE OR REPLACE FUNCTION event_recipient_order_blocker(",
		"CREATE FUNCTION pg_temp.event_recipient_order_blocker_scan(", 1)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, scan); err != nil {
		t.Fatalf("install the account-scan blocker: %v", err)
	}

	account, err := store.CreateAccount(ctx, "order-lane-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "order-lane",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Name: "event-order", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	bind := func(eventType string, ordered bool) {
		t.Helper()
		subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", eventType, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetEventWorkBinding(ctx, app.ID, subscription.ID, policy.Name, "data.order_id",
			state.EventWorkBindingOptions{Ordered: ordered}); err != nil {
			t.Fatal(err)
		}
	}
	bind("order.changed", true)
	bind("*", false)
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.*", nil); err != nil {
		t.Fatal(err)
	}
	publish := func(id, data string) {
		t.Helper()
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.changed", Data: json.RawMessage(data)}).Normalize(account.ID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
			t.Fatal(err)
		}
	}
	// Adopted: a-1 settles its first delivery and retries another; a-2 and b-1
	// stay pending behind it or beside it.
	for _, event := range [][2]string{{"a-1", `{"order_id":"a"}`}, {"a-2", `{"order_id":"a"}`}, {"b-1", `{"order_id":"b"}`}, {"no-key", `{}`}} {
		publish(event[0], event[1])
	}
	now := time.Now().UTC()
	adoptLaneTestReceipts(t, store, now)
	first, err := store.ClaimDuePublishedEventRecipient(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	finishRecipient(t, store, first, state.PublishedEventRecipientEnqueued)
	retry, err := store.ClaimDuePublishedEventRecipient(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	finishRecipient(t, store, retry, state.PublishedEventRecipientPending)
	// Unadopted: later events in the same and other lanes, and a number key.
	for _, event := range [][2]string{{"a-3", `{"order_id":"a"}`}, {"c-1", `{"order_id":"c"}`}, {"n-1", `{"order_id":1}`}, {"n-2", `{"order_id":1.0}`}} {
		publish(event[0], event[1])
	}

	var lanes int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM event_fanout_recipients r JOIN event_fanout_outbox o ON o.id=r.outbox_id
		WHERE o.account_id=$1 AND r.order_lane IS NOT NULL`, uuid.MustParse(account.ID)).Scan(&lanes); err != nil {
		t.Fatal(err)
	}
	if lanes == 0 {
		t.Fatal("adopted recipients with a work policy carry no lane id")
	}
	rows, err := conn.Query(ctx, `SELECT o.event_id, item->>'id', same,
		event_recipient_order_blocker(o.id, item->>'id', item, same),
		pg_temp.event_recipient_order_blocker_scan(o.id, item->>'id', item, same)
		FROM event_fanout_outbox o
		CROSS JOIN LATERAL jsonb_array_elements(o.recipient_snapshot) item
		CROSS JOIN (VALUES (true), (false)) v(same)
		WHERE o.account_id=$1 ORDER BY o.id, item->>'id', same`, uuid.MustParse(account.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	compared, blocked := 0, 0
	for rows.Next() {
		var eventID, subscriptionID string
		var same bool
		var lookup, accountScan []byte
		if err := rows.Scan(&eventID, &subscriptionID, &same, &lookup, &accountScan); err != nil {
			t.Fatal(err)
		}
		compared++
		if lookup != nil {
			blocked++
		}
		if !jsonEqualOrBothNull(lookup, accountScan) {
			t.Errorf("%s/%s same-receipt=%v: lane lookup %s, account scan %s", eventID, subscriptionID, same, lookup, accountScan)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if compared < 32 || blocked == 0 {
		t.Fatalf("compared %d recipients, %d blocked: the dataset no longer exercises ordering", compared, blocked)
	}
}

// adoptLaneTestReceipts adopts every published receipt for independent
// recipient routing, as the fanout sweep does.
func adoptLaneTestReceipts(t *testing.T, store *state.PgStore, now time.Time) {
	t.Helper()
	for {
		receipt, err := store.ClaimDuePublishedEventForRecipientAdoption(t.Context(), now)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InitializePublishedEventRecipients(t.Context(), receipt, now); err != nil {
			t.Fatal(err)
		}
	}
}

func jsonEqualOrBothNull(left, right []byte) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(a)
	rightJSON, _ := json.Marshal(b)
	return string(leftJSON) == string(rightJSON)
}
