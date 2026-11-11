// adr: 20261111140000 — event delivery reliability repairs.
package sched

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func ReliabilityNATSPoller(js jetstream.JetStream, trigger sqlc.Trigger) (TriggerSource, error) {
	return newNATSPoller(&natsBroker{js: js}, trigger)
}
func ReliabilityWrapNATS(src TriggerSource) TriggerSource {
	return &pooledNATSPoller{triggerSource: src, release: func() {}}
}
func ReliabilityKafkaBuffered(src TriggerSource) (int, int) {
	p := src.(*kafkaPoller)
	p.mu.Lock()
	defer p.mu.Unlock()
	var count, bytes int
	for _, messages := range p.settled {
		count += len(messages)
		for _, message := range messages {
			bytes += len(message.Value) + len(message.Key)
			for _, header := range message.Headers {
				bytes += len(header.Value)
			}
		}
	}
	return count, bytes
}
func ReliabilityRedisKeepLeases(ctx context.Context, src TriggerSource, ids []string) func() {
	p := src.(*redisPoller)
	p.visibility = 30 * time.Millisecond
	return p.keepLeases(ctx, ids)
}
