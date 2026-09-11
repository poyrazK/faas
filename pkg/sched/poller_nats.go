// poller_nats.go — NATS JetStream durable consumer poller
// (issue #757 / ADR-0NN, commit #9 of feat-triggers-mega).
//
// One JetStream pull consumer per kind=nats trigger. Pulls batches
// via Consumer.Fetch, hands the broker-delivered messages to the
// dispatch tick as SourceRecord slices.
//
// Ack: Msg.Ack() — broker-side ack. Until Ack is called the
// message is "in flight" on the server and will be re-delivered
// on consumer restart (JetStream's "at-least-once" guarantee).
//
// Nack: Msg.NakWithDelay(delay) — broker-side redelivery with a
// delay window. The dispatcher's retry FSM passes the
// trigger_records.next_fire_at - now() delta as the delay so a
// retry row in trigger_records and a Nak in the broker stay in
// sync (same wake-up time on both sides of the dispatch seam).
//
// Connection sharing: many kind=nats triggers share one
// nats.Conn. Per-trigger state is the durable Consumer handle —
// JetStream keeps the consumer's position server-side keyed by
// `DurableName`. We DON'T spin a new connection per trigger.
//
// Why pull, not push (the JetStream SDK also offers Messages() /
// Consume() for push): pull gives us back-pressure on our side.
// A burst of 50k messages can't OOM the schedd — Fetch caps the
// in-flight batch and the dispatch tick throttles via the
// rate-limiter gate (§3.1 of the design doc).

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type natsEgressDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func newNATSEgressDialer(timeout time.Duration) *natsEgressDialer {
	parent := &net.Dialer{Timeout: timeout}
	return &natsEgressDialer{dial: oci.EgressDialContext(parent)}
}

func (d *natsEgressDialer) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.dial(ctx, network, address)
}

// natsBroker is the shared, per-schedd NATS connection. Opened at
// schedd boot via NewNATSBroker and passed to every kind=nats
// poller's factory. One Conn handles an entire control-plane
// node's pull subscriptions.
//
// Thread safety: nats.Conn is goroutine-safe per the official
// docs — multiple Consumer handles can issue Fetch concurrently.
type natsBroker struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

// NewNATSBroker connects to the NATS cluster URL pinned in the
// trigger config (URL field on the trigger.Config blob). Returns
// a broker with an open connection + JetStream context.
//
// Connection lifecycle: caller MUST invoke broker.Close() at
// schedd shutdown so in-flight Fetch goroutines unwind and the
// TCP socket closes.
func NewNATSBroker(rawURL string) (*natsBroker, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "nats" && parsed.Scheme != "tls") || parsed.Host == "" {
		return nil, fmt.Errorf("nats_broker: invalid server URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("nats_broker: credentials, query parameters, and fragments are forbidden in server URL")
	}
	conn, err := nats.Connect(rawURL,
		nats.Name("gregale-schedd"),
		nats.SetCustomDialer(newNATSEgressDialer(5*time.Second)),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
		nats.PingInterval(20*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats_broker: connect %s: %w", parsed.Redacted(), err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("nats_broker: jetstream context: %w", err)
	}
	return &natsBroker{conn: conn, js: js}, nil
}

// Close drains the connection. Safe to call multiple times.
func (b *natsBroker) Close() error {
	if b.conn == nil {
		return nil
	}
	b.conn.Close()
	return nil
}

// natsPoller is the per-trigger pull consumer.
//
// inFlight holds JetStream message handles keyed by Sequence
// string. The dispatcher passes a slice of seq-as-string ids to
// Ack/Nack and we look each handle up to ack/nak — the JetStream
// API only exposes Ack/Nak on the Msg object, not on the seq.
//
// batchMax is the broker-natural default (100). The dispatcher
// truncates-or-extends as needed to honour the per-trigger cap.
type natsPoller struct {
	broker   *natsBroker
	consumer jetstream.Consumer
	stream   string
	subject  string
	durable  string
	batchMax int

	mu       sync.Mutex
	inFlight map[string]natsMsg
	// seqFallback is an atomic-ish counter incremented under mu
	// and used to disambiguate seqStr when msg.Metadata() returns
	// an error or nil (e.g. legacy un-acked JetStream messages).
	// Without this, every such message collides on "seq-0" and
	// the dispatch tick treats them as the same record.
	seqFallback uint64
}

// natsMsg is the minimal interface we need from a JetStream
// message — Ack, NakWithDelay, TermWithReason. Defined as an
// interface so unit tests can pass a stub.
//
// jetStreamMsg (in nats.go/jetstream/message.go) implements all
// three.
type natsMsg interface {
	Ack() error
	NakWithDelay(time.Duration) error
	TermWithReason(string) error
}

// natsConfig is the per-kind config blob decoded from
// trigger.Config json.RawMessage.
//
// Schema (validated in pkg/gregalemanifest.validateKindConfig):
//
//	{
//	  "url":     "nats://broker:4222",
//	  "stream":  "events",
//	  "subject": "events.>",
//	  "durable": "faas-<account>-<slug>"
//	}
type natsConfig struct {
	URL     string `json:"url"`
	Stream  string `json:"stream"`
	Subject string `json:"subject"`
	Durable string `json:"durable,omitempty"`
}

func decodeNATSConfig(t sqlc.Trigger) (natsConfig, error) {
	var cfg natsConfig
	if len(t.Config) == 0 {
		return cfg, fmt.Errorf("nats_poller: trigger missing config")
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("nats_poller: decode config: %w", err)
	}
	if cfg.URL == "" {
		return cfg, fmt.Errorf("nats_poller: trigger missing url")
	}
	if cfg.Stream == "" || cfg.Subject == "" {
		return cfg, fmt.Errorf("nats_poller: trigger missing stream or subject")
	}
	return cfg, nil
}

// newNATSPoller resolves the trigger's per-kind config and
// creates-or-updates the durable consumer.
//
// `durable` defaults to "faas-<trigger-id>" if empty — JetStream
// requires every consumer to have a name, and naming it after
// the trigger id guarantees uniqueness across the cluster
// without the customer having to invent one.
func newNATSPoller(broker *natsBroker, t sqlc.Trigger) (triggerSource, error) {
	if broker == nil {
		return nil, fmt.Errorf("nats_poller: nil broker")
	}
	cfg, err := decodeNATSConfig(t)
	if err != nil {
		return nil, err
	}
	if cfg.Durable == "" {
		cfg.Durable = "faas-" + t.ID.String()
	}
	consumer, err := broker.js.CreateOrUpdateConsumer(context.Background(), cfg.Stream, jetstream.ConsumerConfig{
		Name:          cfg.Durable,
		FilterSubject: cfg.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    25,
	})
	if err != nil {
		return nil, fmt.Errorf("nats_poller: create consumer %s/%s: %w", cfg.Stream, cfg.Durable, err)
	}
	return &natsPoller{
		broker:   broker,
		consumer: consumer,
		stream:   cfg.Stream,
		subject:  cfg.Subject,
		durable:  cfg.Durable,
		batchMax: 100,
		inFlight: map[string]natsMsg{},
	}, nil
}

// Kind returns the trigger kind this poller handles.
func (n *natsPoller) Kind() string { return "nats" }

// Poll fetches up to batchMax messages from the consumer. Uses
// Fetch with a 250ms max-wait so a burst coalesces but an idle
// trigger doesn't hog the dispatcher.
func (n *natsPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	limit := n.batchMax
	if t.BatchSizeMax > 0 && t.BatchSizeMax < int32(limit) {
		limit = int(t.BatchSizeMax)
	}
	batch, err := n.consumer.Fetch(limit, jetstream.FetchMaxWait(250*time.Millisecond))
	if err != nil {
		// ErrNoMessages / Timeout are normal idle signals — surface
		// as empty PollResult, NOT as an Error.
		if errors.Is(err, jetstream.ErrNoMessages) || errors.Is(err, nats.ErrTimeout) {
			return PollResult{Records: []SourceRecord{}}
		}
		return PollResult{Error: fmt.Errorf("nats_poller: fetch: %w", err)}
	}
	out := make([]SourceRecord, 0, limit)
	for msg := range batch.Messages() {
		// Headers: NATS headers are multi-map (map[string][]string)
		// — the SourceRecord envelope is single-map, so we take
		// the first value per key. The function runner receives
		// the single-value form which is what most brokers ship.
		hdrs := map[string]string{}
		for k, vs := range msg.Headers() {
			if len(vs) > 0 {
				hdrs[k] = vs[0]
			}
		}
		meta := map[string]any{"subject": msg.Subject()}
		var (
			seqStr      string
			receivedAt  = time.Now()
			deliveryNum uint64
		)
		if md, mdErr := msg.Metadata(); mdErr == nil && md != nil {
			seqStr = fmt.Sprintf("%d", md.Sequence.Stream)
			meta["sequence"] = md.Sequence.Stream
			meta["delivery_count"] = md.NumDelivered
			if !md.Timestamp.IsZero() {
				receivedAt = md.Timestamp
			}
			deliveryNum = md.NumDelivered
			_ = deliveryNum //nolint:staticcheck // SA4006: written in the if-branch, read in the else-branch below via seqStr
		} else {
			// msg.Metadata() returned an error or nil — fall
			// back to a per-instance monotonic counter so the
			// ItemIdentifier is unique across messages in this
			// Poll. The previous code used the zero-value
			// deliveryNum (always 0) here, which collided on
			// "seq-0" and made the dispatch tick treat all
			// such messages as one record (audit #3).
			n.mu.Lock()
			n.seqFallback++
			fb := n.seqFallback
			n.mu.Unlock()
			seqStr = fmt.Sprintf("seq-fallback-%d", fb)
		}
		out = append(out, SourceRecord{
			ItemIdentifier: seqStr,
			Payload:        msg.Data(),
			Headers:        hdrs,
			Metadata:       meta,
			ReceivedAt:     receivedAt,
		})
		// Stash the message handle so Ack/Nack can find it.
		n.mu.Lock()
		n.inFlight[seqStr] = msg
		n.mu.Unlock()
	}
	if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
		return PollResult{Error: fmt.Errorf("nats_poller: batch: %w", err)}
	}
	if out == nil {
		out = []SourceRecord{}
	}
	return PollResult{Records: out}
}

// Ack acks each in-flight message by Sequence-string id.
func (n *natsPoller) Ack(_ context.Context, _ sqlc.Trigger, ids []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		msg, ok := n.inFlight[id]
		if !ok {
			continue
		}
		if err := msg.Ack(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("nats_poller: ack %s: %w", id, err)
		}
		delete(n.inFlight, id)
	}
	return firstErr
}

// Nack signals broker-side redelivery with a delay. poison_record
// becomes Term (drop without re-deliver); everything else
// becomes NakWithDelay(2s).
//
// 2s is a fixed broker-side delay window — the per-record
// next_fire_at on the trigger_records row carries the
// application-level retry timing. The broker-side Nak delay is
// only there to give the broker's redelivery loop sane spacing
// without re-fetching immediately.
func (n *natsPoller) Nack(_ context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		msg, ok := n.inFlight[id]
		if !ok {
			continue
		}
		var err error
		if reason == triggerReasonPoisonRecord {
			err = msg.TermWithReason("poison")
		} else {
			err = msg.NakWithDelay(2 * time.Second)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("nats_poller: nack %s: %w", id, err)
		}
		delete(n.inFlight, id)
	}
	return firstErr
}

// Close releases the consumer handle. The shared nats.Conn stays
// open (broker.Close owns it).
func (n *natsPoller) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k := range n.inFlight {
		delete(n.inFlight, k)
	}
	return nil
}

// jsonStdUnmarshal is a package-local alias for encoding/json's
// Unmarshal — the rest of pkg/sched uses it via the
// parseJSONHeaders/Metadata helpers in poller_queue.go. Defined
// here too so this file is self-contained.
//
//nolint:unused // reserved for the next NATS PR-cluster; alias so this file is self-contained.
func jsonStdUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

func init() {
	registerPoller("nats", func(t sqlc.Trigger) (triggerSource, error) {
		broker := getCurrentNATSBroker()
		if broker == nil {
			return nil, fmt.Errorf("nats_poller: no broker registered")
		}
		return newNATSPoller(broker, t)
	})
}

var currentNATSBroker *natsBroker

// setCurrentNATSBroker is invoked from schedd boot once per URL
// group. Triggers that share a URL share a broker.
//
//nolint:unused // reserved for cmd/schedd boot wiring (PR-B).
func setCurrentNATSBroker(b *natsBroker) { currentNATSBroker = b }

// getCurrentNATSBroker returns the broker registered at startup,
// or nil if no broker is configured. Dispatcher treats nil as
// "skip kind=nats" and continues.
func getCurrentNATSBroker() *natsBroker { return currentNATSBroker }
