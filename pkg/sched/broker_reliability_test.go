// adr: 20261111140000 — event delivery reliability repairs.
package sched_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestBrokerReliabilityNATSConsumerSurvivesIdleSchedulingGap(t *testing.T) {
	ctx := context.Background()
	serverURL := os.Getenv("NATS_TEST_URL")
	if serverURL == "" {
		t.Skip("set NATS_TEST_URL to a local JetStream test server")
	}
	connection, err := nats.Connect(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{Name: "AUDIT", Subjects: []string{"audit.>"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(ctx, "AUDIT") })
	trigger := sqlc.Trigger{Config: []byte(`{"url":"nats://127.0.0.1:14222","stream":"AUDIT","subject":"audit.>","durable":"audit-consumer"}`), BatchSizeMax: 1}
	poller, err := sched.ReliabilityNATSPoller(js, trigger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(ctx, "audit.order", []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	first := poller.Poll(ctx, trigger)
	if first.Error != nil || len(first.Records) != 1 {
		t.Fatalf("initial poll=%+v", first)
	}
	if err := poller.Ack(ctx, trigger, []string{first.Records[0].ItemIdentifier}); err != nil {
		t.Fatal(err)
	}
	consumer, err := js.Consumer(ctx, "AUDIT", "audit-consumer")
	if err != nil {
		t.Fatal(err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual server config: Name=%q Durable=%q InactiveThreshold=%v", info.Config.Name, info.Config.Durable, info.Config.InactiveThreshold)
	start := time.Now()
	for time.Since(start) < 15*time.Second {
		time.Sleep(200 * time.Millisecond)
		if _, err := consumer.Info(ctx); err != nil {
			next := poller.Poll(ctx, trigger)
			t.Fatalf("consumer deleted after %v without another pull between scheduler lanes: info error=%v next poll=%+v", time.Since(start).Round(time.Millisecond), err, next)
		}
	}
}

func TestBrokerReliabilityNATSConsumerIsDurable(t *testing.T) {
	cfg := sched.NATSConsumerConfig(sched.NATSConfig{Durable: "gregale-orders", Subject: "orders.>"})
	if cfg.Durable != "gregale-orders" {
		t.Fatalf("configured consumer is ephemeral: Name=%q, Durable=%q", cfg.Name, cfg.Durable)
	}
}

func TestBrokerReliabilityNATSPoolPreservesRetryScheduling(t *testing.T) {
	msg := &reliabilityNATSMessage{}
	underlying := sched.NewNATSPollerWithMessages(map[string]sched.NATSMsg{"events/1@1": msg})
	if _, ok := underlying.(reliabilityRetryScheduler); !ok {
		t.Fatal("unwrapped poller unexpectedly lacks retry scheduling")
	}
	wrapped := sched.ReliabilityWrapNATS(underlying)
	if _, ok := wrapped.(reliabilityRetryScheduler); !ok {
		// This is the dispatcher's fallback when the wrapper hides NackUntil.
		if err := wrapped.Nack(context.Background(), sqlc.Trigger{}, []string{"events/1@1"}, sched.TriggerReasonBrokerError); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("production wrapper hides Defer/NackUntil; a one-minute retry becomes Nack delays=%v", msg.delays)
	}
}

func TestBrokerReliabilityKafkaPoisonRecordBoundsRetainedPayloads(t *testing.T) {
	reader := &replayReliabilityKafkaReader{}
	for offset := int64(1); offset <= 2001; offset++ {
		reader.queue = append(reader.queue, kafka.Message{Topic: "orders", Partition: 0, Offset: offset, Value: make([]byte, 8192)})
	}
	poller := sched.NewKafkaPollerWithReader(reader)
	ctx, trigger := context.Background(), sqlc.Trigger{BrokerPoisonStrategy: "seek-to-offset"}
	var poison string
	for poll := 0; poll < 30; poll++ {
		batch := poller.Poll(ctx, trigger)
		if batch.Error != nil {
			t.Fatal(batch.Error)
		}
		if poison == "" {
			poison = batch.Records[0].ItemIdentifier
		}
		var good []string
		for _, rec := range batch.Records {
			if rec.ItemIdentifier == poison {
				if err := poller.Nack(ctx, trigger, []string{poison}, sched.TriggerReasonPoisonRecord); err != nil {
					t.Fatal(err)
				}
			} else {
				good = append(good, rec.ItemIdentifier)
			}
		}
		if err := poller.Ack(ctx, trigger, good); err != nil {
			t.Fatal(err)
		}
	}
	count, bytes := sched.ReliabilityKafkaBuffered(poller)
	if count > 100 || bytes != 0 {
		t.Fatalf("one poison record retained %d settled payloads (%d bytes) while continuing to drain the partition; commits=%v", count, bytes, reader.commits())
	}
}

func TestBrokerReliabilityRedisReplacementConsumerRecoversPending(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Now()
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.XGroupCreateMkStream(ctx, "orders", "workers", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: "orders", Values: map[string]any{"payload": `{"id":1}`}}).Err(); err != nil {
		t.Fatal(err)
	}
	original := sched.NewRedisStreamPoller(client, "orders", "workers", "old-trigger")
	if got := original.Poll(ctx, sqlc.Trigger{BatchSizeMax: 1}); got.Error != nil || len(got.Records) != 1 {
		t.Fatalf("original poll=%+v", got)
	}
	server.SetTime(now.Add(24 * time.Hour))
	replacement := sched.NewRedisStreamPoller(client, "orders", "workers", "replacement-trigger")
	got := replacement.Poll(ctx, sqlc.Trigger{BatchSizeMax: 1})
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	pending, err := client.XPending(ctx, "orders", "workers").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 {
		t.Fatalf("replacement got %d records with one-day-old pending work still assigned to %v", len(got.Records), pending.Consumers)
	}
}

type reliabilityRetryScheduler interface {
	NackUntil(context.Context, sqlc.Trigger, []string, []time.Time) error
}
type reliabilityNATSMessage struct{ delays []time.Duration }

func (m *reliabilityNATSMessage) Ack() error { return nil }
func (m *reliabilityNATSMessage) NakWithDelay(delay time.Duration) error {
	m.delays = append(m.delays, delay)
	return nil
}
func (m *reliabilityNATSMessage) TermWithReason(string) error { return nil }

func TestBrokerReliabilityNATSWrapperUsesScheduledDelay(t *testing.T) {
	message := &reliabilityNATSMessage{}
	source := sched.ReliabilityWrapNATS(sched.NewNATSPollerWithMessages(map[string]sched.NATSMsg{"m": message}))
	if err := source.(reliabilityRetryScheduler).NackUntil(t.Context(), sqlc.Trigger{}, []string{"m"}, []time.Time{time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(message.delays) != 1 || message.delays[0] < 55*time.Second {
		t.Fatalf("retry delays=%v", message.delays)
	}
	message = &reliabilityNATSMessage{}
	source = sched.ReliabilityWrapNATS(sched.NewNATSPollerWithMessages(map[string]sched.NATSMsg{"m": message}))
	if err := source.(interface {
		Defer(context.Context, sqlc.Trigger, []string) error
	}).Defer(t.Context(), sqlc.Trigger{}, []string{"m"}); err != nil {
		t.Fatal(err)
	}
	if len(message.delays) != 1 || message.delays[0] != 0 {
		t.Fatalf("defer delays=%v", message.delays)
	}
}

func TestBrokerReliabilityRedisRenewalCannotStealChangedOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := t.Context()
	if err := client.XGroupCreateMkStream(ctx, "orders", "workers", "0").Err(); err != nil {
		t.Fatal(err)
	}
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: "orders", Values: map[string]any{"payload": `{"id":1}`}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	source := sched.NewRedisStreamPoller(client, "orders", "workers", "active")
	if result := source.Poll(ctx, sqlc.Trigger{BatchSizeMax: 1}); result.Error != nil || len(result.Records) != 1 {
		t.Fatalf("poll=%+v", result)
	}
	stop := sched.ReliabilityRedisKeepLeases(ctx, source, []string{id})
	defer stop()
	if _, err = client.XClaim(ctx, &redis.XClaimArgs{Stream: "orders", Group: "workers", Consumer: "new-owner", MinIdle: 0, Messages: []string{id}}).Result(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: "orders", Group: "workers", Start: id, End: id, Count: 1}).Result()
	if err != nil || len(pending) != 1 || pending[0].Consumer != "new-owner" {
		t.Fatalf("renewal stole changed ownership: pending=%+v error=%v", pending, err)
	}
}

func TestBrokerReliabilityNATSRebuildsDeletedConsumer(t *testing.T) {
	raw := os.Getenv("NATS_TEST_URL")
	if raw == "" {
		t.Skip("set NATS_TEST_URL to a local JetStream test server")
	}
	ctx := context.Background()
	connection, err := nats.Connect(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{Name: "RELIABILITY_RECREATE", Subjects: []string{"reliability.recreate"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(ctx, "RELIABILITY_RECREATE") })
	trigger := sqlc.Trigger{Kind: "nats", Config: []byte(`{"url":"nats://127.0.0.1:14222","stream":"RELIABILITY_RECREATE","subject":"reliability.recreate","durable":"reliability-recreate"}`), BatchSizeMax: 10}
	var builds int
	sched.InstallPollerFactory(t, "nats", func(t sqlc.Trigger) (sched.TriggerSource, error) {
		builds++
		source, err := sched.ReliabilityNATSPoller(js, t)
		if err != nil {
			return nil, err
		}
		return sched.ReliabilityWrapNATS(source), nil
	})
	loop := sched.MakeLoopForDLQ()
	original, _, err := loop.PollerForTrigger(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if err = js.DeleteConsumer(ctx, "RELIABILITY_RECREATE", "reliability-recreate"); err != nil {
		t.Fatal(err)
	}
	_ = original.Poll(ctx, trigger)
	if !original.(interface{ Closed() bool }).Closed() {
		t.Fatal("pool hid missing consumer from cache invalidation")
	}
	replacement, _, err := loop.PollerForTrigger(trigger)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == original || builds != 2 {
		t.Fatalf("missing consumer did not rebuild: builds=%d", builds)
	}
	if _, err = js.Publish(ctx, "reliability.recreate", []byte(`{"id":2}`)); err != nil {
		t.Fatal(err)
	}
	result := replacement.Poll(ctx, trigger)
	if result.Error != nil || len(result.Records) != 1 {
		t.Fatalf("rebuilt poll=%+v", result)
	}
}

func TestBrokerReliabilityNATSUpgradesEphemeralConsumer(t *testing.T) {
	raw := os.Getenv("NATS_TEST_URL")
	if raw == "" {
		t.Skip("set NATS_TEST_URL to a local JetStream test server")
	}
	ctx := context.Background()
	connection, err := nats.Connect(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{Name: "RELIABILITY_UPGRADE", Subjects: []string{"reliability.upgrade"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(ctx, "RELIABILITY_UPGRADE") })
	legacy, err := js.CreateOrUpdateConsumer(ctx, "RELIABILITY_UPGRADE", jetstream.ConsumerConfig{Name: "reliability-upgrade", FilterSubject: "reliability.upgrade", AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverAllPolicy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = js.Publish(ctx, "reliability.upgrade", []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	message, err := legacy.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = message.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	trigger := sqlc.Trigger{Kind: "nats", Config: []byte(`{"url":"nats://127.0.0.1:14222","stream":"RELIABILITY_UPGRADE","subject":"reliability.upgrade","durable":"reliability-upgrade"}`), BatchSizeMax: 10}
	if _, err = sched.ReliabilityNATSPoller(js, trigger); err != nil {
		t.Fatalf("upgrade of existing ephemeral consumer: %v", err)
	}
	consumer, err := js.Consumer(ctx, "RELIABILITY_UPGRADE", "reliability-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	info, err := consumer.Info(ctx)
	if err != nil || info.Config.Durable != "reliability-upgrade" || info.Config.InactiveThreshold != 0 {
		t.Fatalf("upgraded consumer info=%+v error=%v", info, err)
	}
	if info.AckFloor.Stream != 1 {
		t.Fatalf("upgrade reset the acknowledgement position: %+v", info.AckFloor)
	}
	time.Sleep(8 * time.Second)
	if _, err = consumer.Info(ctx); err != nil {
		t.Fatalf("upgraded durable consumer expired: %v", err)
	}
}
