package fcvm

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
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
	// ADR-361 per-plan new-connection limit, at the same per-plan
	// granularity as egressMbit.
	egressConnRate  int
	egressConnBurst int
	// ADR-361 decision 9 per-destination new-connection limit.
	egressDestConnRate  int
	egressDestConnBurst int
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
	closed   bool
	// Injected only by tests; production uses the native namespace binding.
	move    func(string, string) error
	removed func(netns.Config) bool
}

// EnablePreparedNetworks is an opt-in daemon wiring operation. It must run
// before serving Wake RPCs. Call ClosePreparedNetworks after draining RPCs.
func (m *Manager) EnablePreparedNetworks(ctx context.Context, capacity int) error {
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return fmt.Errorf("prepared networks: native ownership recovery: %w", err)
	}
	if capacity < 0 || capacity > maxPreparedNetworks {
		return fmt.Errorf("prepared networks: capacity must be between 0 and %d", maxPreparedNetworks)
	}
	if capacity == 0 {
		return nil
	}
	if m.nativeVMM() != nil {
		return fmt.Errorf("prepared networks: native recovery requires a journaled cache claim; set prepared_networks = 0")
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
// guest port is not part of the policy: a claimed namespace is retargeted to
// the request's port (setupWakeNetwork), so apps on 3000 or 8000 share the
// pool with apps on 8080. The full resulting config is checked again after
// Wake validates its request.
func (m *Manager) preparedPolicy(req WakeRequest) (preparedNetworkPolicy, bool) {
	if !req.Plan.Valid() || req.ExportDir != "" || req.StaticEgressIP != "" ||
		len(req.EgressAllowlist) != 0 || len(m.mergeOperatorBundle(nil)) != 0 ||
		req.Port < 0 || req.Port > 65535 {
		return preparedNetworkPolicy{}, false
	}
	var egress netns.Config
	applyTenantEgressPolicy(&egress, req.Plan, nil)
	return preparedNetworkPolicy{egressMbit: req.EgressMbit, conntrackCap: m.conntrackCap, baseIP: hostIPForSlot(0),
		egressConnRate: egress.EgressConnRate, egressConnBurst: egress.EgressConnBurst,
		egressDestConnRate: egress.EgressDestConnRate, egressDestConnBurst: egress.EgressDestConnBurst}, true
}

func (p *preparedNetworkPool) observe(policy preparedNetworkPolicy) {
	p.mu.Lock()
	if !p.closed {
		p.desired = &policy
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
	// Refresh ahead of hard expiry. A partially consumed pool can retain an
	// old spare while successful wakes replace only its younger entries.
	// Waiting until hard expiry leaves that spare unusable between ticks.
	ticker := time.NewTicker(preparedNetworkTTL / 4)
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
		var expired []preparedNetworkEntry
		expired = append(expired, p.retired...)
		p.retired = nil
		var kept []preparedNetworkEntry
		for _, e := range p.ready {
			if time.Since(e.created) >= preparedNetworkTTL/2 || p.desired == nil {
				expired = append(expired, e)
			} else {
				kept = append(kept, e)
			}
		}
		// ADR-460: preserve fresh spares for other exact policies. If the
		// latest target has no spare in a full pool, replace only the oldest
		// entry; mixed-policy traffic still shares the same global capacity.
		if !p.closed && p.desired != nil && len(kept) > 0 && len(kept) >= p.capacity {
			oldest := 0
			for i, e := range kept {
				if e.policy == *p.desired {
					oldest = -1
					break
				}
				if e.created.Before(kept[oldest].created) {
					oldest = i
				}
			}
			if oldest >= 0 {
				expired = append(expired, kept[oldest])
				kept = slices.Delete(kept, oldest, oldest+1)
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
		nc.EgressPorts = api.TenantEgressBasePorts()
		nc.EgressConnRate, nc.EgressConnBurst = policy.egressConnRate, policy.egressConnBurst
		nc.EgressDestConnRate, nc.EgressDestConnBurst = policy.egressDestConnRate, policy.egressDestConnBurst
		nc.DNSGated = !p.m.dnsGatingOff // every prepared namespace serves a tenant (ADR-373)
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
		// A newer target does not invalidate this fresh exact-policy spare.
		// Claims and the full-config check still enforce the requested policy.
		keep := !p.closed && p.ctx.Err() == nil && p.desired != nil
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
	if prepared != nil && preparedNetworkConfigMatches(prepared.config, nc) {
		return true, nil
	}
	if prepared != nil {
		if cmds, ok := preparedNetworkRetargetCommands(prepared.config, nc); ok {
			// No VMM has started and the namespace never carried traffic,
			// so only the DNAT target and/or the egress port set are
			// wrong. One nft transaction replaces them — milliseconds,
			// against 40-110 ms to rebuild the namespace.
			err := m.runNftCommands(ctx, nc.Netns, cmds)
			if err == nil {
				return true, nil
			}
			m.log.Warn("prepared network retarget failed; rebuilding", "instance", nc.Instance, "err", err)
		}
	}
	// A bundle reload may change the policy after claim. setupNetwork destroys
	// the unused network and installs the complete validated current policy.
	return false, m.setupNetwork(ctx, nc)
}

func preparedNetworkConfigMatches(prepared, requested netns.Config) bool {
	// NewConfig leaves GuestAppPort at zero, while a normal deployment sends
	// the explicit default, 8080. Both render the same DNAT rule. Comparing
	// their raw values discards an otherwise ready namespace on every wake.
	// Normalize only this documented alias, on copies: every identity and
	// policy field must still match, including future additions to Config.
	if prepared.GuestAppPort == 0 {
		prepared.GuestAppPort = netns.AppPort
	}
	if requested.GuestAppPort == 0 {
		requested.GuestAppPort = netns.AppPort
	}
	return reflect.DeepEqual(prepared, requested)
}

// preparedNetworkDiffersOnlyInPort reports whether requested is prepared
// with a different, valid guest port and every other field equal — the one
// difference RetargetAppPortCommands can repair in place. Invalid ports are
// never normalized into eligibility.
func preparedNetworkDiffersOnlyInPort(prepared, requested netns.Config) bool {
	if requested.GuestAppPort < 1 || requested.GuestAppPort > 65535 {
		return false
	}
	prepared.GuestAppPort = requested.GuestAppPort
	return reflect.DeepEqual(prepared, requested)
}

// applyTenantEgressPolicy sets the ADR-361 guest egress policy on a tenant
// network plan: the base TCP ports every plan may reach plus the app's
// declared extra ports, and the plan's new-connection rate limit. An
// unknown plan keeps a zero rate (no limit) but still gets the port policy;
// Wake rejects invalid plans before this.
func applyTenantEgressPolicy(nc *netns.Config, plan api.Plan, extra []uint16) {
	nc.EgressPorts = tenantEgressPorts(plan, extra)
	nc.DNSGated = true
	if lim, ok := api.LimitsFor(plan); ok {
		nc.EgressConnRate, nc.EgressConnBurst = lim.EgressNewConnPerSecond, lim.EgressNewConnBurst
		nc.EgressDestConnRate, nc.EgressDestConnBurst = lim.EgressNewConnPerDestPerSecond, lim.EgressNewConnPerDestBurst
	}
}

// tenantEgressPorts is the base web ports plus the app's extra ports, with
// port 0, duplicates and forbidden ports (SMTP, remote admin, mining, DNS,
// ...) dropped and the extras capped at the plan's allowance. apid refuses
// those already; vmmd re-checks because it is the component that enforces
// the policy, and because a plan downgrade leaves the stored list in place.
func tenantEgressPorts(plan api.Plan, extra []uint16) []uint16 {
	ports := api.TenantEgressBasePorts()
	base, allowance := len(ports), plan.EgressExtraPortsMax()
	for _, p := range extra {
		if len(ports)-base >= allowance {
			break
		}
		if p == 0 || slices.Contains(ports, p) {
			continue
		}
		if _, forbidden := api.TenantEgressForbiddenPort(int(p)); forbidden {
			continue
		}
		ports = append(ports, p)
	}
	return ports
}

// preparedNetworkRetargetCommands returns the nft commands that turn an
// unused prepared namespace into the requested one when they differ only in
// the guest app port (ADR-149) and/or the egress port set (ADR-361). ok is
// false when anything else differs or nothing needs to change.
func preparedNetworkRetargetCommands(prepared, requested netns.Config) ([][]string, bool) {
	aligned := withEgressPorts(prepared, requested.EgressPorts)
	var cmds [][]string
	switch {
	case preparedNetworkConfigMatches(aligned, requested):
		// The app port is already equivalent; only the port set may differ.
	case preparedNetworkDiffersOnlyInPort(aligned, requested):
		cmds = append(cmds, requested.RetargetAppPortCommands()...)
	default:
		return nil, false
	}
	if !slices.Equal(prepared.EgressPorts, requested.EgressPorts) {
		cmds = append(cmds, requested.EgressPortsUpdateCommands()...)
	}
	return cmds, len(cmds) > 0
}

func withEgressPorts(c netns.Config, ports []uint16) netns.Config {
	c.EgressPorts = ports
	return c
}
