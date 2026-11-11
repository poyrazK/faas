// adr: 20261111140000 — event delivery reliability repairs.
package sched_test

import (
	"context"
	"io"
	"sync"

	"github.com/segmentio/kafka-go"
)

type replayReliabilityKafkaReader struct {
	mu      sync.Mutex
	queue   []kafka.Message
	offsets []int64
}

func (r *replayReliabilityKafkaReader) FetchMessage(context.Context) (kafka.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) == 0 {
		return kafka.Message{}, io.EOF
	}
	message := r.queue[0]
	r.queue = r.queue[1:]
	return message, nil
}
func (r *replayReliabilityKafkaReader) CommitMessages(_ context.Context, messages ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, message := range messages {
		r.offsets = append(r.offsets, message.Offset)
	}
	return nil
}
func (r *replayReliabilityKafkaReader) Stats() kafka.ReaderStats { return kafka.ReaderStats{} }
func (r *replayReliabilityKafkaReader) Close() error             { return nil }
func (r *replayReliabilityKafkaReader) commits() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.offsets...)
}
