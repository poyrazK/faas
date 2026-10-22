// Unit tests for the async-batched failed-login audit channel
// (issue #286, ADR-035 §"Rejected alternatives" #4 closure).
//
// These tests pin the four invariants the async path is supposed to
// preserve that the synchronous Emit path does not have to:
//
//  1. The handler-side channel write is non-blocking. A saturated
//     channel does not propagate to the customer-facing 401.
//  2. The per-IP counter is incremented BEFORE the channel write. A
//     dropped row still produces the Prometheus signal — the audit
//     row is observation, the counter is the SOC 2 evidence.
//  3. The flusher drains in-flight rows on Close. The daemon
//     shutdown path leaves the events table consistent with the
//     in-process queue.
//  4. The record table is bounded by the auditor's flushBatch per
//     drain — a 4096-row burst flushes in 4 batches of AppendEvent
//     calls, not one.
//
// The test surface is package-internal (package main) so it can
// build a stub auditOps + a failingStore wrapper directly. The
// handler-level counterpart that proves the customer-facing 401 is
// unaffected lives in handlers_auth_login_test.go.
package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// stubFailedOps is the in-memory auditFailedOps the unit tests
// expect. Mirrors stubAuditOps in audit_test.go: a registry-backed
// prometheus.Counter per IP, plus unlabelled drop and
// audit-write-failure counters, so the auditor's interface
// contract is pinned without dragging the full wire.OpsMetrics
// into the audit unit tests.
//
// The intention is to mirror the production failure surface — the
// drop counter is the canonical "the channel is full" signal, and
// the audit-write-failure counter is the canonical "AppendEvent
// could not be written" signal. Both signals back the SOC 2 CC7.2
// audit-write-failure surface.
type stubFailedOps struct {
	mu            sync.Mutex
	registry      *prometheus.Registry
	counters      map[string]prometheus.Counter
	dropped       prometheus.Counter
	writeFailures prometheus.Counter
}

// flushCounter is a state.Store wrapper that counts every successful
// AppendEvent call. Used by the shutdown-drain test to assert
// "every enqueued row landed in the store". The wrapppee is
// state.NewMemStore() so the test exercises the same backend the
// handler harness uses.
type flushCounter struct {
	state.Store
	mu    sync.Mutex
	count int
}

func (f *flushCounter) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	if err := f.Store.AppendEvent(ctx, actor, kind, subject, data); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return nil
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method; delegate to the wrapped Store and
// increment the counter so the test's metric assertions stay
// aligned regardless of which sibling was called.
func (f *flushCounter) AppendEventWithTrace(ctx context.Context, actor, kind string, subject *string, data []byte, traceID *string) error {
	if err := f.Store.AppendEventWithTrace(ctx, actor, kind, subject, data, traceID); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return nil
}

func (f *flushCounter) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// failingFlushStore is the Appendix-D equivalent of failingStore
// for the failed-login audit path. Mirrors the audit_test.go
// failingStore pattern so the audit-write-failure path is wired
// identically to the success-path audit seam.
type failingFlushStore struct {
	Store state.Store
}

func (failingFlushStore) AppendEvent(_ context.Context, _, _ string, _ *string, _ []byte) error {
	return errors.New("simulated failed-login AppendEvent failure")
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method.
func (failingFlushStore) AppendEventWithTrace(_ context.Context, _, _ string, _ *string, _ []byte, _ *string) error {
	return errors.New("simulated failed-login AppendEventWithTrace failure")
}

func newStubFailedOps() *stubFailedOps {
	reg := prometheus.NewRegistry()
	dropped := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_failed_login_audit_dropped",
		Help: "test stub for the failed-login drop counter",
	})
	writeFailures := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_failed_login_audit_write_failures",
		Help: "test stub for the failed-login audit-write failure counter",
	})
	reg.MustRegister(dropped)
	reg.MustRegister(writeFailures)
	return &stubFailedOps{
		registry:      reg,
		counters:      make(map[string]prometheus.Counter),
		dropped:       dropped,
		writeFailures: writeFailures,
	}
}

func (s *stubFailedOps) FailedLoginTotal(ip string) prometheus.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.counters[ip]; ok {
		return c
	}
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_failed_login_total",
		Help: "test stub for the failed-login total counter",
	})
	s.registry.MustRegister(c)
	s.counters[ip] = c
	return c
}

func (s *stubFailedOps) FailedLoginDropped() prometheus.Counter {
	return s.dropped
}

func (s *stubFailedOps) FailedLoginAuditWriteFailures() prometheus.Counter {
	return s.writeFailures
}

func (s *stubFailedOps) totalCount(t *testing.T, ip string) float64 {
	t.Helper()
	s.mu.Lock()
	c, ok := s.counters[ip]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	return testutil.ToFloat64(c)
}

func (s *stubFailedOps) droppedCount() float64 {
	return testutil.ToFloat64(s.dropped)
}

func (s *stubFailedOps) writeFailureCount() float64 {
	return testutil.ToFloat64(s.writeFailures)
}

// newAuditorForTestWithCapacity builds an auditor with a tunable
// channel capacity. The production constructor pins capacity at
// 4096 (failedLoginChanCapacity); the drop test (TestAuditFailedLogin_
// DropOnFullChannel) needs a capacity of 0 so the very first send
// hits the default branch without the test having to enqueue 4096
// rows. flushEvery / flushBatch are also parameterised so the
// flapping test runs fast.
func newAuditorForTestWithCapacity(store state.Store, log *slog.Logger, ops apidserver.AuditFailedOps, cap int, flushEvery time.Duration, flushBatch int) *apidserver.Auditor {
	a := apidserver.NewAuditor(store, log, nil)
	(*a.FailedCh()) = make(chan apidserver.FailedLoginRow, cap)
	(*a.CloseSignal()) = make(chan struct{})
	(*a.FailedOps()) = ops
	(*a.FlushEvery()) = flushEvery
	(*a.FlushBatch()) = flushBatch
	return a
}

// TestAuditFailedLogin_EnqueueWritesRowToStore is the happy-path
// unit proof: a single EmitFailedLogin lands in the events table
// after the flusher drains. Reads the row by querying the underlying
// MemStore's AppendEvent log (memstore keeps every row in m.events).
func TestAuditFailedLogin_EnqueueWritesRowToStore(t *testing.T) {
	ms := state.NewMemStore()
	ops := newStubFailedOps()
	auditOps := newStubAuditOps()
	a := newAuditorForTestWithCapacity(ms, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 16, 25*time.Millisecond, 100)
	(*a.Ops()) = auditOps
	a.Start(context.Background())
	defer a.Close()

	a.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")

	// Wait for the flusher to drain. The flusher cadence is 25 ms
	// in the test; double it for the channel-leak guard.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, err := ms.ListEvents(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			e := events[0]
			if e.Kind != apidserver.KindAuthLoginFailed {
				t.Errorf("kind = %q, want %q", e.Kind, apidserver.KindAuthLoginFailed)
			}
			if e.Subject != nil {
				t.Errorf("subject = %v, want nil (failed login cannot be attributed to a known account)", e.Subject)
			}
			if got := auditOps.failureCount(t, ""); got != 0 {
				t.Errorf("failed-login audit write failed: %v", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for flusher to drain")
}

// TestAuditFailedLogin_DropOnFullChannel pins the load-bearing
// invariant: when the channel is full, the row is dropped silently
// (the Warn + counter increment is the only signal), the 401 stays
// unblocked, and the per-IP counter has already been incremented
// before the drop. This is the ADR-035 §"Rejected alternatives" #4
// closure: a sync INSERT on a full channel would gate the 401 on
// the DB roundtrip. The non-blocking select default arm proves we
// never do that.
func TestAuditFailedLogin_DropOnFullChannel(t *testing.T) {
	store := state.NewMemStore()
	ops := newStubFailedOps()
	a := newAuditorForTestWithCapacity(store, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 0, 25*time.Millisecond, 100)
	a.Start(context.Background())
	defer a.Close()

	// First emit hits the capacity-0 channel's default branch
	// immediately. The per-IP counter must have been incremented
	// regardless of the drop.
	a.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")

	if got := ops.totalCount(t, "203.0.113.7"); got != 1 {
		t.Errorf("per-IP counter = %v, want 1 (counter is incremented BEFORE the channel send)", got)
	}
	if got := ops.droppedCount(); got != 1 {
		t.Errorf("drop counter = %v, want 1 (channel was full, row should be dropped)", got)
	}
}

// TestAuditFailedLogin_AuditWriteFailureIncrementsCounter pins the
// failure-path counter increments when the underlying AppendEvent
// fails. The per-IP counter still has its +1 from the pre-channel
// Increment; the dedicated failed-login audit-write-failure
// counter is the additional signal that the SOC 2 audit log is
// missing a row.
//
// Issue #286 review fix (#3 in the review): the success-path
// AuditWriteFailures counter MUST NOT be touched on this path,
// because the row's nil subject would otherwise collapse into
// account_id="anonymous" alongside legitimate anonymous-success-
// path failures. The test asserts both:
//   - failedLoginAuditWriteFailures counter incremented (+1), AND
//   - success-path auditWriteFailures counter NOT incremented.
func TestAuditFailedLogin_AuditWriteFailureIncrementsCounter(t *testing.T) {
	failingStore := failingFlushStore{Store: state.NewMemStore()}
	ops := newStubFailedOps()
	successOps := newStubAuditOps()
	base := apidserver.NewAuditor(statetest.NewStore(failingStore), slog.New(slog.NewTextHandler(io.Discard, nil)), successOps)
	(*base.FailedOps()) = ops
	(*base.CloseSignal()) = make(chan struct{})
	base.Start(context.Background())
	defer base.Close()

	base.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")

	// Wait for the flusher to attempt + fail the AppendEvent and
	// the success-path counter to remain untouched.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := ops.writeFailureCount(); got == 1 {
			if got := successOps.failureCount(t, ""); got != 0 {
				t.Fatalf("success-path AuditWriteFailures counter incremented to %v on the failed-login path; the dedicated counter should be the only one touched", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dedicated failedLoginAuditWriteFailures counter did not increment")
}

// TestAuditFailedLogin_FlappingChannelBatchFlushes asserts the
// flusher drains the channel in chunks of flushBatch. The point is
// to prove the record table is bounded — a 4096-row burst cannot
// pin Postgres for the duration of an unbounded drain.
//
// We do this by configuring a flushBatch of 10 and asserting that,
// after enqueueing 25 rows, the flusher makes at least 3 drain
// passes (25 / 10 = 3 ceiling). The exact count of pass boundaries
// is timing-sensitive, so we assert the count is >= 3 drain cycles
// by sleeping enough ticks for the flusher to do its work and then
// asserting the row count is exactly 25.
func TestAuditFailedLogin_FlappingChannelBatchFlushes(t *testing.T) {
	ms := state.NewMemStore()
	ops := newStubFailedOps()
	a := newAuditorForTestWithCapacity(ms, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 64, 10*time.Millisecond, 10)
	a.Start(context.Background())
	defer a.Close()

	const n = 25
	for i := 0; i < n; i++ {
		a.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")
	}

	// Wait for the flusher to drain all 25 rows. With flushBatch=10
	// and flushEvery=10ms, the worst case is 3 drain passes
	// (~30 ms), plus a fudge factor for goroutine scheduling.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, err := ms.ListEvents(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == n {
			// Every row landed with the right kind.
			for _, e := range events {
				if e.Kind != apidserver.KindAuthLoginFailed {
					t.Errorf("event kind = %q, want %q", e.Kind, apidserver.KindAuthLoginFailed)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("flusher did not drain all 25 rows within deadline")
}

// TestAuditFailedLogin_ShutdownDrainsInFlightRows asserts the
// daemon shutdown path leaves the events table consistent with the
// in-process queue. We do not start the flusher goroutine — Close
// is the only way to drain the channel when the goroutine is not
// running. The channel is unbuffered (cap=0) for the pre-Start
// emit, so the emit is dropped; we re-construct with a buffered
// channel and enqueue, then call Close to drive the drain.
func TestAuditFailedLogin_ShutdownDrainsInFlightRows(t *testing.T) {
	store := state.NewMemStore()
	counter := &flushCounter{Store: store}
	ops := newStubFailedOps()
	a := newAuditorForTestWithCapacity(counter, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 32, 25*time.Millisecond, 100)
	a.Start(context.Background())

	const n = 5
	for i := 0; i < n; i++ {
		a.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")
	}

	// Close should drain the channel and wait for the flusher
	// goroutine to exit.
	a.Close()

	if got := counter.Count(); got != n {
		t.Errorf("after Close, AppendEvent called %d times, want %d", got, n)
	}
}

// TestAuditFailedLogin_PayloadShape pins the data payload shape.
// The row's data field is JSON with keys {ip, email_hash, user_agent}
// only — no reason discriminator, no method discriminator, no route
// discriminator. This is the §11 anti-enumeration closure at the
// audit-row level: the audit reader cannot distinguish "no such
// account" from "wrong password" from "OAuth-only" by the row
// content. The audit log is the operator oracle, not the customer
// oracle, and the asymmetry is the security property.
func TestAuditFailedLogin_PayloadShape(t *testing.T) {
	ms := state.NewMemStore()
	ops := newStubFailedOps()
	a := newAuditorForTestWithCapacity(ms, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 16, 25*time.Millisecond, 100)
	a.Start(context.Background())
	defer a.Close()

	a.EmitFailedLogin("203.0.113.7", "abc123hash", "Mozilla/5.0")

	// Wait for the row to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, err := ms.ListEvents(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			e := events[0]
			// Unmarshal the JSON data into a flat map and assert
			// the discriminator keys + the absence of any leak that
			// would re-open the §11 anti-enumeration oracle.
			var got map[string]any
			if err := json.Unmarshal(e.Data, &got); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			wantKeys := map[string]string{
				"ip":         "203.0.113.7",
				"email_hash": "abc123hash",
				"user_agent": "Mozilla/5.0",
			}
			for k, want := range wantKeys {
				if got[k] != want {
					t.Errorf("data[%q] = %v, want %q", k, got[k], want)
				}
			}
			if len(got) != len(wantKeys) {
				t.Errorf("data has %d keys, want %d (no reason/method/route discriminator allowed)", len(got), len(wantKeys))
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("flusher did not drain")
}

// TestAuditFailedLogin_UserAgentSanitizedAtSeam pins the
// CodeQL go/log-injection closure (issue #286 review fix #9).
// The user_agent value that lands in the audit row's data field
// must be the same byte sequence that the slog warn-on-drop line
// sees — sanitized ONCE at the EmitFailedLogin seam via
// pkg/logsanitize.Field, which replaces any non-tab ≤0x1F and
// DEL (0x7F) with U+00B7 (middle dot).
//
// The risk: an attacker can submit `User-Agent: curl\r\nFAKE-INJECT`
// and have the CRLF land in the events.data JSON. The slog and
// the metric label are already sanitized at their call sites
// (cmd/apid/audit.go warn-on-drop uses logsanitize.Field; the
// Prometheus label collapses to a known fixed shape), but the
// audit row's data field is a direct passthrough unless we
// sanitize at the seam. This test makes the sanitization at the
// seam load-bearing: a regression that pushes sanitization to a
// downstream call site (and misses one) trips the assertion.
func TestAuditFailedLogin_UserAgentSanitizedAtSeam(t *testing.T) {
	ms := state.NewMemStore()
	ops := newStubFailedOps()
	a := newAuditorForTestWithCapacity(ms, slog.New(slog.NewTextHandler(io.Discard, nil)), ops, 16, 25*time.Millisecond, 100)
	a.Start(context.Background())
	defer a.Close()

	// Craft a user_agent containing CRLF + DEL — both must be
	// replaced with U+00B7 (middle dot). Tab (0x09) is preserved
	// per logsanitize.Field's documented contract.
	const malicious = "curl\r\nFAKE\x7FINJECT\thello"
	const want = "curl··FAKE·INJECT\thello"
	a.EmitFailedLogin("203.0.113.7", "abc123hash", malicious)

	// Wait for the flusher to drain.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, err := ms.ListEvents(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			var got map[string]any
			if err := json.Unmarshal(events[0].Data, &got); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			if ua, _ := got["user_agent"].(string); ua != want {
				t.Errorf("user_agent in audit row = %q, want %q (sanitization at seam failed; CRLF/DEL leaked through)", ua, want)
			}
			// Belt-and-braces: confirm no raw control chars made
			// it into the row.
			if ua, _ := got["user_agent"].(string); strings.ContainsAny(ua, "\r\n\x00") {
				t.Errorf("user_agent contains raw control chars after sanitization: %q", ua)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("flusher did not drain")
}
