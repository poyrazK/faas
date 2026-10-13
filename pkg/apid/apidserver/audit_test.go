// Unit tests for the auditor seam introduced for issue #278. These
// exercise the wire-up of the account_label argument, the new
// failure-path duration observation, and the success-path duration
// observation — without going through the full handler harness.
// The handler-level failing-store test
// (handlers_audit_test.go::TestAuditEvents_FailingStoreDoesNotRollback)
// is the end-to-end counterpart that pins the action-not-rolled-back
// invariant under ADR-035 and exercises the new account_label arg.

package apidserver_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// stubAuditOps is an in-memory auditOps for unit tests. The full
// wire.OpsMetrics has a Prometheus counter+histogram; this stub
// exposes the same shape so the auditor's interface contract is
// pinned without dragging the registry into the audit unit tests.
//
// The Counter returned by AuditWriteFailures is a real
// prometheus.Counter (registry-backed), which satisfies the full
// Counter interface (Inc/Add/Collect/Desc) so the auditor's
// interface stays the same as against wire.OpsMetrics.
type stubAuditOps struct {
	mu        sync.Mutex
	registry  *prometheus.Registry
	counters  map[string]prometheus.Counter
	durations []stubDuration // ordered observations
}

type stubDuration struct {
	result string
	secs   float64
}

func newStubAuditOps() *stubAuditOps {
	return &stubAuditOps{
		registry: prometheus.NewRegistry(),
		counters: make(map[string]prometheus.Counter),
	}
}

func (s *stubAuditOps) AuditWriteFailures(accountID string) prometheus.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.counters[accountID]; ok {
		return c
	}
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_audit_write_failures",
		Help: "test stub for the audit-write-failure counter",
	})
	s.registry.MustRegister(c)
	s.counters[accountID] = c
	return c
}

func (s *stubAuditOps) AuditWriteFailureDuration(result string) prometheus.Observer {
	return stubObserver{s: s, result: result}
}

// AuditLogWriteTotal (PR-#TBD / C5) — success-path counter.
// Returns a fresh per-call counter so each emit site
// increments its own series; mirrors the existing per-
// accountID counter behaviour for AuditWriteFailures.
func (s *stubAuditOps) AuditLogWriteTotal(endpoint, kind string) prometheus.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_audit_log_write_total",
		Help: "test stub for the audit_log_write_total counter",
	})
	s.registry.MustRegister(c)
	return c
}

// AuditLogWriteFailuresTotal (PR-#TBD / C5) — failure-path
// counter. Returns a fresh counter per call so each failure
// mode can be inspected individually.
func (s *stubAuditOps) AuditLogWriteFailuresTotal(endpoint, kind, errorClass string) prometheus.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "stub_audit_log_write_failures_total",
		Help: "test stub for the audit_log_write_failures_total counter",
	})
	s.registry.MustRegister(c)
	return c
}

func (s *stubAuditOps) failureCount(t *testing.T, accountID string) float64 {
	t.Helper()
	s.mu.Lock()
	c, ok := s.counters[accountID]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	return testutil.ToFloat64(c)
}

type stubObserver struct {
	s      *stubAuditOps
	result string
}

func (o stubObserver) Observe(secs float64) {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	o.s.durations = append(o.s.durations, stubDuration{result: o.result, secs: secs})
}

// failingStore is a state.Store wrapper that fails every AppendEvent.
// Same pattern as handlers_audit_test.go::failingAuditStore, kept
// inline here so audit_test.go is self-contained.
type failingStore struct {
	Store state.Store
}

func (failingStore) AppendEvent(_ context.Context, _, _ string, _ *string, _ []byte) error {
	return errors.New("simulated AppendEvent failure")
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method; the test fixture must override
// it to keep the failure contract alive.
func (failingStore) AppendEventWithTrace(_ context.Context, _, _ string, _ *string, _ []byte, _ *string) error {
	return errors.New("simulated AppendEventWithTrace failure")
}

// TestAuditEmit_SuccessObservesOkHistogram asserts the success
// branch observes the AppendEvent latency under result="ok" and
// does NOT increment the failure counter.
func TestAuditEmit_SuccessObservesOkHistogram(t *testing.T) {
	store := state.NewMemStore()
	ops := newStubAuditOps()
	a := apidserver.NewAuditor(store, slog.New(slog.NewTextHandler(io.Discard, nil)), ops)
	id := "acct-success"
	a.Emit(context.Background(), "test.kind", &id, map[string]any{"k": "v"})
	if got := ops.failureCount(t, id); got != 0 {
		t.Errorf("success branch incremented failure counter: %v", got)
	}
	if len(ops.durations) != 1 || ops.durations[0].result != "ok" {
		t.Errorf("expected 1 ok observation, got %+v", ops.durations)
	}
}

// TestAuditEmit_FailureObservesFailedAndIncrementsCounter asserts
// the AppendEvent failure branch observes result="failed" AND
// increments the counter under the resolved account_id.
func TestAuditEmit_FailureObservesFailedAndIncrementsCounter(t *testing.T) {
	store := failingStore{Store: state.NewMemStore()}
	ops := newStubAuditOps()
	a := apidserver.NewAuditor(statetest.NewStore(store), slog.New(slog.NewTextHandler(io.Discard, nil)), ops)
	id := "acct-failure"
	a.Emit(context.Background(), "test.kind", &id, map[string]any{"k": "v"})
	if got := ops.failureCount(t, id); got != 1 {
		t.Errorf("failure counter for %q = %v, want 1", id, got)
	}
	if len(ops.durations) != 1 || ops.durations[0].result != "failed" {
		t.Errorf("expected 1 failed observation, got %+v", ops.durations)
	}
}

// TestAuditEmit_NilAccountNormalizesToEmpty asserts a nil account_id
// pointer passes the empty string downstream so the metric layer's
// accountLabel helper maps it to "anonymous" (the bounded admission
// set reserves that label).
func TestAuditEmit_NilAccountNormalizesToEmpty(t *testing.T) {
	store := failingStore{Store: state.NewMemStore()}
	ops := newStubAuditOps()
	a := apidserver.NewAuditor(statetest.NewStore(store), slog.New(slog.NewTextHandler(io.Discard, nil)), ops)
	a.Emit(context.Background(), "test.kind", nil, map[string]any{"k": "v"})
	if got := ops.failureCount(t, ""); got != 1 {
		t.Errorf("nil-account failure counter (empty key) = %v, want 1", got)
	}
}

// TestAuditEmit_DoesNotPanicOnFailingStore pins the failure-path
// non-panicking contract. The handler-level test in
// handlers_audit_test.go is the integration counterpart that
// asserts the HTTP response is still 201.
func TestAuditEmit_DoesNotPanicOnFailingStore(t *testing.T) {
	store := failingStore{Store: state.NewMemStore()}
	ops := newStubAuditOps()
	a := apidserver.NewAuditor(statetest.NewStore(store), slog.New(slog.NewTextHandler(io.Discard, nil)), ops)
	id := "acct-no-panic"
	a.Emit(context.Background(), "test.kind", &id, map[string]any{"k": "v"})
}
