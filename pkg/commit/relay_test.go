package commit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type acceptFunc func(context.Context, Event) (Receipt, error)

func (f acceptFunc) Accept(ctx context.Context, e Event) (Receipt, error) { return f(ctx, e) }

func TestPostgresRelayCommitRollbackAndLostAcceptance(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	event := Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{"order":"one"}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	calls := 0
	receipt := Receipt{ID: uuid.NewString(), OperationID: uuid.NewString()}
	lost := true
	relay := Relay{Pool: pool, Acceptor: acceptFunc(func(_ context.Context, e Event) (Receipt, error) {
		calls++
		if e.ID != event.ID {
			t.Fatal("event identity changed")
		}
		if lost {
			lost = false
			return Receipt{}, errors.New("response lost after durable acceptance")
		}
		return receipt, nil
	})}
	if n, err := relay.Tick(ctx); err != nil || n != 0 || calls != 0 {
		t.Fatalf("uncommitted event visible: %d %v calls=%d", n, err, calls)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rolledBack := event
	rolledBack.ID = uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, rolledBack); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := relay.Tick(ctx); err == nil || n != 0 {
		t.Fatalf("lost response: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gregale_outbox SET next_attempt_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	// A new relay instance models restart with no process-local checkpoint.
	recovered := relay
	if n, err := recovered.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("recovery: %d %v", n, err)
	}
	var got Receipt
	if err := pool.QueryRow(ctx, `SELECT receipt_id::text,COALESCE(invocation_id::text,''),COALESCE(operation_id::text,'') FROM gregale_outbox WHERE event_id=$1`, event.ID).Scan(&got.ID, &got.InvocationID, &got.OperationID); err != nil {
		t.Fatal(err)
	}
	if got != receipt || calls != 2 {
		t.Fatalf("checkpoint=%+v calls=%d", got, calls)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gregale_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback leaked event: count=%d err=%v", count, err)
	}
	if n, err := recovered.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("accepted event resent: %d %v", n, err)
	}
	backlog, err := recovered.Backlog(ctx)
	if err != nil || backlog.Accepted != 1 || backlog.Pending != 0 || backlog.Blocked != 0 {
		t.Fatalf("backlog: %+v %v", backlog, err)
	}
	if n, err := recovered.Cleanup(ctx, time.Now().Add(time.Second), 32); err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
}

func TestPostgresRelayBlockedReplayAndPendingCleanup(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	event := Event{ID: uuid.NewString(), Type: "bad.event", Data: []byte(`{}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	relay := Relay{Pool: pool, Acceptor: acceptFunc(func(context.Context, Event) (Receipt, error) {
		return Receipt{}, &PermanentError{Code: "schema_invalid"}
	})}
	if n, err := relay.Tick(ctx); n != 0 || err == nil {
		t.Fatalf("block: %d %v", n, err)
	}
	b, err := relay.Backlog(ctx)
	if err != nil || b.Blocked != 1 {
		t.Fatalf("backlog: %+v %v", b, err)
	}
	if n, err := relay.Cleanup(ctx, time.Now().Add(time.Hour), 32); err != nil || n != 0 {
		t.Fatalf("deleted unaccepted event: %d %v", n, err)
	}
	if ok, err := relay.ReplayBlocked(ctx, event.ID); err != nil || !ok {
		t.Fatalf("replay: %v %v", ok, err)
	}
	relay.Acceptor = acceptFunc(func(_ context.Context, e Event) (Receipt, error) {
		if e.ID != event.ID {
			t.Fatal("replay changed ID")
		}
		return Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}, nil
	})
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("replayed delivery: %d %v", n, err)
	}
}

func TestPostgresRelayExpiredLeaseFencesOriginalWorker(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	event := Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	claimed := make(chan struct{})
	releaseOriginal := make(chan struct{})
	receipt := Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}
	original := Relay{Pool: pool, Acceptor: acceptFunc(func(context.Context, Event) (Receipt, error) { close(claimed); <-releaseOriginal; return receipt, nil })}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := original.Tick(ctx); done <- result{n, err} }()
	select {
	case <-claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("original did not claim")
	}
	// A competing worker cannot claim a live lease.
	competitor := Relay{Pool: pool, Acceptor: acceptFunc(func(context.Context, Event) (Receipt, error) { return receipt, nil })}
	if n, err := competitor.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("live lease stolen: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gregale_outbox SET lease_until=now()-interval '1 second' WHERE event_id=$1`, event.ID); err != nil {
		t.Fatal(err)
	}
	takeover := make(chan struct{})
	releaseTakeover := make(chan struct{})
	newDone := make(chan result, 1)
	competitor.Acceptor = acceptFunc(func(context.Context, Event) (Receipt, error) { close(takeover); <-releaseTakeover; return receipt, nil })
	go func() { n, err := competitor.Tick(ctx); newDone <- result{n, err} }()
	select {
	case <-takeover:
	case <-time.After(5 * time.Second):
		t.Fatal("expired lease not recovered")
	}
	close(releaseOriginal)
	select {
	case r := <-done:
		if r.err != nil || r.n != 0 {
			t.Fatalf("stale checkpoint: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original hung")
	}
	close(releaseTakeover)
	select {
	case r := <-newDone:
		if r.err != nil || r.n != 1 {
			t.Fatalf("takeover checkpoint: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("takeover hung")
	}
}
