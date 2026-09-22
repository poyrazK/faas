package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const vmConcurrencyRetryInterval = 10 * time.Millisecond

const concurrencyQueueLeaseGrace = 5 * time.Second

// ConcurrencyQueueAdmission owns the fleet-wide permit budget for requests
// waiting on warm instance capacity. Implementations coordinate only permits;
// request bodies and FIFO ordering remain local to the accepting gateway.
type ConcurrencyQueueAdmission interface {
	TryAcquireConcurrencyQueueLease(ctx context.Context, appID string, limit int, ttl time.Duration) (leaseID string, depth int, admitted bool, err error)
	ReleaseConcurrencyQueueLease(ctx context.Context, appID, leaseID string) error
}

// effectiveAppConcurrencyLimit resolves the app's configured instance
// ceiling against the plan ceiling. Keeping this calculation in one place
// makes capacity errors describe the same boundary the wake gate enforces.
func effectiveAppConcurrencyLimit(app App, planLimit int) int {
	if planLimit <= 0 || app.MaxConcurrency <= 0 || app.MaxConcurrency > planLimit {
		return planLimit
	}
	return app.MaxConcurrency
}

// effectiveVMConcurrencyLimit is the listener-level concurrency contract
// published as concurrency_per_vm. Generated function runners keep their
// smaller interpreter pool as an internal execution queue; it must not become
// a second, undocumented gateway limit. Doing so made a Scale function that
// advertised 80 requests per VM block its fifth request in the gateway and
// intermittently exhaust the request budget during ordinary 20-way bursts.
func effectiveVMConcurrencyLimit(_ App, planLimit int) int {
	return planLimit
}

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

	queueMu      sync.Mutex
	queues       map[string]*concurrencyWaitQueue
	onQueueDepth func(appID, plan string, depth int)
	admission    ConcurrencyQueueAdmission
}

type concurrencyWaitQueue struct {
	plan    string
	tickets []*concurrencyWaitTicket
}

type concurrencyWaitTicket struct {
	manager *vmConcurrencyManager
	appID   string
	ready   chan struct{}
	left    bool
	leaseID string
}

type vmConcurrencyGate struct {
	mu       sync.Mutex
	limit    int
	inflight int
	waiters  int
	refs     int
	notify   chan struct{}
}

func newVMConcurrencyManager(onDelta func(plan string, delta int64)) *vmConcurrencyManager {
	return &vmConcurrencyManager{
		gates:   make(map[string]*vmConcurrencyGate),
		queues:  make(map[string]*concurrencyWaitQueue),
		onDelta: onDelta,
	}
}

func (m *vmConcurrencyManager) setQueueDepthSink(sink func(appID, plan string, depth int)) {
	if m == nil {
		return
	}
	m.queueMu.Lock()
	m.onQueueDepth = sink
	m.queueMu.Unlock()
}

func (m *vmConcurrencyManager) setQueueAdmission(admission ConcurrencyQueueAdmission) {
	if m == nil {
		return
	}
	m.queueMu.Lock()
	m.admission = admission
	m.queueMu.Unlock()
}

// enterQueue appends one request to the per-app FIFO warm-capacity queue.
// The returned ticket becomes runnable only when it reaches the head. This
// avoids the notify-all race in which newer requests can repeatedly beat an
// older waiter to a released VM slot.
func (m *vmConcurrencyManager) enterQueue(ctx context.Context, appID, plan string, limit int, maxWait time.Duration) (*concurrencyWaitTicket, int, bool, error) {
	if m == nil || appID == "" || limit <= 0 {
		return nil, 0, false, nil
	}
	m.queueMu.Lock()
	admission := m.admission
	m.queueMu.Unlock()
	leaseID := ""
	globalDepth := 0
	if admission != nil {
		var err error
		var admitted bool
		leaseID, globalDepth, admitted, err = admission.TryAcquireConcurrencyQueueLease(ctx, appID, limit, maxWait+concurrencyQueueLeaseGrace)
		if err != nil {
			return nil, 0, false, err
		}
		if !admitted || leaseID == "" {
			return nil, globalDepth, false, nil
		}
	}
	m.queueMu.Lock()
	q := m.queues[appID]
	if q == nil {
		q = &concurrencyWaitQueue{plan: plan}
		m.queues[appID] = q
	}
	if len(q.tickets) >= limit {
		depth := len(q.tickets)
		m.queueMu.Unlock()
		if admission != nil && leaseID != "" {
			_ = releaseConcurrencyQueueLease(ctx, admission, appID, leaseID)
		}
		return nil, depth, false, nil
	}
	ticket := &concurrencyWaitTicket{manager: m, appID: appID, ready: make(chan struct{}), leaseID: leaseID}
	q.tickets = append(q.tickets, ticket)
	depth := len(q.tickets)
	if depth == 1 {
		close(ticket.ready)
	}
	sink := m.onQueueDepth
	m.queueMu.Unlock()
	if sink != nil {
		sink(appID, plan, depth)
	}
	if globalDepth > depth {
		depth = globalDepth
	}
	return ticket, depth, true, nil
}

func (t *concurrencyWaitTicket) wait(ctx context.Context) error {
	if t == nil {
		return ErrConcurrencyQueueFull
	}
	select {
	case <-t.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *concurrencyWaitTicket) leave(ctx context.Context) error {
	if t == nil || t.manager == nil {
		return nil
	}
	m := t.manager
	m.queueMu.Lock()
	if t.left {
		m.queueMu.Unlock()
		return nil
	}
	t.left = true
	admission := m.admission
	leaseID := t.leaseID
	q := m.queues[t.appID]
	if q == nil {
		m.queueMu.Unlock()
		return releaseConcurrencyQueueLease(ctx, admission, t.appID, leaseID)
	}
	idx := -1
	for i, candidate := range q.tickets {
		if candidate == t {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.queueMu.Unlock()
		return releaseConcurrencyQueueLease(ctx, admission, t.appID, leaseID)
	}
	wasHead := idx == 0
	copy(q.tickets[idx:], q.tickets[idx+1:])
	q.tickets[len(q.tickets)-1] = nil
	q.tickets = q.tickets[:len(q.tickets)-1]
	depth := len(q.tickets)
	plan := q.plan
	if depth == 0 {
		delete(m.queues, t.appID)
	} else if wasHead {
		close(q.tickets[0].ready)
	}
	sink := m.onQueueDepth
	m.queueMu.Unlock()
	if sink != nil {
		sink(t.appID, plan, depth)
	}
	return releaseConcurrencyQueueLease(ctx, admission, t.appID, leaseID)
}

func releaseConcurrencyQueueLease(ctx context.Context, admission ConcurrencyQueueAdmission, appID, leaseID string) error {
	if admission == nil || leaseID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return admission.ReleaseConcurrencyQueueLease(ctx, appID, leaseID)
}

func (m *vmConcurrencyManager) queueDepth(appID string) int {
	if m == nil {
		return 0
	}
	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if q := m.queues[appID]; q != nil {
		return len(q.tickets)
	}
	return 0
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
	} else {
		g.setLimit(limit)
	}
	// Hold a manager reference from pointer lookup through the gate
	// operation. Without this, removeIfIdle can delete the gate after
	// m.gate returns but before acquire/tryAcquire takes g.mu; a racing
	// caller can then create a second gate for the same instance and
	// temporarily exceed the per-instance limit.
	g.refs++
	return g
}

func (m *vmConcurrencyManager) releaseGateRef(instanceID string, g *vmConcurrencyGate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gates[instanceID] != g {
		return
	}
	g.mu.Lock()
	if g.refs > 0 {
		g.refs--
	}
	idle := g.refs == 0 && g.inflight == 0 && g.waiters == 0
	g.mu.Unlock()
	if idle {
		delete(m.gates, instanceID)
	}
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
	idle := g.refs == 0 && g.inflight == 0 && g.waiters == 0
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
	ok := g.tryAcquire()
	m.releaseGateRef(instanceID, g)
	if !ok {
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
	m.releaseGateRef(instanceID, g)
	if err != nil {
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
// instance is saturated. While every routable VM is full, it periodically
// picks again so a request queued behind the first restored VM can move to a
// sibling as soon as that sibling becomes ready.
func (h *Handler) acquireVMTarget(ctx context.Context, app App, pick PickResult, perVM int, deploymentID string) (PickResult, func(), bool, error) {
	if h == nil || h.backend == nil || h.vmConcurrency == nil || perVM <= 0 || !pick.OK || pick.Target.InstanceID == "" {
		return pick, func() {}, false, nil
	}
	if release, ok := h.vmConcurrency.tryAcquire(pick.Target.InstanceID, string(app.Plan), perVM); ok {
		return pick, release, false, nil
	}
	tryReadyTarget := func() (PickResult, func(), bool) {
		// HealthyCount is a bounded upper estimate of useful retries. The hard
		// cap avoids turning a saturated request into an unbounded picker loop
		// if a custom backend reports a bad count.
		attempts := h.backend.HealthyCount(app.ID)
		if attempts < 1 {
			attempts = 1
		}
		if attempts > 16 {
			attempts = 16
		}
		for i := 0; i < attempts; i++ {
			var candidate PickResult
			if deploymentID != "" {
				picker, ok := h.backend.(deploymentTargetPicker)
				if !ok {
					continue
				}
				candidate = picker.PickForDeployment(app.ID, deploymentID)
			} else {
				candidate = h.backend.Pick(app.ID)
			}
			if !candidate.OK || candidate.Target.InstanceID == "" {
				continue
			}
			if release, ok := h.vmConcurrency.tryAcquire(candidate.Target.InstanceID, string(app.Plan), perVM); ok {
				return candidate, release, true
			}
		}
		return PickResult{}, nil, false
	}
	if candidate, release, ok := tryReadyTarget(); ok {
		return candidate, release, false, nil
	}
	policy := ConcurrencyAdmissionPolicyForApp(app.Plan, app.ConcurrencyOverflow, app.MaxQueueWaitMS, app.MaxQueueDepth)
	if app.ConcurrencyOverflow == api.ConcurrencyOverflowDrop {
		return pick, nil, true, &WakeConcurrencyDropError{RetryAfter: policy.MaxWait}
	}
	ticket, depth, ok, queueErr := h.vmConcurrency.enterQueue(ctx, app.ID, string(app.Plan), policy.MaxWaiters, policy.MaxWait)
	if queueErr != nil {
		return pick, nil, true, &ConcurrencyQueueAdmissionError{Err: queueErr, RetryAfter: policy.MaxWait}
	}
	if !ok {
		return pick, nil, true, &ConcurrencyQueueFullError{Depth: depth, Limit: policy.MaxWaiters, RetryAfter: policy.MaxWait}
	}
	queuedAt := time.Now()
	defer func() {
		if err := ticket.leave(ctx); err != nil && h.log != nil {
			h.log.Warn("gateway: release fleet concurrency queue lease", "app_id", app.ID, "err", err)
		}
	}()
	if err := ticket.wait(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return pick, nil, true, &ConcurrencyQueueWaitTimeoutError{Waited: time.Since(queuedAt), RetryAfter: policy.MaxWait}
		}
		return pick, nil, true, err
	}
	if candidate, release, ok := tryReadyTarget(); ok {
		return candidate, release, true, nil
	}

	ticker := time.NewTicker(vmConcurrencyRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if candidate, release, ok := tryReadyTarget(); ok {
				return candidate, release, true, nil
			}
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return pick, nil, true, &ConcurrencyQueueWaitTimeoutError{Waited: time.Since(queuedAt), RetryAfter: policy.MaxWait}
			}
			return pick, nil, true, ctx.Err()
		}
	}
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
