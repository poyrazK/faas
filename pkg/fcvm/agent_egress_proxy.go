package fcvm

// Registration of agent VMs with the agent egress gateway's politeness proxy
// (ADR-20261117185346 §4). The proxy refuses connections from addresses it
// holds no live registration for, so vmmd registers each agent VM before it
// boots, renews the registration while the VM lives, and removes it when the
// VM is gone.

import (
	"context"
	"net/netip"
	"slices"
	"time"
)

// AgentEgressProxy is the proxy's registration API (agentproxy.Client).
type AgentEgressProxy interface {
	Register(ctx context.Context, addr netip.Addr, accountID, sandboxID string) error
	Unregister(ctx context.Context, addr netip.Addr) error
}

// agentEgressProxyTimeout bounds one registration call. A boot waits for
// it, so it is short; the proxy is one tunnel hop away.
const agentEgressProxyTimeout = 5 * time.Second

// agentEgressRegistration is what the proxy holds for one address.
type agentEgressRegistration struct {
	instance  string
	accountID string
	sandboxID string
}

// registerAgentEgress registers addr for a booting VM. Without a proxy it
// does nothing.
func (m *Manager) registerAgentEgress(ctx context.Context, addr netip.Addr, instance, accountID, sandboxID string) error {
	s := &m.agentEgress
	if s.cfg == nil || s.cfg.Proxy == nil {
		return nil
	}
	s.regMu.Lock()
	defer s.regMu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, agentEgressProxyTimeout)
	err := s.cfg.Proxy.Register(callCtx, addr, accountID, sandboxID)
	cancel()
	if err != nil {
		return err
	}
	m.mu.Lock()
	if s.addrs[addr] == instance {
		s.registered[addr] = agentEgressRegistration{instance: instance, accountID: accountID, sandboxID: sandboxID}
	}
	m.mu.Unlock()
	return nil
}

// unregisterAgentEgress removes a released address's registration, then
// frees the address. A failed removal still frees it: the registration
// expires within the proxy's TTL, and the next VM's registration replaces
// it before then.
func (m *Manager) unregisterAgentEgress(addr netip.Addr) {
	s := &m.agentEgress
	s.regMu.Lock()
	defer s.regMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), agentEgressProxyTimeout)
	err := s.cfg.Proxy.Unregister(ctx, addr)
	cancel()
	if err != nil && m.log != nil {
		m.log.Warn("fcvm: agent egress proxy kept a released address until it expires", "addr", addr.String(), "err", err)
	}
	m.mu.Lock()
	if owner, held := s.addrs[addr]; held && owner == "" {
		delete(s.addrs, addr)
	}
	m.mu.Unlock()
}

// RunAgentEgressRenewal renews every live registration until ctx ends. A
// registration that cannot be renewed expires, and the proxy then refuses
// that VM's connections: the path fails closed.
func (m *Manager) RunAgentEgressRenewal(ctx context.Context) {
	s := &m.agentEgress
	if s.cfg == nil || s.cfg.Proxy == nil {
		return
	}
	t := time.NewTicker(s.cfg.ProxyRenewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.renewAgentEgress(ctx)
		}
	}
}

// renewAgentEgress renews each registration that is still current when its
// turn comes.
func (m *Manager) renewAgentEgress(ctx context.Context) {
	s := &m.agentEgress
	m.mu.Lock()
	addrs := make([]netip.Addr, 0, len(s.registered))
	for addr := range s.registered {
		addrs = append(addrs, addr)
	}
	m.mu.Unlock()
	slices.SortFunc(addrs, netip.Addr.Compare)
	for _, addr := range addrs {
		if ctx.Err() != nil {
			return
		}
		m.renewOneAgentEgress(ctx, addr)
	}
}

func (m *Manager) renewOneAgentEgress(ctx context.Context, addr netip.Addr) {
	s := &m.agentEgress
	s.regMu.Lock()
	defer s.regMu.Unlock()
	m.mu.Lock()
	reg, ok := s.registered[addr]
	m.mu.Unlock()
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, agentEgressProxyTimeout)
	err := s.cfg.Proxy.Register(callCtx, addr, reg.accountID, reg.sandboxID)
	cancel()
	if err != nil && m.log != nil {
		m.log.Warn("fcvm: agent egress registration renewal failed", "addr", addr.String(),
			"instance", reg.instance, "err", err)
	}
}
