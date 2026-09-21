// adr: 100
package sched

import (
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type fakeAMQPBrokerOp struct {
	messages []amqp.Delivery
	index    int
	queue    amqp.Queue
	acked    []uint64
	nacked   []uint64
	requeued []bool
	closed   bool
}

func (f *fakeAMQPBrokerOp) QueueInspect(name string) (amqp.Queue, error) {
	return f.queue, nil
}

func (f *fakeAMQPBrokerOp) Get(queue string, autoAck bool) (amqp.Delivery, bool, error) {
	if f.index >= len(f.messages) {
		return amqp.Delivery{}, false, nil
	}
	msg := f.messages[f.index]
	f.index++
	return msg, true, nil
}

func (f *fakeAMQPBrokerOp) Ack(tag uint64, multiple bool) error {
	f.acked = append(f.acked, tag)
	return nil
}

func (f *fakeAMQPBrokerOp) Nack(tag uint64, multiple bool, requeue bool) error {
	f.nacked = append(f.nacked, tag)
	f.requeued = append(f.requeued, requeue)
	return nil
}

func (f *fakeAMQPBrokerOp) Close() error {
	f.closed = true
	return nil
}

func TestAMQPPoller_PollAckNack(t *testing.T) {
	fake := &fakeAMQPBrokerOp{
		queue: amqp.Queue{Name: "orders", Messages: 100, Consumers: 2},
		messages: []amqp.Delivery{
			{
				DeliveryTag: 101,
				Body:        []byte("msg-1"),
				RoutingKey:  "order.created",
				Exchange:    "orders-ex",
				Timestamp:   time.Now(),
			},
			{
				DeliveryTag: 102,
				Body:        []byte("msg-2"),
				RoutingKey:  "order.created",
				Exchange:    "orders-ex",
				Timestamp:   time.Now(),
			},
		},
	}

	poller := &amqpPoller{
		op:       fake,
		queue:    "orders",
		batchMax: 10,
		inFlight: make(map[string]uint64),
	}

	// 1. BrokerStats reports 100 messages backlog
	stats := poller.BrokerStats(context.Background(), sqlc.Trigger{})
	if !stats.Available || stats.Lag != 100 || stats.Depth != 100 {
		t.Fatalf("BrokerStats = %+v, want lag=depth=100", stats)
	}

	// 2. Poll pulls both messages
	res := poller.Poll(context.Background(), sqlc.Trigger{BatchSizeMax: 10})
	if res.Error != nil {
		t.Fatalf("Poll error: %v", res.Error)
	}
	if len(res.Records) != 2 {
		t.Fatalf("Poll got %d records, want 2", len(res.Records))
	}
	if string(res.Records[0].Payload) != "msg-1" || res.Records[0].ItemIdentifier != "101" {
		t.Errorf("Record 0 = %+v", res.Records[0])
	}
	if string(res.Records[1].Payload) != "msg-2" || res.Records[1].ItemIdentifier != "102" {
		t.Errorf("Record 1 = %+v", res.Records[1])
	}

	// 3. Ack msg-1
	if err := poller.Ack(context.Background(), sqlc.Trigger{}, []string{"101"}); err != nil {
		t.Fatalf("Ack error: %v", err)
	}
	if len(fake.acked) != 1 || fake.acked[0] != 101 {
		t.Errorf("fake acked = %v, want [101]", fake.acked)
	}

	// 4. Nack msg-2 with poison_record (must not requeue)
	if err := poller.Nack(context.Background(), sqlc.Trigger{}, []string{"102"}, "poison_record"); err != nil {
		t.Fatalf("Nack error: %v", err)
	}
	if len(fake.nacked) != 1 || fake.nacked[0] != 102 {
		t.Errorf("fake nacked = %v, want [102]", fake.nacked)
	}
	if len(fake.requeued) != 1 || fake.requeued[0] != false {
		t.Errorf("fake requeued = %v, want [false]", fake.requeued)
	}

	// 5. Close closes op
	if err := poller.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fake.closed {
		t.Error("fake not closed")
	}
}
