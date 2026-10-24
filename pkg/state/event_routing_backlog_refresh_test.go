package state_test

// adr: 617

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

const routingBacklogColumns = `outbox_id,subscription_id,account_id,app_id,accepted_at,routing_mode,routing_state,capacity_scope,
attempts,capacity_deferrals,next_attempt_at,lease_until,consumer_kind,origin,workflow_name`

// requireRoutingBacklogMatchesSource fails when the projection differs from
// its source view in any row or column.
func requireRoutingBacklogMatchesSource(t *testing.T, ctx context.Context, pool *pgxpool.Pool, step string) {
	t.Helper()
	var missing, stale, rows int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM (SELECT `+routingBacklogColumns+` FROM event_routing_backlog_source
			EXCEPT SELECT `+routingBacklogColumns+` FROM event_routing_backlog) d),
		(SELECT count(*) FROM (SELECT `+routingBacklogColumns+` FROM event_routing_backlog
			EXCEPT SELECT `+routingBacklogColumns+` FROM event_routing_backlog_source) d),
		(SELECT count(*) FROM event_routing_backlog)`).Scan(&missing, &stale, &rows); err != nil {
		t.Fatalf("%s: compare projection: %v", step, err)
	}
	if missing != 0 || stale != 0 {
		t.Fatalf("%s: projection has %d rows, %d missing and %d stale against its source", step, rows, missing, stale)
	}
}

// The projection refreshes at commit once per key (ADR-617 amendment
// 2026-10-25); after each step of a receipt's routing it must equal the view.
func TestPgEventRoutingBacklogMatchesSourceThroughRouting(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, _, _, work := seedRecipientClaims(t, store)
	requireRoutingBacklogMatchesSource(t, ctx, pool, "published and claimed")
	if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
		t.Fatal(err)
	}
	requireRoutingBacklogMatchesSource(t, ctx, pool, "adopted")
	first, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now())
	if err != nil || first == nil {
		t.Fatalf("claim: %+v %v", first, err)
	}
	requireRoutingBacklogMatchesSource(t, ctx, pool, "recipient claimed")
	finishRecipient(t, store, first, state.PublishedEventRecipientFailed)
	requireRoutingBacklogMatchesSource(t, ctx, pool, "recipient failed")
	for i := 0; ; i++ {
		next, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now())
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		finishRecipient(t, store, next, state.PublishedEventRecipientEnqueued)
		requireRoutingBacklogMatchesSource(t, ctx, pool, "recipient enqueued")
		if i > 5 {
			t.Fatal("recipients kept coming")
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM event_fanout_outbox WHERE id=$1`, work.ID); err != nil {
		t.Fatal(err)
	}
	requireRoutingBacklogMatchesSource(t, ctx, pool, "receipt deleted")
}

// Deferred triggers can fire early, at SET CONSTRAINTS ... IMMEDIATE; a key
// refreshed then must refresh again for a change made after.
func TestPgEventRoutingBacklogRefreshesAgainAfterEarlyFiring(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, _, _, work := seedRecipientClaims(t, store)
	if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recipients := `UPDATE event_fanout_recipients SET %s WHERE outbox_id=` + strconv.FormatInt(work.ID, 10)
	for _, stmt := range []string{
		fmt.Sprintf(recipients, `capacity_deferrals=capacity_deferrals+1`),
		`SET CONSTRAINTS ALL IMMEDIATE`,
		`SET CONSTRAINTS ALL DEFERRED`,
		fmt.Sprintf(recipients, `capacity_deferrals=capacity_deferrals+1, available_at=available_at+interval '1 minute'`),
		`SAVEPOINT undone`,
		fmt.Sprintf(recipients, `capacity_deferrals=capacity_deferrals+5`),
		`ROLLBACK TO SAVEPOINT undone`,
	} {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireRoutingBacklogMatchesSource(t, ctx, pool, "early firing, later change and rolled back savepoint")
}

// With recipient claims an outbox move skips recipients that have their own
// row but must refresh a captured recipient without one, and a recipient
// update to columns the view does not read changes nothing.
func TestPgEventRoutingBacklogRecipientModeOutboxMoves(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, _, _, work := seedRecipientClaims(t, store)
	if err := store.InitializePublishedEventRecipients(ctx, work, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ name, sql string }{
		{"recipient row removed", `DELETE FROM event_fanout_recipients WHERE outbox_id=$1 AND subscription_id=(SELECT min(subscription_id) FROM event_fanout_recipients WHERE outbox_id=$1)`},
		{"outbox moved", `UPDATE event_fanout_outbox SET available_at=available_at+interval '1 hour' WHERE id=$1`},
		{"unread recipient columns", `UPDATE event_fanout_recipients SET generation=generation+1, delivery_deadline_at=now()+interval '1 day' WHERE outbox_id=$1`},
		{"progress for one subscriber", `UPDATE event_fanout_outbox SET recipient_progress=jsonb_set(recipient_progress, ARRAY[(SELECT max(subscription_id) FROM event_fanout_recipients WHERE outbox_id=$1)], '{"state":"pending","capacity_scope":"app","capacity_deferrals":3}'::jsonb, true) WHERE id=$1`},
	} {
		if _, err := pool.Exec(ctx, step.sql, work.ID); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		requireRoutingBacklogMatchesSource(t, ctx, pool, step.name)
	}
}
