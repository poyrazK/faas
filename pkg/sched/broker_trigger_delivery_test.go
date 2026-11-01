// adr: 20261101093512 — external broker trigger delivery: batch carry-over
// and payload_too_large dead letters, settled redeliveries of dead letters,
// claim renewal, NATS retry holds, record identities naming the topic or
// stream, and the Redis pending-list read bound.
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const scriptedBrokerKind = "scripted-broker"

// scriptedBroker is an external broker poller. Poll serves what is queued;
// a retryable Nack or Defer queues the record again. Every settlement is
// recorded as "<verb> <item>".
type scriptedBroker struct {
	mu     sync.Mutex
	queue  []sched.SourceRecord
	served map[string]sched.SourceRecord
	calls  []string
}

func newScriptedBroker(records ...sched.SourceRecord) *scriptedBroker {
	return &scriptedBroker{queue: records, served: map[string]sched.SourceRecord{}}
}

func (b *scriptedBroker) Kind() string { return scriptedBrokerKind }
func (b *scriptedBroker) Close() error { return nil }

func (b *scriptedBroker) Poll(context.Context, sqlc.Trigger) sched.PollResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.queue
	b.queue = nil
	for _, rec := range out {
		b.served[rec.ItemIdentifier] = rec
	}
	return sched.PollResult{Records: out}
}

func (b *scriptedBroker) record(verb string, ids []string) {
	for _, id := range ids {
		b.calls = append(b.calls, verb+" "+id)
	}
}

func (b *scriptedBroker) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("ack", ids)
	return nil
}

func (b *scriptedBroker) Nack(_ context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("nack "+reason, ids)
	if reason == sched.TriggerReasonBrokerError {
		for _, id := range ids {
			b.queue = append(b.queue, b.served[id])
		}
	}
	return nil
}

func (b *scriptedBroker) Defer(_ context.Context, _ sqlc.Trigger, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record("defer", ids)
	back := make([]sched.SourceRecord, 0, len(ids))
	for _, id := range ids {
		back = append(back, b.served[id])
	}
	b.queue = append(back, b.queue...)
	return nil
}

func (b *scriptedBroker) takeCalls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.calls
	b.calls = nil
	slices.Sort(out)
	return out
}

// deliveryFixture is a MemStore trigger on a scripted broker and a gateway
// that answers every record with status(item).
type deliveryFixture struct {
	store    *state.MemStore
	trigger  sqlc.Trigger
	loop     *sched.Loop
	mu       sync.Mutex
	gateway  [][]string
	onRecord func(item string)
}

func newDeliveryFixture(t *testing.T, broker *scriptedBroker, batchSizeMax, payloadMaxBytes int32) *deliveryFixture {
	t.Helper()
	ctx := context.Background()
	f := &deliveryFixture{store: state.NewMemStore()}
	acct, err := f.store.CreateAccount(ctx, "delivery-"+fmt.Sprint(time.Now().UnixNano())+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "delivery-app"})
	if err != nil {
		t.Fatal(err)
	}
	f.trigger, err = f.store.CreateTriggerIfUnderQuota(ctx, app.ID, scriptedBrokerKind, "events", true, []byte(`{}`), "",
		batchSizeMax, 1000, 5, payloadMaxBytes, "commit", api.MustLimitsFor(api.PlanScale))
	if err != nil {
		t.Fatal(err)
	}
	sched.InstallPollerFactory(t, scriptedBrokerKind, func(sqlc.Trigger) (sched.TriggerSource, error) { return broker, nil })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req sched.TriggerDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		onRecord := f.onRecord
		f.mu.Unlock()
		var items []string
		var resp sched.TriggerDispatchResponse
		for _, rec := range req.Records {
			items = append(items, rec.ItemIdentifier)
			if onRecord != nil {
				onRecord(rec.ItemIdentifier)
			}
			resp.Results = append(resp.Results, sched.TriggerDispatchResult{ItemIdentifier: rec.ItemIdentifier, Status: "succeeded"})
		}
		f.mu.Lock()
		f.gateway = append(f.gateway, items)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(gateway.Close)
	f.loop = sched.MakeLoopForDLQ()
	(*f.loop.GatewayHTTPClient()), (*f.loop.GatewayBaseURL()) = gateway.Client(), gateway.URL
	return f
}

func (f *deliveryFixture) dispatch(t *testing.T) {
	t.Helper()
	if err := f.loop.DispatchOneTrigger(context.Background(), f.trigger, f.store, func(string) api.Plan { return api.PlanScale }); err != nil {
		t.Fatal(err)
	}
}

func (f *deliveryFixture) dispatched() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.gateway
	f.gateway = nil
	return out
}

func (f *deliveryFixture) states(t *testing.T, items ...string) map[string]state.TriggerRecordItemState {
	t.Helper()
	rows, err := f.store.TriggerRecordStatesByItems(context.Background(), f.trigger.ID.String(), items)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]state.TriggerRecordItemState, len(rows))
	for _, row := range rows {
		out[row.Item] = row
	}
	return out
}

// seed stores item and settles it: "succeeded", "retry" (due now) or a
// dead-letter reason.
func (f *deliveryFixture) seed(t *testing.T, item, outcome string, metadata []byte) {
	t.Helper()
	ctx := context.Background()
	id, err := f.store.InsertTriggerRecord(ctx, f.trigger.ID.String(), item, []byte(`{}`), nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.store.ClaimTriggerRecordsByItems(ctx, f.trigger.ID.String(), []string{item})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim %s = %+v, %v", item, claimed, err)
	}
	gen := claimed[0].ClaimGeneration
	switch outcome {
	case "succeeded":
		err = f.store.CompleteClaimedTriggerRecord(ctx, id, gen)
	case "retry":
		err = f.store.RetryClaimedTriggerRecord(ctx, id, gen, "earlier failure", time.Now().Add(-time.Second))
	default:
		err = f.store.RouteClaimedTriggerDeadLetter(ctx, id, gen, f.trigger.ID.String(), outcome, []byte(`{}`))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func payloadRecord(item string, size int) sched.SourceRecord {
	return sched.SourceRecord{ItemIdentifier: item, StableIdentifier: item,
		Payload: []byte(`{"blob":"` + strings.Repeat("x", size) + `"}`)}
}

// A batch stopped at the first record that did not fit and left the rest,
// and a record over payload_max_bytes, with the poller unsettled: Kafka
// stopped committing, Redis kept the entry at the head of its pending list
// and NATS redelivered it until MaxDeliver. Records that did not fit now go
// back for the next batch, and an oversized one is dead-lettered as
// payload_too_large and settled as a poison record.
func TestBrokerBatchCarriesTheRestAndDeadLettersOversizedRecords(t *testing.T) {
	broker := newScriptedBroker(payloadRecord("a", 10), payloadRecord("big", 2000), payloadRecord("b", 10), payloadRecord("c", 10))
	f := newDeliveryFixture(t, broker, 2, 1024)

	f.dispatch(t)
	if got := f.dispatched(); len(got) != 1 || !slices.Equal(got[0], []string{"a", "b"}) {
		t.Fatalf("first batch = %v, want [[a b]]", got)
	}
	if got, want := broker.takeCalls(), []string{"ack a", "ack b", "defer c", "nack poison_record big"}; !slices.Equal(got, want) {
		t.Fatalf("broker calls = %q, want %q", got, want)
	}
	big := f.states(t, "big")["big"]
	if big.State != "dead_letter" || big.DeadLetterReason != sched.TriggerReasonPayloadTooLarge {
		t.Fatalf("oversized record = %+v, want a payload_too_large dead letter", big)
	}
	rows, err := f.store.ListTriggerRecordsForTrigger(context.Background(), f.trigger.ID.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemIdentifier == "big" && (string(row.Payload) != "{}" || !strings.Contains(string(row.Metadata), "payload_omitted_bytes")) {
			t.Fatalf("oversized record kept payload %q metadata %s", row.Payload, row.Metadata)
		}
	}

	f.dispatch(t)
	if got := f.dispatched(); len(got) != 1 || !slices.Equal(got[0], []string{"c"}) {
		t.Fatalf("second batch = %v, want the carried record", got)
	}
	if got := broker.takeCalls(); !slices.Equal(got, []string{"ack c"}) {
		t.Fatalf("second batch broker calls = %q", got)
	}
}

// A dead-lettered record was neither terminal nor claimable, so its
// redelivery went back to the broker forever; a Kafka partition whose dead
// letter waited for lower offsets stopped committing after a restart. Each
// now settles as its dead letter did, without running again.
func TestDeadLetteredBrokerRedeliverySettles(t *testing.T) {
	broker := newScriptedBroker()
	f := newDeliveryFixture(t, broker, 10, 1<<20)
	f.seed(t, "poisoned", sched.TriggerReasonPoisonRecord, nil)
	f.seed(t, "exhausted", sched.TriggerReasonMaxAttempts, nil)
	f.seed(t, "limited", sched.TriggerReasonRateLimited, nil)
	f.seed(t, "done", "succeeded", nil)
	for _, item := range []string{"poisoned", "exhausted", "limited", "done", "fresh"} {
		broker.queue = append(broker.queue, payloadRecord(item, 1))
	}

	f.dispatch(t)
	if got := f.dispatched(); len(got) != 1 || !slices.Equal(got[0], []string{"fresh"}) {
		t.Fatalf("dispatched = %v, want only the fresh record", got)
	}
	want := []string{"ack done", "ack fresh", "ack limited", "nack max_attempts exhausted", "nack poison_record poisoned"}
	if got := broker.takeCalls(); !slices.Equal(got, want) {
		t.Fatalf("broker calls = %q, want %q", got, want)
	}
}

// The claim lasted ten minutes while a long-timeout app's batch may wait
// 16.5: the outcome could not be recorded, the delivery was never settled
// and the record ran again. A running batch now renews its claims.
func TestLongBrokerBatchRenewsItsClaims(t *testing.T) {
	broker := newScriptedBroker(payloadRecord("slow", 1))
	f := newDeliveryFixture(t, broker, 10, 1<<20)
	*f.loop.TriggerClaimRenewInterval() = 5 * time.Millisecond
	expiry := func(item string) time.Time {
		rows, _ := f.store.TriggerRecordStatesByItems(context.Background(), f.trigger.ID.String(), []string{item})
		if len(rows) != 1 {
			return time.Time{}
		}
		return rows[0].ClaimExpiresAt
	}
	var renewed atomic.Bool
	// The gateway holds the batch until the claim has been renewed.
	f.mu.Lock()
	f.onRecord = func(item string) {
		first := expiry(item)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			if expiry(item).After(first) {
				renewed.Store(true)
				return
			}
		}
	}
	f.mu.Unlock()
	f.dispatch(t)
	if !renewed.Load() {
		t.Fatal("the claim was not renewed while the batch ran")
	}
	if got := f.states(t, "slow")["slow"].State; got != "succeeded" {
		t.Fatalf("record = %s, want succeeded", got)
	}
	if got := broker.takeCalls(); !slices.Equal(got, []string{"ack slow"}) {
		t.Fatalf("broker calls = %q", got)
	}
}

// A transient store error at an ack point skipped the broker settlement; it
// is now retried, and a lost claim (ErrNotFound) is final.
func TestTriggerClaimOutcomeRetriesTransientErrors(t *testing.T) {
	calls := 0
	err := sched.WriteTriggerClaimOutcome(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errors.New("connection reset")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("transient errors: err=%v calls=%d, want success on the third write", err, calls)
	}
	calls = 0
	err = sched.WriteTriggerClaimOutcome(context.Background(), func() error {
		calls++
		return state.ErrNotFound
	})
	if !errors.Is(err, state.ErrNotFound) || calls != 1 {
		t.Fatalf("lost claim: err=%v calls=%d, want one ErrNotFound", err, calls)
	}
}

// Kafka and NATS record IDs were partition-offset and the stream sequence,
// so a new topic or recreated stream reused stored IDs and new records were
// acknowledged as already done. Records an earlier release stored keep their
// row while it is in progress, or (Kafka) while its topic matches.
func TestBrokerRecordsKeepEarlierReleaseRows(t *testing.T) {
	broker := newScriptedBroker()
	f := newDeliveryFixture(t, broker, 10, 1<<20)
	f.seed(t, "2-7", "retry", []byte(`{"topic":"orders"}`))
	f.seed(t, "3-9", "succeeded", []byte(`{"topic":"old-orders"}`))
	f.seed(t, "4-1", "succeeded", []byte(`{"topic":"orders"}`))
	for _, rec := range []struct{ id, legacy string }{{"orders/2/7@1", "2-7"}, {"orders/3/9@1", "3-9"}, {"orders/4/1@1", "4-1"}} {
		r := payloadRecord(rec.id, 1)
		r.LegacyIdentifiers = []string{rec.legacy}
		r.Metadata = map[string]any{"topic": "orders"}
		broker.queue = append(broker.queue, r)
	}

	f.dispatch(t)
	if got := f.dispatched(); len(got) != 1 || !slices.Equal(got[0], []string{"2-7", "orders/3/9@1"}) {
		t.Fatalf("dispatched = %v, want the in-progress legacy row and the new topic's record", got)
	}
	want := []string{"ack orders/2/7@1", "ack orders/3/9@1", "ack orders/4/1@1"}
	if got := broker.takeCalls(); !slices.Equal(got, want) {
		t.Fatalf("broker calls = %q, want %q", got, want)
	}
	states := f.states(t, "2-7", "3-9", "orders/3/9@1", "orders/2/7@1")
	if states["2-7"].State != "succeeded" || states["orders/3/9@1"].State != "succeeded" {
		t.Fatalf("states = %+v", states)
	}
	if _, split := states["orders/2/7@1"]; split {
		t.Fatal("an in-progress legacy record got a second row")
	}
}

func TestKafkaRecordIDNamesTopicAndGeneration(t *testing.T) {
	stored := time.UnixMilli(1_700_000_000_000)
	reader := &scriptedKafka{queue: []kafka.Message{
		{Topic: "orders", Partition: 2, Offset: 7, Time: stored},
		{Topic: "payments", Partition: 2, Offset: 7, Time: stored},
		{Topic: "orders", Partition: 2, Offset: 7, Time: stored.Add(time.Hour)},
	}}
	res := sched.NewKafkaPollerWithReader(reader).Poll(context.Background(), sqlc.Trigger{})
	if res.Error != nil || len(res.Records) != 3 {
		t.Fatalf("poll = %+v", res)
	}
	first := res.Records[0]
	if first.ItemIdentifier != "orders/2/7@1700000000000" || first.StableIdentifier != first.ItemIdentifier ||
		!slices.Equal(first.LegacyIdentifiers, []string{"2-7", "orders/2/7"}) {
		t.Fatalf("record = %q stable %q legacy %q", first.ItemIdentifier, first.StableIdentifier, first.LegacyIdentifiers)
	}
	if res.Records[1].ItemIdentifier == first.ItemIdentifier || res.Records[2].ItemIdentifier == first.ItemIdentifier {
		t.Fatal("another topic or a recreated topic reused the record ID")
	}
}

// Acknowledging a terminal redelivery commits nothing past an earlier
// offset that is still open, so the dispatcher can acknowledge it at once.
func TestKafkaTerminalAckDoesNotCommitPastOpenOffset(t *testing.T) {
	reader := &scriptedKafka{queue: []kafka.Message{
		{Topic: "orders", Partition: 0, Offset: 1}, {Topic: "orders", Partition: 0, Offset: 2},
	}}
	poller := sched.NewKafkaPollerWithReader(reader)
	ctx, trigger := context.Background(), sqlc.Trigger{}
	res := poller.Poll(ctx, trigger)
	if err := poller.Ack(ctx, trigger, []string{res.Records[1].ItemIdentifier}); err != nil {
		t.Fatal(err)
	}
	if got := reader.commits(); len(got) != 0 {
		t.Fatalf("committed %v past open offset 1", got)
	}
	if err := poller.Ack(ctx, trigger, []string{res.Records[0].ItemIdentifier}); err != nil {
		t.Fatal(err)
	}
	if got := reader.commits(); !slices.Contains(got, 2) {
		t.Fatalf("commits = %v, want through offset 2", got)
	}
}

type kafkaHolder interface {
	Defer(context.Context, sqlc.Trigger, []string) error
	NackUntil(context.Context, sqlc.Trigger, []string, []time.Time) error
}

// Defer hands records back first in the next poll; NackUntil keeps a
// cooling retry back until it is due. Neither commits.
func TestKafkaDeferAndRetryHold(t *testing.T) {
	reader := &scriptedKafka{queue: []kafka.Message{
		{Topic: "orders", Partition: 0, Offset: 1}, {Topic: "orders", Partition: 0, Offset: 2}, {Topic: "orders", Partition: 0, Offset: 3},
	}}
	poller := sched.NewKafkaPollerWithReader(reader)
	holder := poller.(kafkaHolder)
	ctx, trigger := context.Background(), sqlc.Trigger{}
	res := poller.Poll(ctx, trigger)
	ids := []string{res.Records[0].ItemIdentifier, res.Records[1].ItemIdentifier, res.Records[2].ItemIdentifier}
	if err := holder.NackUntil(ctx, trigger, ids[:1], []time.Time{time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := holder.Defer(ctx, trigger, ids[1:]); err != nil {
		t.Fatal(err)
	}
	again := poller.Poll(ctx, trigger)
	if len(again.Records) != 2 || again.Records[0].ItemIdentifier != ids[1] || again.Records[1].ItemIdentifier != ids[2] {
		t.Fatalf("next poll = %+v, want the deferred records and not the cooling one", again.Records)
	}
	if got := reader.commits(); len(got) != 0 {
		t.Fatalf("commits = %v, want none while offset 1 cools", got)
	}
	if err := holder.NackUntil(ctx, trigger, ids[1:2], []time.Time{time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if due := poller.Poll(ctx, trigger); len(due.Records) != 1 || due.Records[0].ItemIdentifier != ids[1] {
		t.Fatalf("a due retry did not come back: %+v", due.Records)
	}
}

// natsStub records the delays a message was nakked with.
type natsStub struct {
	delays []time.Duration
}

func (s *natsStub) Ack() error                  { return nil }
func (s *natsStub) TermWithReason(string) error { return nil }
func (s *natsStub) NakWithDelay(d time.Duration) error {
	s.delays = append(s.delays, d)
	return nil
}

// The consumer had MaxDeliver 25 and every retry nakked with a fixed 2 s,
// so redeliveries during a backoff spent the deliveries and the server
// dropped the message around attempt 5 while its record stayed `retry`.
// The consumer now has no delivery cap and a retry waits out its delay.
func TestNATSRetriesWaitForTheirDueTime(t *testing.T) {
	cfg := sched.NATSConsumerConfig(sched.NATSConfig{Durable: "faas-t", Subject: "events.>"})
	if cfg.MaxDeliver != -1 || cfg.DeliverPolicy != jetstream.DeliverAllPolicy || cfg.AckPolicy != jetstream.AckExplicitPolicy {
		t.Fatalf("consumer config = %+v, want unlimited deliveries from the start of the stream", cfg)
	}
	first, early, carried := &natsStub{}, &natsStub{}, &natsStub{}
	poller := sched.NewNATSPollerWithMessages(map[string]sched.NATSMsg{"events/1@1": first, "events/2@1": carried})
	holder := poller.(kafkaHolder)
	ctx, trigger := context.Background(), sqlc.Trigger{}
	if err := holder.NackUntil(ctx, trigger, []string{"events/1@1"}, []time.Time{time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(first.delays) != 1 || first.delays[0] < 55*time.Second {
		t.Fatalf("retry nak delays = %v, want about a minute", first.delays)
	}
	sched.RedeliverNATS(poller, "events/1@1", early)
	if err := poller.Nack(ctx, trigger, []string{"events/1@1"}, sched.TriggerReasonBrokerError); err != nil {
		t.Fatal(err)
	}
	if len(early.delays) != 1 || early.delays[0] < 50*time.Second {
		t.Fatalf("early redelivery nak delays = %v, want the rest of the retry delay", early.delays)
	}
	if err := holder.Defer(ctx, trigger, []string{"events/2@1"}); err != nil {
		t.Fatal(err)
	}
	if len(carried.delays) != 1 || carried.delays[0] != 0 {
		t.Fatalf("carried record nak delays = %v, want an immediate redelivery", carried.delays)
	}
	if got := sched.NATSRecordID("events", 7, time.UnixMilli(5)); got != "events/7@5" {
		t.Fatalf("record ID = %q", got)
	}
	if sched.NATSRecordID("events", 7, time.UnixMilli(6)) == sched.NATSRecordID("events", 7, time.UnixMilli(5)) {
		t.Fatal("a recreated stream reused a record ID")
	}
}

// With the batch's worth of entries pending, the second read asked for
// Count 0; go-redis then sends no COUNT and the whole backlog of new
// entries came back into memory.
func TestRedisPollDoesNotReadPastAFullPendingList(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.XGroupCreateMkStream(ctx, "orders", "faas", "$").Err(); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: "orders", Values: map[string]any{"payload": fmt.Sprintf(`{"n":%d}`, i)}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	poller := sched.NewRedisStreamPoller(client, "orders", "faas", "faas-test")
	trigger := sqlc.Trigger{BatchSizeMax: 2}
	if first := poller.Poll(ctx, trigger); first.Error != nil || len(first.Records) != 2 {
		t.Fatalf("first poll = %d records, %v; want 2", len(first.Records), first.Error)
	}
	again := poller.Poll(ctx, trigger)
	if again.Error != nil || len(again.Records) != 2 {
		t.Fatalf("second poll = %d records, %v; want the 2 pending entries only", len(again.Records), again.Error)
	}
}
