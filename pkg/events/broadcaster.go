// Package events holds the in-process broadcaster the SSE handlers
// (M7.5 slice 5/6) read from. Postgres LISTEN is the cross-process
// story; pkg/events is the in-process wake-up so the dashboard's
// /apps/{slug}/log and /events SSE clients react within milliseconds
// of an AppendDeploymentLog without depending on pg_notify latency.
//
// The broadcaster is a sync.Map of channels keyed by topic. Senders
// fan out to every active subscriber; receivers get a chan that
// closes on unsubscribe. Safe for concurrent use.
package events

import (
	"sync"
)

// Topic constants. Use one of these as the first Publish argument.
const (
	TopicDeploymentLog = "deployment_log"
	TopicAppChanged    = "app_changed"
	TopicInstanceChanged = "instance_changed"
	TopicCronFired    = "cron_fired"
)

// Broadcaster is a process-local pub/sub. The zero value is ready to
// use; tests construct one and subscribe directly.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// Event is a single Pub/Sub envelope. Topic filters at the receiver;
// Payload is the raw JSON byte slice the handler writes verbatim.
//
// Use the per-topic helpers (e.g. DeploymentLog) to keep the
// (Topic, DeploymentID, …) shape honest.
type Event struct {
	Topic   string
	Payload []byte
}

// New returns an empty Broadcaster. The zero-value also works.
func New() *Broadcaster {
	return &Broadcaster{subs: map[string]map[chan Event]struct{}{}}
}

// Subscribe returns a buffered channel receiving every event whose
// Topic == topic. The cancel function removes the subscriber.
//
// Buffered to 64; a slow receiver drops events (the broadcaster
// drops on full buffer — better than back-pressuring the producer
// in an SSE pipeline where the dashboard renders at human speeds).
func (b *Broadcaster) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if _, ok := b.subs[topic]; !ok {
		b.subs[topic] = map[chan Event]struct{}{}
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if s, ok := b.subs[topic]; ok {
			delete(s, ch)
			if len(s) == 0 {
				delete(b.subs, topic)
			}
		}
		b.mu.Unlock()
	}
}

// Publish fans out e to every subscriber of e.Topic. Drops on full
// buffers. Returns the number of receivers the event reached.
func (b *Broadcaster) Publish(e Event) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	subs, ok := b.subs[e.Topic]
	if !ok {
		return 0
	}
	delivered := 0
	for ch := range subs {
		select {
		case ch <- e:
			delivered++
		default:
			// slow receiver, drop
		}
	}
	return delivered
}

// PublishTopic is a shorthand for Publish(Event{Topic, Payload}).
func (b *Broadcaster) PublishTopic(topic string, payload []byte) int {
	return b.Publish(Event{Topic: topic, Payload: payload})
}
