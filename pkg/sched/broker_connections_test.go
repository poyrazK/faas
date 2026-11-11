// adr: 20261101104512 — broker trigger connections are managed (NATS dials
// outside the pool lock, idle and closed connections go), Kafka lag comes
// from committed offsets, and a Kafka group that cannot join fails its poll.
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A server that does not answer holds only the pollers waiting for it: a
// trigger on another server connects meanwhile, and the waiters share one
// dial.
func TestNATSBrokerPoolDialsOutsideItsLock(t *testing.T) {
	unblock := make(chan struct{})
	var slowDials atomic.Int32
	pool := sched.XNATSBrokerPool(func(rawURL string) (*sched.NATSBroker, error) {
		if rawURL == "nats://slow:4222" {
			slowDials.Add(1)
			<-unblock
		}
		return &sched.NATSBroker{}, nil
	}, time.Hour, func(*sched.NATSBroker) bool { return false })
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})

	var waiters sync.WaitGroup
	brokers := make([]*sched.NATSBroker, 3)
	for i := range brokers {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			broker, _, err := pool.Acquire("nats://slow:4222")
			if err != nil {
				t.Error(err)
			}
			brokers[i] = broker
		}()
	}
	eventually(t, "the slow dial to start", func() bool { return slowDials.Load() == 1 })

	done := make(chan error, 1)
	go func() {
		_, _, err := pool.Acquire("nats://fast:4222")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a trigger on another server waited for the slow server's dial")
	}

	close(unblock)
	waiters.Wait()
	if got := slowDials.Load(); got != 1 {
		t.Fatalf("slow server dialed %d times, want once for all its waiters", got)
	}
	if brokers[0] == nil || brokers[0] != brokers[1] || brokers[1] != brokers[2] {
		t.Fatal("pollers waiting on one server did not share its connection")
	}
}

// A connection no poller holds closes once idle; one still held does not.
func TestNATSBrokerPoolClosesIdleConnections(t *testing.T) {
	pool := sched.XNATSBrokerPool(func(string) (*sched.NATSBroker, error) {
		return &sched.NATSBroker{}, nil
	}, 20*time.Millisecond, func(*sched.NATSBroker) bool { return false })

	_, releaseA1, err := pool.Acquire("nats://a:4222")
	if err != nil {
		t.Fatal(err)
	}
	_, releaseA2, _ := pool.Acquire("nats://a:4222")
	_, releaseB, _ := pool.Acquire("nats://b:4222")
	releaseA1()
	releaseA1() // a second release of one reference is a no-op
	releaseB()
	eventually(t, "the unused connection to close", func() bool { return !pool.Open("nats://b:4222") })
	time.Sleep(60 * time.Millisecond)
	if !pool.Open("nats://a:4222") {
		t.Fatal("a connection a poller still holds was closed")
	}
	releaseA2()
	eventually(t, "the last poller's connection to close", func() bool { return !pool.Open("nats://a:4222") })
}

// A connection nats.go closed for good (after repeated authorization
// errors) is replaced by a new dial instead of being handed out again.
func TestNATSBrokerPoolReplacesClosedConnections(t *testing.T) {
	var dials atomic.Int32
	var closed sync.Map
	pool := sched.XNATSBrokerPool(func(string) (*sched.NATSBroker, error) {
		dials.Add(1)
		return &sched.NATSBroker{}, nil
	}, time.Hour, func(b *sched.NATSBroker) bool {
		_, gone := closed.Load(b)
		return gone
	})
	first, _, err := pool.Acquire("nats://a:4222")
	if err != nil {
		t.Fatal(err)
	}
	closed.Store(first, true)
	second, _, err := pool.Acquire("nats://a:4222")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || dials.Load() != 2 {
		t.Fatalf("closed connection handed out again (dials = %d)", dials.Load())
	}
}

// closingLaneSource is a lane-broker poller whose connection can be closed
// for good.
type closingLaneSource struct {
	sched.TriggerSource
	gone   atomic.Bool
	closes atomic.Int32
}

func (s *closingLaneSource) Kind() string { return "lane-broker" }
func (s *closingLaneSource) Poll(context.Context, sqlc.Trigger) sched.PollResult {
	return sched.PollResult{}
}
func (s *closingLaneSource) Close() error { s.closes.Add(1); return nil }
func (s *closingLaneSource) Closed() bool { return s.gone.Load() }

// The trigger tick rebuilds a poller whose broker connection closed for
// good, and closes the old one.
func TestTriggerTickRebuildsAPollerWhoseConnectionClosed(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, trigger := laneTrigger(t, store, "closed-conn@example.test", "closed-conn")
	var built []*closingLaneSource
	var mu sync.Mutex
	sched.InstallPollerFactory(t, "lane-broker", func(sqlc.Trigger) (sched.TriggerSource, error) {
		mu.Lock()
		defer mu.Unlock()
		source := &closingLaneSource{}
		built = append(built, source)
		return source, nil
	})
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil))).WithGatewayHTTPClient(http.DefaultClient, "http://gateway.invalid")
	tick := func() {
		loop.XRunTriggerTick(ctx)
		loop.WorkPool().Drain()
	}

	tick()
	tick()
	mu.Lock()
	if len(built) != 1 {
		mu.Unlock()
		t.Fatalf("a live poller was rebuilt: %d builds", len(built))
	}
	first := built[0]
	mu.Unlock()
	first.gone.Store(true)
	tick()
	mu.Lock()
	defer mu.Unlock()
	if len(built) != 2 {
		t.Fatalf("builds after the connection closed = %d, want 2", len(built))
	}
	if got := first.closes.Load(); got != 1 {
		t.Fatalf("the closed poller was closed %d times, want 1", got)
	}
	if (*loop.TriggerPollers())[trigger.ID.String()] != sched.TriggerSource(built[1]) {
		t.Fatal("the trigger does not use the rebuilt poller")
	}
}

// BrokerLag reads the pollers the trigger tick keeps and never builds one:
// for a trigger the tick does not poll, a Kafka reader would join its
// group only to be evicted on the next tick.
func TestBrokerLagNeverBuildsAPoller(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app, trigger := laneTrigger(t, store, "lag-only@example.test", "lag-only")
	var builds atomic.Int32
	sched.InstallPollerFactory(t, "lane-broker", func(sqlc.Trigger) (sched.TriggerSource, error) {
		builds.Add(1)
		return &closingLaneSource{}, nil
	})
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, available, err := loop.BrokerLag(ctx, app.ID); err != nil || available {
		t.Fatalf("BrokerLag = available %v, err %v; want no reading", available, err)
	}
	if got := builds.Load(); got != 0 {
		t.Fatalf("BrokerLag built %d pollers", got)
	}
	if _, cached := (*loop.TriggerPollers())[trigger.ID.String()]; cached {
		t.Fatal("BrokerLag cached a poller")
	}
}

func TestKafkaGroupLagFromCommittedOffsets(t *testing.T) {
	tests := []struct {
		name       string
		committed  map[int]int64
		watermarks []kafka.PartitionOffsets
		want       int64
		wantErr    bool
	}{
		{
			name:      "committed offsets, summed over partitions",
			committed: map[int]int64{0: 90, 1: 25},
			watermarks: []kafka.PartitionOffsets{
				{Partition: 0, FirstOffset: 0, LastOffset: 100},
				{Partition: 1, FirstOffset: 0, LastOffset: 30},
			},
			want: 15,
		},
		{
			name:       "a group with no commit starts at the log start",
			committed:  map[int]int64{0: -1},
			watermarks: []kafka.PartitionOffsets{{Partition: 0, FirstOffset: 40, LastOffset: 100}},
			want:       60,
		},
		{
			name:       "a partition the group never committed",
			committed:  map[int]int64{},
			watermarks: []kafka.PartitionOffsets{{Partition: 3, FirstOffset: 0, LastOffset: 7}},
			want:       7,
		},
		{
			name:       "a log truncated past the commit",
			committed:  map[int]int64{0: 5},
			watermarks: []kafka.PartitionOffsets{{Partition: 0, FirstOffset: 50, LastOffset: 80}},
			want:       30,
		},
		{
			name:       "caught up",
			committed:  map[int]int64{0: 80},
			watermarks: []kafka.PartitionOffsets{{Partition: 0, FirstOffset: 0, LastOffset: 80}},
			want:       0,
		},
		{
			name:       "a partition error fails the reading",
			committed:  map[int]int64{0: 1},
			watermarks: []kafka.PartitionOffsets{{Partition: 0, Error: kafka.NotLeaderForPartition}},
			wantErr:    true,
		},
		{name: "no watermarks fails the reading", committed: map[int]int64{}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sched.XKafkaGroupLag(tc.committed, tc.watermarks)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("lag = %d, want %d", got, tc.want)
			}
		})
	}
}

// fakeKafkaLagClient answers lag reads for one topic.
type fakeKafkaLagClient struct {
	topic      string
	committed  map[int]int64
	watermarks []kafka.PartitionOffsets
	reads      atomic.Int32
}

func (c *fakeKafkaLagClient) Metadata(context.Context, *kafka.MetadataRequest) (*kafka.MetadataResponse, error) {
	c.reads.Add(1)
	topic := kafka.Topic{Name: c.topic}
	for _, w := range c.watermarks {
		topic.Partitions = append(topic.Partitions, kafka.Partition{Topic: c.topic, ID: w.Partition})
	}
	return &kafka.MetadataResponse{Topics: []kafka.Topic{topic}}, nil
}

func (c *fakeKafkaLagClient) OffsetFetch(_ context.Context, req *kafka.OffsetFetchRequest) (*kafka.OffsetFetchResponse, error) {
	var out []kafka.OffsetFetchPartition
	for _, p := range req.Topics[c.topic] {
		offset, ok := c.committed[p]
		if !ok {
			offset = -1
		}
		out = append(out, kafka.OffsetFetchPartition{Partition: p, CommittedOffset: offset})
	}
	return &kafka.OffsetFetchResponse{Topics: map[string][]kafka.OffsetFetchPartition{c.topic: out}}, nil
}

func (c *fakeKafkaLagClient) ListOffsets(context.Context, *kafka.ListOffsetsRequest) (*kafka.ListOffsetsResponse, error) {
	return &kafka.ListOffsetsResponse{Topics: map[string][]kafka.PartitionOffsets{c.topic: c.watermarks}}, nil
}

// scriptedFetchReader hands out its messages, then reports an idle fetch.
type scriptedFetchReader struct {
	mu       sync.Mutex
	messages []kafka.Message
}

func (r *scriptedFetchReader) push(msg kafka.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
}

func (r *scriptedFetchReader) FetchMessage(context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) == 0 {
		return kafka.Message{}, context.DeadlineExceeded
	}
	msg := r.messages[0]
	r.messages = r.messages[1:]
	return msg, nil
}
func (r *scriptedFetchReader) CommitMessages(context.Context, ...kafka.Message) error { return nil }
func (r *scriptedFetchReader) Stats() kafka.ReaderStats {
	// The old lag source: one gauge, read from the fetch position.
	return kafka.ReaderStats{Lag: 1}
}
func (r *scriptedFetchReader) Close() error { return nil }

type brokerStatsSource interface {
	BrokerStats(context.Context, sqlc.Trigger) sched.BrokerStats
}

// A Kafka trigger's lag is the backlog behind the group's commit on every
// partition, read at most every 10 seconds.
func TestKafkaPollerReportsCommittedLag(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	client := &fakeKafkaLagClient{
		topic:     "orders",
		committed: map[int]int64{0: 90, 1: 10},
		watermarks: []kafka.PartitionOffsets{
			{Partition: 0, FirstOffset: 0, LastOffset: 100},
			{Partition: 1, FirstOffset: 0, LastOffset: 50},
		},
	}
	poller, _ := sched.XNewKafkaPollerWithLag(&scriptedFetchReader{}, client, "orders", "faas-orders", clock)
	stats := poller.(brokerStatsSource).BrokerStats(context.Background(), sqlc.Trigger{})
	if !stats.Available || stats.Lag != 50 {
		t.Fatalf("stats = %+v, want 50 records of lag (10 + 40)", stats)
	}
	client.committed[1] = 50
	if again := poller.(brokerStatsSource).BrokerStats(context.Background(), sqlc.Trigger{}); again.Lag != 50 || client.reads.Load() != 1 {
		t.Fatalf("a read within 10 s went to the broker: lag %d after %d reads", again.Lag, client.reads.Load())
	}
	mu.Lock()
	now = now.Add(11 * time.Second)
	mu.Unlock()
	if later := poller.(brokerStatsSource).BrokerStats(context.Background(), sqlc.Trigger{}); later.Lag != 10 {
		t.Fatalf("lag after the commit moved = %d, want 10", later.Lag)
	}
}

// kafka-go reports a consumer group that cannot join only through its error
// logger; an empty poll carries that error until it ages out or a record
// arrives.
func TestKafkaPollReportsConsumerGroupErrors(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	reader := &scriptedFetchReader{}
	poller, logf := sched.XNewKafkaPollerWithLag(reader, &fakeKafkaLagClient{topic: "orders"}, "orders", "faas-orders", clock)
	poll := func() sched.PollResult { return poller.Poll(context.Background(), sqlc.Trigger{}) }

	if res := poll(); res.Error != nil {
		t.Fatalf("an idle healthy group failed its poll: %v", res.Error)
	}
	logf("the kafka reader is reading before the first offset for partition %d of %s", 0, "orders")
	if res := poll(); res.Error != nil {
		t.Fatalf("an informational log failed the poll: %v", res.Error)
	}
	logf("Failed to join group %s: %v", "faas-orders", errors.New("SASL Authentication failed"))
	res := poll()
	if res.Error == nil || !strings.Contains(res.Error.Error(), "SASL Authentication failed") {
		t.Fatalf("poll of a group that cannot join = %+v, want its error", res)
	}
	advance(31 * time.Second)
	if res := poll(); res.Error != nil {
		t.Fatalf("an error 31 s old still fails the poll: %v", res.Error)
	}

	logf("Unable to establish connection to consumer group coordinator for group %s: %v", "faas-orders", fmt.Errorf("dial tcp: i/o timeout"))
	reader.push(kafka.Message{Topic: "orders", Partition: 0, Offset: 7, Value: []byte(`{}`)})
	if res := poll(); res.Error != nil || len(res.Records) != 1 {
		t.Fatalf("poll with a record = %+v, want the record", res)
	}
	if res := poll(); res.Error != nil {
		t.Fatalf("a fetched record did not clear the group error: %v", res.Error)
	}
}
