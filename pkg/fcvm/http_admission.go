package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrHTTPForwardCapacity  = errors.New("instance forwarding capacity exhausted")
	ErrHTTPForwardNotLive   = errors.New("instance is not available for HTTP forwarding")
	ErrHTTPForwardUntrusted = errors.New("instance has no trusted HTTP forwarding cap")
)

type HTTPForwardCapacityError struct{ Limit, Observed int }

func (e *HTTPForwardCapacityError) Error() string { return ErrHTTPForwardCapacity.Error() }
func (e *HTTPForwardCapacityError) Unwrap() error { return ErrHTTPForwardCapacity }

type httpForwardGeneration struct {
	instance *Instance
	lease    Lease
	cap      int
	retired  bool
	next     uint64
	active   map[uint64]context.CancelFunc
	drained  chan struct{}
}

// AcquireHTTPForward reserves the plan's advertised cap before a bridge can
// reach the guest. The returned release must follow actual bridge cleanup;
// cancellation alone never frees a permit. Empty/unknown plans fail closed.
func (m *Manager) AcquireHTTPForward(ctx context.Context, instance string) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil || inst.Paused || inst.ExecutionOnly || inst.AppTaskOnly || inst.IsJob || inst.Lease.IsBuilder {
		return nil, nil, ErrHTTPForwardNotLive
	}
	cap := inst.Plan.ConcurrencyPerVMBound()
	if cap <= 0 {
		return nil, nil, ErrHTTPForwardUntrusted
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if m.httpForwards == nil {
		m.httpForwards = make(map[string]*httpForwardGeneration)
	}
	g := m.httpForwards[instance]
	if g != nil && (g.retired || g.instance != inst) {
		return nil, nil, ErrHTTPForwardNotLive
	}
	if g == nil {
		ensureHTTPForwardGeneration(inst)
		g = &httpForwardGeneration{instance: inst, lease: inst.Lease, cap: cap, active: make(map[uint64]context.CancelFunc), drained: make(chan struct{})}
		m.httpForwards[instance] = g
	}
	if len(g.active) >= g.cap {
		return nil, nil, &HTTPForwardCapacityError{Limit: g.cap, Observed: len(g.active) + 1}
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	g.next++
	id := g.next
	g.active[id] = cancel
	return forwardCtx, func() { m.releaseHTTPForward(g, id) }, nil
}

func (m *Manager) reopenHTTPForwards(inst *Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live[inst.Lease.Instance] != inst {
		return
	}
	inst.Paused = false
	g := m.httpForwards[inst.Lease.Instance]
	if g != nil && g.retired && g.lease == inst.Lease {
		if len(g.active) == 0 {
			delete(m.httpForwards, inst.Lease.Instance)
			inst.httpForwardGeneration = uuid.NewString()
		} else {
			// A failed migration drain can resume admission on the same VM.
			// Pending cancelled bridges still count until their real release.
			g.retired = false
		}
	}
}

// HTTPAdmissionStatus reports the actual cap of this live VM generation.
// Task/builder/disposable instances do not expose an ordinary HTTP listener.
type HTTPAdmissionStatus struct {
	Enabled         bool
	Limit, Inflight int
	Generation      string
	Retiring        bool
	Plan            api.Plan
}

func (m *Manager) HTTPAdmissionStatus(instance string) (HTTPAdmissionStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil {
		return HTTPAdmissionStatus{}, false
	}
	ensureHTTPForwardGeneration(inst)
	cap := inst.Plan.ConcurrencyPerVMBound()
	state := HTTPAdmissionStatus{Enabled: cap > 0 && !inst.ExecutionOnly && !inst.AppTaskOnly && !inst.IsJob && !inst.Lease.IsBuilder,
		Limit: cap, Generation: inst.httpForwardGeneration, Plan: inst.Plan, Retiring: inst.Paused}
	if g := m.httpForwards[instance]; g != nil && g.lease == inst.Lease {
		state.Inflight = len(g.active)
		state.Retiring = state.Retiring || g.retired
	}
	return state, true
}

// Caller holds m.mu; the generation belongs to the instance pointer, so a
// delayed release or cleanup can never decrement a replacement's count.
func ensureHTTPForwardGeneration(inst *Instance) {
	if inst.httpForwardGeneration == "" {
		inst.httpForwardGeneration = uuid.NewString()
	}
}

func (m *Manager) releaseHTTPForward(g *httpForwardGeneration, id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := g.active[id]; ok {
		cancel()
		delete(g.active, id)
		if g.retired && len(g.active) == 0 {
			close(g.drained)
		}
	}
}

func (m *Manager) retireHTTPForwards(lease Lease) *httpForwardGeneration {
	m.mu.Lock()
	if m.httpForwards == nil {
		m.httpForwards = make(map[string]*httpForwardGeneration)
	}
	g := m.httpForwards[lease.Instance]
	if g == nil {
		inst := m.live[lease.Instance]
		if inst == nil || inst.Lease != lease {
			m.mu.Unlock()
			return nil // failed boots/unknown cleanup have no forwarding owner
		}
		g = &httpForwardGeneration{instance: inst, lease: lease, active: make(map[uint64]context.CancelFunc), drained: make(chan struct{})}
		m.httpForwards[lease.Instance] = g
	}
	if g.lease != lease {
		m.mu.Unlock()
		return nil // cleanup for an older lease must not cancel its successor
	}
	var cancels []context.CancelFunc
	if !g.retired {
		g.retired = true
		for _, cancel := range g.active {
			cancels = append(cancels, cancel)
		}
		if len(g.active) == 0 {
			close(g.drained)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return g
}

func (m *Manager) waitHTTPForwards(ctx context.Context, g *httpForwardGeneration) error {
	if g == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for bridge cleanup: %w", ctx.Err())
	case <-g.drained:
		return nil
	}
}

func (m *Manager) forgetHTTPForwards(g *httpForwardGeneration) {
	if g == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.httpForwards[g.lease.Instance] == g && g.retired && len(g.active) == 0 {
		delete(m.httpForwards, g.lease.Instance)
	}
}
