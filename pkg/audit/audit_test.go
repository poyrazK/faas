// Unit tests for the pkg/audit.Auditor seam (issue #278 surface,
// lifted from cmd/apid/audit.go for cross-daemon reuse). The
// end-to-end counterpart that pins the action-not-rolled-back invariant
// under ADR-035 lives at the call site (cmd/apid/handlers_audit_test.go,
// pkg/sched/cron_test.go for cron.fired).
package audit_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
)

// uuidStringOf normalises either a canonical UUID (with hyphens) or a
// raw 32-char hex string into the canonical UUID form. MemStore's
// newID returns hex; parseSubjectID converts the hex back to canonical
// UUID bytes when storing the Subject, so ListEvents(subject=<hex>)
// returns rows whose Subject.String() always reports the canonical
// form regardless of which store produced it. (Same helper as
// pkg/sched/events_test.go and cmd/apid/handlers_audit_test.go.)
func uuidStringOf(s string) string {
	if strings.Contains(s, "-") {
		return s
	}
	if len(s) != 32 {
		return s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return s
	}
	return uuid.UUID(b).String()
}

// stubAuditOps is an in-memory Ops for unit tests. The full
// wire.OpsMetrics has a Prometheus counter+histogram; this stub
// exposes the same shape so the Auditor's interface contract is
// pinned without dragging the registry into the audit unit tests.
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

func (s *stubAuditOps) durationCount(t *testing.T, result string) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.durations {
		if d.result == result {
			n++
		}
	}
	return n
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

// failingStore wraps a state.Store and returns errAppendEventBoom
// from AppendEvent only. Mirrors cmd/apid/audit_test.go::failingStore.
type failingStore struct {
	state.Store
}

var errAppendEventBoom = errors.New("simulated AppendEvent failure")

func (failingStore) AppendEvent(_ context.Context, _, _ string, _ *string, _ []byte) error {
	return errAppendEventBoom
}

// silentLog discards slog output so test runs stay clean.
func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAuditor_Emit_WritesRowWithActor(t *testing.T) {
	store := state.NewMemStore()
	// MemStore.ListEvents parses the subject via parseSubjectID
	// (memstore.go:2834) which accepts canonical UUIDs (with hyphens)
	// or 32-char hex. CreateAccount returns a real UUID, so the
	// ListEvents filter below will match.
	acctRec, err := store.CreateAccount(context.Background(), "schedd-audit@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	ops := newStubAuditOps()
	a := audit.New(store, silentLog(), ops, "schedd")

	a.Emit(context.Background(), "cron.fired", &acctRec.ID, map[string]any{
		"cron_id": "c-1",
		"app_id":  "a-1",
	})

	rows, err := store.ListEvents(context.Background(), acctRec.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Actor != "schedd" {
		t.Errorf("Actor = %q, want schedd", rows[0].Actor)
	}
	if rows[0].Kind != "cron.fired" {
		t.Errorf("Kind = %q, want cron.fired", rows[0].Kind)
	}
	if rows[0].Subject == nil || rows[0].Subject.String() != uuidStringOf(acctRec.ID) {
		t.Errorf("Subject = %v, want %s", rows[0].Subject, uuidStringOf(acctRec.ID))
	}
	if got := ops.durationCount(t, "ok"); got != 1 {
		t.Errorf("ok observations = %d, want 1", got)
	}
	if got := ops.failureCount(t, acctRec.ID); got != 0 {
		t.Errorf("failure counter for %s = %v, want 0", acctRec.ID, got)
	}
}

func TestAuditor_Emit_NilAccountIDAllowed(t *testing.T) {
	store := state.NewMemStore()
	a := audit.New(store, silentLog(), newStubAuditOps(), "schedd")

	// nil subject is allowed for system-level events (e.g. cron.fired
	// when the account resolution failed earlier in the path). The
	// row still lands, just with an empty subject.
	a.Emit(context.Background(), "system.boot", nil, map[string]any{"k": "v"})

	rows, err := store.ListEvents(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Subject != nil {
		t.Errorf("Subject = %v, want nil", rows[0].Subject)
	}
}

func TestAuditor_Emit_NilDataMarshalsAsEmptyObject(t *testing.T) {
	store := state.NewMemStore()
	a := audit.New(store, silentLog(), newStubAuditOps(), "apid")
	acctRec, err := store.CreateAccount(context.Background(), "apid-audit@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	a.Emit(context.Background(), "auth.logout", &acctRec.ID, nil)

	rows, _ := store.ListEvents(context.Background(), acctRec.ID, 0)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if string(rows[0].Data) != "{}" {
		t.Errorf("Data = %q, want \"{}\"", rows[0].Data)
	}
}

// TestAuditor_Emit_LiftsTraceContext pins issue #555 PR-5: when
// ctx carries an OTel span, the trace_id + span_id are stamped onto
// the audit row's data JSON so the row joins the in-memory trace ring
// on the same key. The lift is best-effort: a missing span context
// leaves the data unchanged.
func TestAuditor_Emit_LiftsTraceContext(t *testing.T) {
	store := state.NewMemStore()
	a := audit.New(store, silentLog(), newStubAuditOps(), "apid")
	acctRec, err := store.CreateAccount(context.Background(), "apid-audit-trace@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	var tid oteltrace.TraceID
	var sid oteltrace.SpanID
	for i := range tid {
		tid[i] = byte(i + 1)
	}
	for i := range sid {
		sid[i] = byte(i + 1)
	}
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: tid,
		SpanID:  sid,
		Remote:  true,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), sc)

	a.Emit(ctx, "auth.login", &acctRec.ID, map[string]any{"ip": "127.0.0.1"})

	rows, _ := store.ListEvents(context.Background(), acctRec.ID, 0)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data["trace_id"] != tid.String() {
		t.Errorf("trace_id = %v, want %s", data["trace_id"], tid.String())
	}
	if data["span_id"] != sid.String() {
		t.Errorf("span_id = %v, want %s", data["span_id"], sid.String())
	}
	if data["ip"] != "127.0.0.1" {
		t.Errorf("ip attr dropped: %v", data)
	}
}

// TestAuditor_Emit_PreservesCustomerTraceID pins the merge contract:
// a customer-supplied trace_id in the data map is NOT overwritten by
// the active span context. The cron-fired path (issue #517) and any
// other seam that builds a trace_id outside the OTel SDK must keep
// its value.
func TestAuditor_Emit_PreservesCustomerTraceID(t *testing.T) {
	store := state.NewMemStore()
	a := audit.New(store, silentLog(), newStubAuditOps(), "apid")
	acctRec, err := store.CreateAccount(context.Background(), "apid-audit-cust-tid@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  oteltrace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		Remote:  true,
	})
	ctx := oteltrace.ContextWithSpanContext(context.Background(), sc)

	a.Emit(ctx, "cron.fired", &acctRec.ID, map[string]any{
		"trace_id": "00000000000000000000000000000000",
	})

	rows, _ := store.ListEvents(context.Background(), acctRec.ID, 0)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["trace_id"] != "00000000000000000000000000000000" {
		t.Errorf("trace_id = %v, want preserved customer value", data["trace_id"])
	}
	// span_id is still lifted from the active span (no collision
	// because the customer only set trace_id).
	if data["span_id"] == "" {
		t.Errorf("span_id lifted = %v, want from active span", data["span_id"])
	}
}

func TestAuditor_Emit_AppendEventFailureDoesNotPanic(t *testing.T) {
	base := state.NewMemStore()
	acctRec, err := base.CreateAccount(context.Background(), "schedd-fail@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	store := failingStore{base}
	ops := newStubAuditOps()
	a := audit.New(store, silentLog(), ops, "schedd")

	// Must NOT panic; failure semantics (ADR-035) require this to be
	// observable only — the action has already returned 200 by the
	// time Emit fires, so the audit row is observation, not source of
	// truth. The Warn log + counter increment + "failed" duration
	// observation are the only effects.
	a.Emit(context.Background(), "cron.fired", &acctRec.ID, map[string]any{"cron_id": "c-1"})

	if got := ops.durationCount(t, "failed"); got != 1 {
		t.Errorf("failed observations = %d, want 1", got)
	}
	if got := ops.failureCount(t, acctRec.ID); got != 1 {
		t.Errorf("failure counter for %s = %v, want 1", acctRec.ID, got)
	}
}

func TestAuditor_Emit_NilOpsAllowed(t *testing.T) {
	// Unit tests without an OpsMetrics (and the cmd/apid test
	// harness when ops wiring is skipped) must still work. The
	// counter increment + duration observation are guarded.
	store := state.NewMemStore()
	acctRec, err := store.CreateAccount(context.Background(), "apid-nilops@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	a := audit.New(store, silentLog(), nil, "apid")

	a.Emit(context.Background(), "key.created", &acctRec.ID, map[string]any{"key_id": "k-1"})

	rows, _ := store.ListEvents(context.Background(), acctRec.ID, 0)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestAuditor_Emit_AppendEventFailureWithNilOpsAlsoDoesNotPanic(t *testing.T) {
	// Double-failure: AppendEvent errors AND ops is nil. The
	// counter+observation branches must be no-ops, not panics.
	store := failingStore{state.NewMemStore()}
	a := audit.New(store, silentLog(), nil, "schedd")
	acct := "acct-1"

	a.Emit(context.Background(), "cron.fired", &acct, map[string]any{"cron_id": "c-1"})
}
