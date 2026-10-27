package sched_test

// adr: 100

import (
	"context"
	"sync"
	"testing"

	"github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// scriptedKafka serves the queued messages once each, then reports an
// idle fetch, and records every commit.
type scriptedKafka struct {
	mu        sync.Mutex
	queue     []kafka.Message
	committed []int64
}

func (r *scriptedKafka) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) == 0 {
		return kafka.Message{}, context.DeadlineExceeded
	}
	msg := r.queue[0]
	r.queue = r.queue[1:]
	return msg, nil
}

func (r *scriptedKafka) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.committed = append(r.committed, m.Offset)
	}
	return nil
}

func (r *scriptedKafka) Stats() kafka.ReaderStats { return kafka.ReaderStats{} }
func (r *scriptedKafka) Close() error             { return nil }

func (r *scriptedKafka) commits() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.committed...)
}

// A failed record was skipped: Nack called SetOffset, which a
// consumer-group reader refuses, and the next commit of a later offset
// committed past it. Now the record comes back on the next Poll under the
// same ID, and nothing past it commits until it settles.
func TestKafkaFailedRecordIsRedeliveredAndHoldsCommits(t *testing.T) {
	reader := &scriptedKafka{queue: []kafka.Message{
		{Topic: "orders", Partition: 0, Offset: 1, HighWaterMark: 4},
		{Topic: "orders", Partition: 0, Offset: 2, HighWaterMark: 4},
		{Topic: "orders", Partition: 0, Offset: 3, HighWaterMark: 4},
	}}
	poller := sched.NewKafkaPollerWithReader(reader)
	ctx, trigger := context.Background(), sqlc.Trigger{}

	first := poller.Poll(ctx, trigger)
	if first.Error != nil || len(first.Records) != 3 {
		t.Fatalf("first poll = %d records, %v", len(first.Records), first.Error)
	}
	failed := first.Records[0].ItemIdentifier
	if err := poller.Nack(ctx, trigger, []string{failed}, sched.TriggerReasonBrokerError); err != nil {
		t.Fatal(err)
	}
	if err := poller.Ack(ctx, trigger, []string{first.Records[1].ItemIdentifier, first.Records[2].ItemIdentifier}); err != nil {
		t.Fatal(err)
	}
	if got := reader.commits(); len(got) != 0 {
		t.Fatalf("committed %v while offset 1 is still open; a restart would skip it", got)
	}

	again := poller.Poll(ctx, trigger)
	if again.Error != nil || len(again.Records) != 1 || again.Records[0].ItemIdentifier != failed {
		t.Fatalf("second poll = %+v, %v; want the failed record back as %s", again.Records, again.Error, failed)
	}
	if err := poller.Ack(ctx, trigger, []string{failed}); err != nil {
		t.Fatal(err)
	}
	got := reader.commits()
	highest := int64(-1)
	for _, off := range got {
		highest = max(highest, off)
	}
	if highest != 3 {
		t.Fatalf("commits = %v, want through offset 3 once offset 1 settled", got)
	}
}

// Exhausted attempts are terminal: the record settles and commits in turn.
func TestKafkaExhaustedRecordCommits(t *testing.T) {
	reader := &scriptedKafka{queue: []kafka.Message{{Topic: "orders", Partition: 0, Offset: 7}}}
	poller := sched.NewKafkaPollerWithReader(reader)
	ctx, trigger := context.Background(), sqlc.Trigger{}
	res := poller.Poll(ctx, trigger)
	if len(res.Records) != 1 {
		t.Fatalf("poll = %d records", len(res.Records))
	}
	if err := poller.Nack(ctx, trigger, []string{res.Records[0].ItemIdentifier}, sched.TriggerReasonMaxAttempts); err != nil {
		t.Fatal(err)
	}
	if got := reader.commits(); len(got) != 1 || got[0] != 7 {
		t.Fatalf("commits = %v, want [7]", got)
	}
	if again := poller.Poll(ctx, trigger); len(again.Records) != 0 {
		t.Fatalf("an exhausted record came back: %+v", again.Records)
	}
}
