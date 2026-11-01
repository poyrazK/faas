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
// delay window. The dispatcher's retry FSM passes the record's
// trigger_records.next_fire_at (NackUntil) so a retry row in
// trigger_records and a Nak in the broker stay in sync: the record
// comes back when it is due, not every 2 s while it cools. The
// consumer has no MaxDeliver: the trigger's max_attempts decides when
// a record is dead-lettered (and then terminated), so cooling
// redeliveries and batch carry-over cannot make the server drop a
// record that is still `retry` (ADR-20261101093512).
//
// Record IDs are stream/sequence@timestamp: a recreated stream restarts
// its sequences, and the stream name and stored time keep a new message
// from reusing an old record's ID. Records stored before that used the
// bare sequence.
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

	"github.com/onebox-faas/faas/pkg/api"
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
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("nats_broker: query parameters and fragments are forbidden in server URL")
	}
	opts := []nats.Option{
		nats.Name("gregale-schedd"),
		nats.SetCustomDialer(newNATSEgressDialer(5 * time.Second)),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.Timeout(5 * time.Second),
		nats.PingInterval(20 * time.Second),
	}
	if parsed.User != nil {
		user := parsed.User.Username()
		pass, _ := parsed.User.Password()
		opts = append(opts, nats.UserInfo(user, pass))
	}
	conn, err := nats.Connect(rawURL, opts...)
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
	// due remembers when a record NackUntil held is due, so an early
	// redelivery of it waits out the rest of its retry delay.
	due map[string]time.Time
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
	consumer, err := broker.js.CreateOrUpdateConsumer(context.Background(), cfg.Stream, natsConsumerConfig(cfg))
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

// natsConsumerConfig is the durable consumer a NATS trigger reads. It
// starts at the stream's first message (DeliverAll) when it is created, and
// a recreated trigger, with a new id, gets a new consumer that starts there
// again. MaxDeliver is unlimited: with the old 25, redeliveries during a
// retry's backoff used up the deliveries around attempt 5 and the server
// dropped the message while its record stayed `retry` with no dead letter.
// The dispatcher dead-letters at the trigger's max_attempts and terminates
// the message then (ADR-20261101093512). An existing consumer is updated in
// place; its deliver policy is unchanged.
func natsConsumerConfig(cfg natsConfig) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:          cfg.Durable,
		FilterSubject: cfg.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxDeliver:    -1,
	}
}

// natsRecordID identifies a message across redeliveries: its stream and
// stream sequence, plus the time the stream stored it, so a recreated
// stream whose sequences restart does not reuse an earlier record's ID.
func natsRecordID(stream string, seq uint64, stored time.Time) string {
	id := fmt.Sprintf("%s/%d", stream, seq)
	if !stored.IsZero() {
		id += fmt.Sprintf("@%d", stored.UnixMilli())
	}
	return id
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
			stableID    string
			legacyIDs   []string
			receivedAt  = time.Now()
			deliveryNum uint64
		)
		if md, mdErr := msg.Metadata(); mdErr == nil && md != nil {
			stream := md.Stream
			if stream == "" {
				stream = n.stream
			}
			seqStr = natsRecordID(stream, md.Sequence.Stream, md.Timestamp)
			legacyIDs = []string{fmt.Sprintf("%d", md.Sequence.Stream)}
			if md.Sequence.Stream > 0 {
				stableID = seqStr
			}
			meta["stream"] = stream
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
			ItemIdentifier:    seqStr,
			StableIdentifier:  stableID,
			LegacyIdentifiers: legacyIDs,
			Payload:           msg.Data(),
			Headers:           hdrs,
			Metadata:          meta,
			ReceivedAt:        receivedAt,
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
		delete(n.due, id)
	}
	return firstErr
}

// NackUntil holds retried messages at the server until their retry is due
// (until[i] for ids[i]), so a cooling retry spends no deliveries.
func (n *natsPoller) NackUntil(_ context.Context, _ sqlc.Trigger, ids []string, until []time.Time) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if n.due == nil {
		n.due = map[string]time.Time{}
	}
	var firstErr error
	for i, id := range ids {
		msg, ok := n.inFlight[id]
		if !ok {
			continue
		}
		n.due[id] = until[i]
		if err := msg.NakWithDelay(max(until[i].Sub(now), 0)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("nats_poller: nak %s: %w", id, err)
		}
		delete(n.inFlight, id)
	}
	n.forgetPastDueLocked(now)
	return firstErr
}

// Defer hands messages the batch left out straight back to the server.
func (n *natsPoller) Defer(_ context.Context, _ sqlc.Trigger, ids []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		msg, ok := n.inFlight[id]
		if !ok {
			continue
		}
		if err := msg.NakWithDelay(0); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("nats_poller: nak %s: %w", id, err)
		}
		delete(n.inFlight, id)
	}
	return firstErr
}

// forgetPastDueLocked drops due times that passed long ago, for messages
// that never came back (a trigger change, another node). Caller holds n.mu.
func (n *natsPoller) forgetPastDueLocked(now time.Time) {
	if len(n.due) < 1024 {
		return
	}
	for id, at := range n.due {
		if at.Before(now.Add(-time.Hour)) {
			delete(n.due, id)
		}
	}
}

// Nack signals broker-side redelivery with a delay. poison_record and
// max_attempts become Term (drop without re-deliver); everything else
// becomes NakWithDelay: the rest of the record's retry delay when
// NackUntil set one (an early redelivery while it cools), else
// api.TriggerBrokerEarlyRedeliveryDelay (2 s) of spacing.
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
		switch reason {
		case triggerReasonMaxAttempts:
			err = msg.TermWithReason("max_attempts")
			delete(n.due, id)
		case triggerReasonPoisonRecord:
			err = msg.TermWithReason("poison")
			delete(n.due, id)
		default:
			delay := api.TriggerBrokerEarlyRedeliveryDelay
			if rest := time.Until(n.due[id]); rest > delay {
				delay = rest
			}
			err = msg.NakWithDelay(delay)
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

// BrokerStats queries NATS JetStream ConsumerInfo for pending message counts.
func (n *natsPoller) BrokerStats(ctx context.Context, _ sqlc.Trigger) BrokerStats {
	if n == nil || n.consumer == nil {
		return BrokerStats{Available: false}
	}
	info, err := n.consumer.Info(ctx)
	if err != nil {
		return BrokerStats{Available: false}
	}
	return BrokerStats{
		Lag:       int64(info.NumPending),
		Depth:     int64(info.NumPending + uint64(info.NumAckPending)),
		Available: true,
	}
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
		cfg, err := decodeNATSConfig(t)
		if err != nil {
			return nil, err
		}
		broker, err := natsBrokers.forURL(cfg.URL)
		if err != nil {
			return nil, err
		}
		return newNATSPoller(broker, t)
	})
}

// natsBrokerPool holds one connection per server URL (credentials
// included), opened when the first trigger naming the URL builds its
// poller. Triggers that share a URL share the connection. Nothing ever
// registered a broker before, so every NATS trigger failed to construct.
type natsBrokerPool struct {
	mu      sync.Mutex
	dial    func(rawURL string) (*natsBroker, error)
	brokers map[string]*natsBroker
}

var natsBrokers = &natsBrokerPool{dial: NewNATSBroker, brokers: map[string]*natsBroker{}}

func (p *natsBrokerPool) forURL(rawURL string) (*natsBroker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok := p.brokers[rawURL]; ok {
		return b, nil
	}
	b, err := p.dial(rawURL)
	if err != nil {
		return nil, err
	}
	p.brokers[rawURL] = b
	return b, nil
}
