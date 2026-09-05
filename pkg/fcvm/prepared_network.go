package fcvm

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// These limits bound host network objects, not tenant VM capacity. A parked
// application still has no VM process, cgroup, or guest memory allocation.
const (
	maxPreparedNetworks    = api.MaxPreparedNetworkCacheSize
	preparedNetworkTTL     = api.PreparedNetworkCacheTTLSeconds * time.Second
	preparedNetworkTimeout = api.PreparedNetworkOperationTimeoutSeconds * time.Second
)

type preparedNetworkPolicy struct {
	egressMbit   int
	conntrackCap int64
	baseIP       netip.Addr
}

type preparedNetworkEntry struct {
	lease   Lease
	config  netns.Config
	policy  preparedNetworkPolicy
	created time.Time
	adopted bool
}

// preparedNetworkPool owns only networks which have never hosted a VM. An
// entry leaves the pool permanently on claim; normal VM teardown destroys it.
type preparedNetworkPool struct {
	m        *Manager
	capacity int
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	notify   chan struct{}
	mu       sync.Mutex
	desired  *preparedNetworkPolicy
	ready    []preparedNetworkEntry
	retired  []preparedNetworkEntry // failed teardown; retain the slot until removal succeeds
	observed time.Time
	closed   bool
	// Injected only by tests; production uses the native namespace binding.
	move    func(string, string) error
	removed func(netns.Config) bool
}

// EnablePreparedNetworks is an opt-in daemon wiring operation. It must run
// before serving Wake RPCs. Call ClosePreparedNetworks after draining RPCs.
func (m *Manager) EnablePreparedNetworks(ctx context.Context, capacity int) error {
	if capacity < 0 || capacity > maxPreparedNetworks {
		return fmt.Errorf("prepared networks: capacity must be between 0 and %d", maxPreparedNetworks)
	}
	if capacity == 0 {
		return nil
	}
	if m.preparedNetworks != nil {
		return fmt.Errorf("prepared networks: already enabled")
	}
	pinCtx, pinCancel := context.WithTimeout(ctx, preparedNetworkTimeout)
	pinErr := pinPreparedNetworkBridge(pinCtx, m.run)
	pinCancel()
	if pinErr != nil {
		return pinErr
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &preparedNetworkPool{m: m, capacity: capacity, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), notify: make(chan struct{}, 1), move: movePreparedNetns, removed: preparedNetworkRemoved}
	m.preparedNetworks = p
	go p.run() //nolint:contextcheck // The worker owns the daemon context; teardown must outlive its cancellation.
	return nil
}

func (m *Manager) ClosePreparedNetworks() error {
	if p := m.preparedNetworks; p != nil {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.cancel()
		<-p.done
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.retired) > 0 {
			return fmt.Errorf("prepared networks: %d networks could not be removed", len(p.retired))
		}
	}
	return nil
}

// Restrict the first implementation to the default per-app policy. Static IP,
// builders, per-app allowlists, and operator bundles use ordinary setup. The
// full resulting config is checked again after Wake validates its request.
func (m *Manager) preparedPolicy(req WakeRequest) (preparedNetworkPolicy, bool) {
	if !req.Plan.Valid() || req.ExportDir != "" || req.StaticEgressIP != "" ||
		len(req.EgressAllowlist) != 0 || len(m.mergeOperatorBundle(nil)) != 0 {
		return preparedNetworkPolicy{}, false
	}
	return preparedNetworkPolicy{req.EgressMbit, m.conntrackCap, hostIPForSlot(0)}, true
}

func (p *preparedNetworkPool) observe(policy preparedNetworkPolicy) {
	p.mu.Lock()
	if !p.closed {
		p.desired = &policy
		p.observed = time.Now()
	}
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *preparedNetworkPool) claim(instance string, policy preparedNetworkPolicy) *preparedNetworkEntry {
	p.mu.Lock()
	var entry *preparedNetworkEntry
	if !p.closed {
		for i := range p.ready {
			e := p.ready[i]
			if e.policy == policy && time.Since(e.created) < preparedNetworkTTL {
				entry = &e
				p.ready = append(p.ready[:i], p.ready[i+1:]...)
				break
			}
		}
	}
	p.mu.Unlock()
	if entry == nil {
		return nil
	}
	lease, err := p.m.alloc.adoptNetwork(entry.lease.Instance, instance)
	if err != nil {
		p.discard(*entry)
		return nil
	}
	oldNS := entry.config.Netns
	entry.lease, entry.adopted = lease, true
	if err := p.move(oldNS, lease.Netns); err != nil {
		// move rolls back the new binding on failure. No VMM has started;
		// destroy the old namespace before releasing the adopted slot.
		p.discard(*entry)
		p.m.log.Warn("prepared network claim failed; using ordinary setup", "instance", instance, "err", err)
		return nil
	}
	entry.config.Instance, entry.config.Netns = instance, lease.Netns
	return entry
}

func (p *preparedNetworkPool) run() {
	defer close(p.done)
	ticker := time.NewTicker(preparedNetworkTTL / 2)
	defer ticker.Stop()
	defer func() {
		p.mu.Lock()
		entries := append(p.ready, p.retired...)
		p.ready = nil
		p.retired = nil
		p.mu.Unlock()
		for _, e := range entries {
			p.discard(e)
		}
	}()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		case <-p.notify:
		}
		p.fill()
	}
}

func (p *preparedNetworkPool) fill() {
	for p.ctx.Err() == nil {
		p.mu.Lock()
		if time.Since(p.observed) >= preparedNetworkTTL {
			p.desired = nil
		}
		var expired []preparedNetworkEntry
		expired = append(expired, p.retired...)
		p.retired = nil
		var kept []preparedNetworkEntry
		for _, e := range p.ready {
			if time.Since(e.created) >= preparedNetworkTTL || p.desired == nil || e.policy != *p.desired {
				expired = append(expired, e)
			} else {
				kept = append(kept, e)
			}
		}
		p.ready = kept
		full := p.closed || p.desired == nil || len(kept) >= p.capacity
		var policy preparedNetworkPolicy
		if p.desired != nil {
			policy = *p.desired
		}
		p.mu.Unlock()
		for _, e := range expired {
			p.discard(e)
		}
		p.mu.Lock()
		full = full || len(p.ready)+len(p.retired) >= p.capacity
		p.mu.Unlock()
		if full {
			return
		}
		lease, err := p.m.alloc.reserveNetwork("prepared-" + uuid.NewString())
		if err != nil {
			return
		}
		nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
		nc.TapUID, nc.EgressMbit, nc.ConntrackCap = lease.UID, policy.egressMbit, policy.conntrackCap
		e := preparedNetworkEntry{lease: lease, config: nc, policy: policy}
		ctx, cancel := context.WithTimeout(p.ctx, preparedNetworkTimeout)
		err = p.m.setupNetwork(ctx, nc)
		cancel()
		if err != nil {
			p.discard(e)
			p.m.log.Warn("prepared network setup failed", "err", err)
			return // retry on the next wake or maintenance tick, never spin
		}
		e.created = time.Now()
		p.mu.Lock()
		keep := !p.closed && p.ctx.Err() == nil && p.desired != nil && *p.desired == policy
		if keep {
			p.ready = append(p.ready, e)
		}
		p.mu.Unlock()
		if !keep {
			p.discard(e)
		}
	}
}

func (p *preparedNetworkPool) teardown(nc netns.Config) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), preparedNetworkTimeout)
	defer cancel()
	for _, argv := range nc.TeardownCommands() {
		if err := p.m.run.Run(ctx, argv); err != nil {
			p.m.log.Debug("prepared network teardown", "netns", nc.Netns, "err", err)
		}
	}
	removeStaleNetnsMarker(nc.Netns)
	return p.removed(nc)
}

func (p *preparedNetworkPool) discard(e preparedNetworkEntry) {
	if !p.teardown(e.config) {
		p.mu.Lock()
		p.retired = append(p.retired, e)
		p.mu.Unlock()
		p.m.log.Error("prepared network survived teardown; retaining slot", "netns", e.config.Netns, "slot", e.lease.Slot)
		return
	}
	if e.adopted {
		_ = p.m.alloc.Release(e.lease.Instance)
	} else {
		p.m.alloc.releaseNetwork(e.lease.Instance)
	}
}

func (m *Manager) acquireWakeNetwork(req WakeRequest) (Lease, *preparedNetworkEntry, error) {
	if p := m.preparedNetworks; p != nil {
		if policy, ok := m.preparedPolicy(req); ok {
			if entry := p.claim(req.Instance, policy); entry != nil {
				return entry.lease, entry, nil
			}
		}
	}
	lease, err := m.alloc.Acquire(req.Instance)
	return lease, nil, err
}

func (m *Manager) setupWakeNetwork(ctx context.Context, nc netns.Config, prepared *preparedNetworkEntry) (bool, error) {
	if prepared != nil && reflect.DeepEqual(prepared.config, nc) {
		return true, nil
	}
	// A bundle reload may change the policy after claim. setupNetwork destroys
	// the unused network and installs the complete validated current policy.
	return false, m.setupNetwork(ctx, nc)
}
