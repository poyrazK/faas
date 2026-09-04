// teststore.go — unit-test fakes for pkg/events.
//
// Two stubs:
//
//   - stubStore wraps state.MemStore so the platform tests can
//     use a real in-memory AppendEvent path (the
//     pkg/audit.Auditor tests use the same pattern, see
//     pkg/audit/audit_test.go::failingStore).
//   - failingStore wraps state.Store and returns an error from
//     AppendEvent only. Mirrors the audit_test failingStore
//     precedent so the failure-path counter (result="failed")
//     gets its own coverage.
//
// The fake broadcaster captures the last published payload so the
// publish path can be asserted without spinning up a real SSE
// subscriber.
package events

import (
	"context"
	"errors"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// stubStore wraps state.MemStore. The platform tests run the
// AppendEvent code path against in-memory state so the resulting
// events row is observable through ListEvents.
//
// Concrete pointer-required interface satisfaction: MemStore
// returns *MemStore from state.NewMemStore(); the wrapper keeps
// the pointer unchanged so ListEvents works.
type stubStore struct {
	*state.MemStore
}

func newStubStore() *stubStore {
	return &stubStore{MemStore: state.NewMemStore()}
}

// failingStore wraps a state.Store and returns an error from
// AppendEvent only. Mirrors pkg/audit/audit_test.go::failingStore.
type failingStore struct {
	state.Store
}

var errAppendEventBoom = errors.New("simulated AppendEvent failure")

func (failingStore) AppendEvent(_ context.Context, _, _ string, _ *string, _ []byte) error {
	return errAppendEventBoom
}

// stubBroadcaster records the last publish call so the platform
// tests can assert the SSE pub/sub path runs end-to-end without
// standing up a real subscriber. Safe for concurrent use.
type stubBroadcaster struct {
	mu        sync.Mutex
	calls     int
	lastTopic string
	lastBody  []byte
}

func (b *stubBroadcaster) PublishTopic(topic string, payload []byte) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	b.lastTopic = topic
	b.lastBody = payload
	return 1
}

// stubOps captures the per-phase emit + per-phase duration calls.
// Mirrors the audit.ops stub pattern (pkg/audit/audit_test.go).
type stubOps struct {
	mu            sync.Mutex
	emittedCalls  []string // "<phase>:<result>"
	durationCalls []string
	durationSecs  []float64
	recoveryCalls []string // "<kind>:<result>" — recovery-timeline

	// backbone is a private registry so the WakePhaseEmitted
	// stub returns a real Counter that satisfies .Inc(). The
	// call is recorded in emittedCalls; the counter itself is a
	// no-op for assertion purposes.
	backbone         *prometheus.CounterVec
	recoveryBackbone *prometheus.CounterVec
}

func newStubOps() *stubOps {
	return &stubOps{
		backbone: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "events_platform_test_wake_phase_emitted",
			Help: "test",
		}, []string{"phase", "result"}),
		recoveryBackbone: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "events_platform_test_recovery_event_emitted",
			Help: "test",
		}, []string{"kind", "result"}),
	}
}

func (o *stubOps) WakePhaseEmitted(phase, result string) prometheus.Counter {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.emittedCalls = append(o.emittedCalls, phase+":"+result)
	return o.backbone.WithLabelValues(phase, result)
}

func (o *stubOps) WakePhaseDuration(phase, result string) prometheus.Observer {
	// Mirror the emit pattern — the duration is recorded
	// through Observe, which we add in the test.
	o.mu.Lock()
	defer o.mu.Unlock()
	o.durationCalls = append(o.durationCalls, phase+":"+result)
	return &fakeObserver{parent: o}
}

func (o *stubOps) RecoveryEventEmitted(kind, result string) prometheus.Counter {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recoveryCalls = append(o.recoveryCalls, kind+":"+result)
	return o.recoveryBackbone.WithLabelValues(kind, result)
}

// fakeObserver captures the observe-sec value for the
// WakePhaseDuration unit test. The same value is reused by
// every Observe call so the platform's Observe path is
// exercised without a real Prometheus registry.
type fakeObserver struct {
	parent *stubOps
}

func (f *fakeObserver) Observe(secs float64) {
	f.parent.mu.Lock()
	defer f.parent.mu.Unlock()
	f.parent.durationSecs = append(f.parent.durationSecs, secs)
}

// fakeCounter is no longer needed — the stubOps returns a real
// prometheus.Counter backed by a private registry. The
// previous attempt to roll a fake Counter tripped on the
// proto-typed Metric in the Write signature, which is more
// trouble than it's worth for a unit-test stub.
