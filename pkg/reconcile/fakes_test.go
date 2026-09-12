// Test fakes for pkg/reconcile. The fakeStore embeds *state.MemStore
// (which already implements the full state.Store interface) and
// overrides only the methods the reconcile package exercises.
// This keeps the fake authoritative on the 200+ method interface
// while letting tests assert on the few that matter.
//
// The fakeAuditor is intentionally a thin wrapper around the real
// pkg/audit.Auditor. audit.New requires a Store (for AppendEvent),
// a Logger, and an Ops (counters). For tests we need:
//
//   - a Store that records AppendEvent calls so assertions can
//     check the audit-row payload structure
//   - a Logger (slog.Default is fine)
//   - an Ops (a no-op stub that satisfies the 2-method interface)
//
// The Auditor.Emit signature is void (no error), so the fakes
// don't need to model error injection — the auditor's contract
// is best-effort.

package reconcile

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeStore wraps *state.MemStore and exposes two test hooks the
// reconcile tests inspect:
//
//   - appendEvents (recorded via the AppendEvent override below):
//     every audit row the service emits, in order. Pinned by the
//     audit-ordering and chronology tests.
//
//   - createAppIfUnderQuotaHook (optional): when non-nil, the
//     per-app create path delegates to this hook instead of the
//     MemStore. Tests use it to inject a *QuotaError on the Nth
//     call so the inner-error path can be exercised without a
//     real DB. When nil, calls go through to MemStore.
//
// All hooks are optional; when nil, the embedded MemStore is the
// substrate for everything.
type fakeStore struct {
	*state.MemStore

	mu           sync.Mutex
	appendEvents []fakeEvent
	accountPlan  api.Plan
	// createAppIfUnderQuotaHook intercepts the per-app create
	// path. When nil, MemStore.CreateAppIfUnderQuota is called.
	createAppIfUnderQuotaHook func(app state.App) (state.App, error)
	// applyProjectReconcileHook intercepts the atomic project path when a
	// test needs to inject a quota/store failure after planning.
	applyProjectReconcileHook func(project state.Project, mutations []state.ProjectReconcileMutation, crons []state.ProjectReconcileCron, scanSource state.ProjectScanSource, limits api.Limits) (state.ProjectReconcileResult, error)
}

type fakeEvent struct {
	Actor   string
	Kind    string
	Subject *string
	Data    []byte
}

// newFakeStore returns a fakeStore fed from a freshly-constructed
// MemStore. The MemStore is also returned so the test can pre-seed
// accounts, projects, and apps without going through the full
// CRUD surface.
func newFakeStore() *fakeStore {
	return &fakeStore{
		MemStore:    state.NewMemStore(),
		accountPlan: api.PlanHobby,
	}
}

// AppendEvent records the call then delegates to the underlying
// MemStore. The MemStore is fine for storing audit rows because
// the fakeAuditor delegates to it via real pkg/audit.
func (s *fakeStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	s.mu.Lock()
	s.appendEvents = append(s.appendEvents, fakeEvent{
		Actor:   actor,
		Kind:    kind,
		Subject: subject,
		Data:    data,
	})
	s.mu.Unlock()
	return s.MemStore.AppendEvent(ctx, actor, kind, subject, data)
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method (audit.go::emit lifts the
// trace_id from data and forwards it via the new sibling).
// Same recording + delegation shape as AppendEvent above so
// the test hooks see every audit emit regardless of which
// entry point (AppendEvent vs AppendEventWithTrace) the
// caller took.
func (s *fakeStore) AppendEventWithTrace(ctx context.Context, actor, kind string, subject *string, data []byte, traceID *string) error {
	s.mu.Lock()
	s.appendEvents = append(s.appendEvents, fakeEvent{
		Actor:   actor,
		Kind:    kind,
		Subject: subject,
		Data:    data,
	})
	s.mu.Unlock()
	return s.MemStore.AppendEventWithTrace(ctx, actor, kind, subject, data, traceID)
}

// CreateAccount promotes the embedded *state.MemStore.CreateAccount
// up so the linter can see it. The staticcheck QF1008 hit on
// "s.MemStore.CreateAccount" is the kind of noise a fake
// shouldn't have to lint around.
func (s *fakeStore) CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error) {
	return s.MemStore.CreateAccount(ctx, email, plan)
}

// CreateAppIfUnderQuota forwards to the test hook when set;
// otherwise delegates to the embedded MemStore. The hook is the
// only way to exercise the inner *QuotaError safety net without
// a real Postgres quota race — MemStore's CreateAppIfUnderQuota
// only fires QuotaError when the cap is genuinely exceeded, which
// is hard to set up reliably across runs.
func (s *fakeStore) CreateAppIfUnderQuota(ctx context.Context, app state.App, limits api.Limits) (state.App, error) {
	if s.createAppIfUnderQuotaHook != nil {
		return s.createAppIfUnderQuotaHook(app)
	}
	return s.MemStore.CreateAppIfUnderQuota(ctx, app, limits)
}

func (s *fakeStore) ApplyProjectReconcile(ctx context.Context, project state.Project, mutations []state.ProjectReconcileMutation, crons []state.ProjectReconcileCron, scanSource state.ProjectScanSource, limits api.Limits) (state.ProjectReconcileResult, error) {
	if s.applyProjectReconcileHook != nil {
		return s.applyProjectReconcileHook(project, mutations, crons, scanSource, limits)
	}
	return s.MemStore.ApplyProjectReconcile(ctx, project, mutations, crons, scanSource, limits)
}

// snapshotEvents returns a copy of the recorded AppendEvent calls
// since the last reset. Safe under concurrent mutation because
// the underlying slice is rebuilt on every call.
func (s *fakeStore) snapshotEvents() []fakeEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeEvent, len(s.appendEvents))
	copy(out, s.appendEvents)
	return out
}

// putAccount seeds an Account with the fakeStore's plan. Used by
// the tests as the prereq for Project + Apps by AccountID.
func (s *fakeStore) putAccount(id string) state.Account {
	acct, err := s.CreateAccount(context.Background(), id+"@example.com", s.accountPlan)
	if err != nil {
		panic(err)
	}
	return acct
}

// noopOps is the audit.Ops stub. pkg/audit requires the interface
// but the tests don't assert on counters; the histogram values
// are observed but discarded via a no-op Counter / Observer.
type noopOps struct{}

func (noopOps) AuditWriteFailures(string) prometheus.Counter {
	// Discard is a real Counter that accepts .Inc() without
	// panicking or registering a metric. Returning nil would
	// crash audit.go:117 which calls .Observe() on the
	// returned Observer.
	return prometheus.NewCounter(prometheus.CounterOpts{})
}
func (noopOps) AuditWriteFailureDuration(string) prometheus.Observer {
	return prometheus.NewHistogram(prometheus.HistogramOpts{})
}

// AuditLogWriteTotal + AuditLogWriteFailuresTotal
// (PR-#TBD / C5) — same no-op shape as AuditWriteFailures
// above. The audit emit path increments these on every
// success / failure; the reconcile tests don't assert on
// them.
func (noopOps) AuditLogWriteTotal(string, string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{})
}
func (noopOps) AuditLogWriteFailuresTotal(string, string, string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{})
}

// newFakeAuditor builds a real pkg/audit.Auditor backed by the
// fakeStore. The "actor" is "reconcile" so the audit-row actor
// column matches the convention. The returned handle also
// exposes the audit hook so tests can introspect the calls.
func newFakeAuditor(store *fakeStore) *audit.Auditor {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return audit.New(store, log, noopOps{}, "reconcile")
}
