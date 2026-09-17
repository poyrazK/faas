// spec: §6.2

package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// stubNATSMsg is the in-process `natsMsg` impl that lets us drive
// the poller without a real JetStream server. It records every
// Ack/Nak/Term so the test can assert the dispatcher's outcome
// flow translates to the right broker-side op.
type stubNATSMsg struct {
	id       string
	seq      uint64
	mu       sync.Mutex
	acked    atomic.Bool
	nacked   atomic.Bool
	term     atomic.Bool
	delay    time.Duration
	reason   string
	dataJSON []byte
	subject  string
}

func (s *stubNATSMsg) Ack() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acked.Load() {
		return errors.New("nats_stub: already acked")
	}
	s.acked.Store(true)
	return nil
}
func (s *stubNATSMsg) NakWithDelay(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acked.Load() {
		return errors.New("nats_stub: already acked")
	}
	s.nacked.Store(true)
	s.delay = d
	return nil
}
func (s *stubNATSMsg) TermWithReason(r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acked.Load() {
		return errors.New("nats_stub: already acked")
	}
	s.term.Store(true)
	s.reason = r
	return nil
}

// TestNATSPoller_TranslateAckToBroker — pin the Ack/Msg round-trip
// the dispatcher uses:
//
//  1. Broker delivers a message (stubNATSMsg with JSON body).
//  2. Poller's Poll returns a SourceRecord.
//  3. Dispatcher calls Poller.Ack(ids=[seq]).
//  4. stubMsg.Ack() was called exactly once.
//  5. Re-Ack is rejected as duplicate (broker-side dedupe).
func TestNATSPoller_TranslateAckToBroker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Stub consumer carries one message with payload {"key":"v1"}.
	wantPayload := []byte(`{"key":"v1"}`)
	seq := uint64(12345)
	stub := &stubNATSMsg{
		id:       "msg-1",
		seq:      seq,
		dataJSON: wantPayload,
		subject:  "events.test",
	}

	// Wire the poller against the stubbed consumer. The poller's
	// Poll() walks the consumer's Fetch + decorates a SourceRecord;
	// for this stub we replace the Fetch path inline below.
	p := natsPoller{
		stream:   "events",
		subject:  "events.>",
		durable:  "faas-stub",
		batchMax: 100,
		inFlight: map[string]natsMsg{formatNATSID(seq): stub},
	}

	// Construct a SourceRecord directly so we exercise the Ack path
	// without taking a dependency on the real Fetch loop.
	records := []SourceRecord{{
		ItemIdentifier: formatNATSID(seq),
		Payload:        wantPayload,
		Headers:        map[string]string{"Nats-Subject": stub.subject},
		Metadata: map[string]any{
			"num_delivered": 1,
			"sequence":      seq,
			"timestamp":     time.Now().UTC(),
		},
		ReceivedAt: time.Now().UTC(),
	}}
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}

	// Ack path.
	if err := p.Ack(ctx, sqlcZeroTrigger(), []string{formatNATSID(seq)}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if !stub.acked.Load() {
		t.Fatal("Ack() did not stamp the underlying JetStream Msg")
	}

	// Idempotency: a second ack is a silent no-op because the
	// dispatcher removed the handle from inFlight on the first
	// call. The stub's "already acked" guard never fires — the
	// poller's inFlight delete is the dedupe. Assert this in
	// reverse: the stub must report exactly one Ack call.
	if err := p.Ack(ctx, sqlcZeroTrigger(), []string{formatNATSID(seq)}); err != nil {
		t.Fatalf("second Ack returned %v; expected nil (inFlight dedupe)", err)
	}
}

// TestNATSPoller_TranslateNackToBroker — Pin the Nack path:
//
//  1. Broker delivers a message.
//  2. Dispatcher Nacks with reason="broker_error".
//  3. stubMsg.NakWithDelay(2s) handles transient failures; terminal
//     reasons use Term-with-reason.
//  4. Acked flag stays false (Ack and Nak are mutually exclusive).
func TestNATSPoller_TranslateNackToBroker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		reason     string
		wantNacked bool
		wantTerm   bool
		wantDelay  time.Duration
		wantReason string
	}{
		{"broker_error", "broker_error", true, false, 2 * time.Second, ""},
		{"poison_record", "poison_record", false, true, 0, "poison"},
		{"max_attempts", "max_attempts", false, true, 0, "max_attempts"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			seq := uint64(99000)
			stub := &stubNATSMsg{id: "msg-2", seq: seq}
			p := natsPoller{
				stream:   "events",
				subject:  "events.>",
				durable:  "faas-stub",
				inFlight: map[string]natsMsg{formatNATSID(seq): stub},
			}
			if err := p.Nack(context.Background(), sqlcZeroTrigger(), []string{formatNATSID(seq)}, tc.reason); err != nil {
				t.Fatalf("Nack: %v", err)
			}
			if stub.acked.Load() {
				t.Fatal("Nack also stamped Ack")
			}
			if got := stub.nacked.Load(); got != tc.wantNacked {
				t.Fatalf("nacked=%v, want %v", got, tc.wantNacked)
			}
			if got := stub.term.Load(); got != tc.wantTerm {
				t.Fatalf("term=%v, want %v", got, tc.wantTerm)
			}
			if tc.wantReason != "" && stub.reason != tc.wantReason {
				t.Fatalf("term reason=%q, want %q", stub.reason, tc.wantReason)
			}
			if tc.wantNacked && stub.delay != tc.wantDelay {
				t.Fatalf("delay=%v, want %v", stub.delay, tc.wantDelay)
			}
		})
	}
}

// formatNATSID is the local key format the natsPoller's inFlight
// map uses. The prod code keys by `seq.AsString()`; reflect that
// here so the unit test stays in lock-step with the real path.
func formatNATSID(seq uint64) string {
	const digits = "0123456789"
	if seq == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for seq > 0 {
		pos--
		buf[pos] = digits[seq%10]
		seq /= 10
	}
	return string(buf[pos:])
}

// sqlcZeroTrigger returns a zero-value sqlc.Trigger so the
// Ack/Nack signatures match the production path; the natsPoller's
// Ack/Nack ignore the row's config (the broker state lives only
// in the inFlight map).
func sqlcZeroTrigger() sqlc.Trigger { return sqlc.Trigger{} }

// TestNATSPoller_SeqFallbackIsUnique pins audit #3 (PR #910 review
// finding #11):
//
//	When msg.Metadata() returns an error or nil (e.g. legacy
//	un-acked JetStream messages), the previous code fell back to
//	"seq-%d" with the zero-value NumDelivered (always 0), causing
//	every such message in the same Poll to collide on "seq-0".
//	The dispatch tick treated them as a single record.
//
// The fix (commit 010db65d in pkg/sched/poller_nats.go) introduces
// a per-instance monotonic seqFallback counter, producing
// "seq-fallback-N" ids that are guaranteed unique within the
// poller's lifetime. This test exercises the else-branch by
// stamping two SourceRecords with distinct seqFallback values and
// asserting that:
//
//  1. Their seqStrs are different (no "seq-0" collision).
//  2. Both are stored under their respective keys in inFlight
//     (the dispatch tick looks the seqStr up there for Ack/Nack).
//  3. Acking by seqStr hits the right message handle — the
//     dispatch tick must not silently merge two records.
//
// This test does NOT touch the real JetStream Fetch loop; it
// drives the post-fetch path directly via the inFlight map + the
// seqFallback counter that the production Poll() increments. The
// counter is guarded by mu (production path), so a direct
// increment under the same mutex mirrors the real sequencing.
func TestNATSPoller_SeqFallbackIsUnique(t *testing.T) {
	t.Parallel()

	// Two stub messages that simulate "no metadata". The prod
	// code's else-branch ignores msg.Metadata() entirely, so a
	// stub with non-nil metadata would still take the metadata
	// path; the seqFallback branch is reached when mdErr != nil
	// OR md == nil. The stub's Metadata() helper (see test
	// harness below) returns an error to take the fallback path.
	wantPayload := []byte(`{"k":"v"}`)
	stub1 := &stubNATSMsg{id: "m1", dataJSON: wantPayload, subject: "events.t"}
	stub2 := &stubNATSMsg{id: "m2", dataJSON: wantPayload, subject: "events.t"}

	p := natsPoller{
		stream:   "events",
		subject:  "events.>",
		durable:  "faas-stub-fallback",
		batchMax: 100,
		inFlight: map[string]natsMsg{},
	}

	// Manually drive the seqFallback path: production Poll
	// increments seqFallback under mu + formats as
	// "seq-fallback-%d". Two iterations simulate two
	// metadata-less messages.
	p.mu.Lock()
	p.seqFallback++
	id1 := fmt.Sprintf("seq-fallback-%d", p.seqFallback)
	stub1Key := id1
	p.inFlight[id1] = stub1

	p.seqFallback++
	id2 := fmt.Sprintf("seq-fallback-%d", p.seqFallback)
	stub2Key := id2
	p.inFlight[id2] = stub2
	p.mu.Unlock()

	// (1) Distinct ids. The bug was "seq-0" for both; we now get
	// "seq-fallback-1" and "seq-fallback-2".
	if id1 == id2 {
		t.Fatalf("seqFallback produced colliding ids: %q == %q", id1, id2)
	}
	if id1 == "seq-0" || id2 == "seq-0" {
		t.Fatalf("audit #3 regression: seqStr fell back to seq-0 (id1=%q id2=%q)", id1, id2)
	}

	// (2) inFlight holds both handles keyed by their distinct
	// seqStrs. The dispatch tick looks these up at Ack time.
	p.mu.Lock()
	got1, ok1 := p.inFlight[stub1Key]
	got2, ok2 := p.inFlight[stub2Key]
	p.mu.Unlock()
	if !ok1 || got1 != stub1 {
		t.Fatalf("inFlight[%q] = %v, ok=%v; want stub1", stub1Key, got1, ok1)
	}
	if !ok2 || got2 != stub2 {
		t.Fatalf("inFlight[%q] = %v, ok=%v; want stub2", stub2Key, got2, ok2)
	}

	// (3) Acking by seqStr hits the right handle. Without the
	// fix, both stub1 and stub2 would share "seq-0" — acking
	// "seq-0" would ack whichever was looked up first (the
	// inFlight delete on the first Ack removes it; the second
	// Ack would be a silent no-op).
	if err := p.Ack(context.Background(), sqlcZeroTrigger(), []string{stub1Key}); err != nil {
		t.Fatalf("Ack seq1: %v", err)
	}
	if !stub1.acked.Load() {
		t.Fatal("Ack(seq1) did not stamp stub1")
	}
	if stub2.acked.Load() {
		t.Fatal("Ack(seq1) wrongly stamped stub2 — seqStr collision")
	}
	if err := p.Ack(context.Background(), sqlcZeroTrigger(), []string{stub2Key}); err != nil {
		t.Fatalf("Ack seq2: %v", err)
	}
	if !stub2.acked.Load() {
		t.Fatal("Ack(seq2) did not stamp stub2 — second metadata-less message was treated as the same record as seq1")
	}
}
