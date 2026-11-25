package commitmanaged

import (
	"slices"
	"testing"
	"time"
)

func TestCommitReplayKeepsIndependentDeliveryMoving(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO public.orders VALUES (1, 'new', NULL), (2, 'new', NULL)`,
		`UPDATE public.orders SET status='paid' WHERE id=1`,
	} {
		if _, err := e.customer.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	insertEvent := func(key string) string {
		t.Helper()
		var id string
		if err := e.customer.QueryRow(ctx, `SELECT event_id::text FROM public.gregale_outbox
 WHERE group_key=$1 AND event_type='database.orders.insert'`, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	held := insertEvent(`orders:{"id": 1}`)
	repairable := insertEvent(`orders:{"id": 2}`)
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET blocked_code='invalid_event'
 WHERE event_id IN ($1::uuid,$2::uuid)`, held, repairable); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox
 SET lease_token=gen_random_uuid(),lease_until=clock_timestamp()+interval '10 minutes' WHERE event_id=$1::uuid`, held); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{held, repairable} {
		if err := e.store.RequestCommitReplay(ctx, e.account.ID, e.source.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.control.Exec(ctx, `UPDATE commit_replay_requests
 SET next_attempt_at=clock_timestamp()-interval '1 minute' WHERE event_id=$1::uuid`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (3, 'new', NULL)`); err != nil {
		t.Fatal(err)
	}
	ordinary := insertEvent(`orders:{"id": 3}`)
	e.makeDue(t)
	var pollStarted time.Time
	if err := e.control.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&pollStarted); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{repairable, ordinary} {
		if _, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, e.source.ID, id); err != nil {
			t.Fatalf("independent event %s was not accepted: %v", id, err)
		}
	}
	delivered := e.delivered(t)
	slices.Sort(delivered)
	if !slices.Equal(delivered, []string{"2:new", "3:new"}) {
		t.Fatalf("delivery while row 1 was held: %v", delivered)
	}
	var pending, completed bool
	if err := e.control.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM commit_replay_requests WHERE event_id=$1::uuid AND state='pending' AND next_attempt_at>$3),
 EXISTS(SELECT 1 FROM commit_replay_requests WHERE event_id=$2::uuid AND state='completed')`, held, repairable, pollStarted).Scan(&pending, &completed); err != nil || !pending || !completed {
		t.Fatalf("held replay deferred=%t, independent replay completed=%t: %v", pending, completed, err)
	}
	source, err := e.store.CommitSourceByID(ctx, e.account.ID, e.source.ID)
	if err != nil || source.RelayStatus != "replay_pending" || source.BlockedEvents == nil || *source.BlockedEvents != 1 {
		t.Fatalf("deferred replay observation: %+v (%v)", source, err)
	}
	// Advance both the customer lease and the control-plane retry deadline.
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1::uuid`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := e.control.Exec(ctx, `UPDATE commit_replay_requests SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE event_id=$1::uuid`, held); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t)
	otherScheduler := &Manager{Store: e.store, Identities: e.manager.Identities, Open: e.manager.Open}
	if err := otherScheduler.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	delivered = e.delivered(t)
	if len(delivered) != 4 || !slices.Equal(delivered[2:], []string{"1:new", "1:paid"}) {
		t.Fatalf("delivery after lease expiry: %v", delivered)
	}
	if err := e.control.QueryRow(ctx, `SELECT state='completed' FROM commit_replay_requests WHERE event_id=$1::uuid`, held).Scan(&completed); err != nil || !completed {
		t.Fatalf("held replay completed=%t: %v", completed, err)
	}
}
