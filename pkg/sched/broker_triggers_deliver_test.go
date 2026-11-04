package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Poll dropped every record it read: it appended into a slice header it
// then threw away, so Redis Streams triggers delivered nothing while the
// entries piled up in the group's pending list.
func TestRedisStreamPollReturnsReadEntries(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.XGroupCreateMkStream(ctx, "orders", "faas", "$").Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: "orders", Values: map[string]any{"payload": `{"id":"` + id + `"}`}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	poller := sched.NewRedisStreamPoller(client, "orders", "faas", "faas-test")
	res := poller.Poll(ctx, sqlc.Trigger{})
	if res.Error != nil || len(res.Records) != 3 {
		t.Fatalf("poll = %d records, %v; want the 3 stream entries", len(res.Records), res.Error)
	}
	ids := make([]string, 0, len(res.Records))
	for _, rec := range res.Records {
		ids = append(ids, rec.ItemIdentifier)
	}
	if err := poller.Ack(ctx, sqlc.Trigger{}, ids); err != nil {
		t.Fatal(err)
	}
	pending, err := client.XPending(ctx, "orders", "faas").Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("pending after ack = %+v, %v; want none", pending, err)
	}
}

// Nothing ever registered a NATS broker, so every NATS trigger failed to
// build. The pool connects once per server URL, on first use.
func TestNATSBrokerPoolConnectsOncePerURL(t *testing.T) {
	var dials atomic.Int32
	fail := true
	pool := sched.NewNATSBrokerPool(func(rawURL string) (*sched.NATSBroker, error) {
		dials.Add(1)
		if rawURL == "nats://down:4222" && fail {
			return nil, errors.New("connect refused")
		}
		return &sched.NATSBroker{}, nil
	})
	a1, err := pool.ForURL("nats://a:4222")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := pool.ForURL("nats://a:4222")
	b, _ := pool.ForURL("nats://b:4222")
	if a1 != a2 || a1 == b {
		t.Fatal("triggers on one URL must share a broker and other URLs get their own")
	}
	if _, err := pool.ForURL("nats://down:4222"); err == nil {
		t.Fatal("a failed connect returned a broker")
	}
	fail = false
	if _, err := pool.ForURL("nats://down:4222"); err != nil {
		t.Fatalf("a failed connect was cached: %v", err)
	}
	if got := dials.Load(); got != 4 {
		t.Fatalf("dials = %d, want 4 (a, b, down twice)", got)
	}
}

type closingSource struct {
	sched.TriggerSource
	closed atomic.Bool
}

func (c *closingSource) Close() error { c.closed.Store(true); return nil }

// Pollers were built once per trigger for the process lifetime: a rotated
// credential or new topic never reached the broker, and a deleted Kafka
// trigger kept its reader in the consumer group.
func TestTriggerPollerRebuiltOnChangeAndClosedWhenGone(t *testing.T) {
	var built []*closingSource
	sched.InstallPollerFactory(t, "fake-broker", func(sqlc.Trigger) (sched.TriggerSource, error) {
		src := &closingSource{}
		built = append(built, src)
		return src, nil
	})
	loop := &sched.Loop{}
	trigger := sqlc.Trigger{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Kind: "fake-broker", Config: json.RawMessage(`{"topic":"a"}`)}
	first, _, err := loop.PollerForTrigger(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if again, _, _ := loop.PollerForTrigger(trigger); again != first || len(built) != 1 {
		t.Fatal("an unchanged trigger must reuse its poller")
	}
	trigger.Config = json.RawMessage(`{"topic":"b"}`)
	changed, _, err := loop.PollerForTrigger(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first || !built[0].closed.Load() {
		t.Fatal("a changed trigger must close its old poller and build a new one")
	}
	loop.EvictTriggerPollers(nil)
	if !built[1].closed.Load() {
		t.Fatal("a trigger that is no longer enabled must have its poller closed")
	}
	if _, ok := (*loop.TriggerPollers())[trigger.ID.String()]; ok {
		t.Fatal("an evicted poller stayed cached")
	}
}
