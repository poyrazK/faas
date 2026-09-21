// adr: 100
// poller_kafka_test.go — Kafka poller unit tests.
//
// The kafka poller's broker side (segmentio/kafka-go Reader) is
// hard to drive in-process — it owns a real network socket, a
// consumer-group coordinator, and a goroutine pump. We can't
// construct a Reader against a fake broker without an external
// test container. So instead of mocking the concrete Reader,
// this file drives the poison-record branch (Nack with
// reason="poison_record") through a stub that satisfies the
// kafkaBrokerOp interface introduced for this test.
//
// What the test pins (audit #10, PR #910 review):
//
//  1. With broker_poison_strategy="commit" (the default), the
//     kafka poller calls CommitMessages on poison. This preserves
//     the pre-migration behaviour byte-for-byte; existing
//     operators see no change.
//
//  2. With broker_poison_strategy="seek-to-offset", the kafka
//     poller calls SetOffset(msg.Offset) on poison instead. The
//     broker offset rewinds to the failed offset so the next
//     Poll re-fetches the same message — the operator-retry
//     path described in pkg/api/trigger.go::BrokerPoisonStrategySeekToOffset.
//
//  3. With broker_poison_strategy="" (the zero value), the
//     poller falls through to "commit" — same as #1. This is
//     the safety net for older trigger rows that pre-date
//     migration 00275 (the column has a DB DEFAULT 'commit', so
//     the Go side never sees "" in practice, but the test pins
//     the in-process contract too).
//
//  4. Non-poison Nack reasons always rewind via SetOffset
//     regardless of broker_poison_strategy — the strategy only
//     governs the poison-specific terminal behaviour.

package sched

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// poisonStrategyOp records the terminal broker op the poller
// chose. The poller doesn't reach for CommitMessages or
// SetOffset directly on a non-poison Nack — those branches are
// not exercised by this test (the test pins audit #10, not the
// existing Nack-on-failure path).
type poisonStrategyOp int

const (
	poisonOpCommit poisonStrategyOp = iota
	poisonOpSetOffset
)

type poisonStrategyReader struct {
	mu        sync.Mutex
	poisonOp  poisonStrategyOp // last terminal op recorded on poison
	offsets   []int64
	commitCtx context.Context
}

// CommitMessages records the messages + the commit op. Mirrors
// the kafka.Reader.CommitMessages(ctx, msgs...) signature.
func (r *poisonStrategyReader) CommitMessages(ctx context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitCtx = ctx
	r.poisonOp = poisonOpCommit
	for _, m := range msgs {
		r.offsets = append(r.offsets, m.Offset)
	}
	return nil
}

// SetOffset records the rewind op + the offset.
func (r *poisonStrategyReader) SetOffset(offset int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poisonOp = poisonOpSetOffset
	r.offsets = append(r.offsets, offset)
	return nil
}

// FetchMessage + Close are part of the kafkaBrokerOp interface
// but are not exercised by these tests (the tests drive Nack
// only). The stubs return zero values — kept here so the
// poisonStrategyReader satisfies the interface and the poller's
// construction compiles.
func (r *poisonStrategyReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	return kafka.Message{}, nil
}

func (r *poisonStrategyReader) Stats() kafka.ReaderStats {
	return kafka.ReaderStats{Lag: 10, QueueLength: 5}
}

func (r *poisonStrategyReader) Close() error {
	return nil
}

// kafkaNackFixture primes a kafkaPoller with one in-flight
// message + a stub broker. Returns the poller, the stub (for
// assertion), and the in-flight id.
func kafkaNackFixture(rdr *poisonStrategyReader, offset int64) (*kafkaPoller, string) {
	id := "0-42-100"
	return &kafkaPoller{
		reader:   rdr,
		batchMax: 100,
		inFlight: map[string]kafka.Message{
			id: {Topic: "orders.v1", Partition: 0, Offset: offset},
		},
	}, id
}

// TestKafka_NackPoisonCommit asserts that with
// broker_poison_strategy="commit" (the default), the poller
// commits the message — the previous behaviour, preserved
// byte-for-byte by the migration.
func TestKafka_NackPoisonCommit(t *testing.T) {
	t.Parallel()

	rdr := &poisonStrategyReader{}
	k, id := kafkaNackFixture(rdr, 42)
	trig := sqlc.Trigger{BrokerPoisonStrategy: "commit"}

	if err := k.Nack(context.Background(), trig, []string{id}, triggerReasonPoisonRecord); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	rdr.mu.Lock()
	defer rdr.mu.Unlock()
	if rdr.poisonOp != poisonOpCommit {
		t.Fatalf("poison op = %v, want %v (commit strategy must CommitMessages)", rdr.poisonOp, poisonOpCommit)
	}
	if len(rdr.offsets) != 1 || rdr.offsets[0] != 42 {
		t.Fatalf("committed offsets = %v, want [42]", rdr.offsets)
	}
	if _, ok := k.inFlight[id]; ok {
		t.Fatal("Nack should drop the inFlight handle after CommitMessages")
	}
}

// TestKafka_NackPoisonSeekToOffset asserts that with
// broker_poison_strategy="seek-to-offset", the poller rewinds
// via SetOffset — broker offset returns to msg.Offset so the
// next Poll re-fetches the same message.
func TestKafka_NackPoisonSeekToOffset(t *testing.T) {
	t.Parallel()

	rdr := &poisonStrategyReader{}
	k, _ := kafkaNackFixture(rdr, 77)
	// Override the inFlight id for clarity (different offset).
	k.inFlight = map[string]kafka.Message{
		"0-77-200": {Topic: "orders.v1", Partition: 0, Offset: 77},
	}
	id := "0-77-200"
	trig := sqlc.Trigger{BrokerPoisonStrategy: "seek-to-offset"}

	if err := k.Nack(context.Background(), trig, []string{id}, triggerReasonPoisonRecord); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	rdr.mu.Lock()
	defer rdr.mu.Unlock()
	if rdr.poisonOp != poisonOpSetOffset {
		t.Fatalf("poison op = %v, want %v (seek-to-offset strategy must SetOffset)", rdr.poisonOp, poisonOpSetOffset)
	}
	if len(rdr.offsets) != 1 || rdr.offsets[0] != 77 {
		t.Fatalf("rewound offsets = %v, want [77]", rdr.offsets)
	}
	if _, ok := k.inFlight[id]; ok {
		t.Fatal("Nack should drop the inFlight handle after SetOffset")
	}
}

// TestKafka_NackPoisonEmptyStrategyDefaultsToCommit pins the
// safety net: broker_poison_strategy="" (the zero value) falls
// through to "commit" so a trigger row that somehow ends up with
// an empty strategy — e.g. a pre-migration row that the migration
// applied to AFTER a default was added — still gets the previous
// behaviour. The DB DEFAULT 'commit' makes this unreachable in
// practice, but the in-process contract is pinned here too.
func TestKafka_NackPoisonEmptyStrategyDefaultsToCommit(t *testing.T) {
	t.Parallel()

	rdr := &poisonStrategyReader{}
	k, _ := kafkaNackFixture(rdr, 99)
	k.inFlight = map[string]kafka.Message{
		"0-99-300": {Topic: "orders.v1", Partition: 0, Offset: 99},
	}
	id := "0-99-300"
	trig := sqlc.Trigger{BrokerPoisonStrategy: ""}

	if err := k.Nack(context.Background(), trig, []string{id}, triggerReasonPoisonRecord); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	rdr.mu.Lock()
	defer rdr.mu.Unlock()
	if rdr.poisonOp != poisonOpCommit {
		t.Fatalf("poison op = %v, want %v (empty strategy must default to commit)", rdr.poisonOp, poisonOpCommit)
	}
}

// TestKafka_NackBrokerErrorAlwaysRewinds asserts that non-poison
// Nack reasons (broker_error) always rewind via SetOffset,
// regardless of broker_poison_strategy. The strategy only
// governs the poison-specific terminal behaviour; transient
// broker failures must always re-poll.
func TestKafka_NackBrokerErrorAlwaysRewinds(t *testing.T) {
	t.Parallel()

	for _, stratName := range []string{"commit", "seek-to-offset", ""} {
		stratName := stratName
		t.Run("strategy="+stratName, func(t *testing.T) {
			t.Parallel()

			rdr := &poisonStrategyReader{}
			k, id := kafkaNackFixture(rdr, 123)
			trig := sqlc.Trigger{BrokerPoisonStrategy: stratName}

			if err := k.Nack(context.Background(), trig, []string{id}, triggerReasonBrokerError); err != nil {
				t.Fatalf("Nack strategy=%q: %v", stratName, err)
			}
			rdr.mu.Lock()
			defer rdr.mu.Unlock()
			if rdr.poisonOp != poisonOpSetOffset {
				t.Fatalf("broker_error strategy=%q: poison op = %v, want %v (broker_error must always SetOffset)", stratName, rdr.poisonOp, poisonOpSetOffset)
			}
			if len(rdr.offsets) != 1 || rdr.offsets[0] != 123 {
				t.Fatalf("broker_error strategy=%q: rewound offsets = %v, want [123]", stratName, rdr.offsets)
			}
		})
	}
}

// ----------------------------------------------------------------------
// Commit 7 (ADR-118 / issue #757 mega-PR) — TLS/SASL decode +
// Dialer assembly + offset/rebalance pinning.
// ----------------------------------------------------------------------

// kafkaDecoderFixture builds a sqlc.Trigger with a JSON-encoded
// Config blob so the decoder can be exercised without a real
// triggers row. The fields mirror what apid writes after
// validation in pkg/gregalemanifest has run.
func kafkaDecoderFixture(t *testing.T, kafkaJSON string) sqlc.Trigger {
	t.Helper()
	return sqlc.Trigger{
		Kind:   "kafka",
		Config: []byte(kafkaJSON),
	}
}

// TestDecodeKafkaConfig_NoTLSSASL pins the no-TLS / no-SASL
// baseline: the poller still constructs a Dialer with nil TLS
// and nil SASLMechanism (plaintext-equivalent). A future refactor
// that drops the empty-dialer branch would regress Confluent
// Cloud brokers with public endpoints — the canonical ~most
// customer shape.
func TestDecodeKafkaConfig_NoTLSSASL(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{"brokers":["broker:9092"],"topic":"t","group":"g"}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	if cfg.TLS != nil {
		t.Errorf("TLS = %+v, want nil", cfg.TLS)
	}
	if cfg.SASL != nil {
		t.Errorf("SASL = %+v, want nil", cfg.SASL)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.TLS != nil {
		t.Errorf("Dialer.TLS = %+v, want nil", d.TLS)
	}
	if d.SASLMechanism != nil {
		t.Errorf("Dialer.SASLMechanism = %+v, want nil", d.SASLMechanism)
	}
	if d.Timeout != 5*time.Second {
		t.Errorf("Dialer.Timeout = %v, want 5s", d.Timeout)
	}
}

// TestDecodeKafkaConfig_TLSSkipVerify exercises the
// skip_verify-only path (no CACert, no mTLS pair). The Dialer
// is constructed with a *tls.Config whose InsecureSkipVerify is
// true; the plan-cap gate on skip_verify lives upstream in
// apid, not here, so the test only pins the runtime contract.
func TestDecodeKafkaConfig_TLSSkipVerify(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"tls":{"skip_verify":true}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	if cfg.TLS == nil || cfg.TLS.SkipVerify != true {
		t.Fatalf("TLS = %+v, want skip_verify=true", cfg.TLS)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.TLS == nil {
		t.Fatal("Dialer.TLS = nil, want non-nil")
	}
	if !d.TLS.InsecureSkipVerify {
		t.Errorf("Dialer.TLS.InsecureSkipVerify = false, want true (skip_verify=true on the trigger row)")
	}
	if d.TLS.MinVersion != tls.VersionTLS12 {
		t.Errorf("Dialer.TLS.MinVersion = %v, want TLS 1.2 (ADR-118 §TLSMinVersionGate)", d.TLS.MinVersion)
	}
}

// TestDecodeKafkaConfig_TLSMalformedCACertFails pins the
// error path: a non-PEM ca_cert surfaces a typed error so the
// dispatcher sees the failure at dial time rather than getting
// a half-built tls.Config that silently trusts nothing.
func TestDecodeKafkaConfig_TLSMalformedCACertFails(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"tls":{"ca_cert":"this is not a PEM block"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig (decode succeeds): %v", err)
	}
	if _, err := buildKafkaDialer(cfg); err == nil {
		t.Fatal("buildKafkaDialer err = nil, want non-nil (malformed ca_cert must surface)")
	}
}

// TestDecodeKafkaConfig_TLSHalfWiredMTLSFails pins the
// half-configured mTLS path: client_cert set without
// client_key (or vice versa) is rejected — tls.X509KeyPair on
// an empty PEM is a footgun, and the customer would otherwise
// only see the failure at the first broker dial.
func TestDecodeKafkaConfig_TLSHalfWiredMTLSFails(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"tls":{"client_cert":"-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig (decode succeeds): %v", err)
	}
	if _, err := buildKafkaDialer(cfg); err == nil {
		t.Fatal("buildKafkaDialer err = nil, want non-nil (client_cert without client_key must fail)")
	}
}

// TestDecodeKafkaConfig_SASLClosedVocab pins the closed-vocab
// guard: anything outside {PLAIN, SCRAM-SHA-256, SCRAM-SHA-512}
// surfaces a typed error at decode time. Without this the
// segmentio/kafka-go scram factory would error inside
// kafkaNewReader (which doesn't surface the error cleanly), and
// the customer would see the dial hang rather than the typo.
func TestDecodeKafkaConfig_SASLClosedVocab(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mech string
		want bool // expected to succeed at decode
	}{
		{"PLAIN", true},
		{"SCRAM-SHA-256", true},
		{"SCRAM-SHA-512", true},
		{"plain", false}, // case-sensitive
		{"SCRAM-SHA-1", false},
		{"OAUTHBEARER", false},
		{"", false},
	}
	for _, c := range cases {
		c := c
		t.Run("mechanism="+c.mech, func(t *testing.T) {
			t.Parallel()
			body := map[string]any{
				"brokers": []string{"broker:9092"},
				"topic":   "t",
				"group":   "g",
				"sasl":    map[string]any{"mechanism": c.mech, "username": "u", "password": "p"},
			}
			b, _ := json.Marshal(body)
			trig := kafkaDecoderFixture(t, string(b))
			cfg, err := decodeKafkaConfig(trig)
			if c.want {
				if err != nil {
					t.Fatalf("decodeKafkaConfig: %v (want success)", err)
				}
				if cfg.SASL == nil || cfg.SASL.Mechanism != c.mech {
					t.Fatalf("SASL = %+v, want mechanism=%s", cfg.SASL, c.mech)
				}
			} else if err == nil {
				t.Fatalf("decodeKafkaConfig err = nil, want non-nil (mechanism=%q must be rejected)", c.mech)
			}
		})
	}
}

// TestDecodeKafkaConfig_SASLMissingCredentialsFails pins the
// username/password required guard. Even with a valid mechanism,
// an empty username or password must surface a typed error so
// the customer can't accidentally deploy a no-auth trigger.
func TestDecodeKafkaConfig_SASLMissingCredentialsFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, user, pass string
	}{
		{"empty_user", "", "p"},
		{"empty_pass", "u", ""},
		{"empty_both", "", ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := map[string]any{
				"brokers": []string{"broker:9092"},
				"topic":   "t",
				"group":   "g",
				"sasl":    map[string]any{"mechanism": "PLAIN", "username": tc.user, "password": tc.pass},
			}
			b, _ := json.Marshal(body)
			_, err := decodeKafkaConfig(kafkaDecoderFixture(t, string(b)))
			if err == nil {
				t.Fatalf("decodeKafkaConfig err = nil, want non-nil (%s must be rejected)", tc.name)
			}
		})
	}
}

// TestBuildKafkaDialer_PLAINMechanism pins the PLAIN SASL path:
// kafkaSASLPlain.Mechanism is a value struct — buildKafkaDialer
// returns a non-nil SASLMechanism named "PLAIN".
func TestBuildKafkaDialer_PLAINMechanism(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"sasl":{"mechanism":"PLAIN","username":"u","password":"p"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.SASLMechanism == nil {
		t.Fatal("Dialer.SASLMechanism = nil, want non-nil (PLAIN)")
	}
	if name := d.SASLMechanism.Name(); name != "PLAIN" {
		t.Errorf("SASLMechanism.Name() = %q, want %q", name, "PLAIN")
	}
}

// TestBuildKafkaDialer_SCRAMSHA256Mechanism pins the
// SCRAM-SHA-256 SASL path. segmentio/kafka-go's scram package
// returns the mechanism via a factory (Mechanism(algo, user,
// pass) (Mechanism, error)); buildKafkaDialer wraps the error.
func TestBuildKafkaDialer_SCRAMSHA256Mechanism(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"sasl":{"mechanism":"SCRAM-SHA-256","username":"u","password":"p"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.SASLMechanism == nil {
		t.Fatal("Dialer.SASLMechanism = nil, want non-nil (SCRAM-SHA-256)")
	}
	if name := d.SASLMechanism.Name(); name != "SCRAM-SHA-256" {
		t.Errorf("SASLMechanism.Name() = %q, want %q", name, "SCRAM-SHA-256")
	}
}

// TestBuildKafkaDialer_SCRAMSHA512Mechanism mirrors the SHA-256
// test for SHA-512. Both branches of the scram switch must land
// in the Dialer with the correct Name().
func TestBuildKafkaDialer_SCRAMSHA512Mechanism(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"sasl":{"mechanism":"SCRAM-SHA-512","username":"u","password":"p"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.SASLMechanism == nil {
		t.Fatal("Dialer.SASLMechanism = nil, want non-nil (SCRAM-SHA-512)")
	}
	if name := d.SASLMechanism.Name(); name != "SCRAM-SHA-512" {
		t.Errorf("SASLMechanism.Name() = %q, want %q", name, "SCRAM-SHA-512")
	}
}

// TestBuildKafkaDialer_TLSPlusSASLPinsBoth asserts the integrated
// shape: one trigger with both tls.skip_verify=true AND
// sasl.mechanism=PLAIN lands in one Dialer with both fields
// populated. A future refactor that gates TLS-only or SASL-only
// paths would regress the Confluent Cloud-with-mTLS shape.
func TestBuildKafkaDialer_TLSPlusSASLPinsBoth(t *testing.T) {
	t.Parallel()
	trig := kafkaDecoderFixture(t, `{
		"brokers":["broker:9092"],"topic":"t","group":"g",
		"tls":{"skip_verify":true},
		"sasl":{"mechanism":"PLAIN","username":"u","password":"p"}
	}`)
	cfg, err := decodeKafkaConfig(trig)
	if err != nil {
		t.Fatalf("decodeKafkaConfig: %v", err)
	}
	d, err := buildKafkaDialer(cfg)
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.TLS == nil {
		t.Error("Dialer.TLS = nil, want non-nil")
	}
	if d.SASLMechanism == nil {
		t.Error("Dialer.SASLMechanism = nil, want non-nil")
	}
	if d.TLS.MinVersion != tls.VersionTLS12 {
		t.Errorf("Dialer.TLS.MinVersion = %v, want TLS 1.2", d.TLS.MinVersion)
	}
}

// kafkaSeekFixture primes a kafkaPoller with multiple in-flight
// messages and returns the poller + the in-flight ids in offset
// order, so commit/rollback tests can drive Ack + Nack against a
// non-empty payload without a real Reader.
func kafkaSeekFixture(rdr kafkaBrokerOp, offsets ...int64) (*kafkaPoller, []string) {
	ids := make([]string, 0, len(offsets))
	k := &kafkaPoller{
		reader:   rdr,
		batchMax: 100,
		inFlight: map[string]kafka.Message{},
	}
	for _, off := range offsets {
		// Mirror the seqStr formula in (*kafkaPoller).Poll so the
		// in-flight key matches the production hot path.
		id := fmt.Sprintf("%d-%d-%d", 0, off, off+100)
		k.inFlight[id] = kafka.Message{Topic: "orders.v1", Partition: 0, Offset: off}
		ids = append(ids, id)
	}
	return k, ids
}

// TestKafka_AckMultipleSucceeds pins the happy path: Ack with N
// ids commits every in-flight message via a single
// CommitMessages call. The reader's inFlight map MUST be drained
// so the broker offset advances server-side.
func TestKafka_AckMultipleSucceeds(t *testing.T) {
	t.Parallel()
	rdr := &poisonStrategyReader{}
	k, ids := kafkaSeekFixture(rdr, 1, 2, 3)
	trig := sqlc.Trigger{BrokerPoisonStrategy: "commit"}

	if err := k.Ack(context.Background(), trig, ids); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	rdr.mu.Lock()
	defer rdr.mu.Unlock()
	if rdr.poisonOp != poisonOpCommit {
		t.Fatalf("terminal op = %v, want commit (Ack = CommitMessages)", rdr.poisonOp)
	}
	wantOff := []int64{1, 2, 3}
	if len(rdr.offsets) != 3 {
		t.Fatalf("CommitMessages offsets = %v, want %v", rdr.offsets, wantOff)
	}
	for i, off := range wantOff {
		if rdr.offsets[i] != off {
			t.Errorf("CommitMessages offsets[%d] = %d, want %d", i, rdr.offsets[i], off)
		}
	}
	for _, id := range ids {
		if _, ok := k.inFlight[id]; ok {
			t.Errorf("inFlight[%s] still present, want drained after Ack", id)
		}
	}
}

// flakyCommitReader is a kafkaBrokerOp that pretends
// CommitMessages succeeded but returns a configured error so the
// poller's error-propagation path can be exercised. The
// ack-drains-inFlight contract is the same as for the happy-path
// branch.
type flakyCommitReader struct {
	commitErr error
}

func (r *flakyCommitReader) FetchMessage(context.Context) (kafka.Message, error) {
	return kafka.Message{}, nil
}
func (r *flakyCommitReader) CommitMessages(_ context.Context, _ ...kafka.Message) error {
	return r.commitErr
}
func (r *flakyCommitReader) SetOffset(_ int64) error  { return nil }
func (r *flakyCommitReader) Stats() kafka.ReaderStats { return kafka.ReaderStats{} }
func (r *flakyCommitReader) Close() error             { return nil }

// TestKafka_AckCommitBrokerError reports a non-nil error from
// the underlying CommitMessages. The poller's contract is
// "propagate the first error"; the in-flight rows are still
// drained (consistent with kafka-go's at-least-once delivery —
// the offset advance is independent of the dial).
func TestKafka_AckCommitBrokerError(t *testing.T) {
	t.Parallel()
	rdr := &flakyCommitReader{commitErr: errors.New("broker conn refused")}
	k, ids := kafkaSeekFixture(rdr, 42)
	trig := sqlc.Trigger{BrokerPoisonStrategy: "commit"}

	err := k.Ack(context.Background(), trig, ids)
	if err == nil {
		t.Fatal("Ack err = nil, want non-nil (CommitMessages returned an error)")
	}
	for _, id := range ids {
		if _, ok := k.inFlight[id]; ok {
			t.Errorf("inFlight[%s] still present after failing Ack", id)
		}
	}
}

// TestKafka_NackBrokerErrorAlwaysSeeks pins the offset-rebalance
// path: Nack with reason="broker_error" must always SetOffset —
// the broker_poison_strategy column is irrelevant outside the
// poison branch. Doubles the TestKafka_NackBrokerErrorAlwaysRewinds
// pin with a different in-flight shape to confirm the
// rebalance-failure surface (commits don't happen on transient
// broker errors).
func TestKafka_NackBrokerErrorAlwaysSeeks(t *testing.T) {
	t.Parallel()
	for _, off := range []int64{10, 20, 30} {
		off := off
		t.Run(fmt.Sprintf("offset=%d", off), func(t *testing.T) {
			t.Parallel()
			rdr := &poisonStrategyReader{}
			k, ids := kafkaSeekFixture(rdr, off)
			trig := sqlc.Trigger{BrokerPoisonStrategy: "seek-to-offset"} // intentionally non-default
			if err := k.Nack(context.Background(), trig, ids, triggerReasonBrokerError); err != nil {
				t.Fatalf("Nack offset=%d: %v", off, err)
			}
			rdr.mu.Lock()
			if rdr.poisonOp != poisonOpSetOffset {
				t.Errorf("offset=%d: terminal op = %v, want SetOffset (broker_error must always rewind)", off, rdr.poisonOp)
			}
			if len(rdr.offsets) != 1 || rdr.offsets[0] != off {
				t.Errorf("offset=%d: SetOffset offsets = %v, want [%d]", off, rdr.offsets, off)
			}
			rdr.mu.Unlock()
		})
	}
}

func TestKafka_BrokerStats(t *testing.T) {
	rdr := &poisonStrategyReader{}
	k := &kafkaPoller{reader: rdr}
	stats := k.BrokerStats(context.Background(), sqlc.Trigger{})
	if !stats.Available {
		t.Fatal("stats.Available = false, want true")
	}
	if stats.Lag != 10 {
		t.Fatalf("stats.Lag = %d, want 10", stats.Lag)
	}
	if stats.Depth != 5 {
		t.Fatalf("stats.Depth = %d, want 5", stats.Depth)
	}
}
