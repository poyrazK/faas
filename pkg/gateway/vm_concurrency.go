package gateway

import (
	"context"
	"sync"
)

// vmConcurrencyManager owns the request slots for routable instances. The
// gateway is the first component that knows which instance a request is
// about to use, so enforcing the plan bound here keeps the limit independent
// of the guest runtime and applies equally to HTTP, streaming, and Upgrade
// requests.
//
// Entries are removed after the last request and waiter leave. Instance IDs
// are unique for the lifetime of an instance, so this keeps the map bounded
// across repeated park/wake cycles without racing a waiter that is about to
// acquire a slot.
type vmConcurrencyManager struct {
	mu      sync.Mutex
	gates   map[string]*vmConcurrencyGate
	onDelta func(plan string, delta int64)
}

type vmConcurrencyGate struct {
	mu       sync.Mutex
	limit    int
	inflight int
	waiters  int
	notify   chan struct{}
}

func newVMConcurrencyManager(onDelta func(plan string, delta int64)) *vmConcurrencyManager {
	return &vmConcurrencyManager{
		gates:   make(map[string]*vmConcurrencyGate),
		onDelta: onDelta,
	}
}

func newVMConcurrencyGate(limit int) *vmConcurrencyGate {
	return &vmConcurrencyGate{limit: limit, notify: make(chan struct{})}
}

func (m *vmConcurrencyManager) gate(instanceID string, limit int) *vmConcurrencyGate {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.gates[instanceID]
	if g == nil {
		g = newVMConcurrencyGate(limit)
		m.gates[instanceID] = g
		return g
	}
	g.setLimit(limit)
	return g
}

func (g *vmConcurrencyGate) setLimit(limit int) {
	g.mu.Lock()
	if g.limit != limit {
		g.limit = limit
		g.signalLocked()
	}
	g.mu.Unlock()
}

func (g *vmConcurrencyGate) signalLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}

func (g *vmConcurrencyGate) tryAcquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inflight >= g.limit {
		return false
	}
	g.inflight++
	return true
}

// acquire waits for a slot until ctx is cancelled. waited reports whether the
// request observed a saturated instance, which is used for the bounded audit
// event and the operator metric.
func (g *vmConcurrencyGate) acquire(ctx context.Context) (waited bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return waited, err
		}
		g.mu.Lock()
		if g.inflight < g.limit {
			g.inflight++
			g.mu.Unlock()
			return waited, nil
		}
		waitCh := g.notify
		g.waiters++
		g.mu.Unlock()
		select {
		case <-waitCh:
			g.mu.Lock()
			g.waiters--
			g.mu.Unlock()
		case <-ctx.Done():
			g.mu.Lock()
			g.waiters--
			g.mu.Unlock()
			return true, ctx.Err()
		}
		waited = true
	}
}

func (g *vmConcurrencyGate) release() {
	g.mu.Lock()
	if g.inflight > 0 {
		g.inflight--
		g.signalLocked()
	}
	g.mu.Unlock()
}

func (m *vmConcurrencyManager) removeIfIdle(instanceID string, g *vmConcurrencyGate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gates[instanceID] != g {
		return
	}
	g.mu.Lock()
	idle := g.inflight == 0 && g.waiters == 0
	g.mu.Unlock()
	if idle {
		delete(m.gates, instanceID)
	}
}

func (m *vmConcurrencyManager) record(plan string, delta int64) {
	if m != nil && m.onDelta != nil && plan != "" && delta != 0 {
		m.onDelta(plan, delta)
	}
}

func (m *vmConcurrencyManager) tryAcquire(instanceID, plan string, limit int) (func(), bool) {
	if m == nil || instanceID == "" || limit <= 0 {
		return nil, false
	}
	g := m.gate(instanceID, limit)
	if !g.tryAcquire() {
		return nil, false
	}
	m.record(plan, 1)
	var once sync.Once
	return func() {
		once.Do(func() {
			g.release()
			m.record(plan, -1)
			m.removeIfIdle(instanceID, g)
		})
	}, true
}

func (m *vmConcurrencyManager) acquire(ctx context.Context, instanceID, plan string, limit int) (func(), bool, error) {
	if m == nil || instanceID == "" || limit <= 0 {
		return func() {}, false, nil
	}
	g := m.gate(instanceID, limit)
	waited, err := g.acquire(ctx)
	if err != nil {
		m.removeIfIdle(instanceID, g)
		return nil, waited, err
	}
	m.record(plan, 1)
	var once sync.Once
	return func() {
		once.Do(func() {
			g.release()
			m.record(plan, -1)
			m.removeIfIdle(instanceID, g)
		})
	}, waited, nil
}

// acquireVMTarget first probes a few other picker entries when the selected
// instance is saturated. This preserves round-robin distribution during a
// burst while retaining a cancellable wait when every routable VM is full.
func (h *Handler) acquireVMTarget(ctx context.Context, app App, pick PickResult, perVM int) (PickResult, func(), bool, error) {
	if h == nil || h.backend == nil || h.vmConcurrency == nil || perVM <= 0 || !pick.OK || pick.Target.InstanceID == "" {
		return pick, func() {}, false, nil
	}
	if release, ok := h.vmConcurrency.tryAcquire(pick.Target.InstanceID, string(app.Plan), perVM); ok {
		return pick, release, false, nil
	}
	// HealthyCount is a bounded upper estimate of useful retries. The hard
	// cap avoids turning a saturated request into an unbounded picker loop if
	// a custom backend reports a bad count.
	attempts := h.backend.HealthyCount(app.ID)
	if attempts > 16 {
		attempts = 16
	}
	for i := 0; i < attempts; i++ {
		candidate := h.backend.Pick(app.ID)
		if !candidate.OK || candidate.Target.InstanceID == "" || candidate.Target.InstanceID == pick.Target.InstanceID {
			continue
		}
		if release, ok := h.vmConcurrency.tryAcquire(candidate.Target.InstanceID, string(app.Plan), perVM); ok {
			return candidate, release, false, nil
		}
	}
	release, waited, err := h.vmConcurrency.acquire(ctx, pick.Target.InstanceID, string(app.Plan), perVM)
	return pick, release, waited, err
}

func (h *Handler) emitVMConcurrencyThreshold(ctx context.Context, app App, target Target, limit int) {
	if h == nil || h.vmConcurrencyAudit == nil || app.ID == "" || target.InstanceID == "" {
		return
	}
	var subject *string
	if app.AccountID != "" {
		accountID := app.AccountID
		subject = &accountID
	}
	h.vmConcurrencyAudit.Emit(ctx, "vm.inflight_threshold_reached", subject, map[string]any{
		"app_id":      app.ID,
		"instance_id": target.InstanceID,
		"plan":        string(app.Plan),
		"limit":       limit,
		"reason":      "per_vm_concurrency",
	})
}
