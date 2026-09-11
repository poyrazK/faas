// poller_kafka.go — Kafka consumer-group poller
// (issue #757 / ADR-0NN, commit #10 of feat-triggers-mega).
//
// One segmentio/kafka-go Reader per kind=kafka trigger. Pulls
// messages via Reader.FetchMessage (non-committing read; commit
// is a separate explicit CommitMessages call) and hands them to
// the dispatch tick as SourceRecord slices.
//
// Ack: CommitMessages on the Reader. After the dispatch tick
// sees the function return 2xx and ReportBatchItemFailures is
// empty, it calls poller.Ack(ctx, t, ids) which calls
// Reader.CommitMessages with the matching Message objects.
//
// Nack: Reader.SetOffset(msg.Offset) — rewinds the consumer to
// re-read the message. We don't try to map per-message
// visibility windows in Kafka; the broker doesn't have that
// primitive. poison_record becomes a Skip+Commit so the same
// offset doesn't redeliver.
//
// Connection sharing: one Reader per trigger (NOT one Reader
// per partition — segmentio/kafka-go handles partition assignment
// internally when you give it GroupID). Many kind=kafka triggers
// → many Readers → many connections. The connection budget is
// bounded by the per-account trigger cap (Hobby 2, Pro 10,
// Scale 50 — see pkg/api/limits.go TriggerLimitPerAccount).

package sched

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	kafka "github.com/segmentio/kafka-go"
	kafkaSASL "github.com/segmentio/kafka-go/sasl"
	kafkaSASLPlain "github.com/segmentio/kafka-go/sasl/plain"
	kafkaSASLScram "github.com/segmentio/kafka-go/sasl/scram"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// kafkaBrokerOp is the minimal broker-side surface the poller's
// Ack/Nack methods need. Defined as an interface so unit tests
// can drive the poison-record branch without a real
// segmentio/kafka-go Reader — the test injects a stub that
// records CommitMessages vs SetOffset and the test asserts on
// the recorded calls.
//
// Production wires the concrete *kafka.Reader (which satisfies
// every method below) in newKafkaPoller.
type kafkaBrokerOp interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	SetOffset(offset int64) error
	Close() error
}

// kafkaPoller wraps a segmentio/kafka-go Reader. One per
// kind=kafka trigger.
//
// inFlight holds Message objects keyed by offset-as-string so
// Ack/Nack can find them — segmentio/kafka-go's CommitMessages
// takes Message values, not offsets.
type kafkaPoller struct {
	reader   kafkaBrokerOp
	brokers  []string
	topic    string
	groupID  string
	batchMax int

	mu       sync.Mutex
	inFlight map[string]kafka.Message
}

// kafkaTLSConfig mirrors pkg/gregalemanifest.TLSConfig for the
// runtime side. The reason it lives here, not imported from the
// manifest package: pkg/gregalemanifest imports pkg/sched
// (sched.ParseSchedule); pkg/sched cannot import
// pkg/gregalemanifest without a cycle (same rationale as the
// parallel FilterClause in pkg/sched/filter.go). The validator
// in pkg/gregalemanifest is the load-time gate; the poller just
// decodes + builds a *tls.Config at dial time.
type kafkaTLSConfig struct {
	CACert     string `json:"ca_cert,omitempty"`
	ClientCert string `json:"client_cert,omitempty"`
	ClientKey  string `json:"client_key,omitempty"`
	SkipVerify bool   `json:"skip_verify,omitempty"`
}

// kafkaSASLConfig mirrors pkg/gregalemanifest.SASLConfig for the
// same reason as kafkaTLSConfig (cycle avoidance). The Mechanism
// string carries the closed-vocab value {PLAIN, SCRAM-SHA-256,
// SCRAM-SHA-512}; decodeSASLMechanism is the only place that
// maps the string onto a segmentio/kafka-go sasl.Mechanism.
type kafkaSASLConfig struct {
	Mechanism string `json:"mechanism"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// kafkaConfig is the per-kind config blob decoded from
// trigger.Config json.RawMessage.
//
// Schema (validated in pkg/gregalemanifest.validateKindConfig,
// commit 2):
//
//	{
//	  "brokers": ["broker:9092", "broker2:9092"],
//	  "topic":   "orders.v1",
//	  "group":   "faas-orders",   // consumer group id
//	  "tls":    { ca_cert: ..., client_cert: ..., client_key: ..., skip_verify: ... }  // optional
//	  "sasl":   { mechanism: "PLAIN", username: ..., password: ... }                  // optional
//	}
type kafkaConfig struct {
	Brokers []string         `json:"brokers"`
	Topic   string           `json:"topic"`
	Group   string           `json:"group"`
	TLS     *kafkaTLSConfig  `json:"tls,omitempty"`
	SASL    *kafkaSASLConfig `json:"sasl,omitempty"`
}

func decodeKafkaConfig(t sqlc.Trigger) (kafkaConfig, error) {
	var cfg kafkaConfig
	if len(t.Config) == 0 {
		return cfg, fmt.Errorf("kafka_poller: trigger missing config")
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("kafka_poller: decode config: %w", err)
	}
	if len(cfg.Brokers) == 0 {
		return cfg, fmt.Errorf("kafka_poller: trigger missing brokers")
	}
	if cfg.Topic == "" {
		return cfg, fmt.Errorf("kafka_poller: trigger missing topic")
	}
	if cfg.Group == "" {
		return cfg, fmt.Errorf("kafka_poller: trigger missing group")
	}
	// sasl.mechanism is the customer-facing string. The closed
	// vocab {PLAIN, SCRAM-SHA-256, SCRAM-SHA-512} mirrors
	// pkg/gregalemanifest.SASLMechanism; we re-validate here so a
	// half-shaped SASL block from a non-manifest edit (CLI hot
	// path, row hand-edit in psql) still surfaces a typed
	// error at the dispatcher rather than crashing inside
	// kafka.NewReader.
	if cfg.SASL != nil {
		switch cfg.SASL.Mechanism {
		case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
			// ok
		default:
			return cfg, fmt.Errorf("kafka_poller: sasl.mechanism=%q is not in the closed vocab {PLAIN, SCRAM-SHA-256, SCRAM-SHA-512}",
				cfg.SASL.Mechanism)
		}
		if cfg.SASL.Username == "" {
			return cfg, fmt.Errorf("kafka_poller: sasl.username is required")
		}
		if cfg.SASL.Password == "" {
			return cfg, fmt.Errorf("kafka_poller: sasl.password is required")
		}
	}
	return cfg, nil
}

// buildKafkaDialer assembles the kafka.Dialer from the decoded
// kafkaConfig. A nil TLS field produces a nil *tls.Config (plain
// TCP), a non-nil SASL field attaches the chosen sasl.Mechanism.
// Validation of the SASL mechanism closed-vocab happens in
// decodeKafkaConfig so this function trusts cfg.SASL.Mechanism.
//
// Returns (dialer, nil) on success or (nil, err) on a malformed
// PEM block (the only path that can fail here — segmentio/kafka-go
// constructors for both PLAIN and SCRAM succeed with non-empty
// username/password).
func buildKafkaDialer(cfg kafkaConfig) (*kafka.Dialer, error) {
	d := &kafka.Dialer{
		Timeout:  5 * time.Second,
		DialFunc: oci.EgressDialContext(&net.Dialer{Timeout: 5 * time.Second}),
	}
	if cfg.TLS != nil {
		tlsCfg, err := buildKafkaTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("kafka_poller: build tls: %w", err)
		}
		d.TLS = tlsCfg
	}
	if cfg.SASL != nil {
		mech, err := kafkaSASLMechanism(cfg.SASL)
		if err != nil {
			return nil, err
		}
		d.SASLMechanism = mech
	}
	return d, nil
}

// buildKafkaTLSConfig assembles a *tls.Config from the per-trigger
// kafkaTLSConfig. MinVersion is forced to TLS 1.2 regardless of
// what the customer wrote (or didn't write) — the financial
// model forbids TLS 1.0/1.1 downgrades on managed tenants. CACert
// is appended to the system trust store via x509.NewCertPool;
// ClientCert + ClientKey become a single tls.Certificate for
// mTLS. SkipVerify is honoured verbatim — the plan-cap gate on
// SkipVerify lives in the apid handler (pkg/api/limits.go
// TLSSkipVerifyAllowed), not here.
func buildKafkaTLSConfig(t *kafkaTLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if t.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(t.CACert)) {
			return nil, fmt.Errorf("kafka_poller: ca_cert is not a valid PEM block")
		}
		tlsCfg.RootCAs = pool
	}
	if t.ClientCert != "" || t.ClientKey != "" {
		// Both must be set together — validator in
		// pkg/gregalemanifest catches the half-wired case at
		// load time, but we surface a typed error here too in
		// case a row reaches the dispatcher outside the
		// validator path (CLI hot edit, psql).
		if t.ClientCert == "" || t.ClientKey == "" {
			return nil, fmt.Errorf("kafka_poller: tls.client_cert + tls.client_key must both be set")
		}
		cert, err := tls.X509KeyPair([]byte(t.ClientCert), []byte(t.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("kafka_poller: tls X509KeyPair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	tlsCfg.InsecureSkipVerify = t.SkipVerify
	return tlsCfg, nil
}

// kafkaSASLMechanism maps the customer-facing mechanism string to
// the segmentio/kafka-go sasl.Mechanism interface. PLAIN is a value
// struct (no error); SCRAM-SHA-256/512 are constructed via the
// scram.Mechanism factory which can fail on a malformed
// username/password (e.g. embedded NUL or unsupported UTF-8 byte
// pattern in the scram library's preprocessing).
func kafkaSASLMechanism(s *kafkaSASLConfig) (kafkaSASL.Mechanism, error) {
	switch s.Mechanism {
	case "PLAIN":
		return kafkaSASLPlain.Mechanism{Username: s.Username, Password: s.Password}, nil
	case "SCRAM-SHA-256":
		m, err := kafkaSASLScram.Mechanism(kafkaSASLScram.SHA256, s.Username, s.Password)
		if err != nil {
			return nil, fmt.Errorf("kafka_poller: scram-sha-256 mechanism: %w", err)
		}
		return m, nil
	case "SCRAM-SHA-512":
		m, err := kafkaSASLScram.Mechanism(kafkaSASLScram.SHA512, s.Username, s.Password)
		if err != nil {
			return nil, fmt.Errorf("kafka_poller: scram-sha-512 mechanism: %w", err)
		}
		return m, nil
	default:
		// Closed vocab is enforced upstream in decodeKafkaConfig;
		// this branch is defensive only (CLI hand-edit path).
		return nil, fmt.Errorf("kafka_poller: unsupported sasl.mechanism=%q", s.Mechanism)
	}
}

// newKafkaPoller resolves the trigger's per-kind config and
// constructs a Reader with the consumer-group machinery.
//
// Reader config knobs that matter:
//
//   - GroupID: enables consumer-group coordination. The trigger's
//     `group` field becomes the kafka group.id; offsets are
//     persisted server-side keyed by this id.
//   - StartOffset: FirstOffset — start at the earliest message on
//     first run. Existing groups resume from their committed
//     offset (segmentio/kafka-go reads that automatically).
//   - MaxWait: 250ms — matches the NATS poller's coalesce window
//     so the dispatch tick treats brokers uniformly.
//   - CommitInterval: 0 — disables auto-commit. We want explicit
//     CommitMessages only after the function returns 2xx.
//   - Dialer: carries the per-trigger TLS + SASL config. Plain
//     TCP + no-SASL is the implicit default — Dialer.TLS nil +
//     Dialer.SASLMechanism nil triggers the no-auth code path in
//     kafka-go.
func newKafkaPoller(t sqlc.Trigger) (triggerSource, error) {
	cfg, err := decodeKafkaConfig(t)
	if err != nil {
		return nil, err
	}
	dialer, err := buildKafkaDialer(cfg)
	if err != nil {
		return nil, err
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		GroupID:        cfg.Group,
		Topic:          cfg.Topic,
		StartOffset:    kafka.FirstOffset,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        250 * time.Millisecond,
		CommitInterval: 0, // explicit commits only
		Dialer:         dialer,
	})
	return &kafkaPoller{
		reader:   reader,
		brokers:  cfg.Brokers,
		topic:    cfg.Topic,
		groupID:  cfg.Group,
		batchMax: 100,
		inFlight: map[string]kafka.Message{},
	}, nil
}

// Kind returns the trigger kind this poller handles.
func (k *kafkaPoller) Kind() string { return "kafka" }

// Poll fetches up to batchMax messages. We use FetchMessage
// (non-committing) so we can hold the messages across the
// dispatch tick and only commit them after the function returns
// 2xx via Ack.
//
// Loop exits early on:
//   - context cancel  (schedd shutdown / per-tick deadline)
//   - empty fetch     (no messages available right now)
//   - PollResult.Error (broker-side problem)
func (k *kafkaPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	limit := k.batchMax
	if t.BatchSizeMax > 0 && t.BatchSizeMax < int32(limit) {
		limit = int(t.BatchSizeMax)
	}
	out := make([]SourceRecord, 0, limit)
	for i := 0; i < limit; i++ {
		// Per-fetch context: short enough that an idle trigger
		// doesn't block the dispatcher, long enough to coalesce.
		fetchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		msg, err := k.reader.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			// io.EOF + context.DeadlineExceeded are the normal
			// idle signals — exit the loop with what we have.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
				break
			}
			// Context cancel from the parent (schedd shutdown)
			// is also a normal exit.
			if errors.Is(err, context.Canceled) {
				break
			}
			// Anything else is a broker error — surface so the
			// dispatcher can mark the records as
			// dead_letter(reason='broker_error').
			return PollResult{Error: fmt.Errorf("kafka_poller: fetch: %w", err)}
		}
		// Headers: kafka-go's Header is [{Key, Value}]; flatten
		// into a map[string]string. Duplicate keys are kept-last
		// (matches the wire's "last wins" semantics).
		hdrs := map[string]string{}
		for _, h := range msg.Headers {
			hdrs[h.Key] = string(h.Value)
		}
		meta := map[string]any{
			"topic":     msg.Topic,
			"partition": msg.Partition,
			"offset":    msg.Offset,
		}
		seqStr := fmt.Sprintf("%d-%d-%d", msg.Partition, msg.Offset, msg.HighWaterMark)
		out = append(out, SourceRecord{
			ItemIdentifier: seqStr,
			Payload:        msg.Value,
			Headers:        hdrs,
			Metadata:       meta,
			ReceivedAt:     msg.Time,
		})
		// Stash the message so Ack can find it. The map key is
		// partition+offset+highWaterMark — unique within a topic
		// (kafka guarantees offset uniqueness per partition).
		k.mu.Lock()
		k.inFlight[seqStr] = msg
		k.mu.Unlock()
	}
	if out == nil {
		out = []SourceRecord{}
	}
	return PollResult{Records: out}
}

// Ack commits each in-flight message by offset-key. CommitMessages
// advances the consumer-group offset server-side; on next poll we
// won't see these again.
func (k *kafkaPoller) Ack(commitCtx context.Context, _ sqlc.Trigger, ids []string) error {
	k.mu.Lock()
	msgs := make([]kafka.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := k.inFlight[id]; ok {
			msgs = append(msgs, m)
			delete(k.inFlight, id)
		}
	}
	k.mu.Unlock()
	if len(msgs) == 0 {
		return nil
	}
	if err := k.reader.CommitMessages(commitCtx, msgs...); err != nil {
		return fmt.Errorf("kafka_poller: commit: %w", err)
	}
	return nil
}

// Nack rewinds the consumer to re-fetch the offset. Kafka
// doesn't have a native "delay this offset" primitive, so we
// rewind to msg.Offset — next FetchMessage will redeliver.
//
// poison_record is different: we don't want infinite redelivery
// for a poison pill. The dispatch tick has already minted a
// dead_letter row (commit #15 audit + DLQ table); what the
// broker side does depends on the trigger's broker_poison_strategy
// (audit #10, migration 00275):
//
//   - "commit" (default) — call CommitMessages so the broker
//     offset advances. The broker offset and the DB
//     dead-letter state are permanently out of sync for that
//     offset; operator retry works via the dashboard's
//     "re-drive from DLQ" action.
//   - "seek-to-offset" — call SetOffset(msg.Offset) so the next
//     FetchMessage re-fetches the same message. Operator retry
//     combines a trigger re-enable with a dashboard "reset
//     offset" action that re-fetches the dead-lettered payload.
//
// Non-poison failures always rewind via SetOffset regardless of
// broker_poison_strategy — the strategy only governs the
// poison-specific terminal behaviour.
func (k *kafkaPoller) Nack(commitCtx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		msg, ok := k.inFlight[id]
		if !ok {
			continue
		}
		var err error
		if reason == triggerReasonPoisonRecord {
			// Audit #10: the strategy column gates the
			// poison-specific terminal broker op. The empty
			// default ("") is treated as "commit" — matches the
			// pre-migration behaviour byte-for-byte.
			strategy := t.BrokerPoisonStrategy
			if strategy == "" {
				strategy = "commit"
			}
			switch strategy {
			case "seek-to-offset":
				// Rewind the broker to the failed offset so the
				// next FetchMessage re-fetches it. SetOffset
				// advances the reader's local position; no
				// commit-side bookkeeping is touched.
				err = k.reader.SetOffset(msg.Offset)
			default:
				// "commit" or any unknown value — fall
				// through to the previous CommitMessages
				// path. An unknown value is rejected by the
				// SQL CHECK on insert, so by the time we see
				// it here it's a deliberate "commit".
				err = k.reader.CommitMessages(commitCtx, msg)
			}
		} else {
			// Rewind to the failed offset. segmentio/kafka-go's
			// Reader tracks its own offset; SetOffset rewinds.
			// Next FetchMessage returns this message again.
			err = k.reader.SetOffset(msg.Offset)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("kafka_poller: nack %s: %w", id, err)
		}
		delete(k.inFlight, id)
	}
	return firstErr
}

// Close closes the Reader. After Close, the Reader can't be
// reused — the dispatcher must construct a new poller if the
// trigger is unpaused.
func (k *kafkaPoller) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for kk := range k.inFlight {
		delete(k.inFlight, kk)
	}
	return k.reader.Close()
}

func init() {
	registerPoller("kafka", func(t sqlc.Trigger) (triggerSource, error) {
		return newKafkaPoller(t)
	})
}
