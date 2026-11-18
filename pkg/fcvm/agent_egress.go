package fcvm

// The agent egress gateway (ADR-20261117185346): every sandbox session VM
// with egress leaves through the agent tunnel (wg-agent), on its own address
// in this node's slice, and never through the tenant gateway or the node's
// own address. vmmd marks the VM's bridged traffic, routes the mark to the
// agent table, and drops marked traffic that table cannot route.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// AgentEgressConfig is this node's side of the agent tunnel.
type AgentEgressConfig struct {
	// Iface is the agent tunnel's interface, wg-agent.
	Iface string
	// Slice is this node's slice of the agent tunnel. Its first host
	// address is the node's; every other host address is an agent VM's.
	Slice netip.Prefix
	// Table is the policy-routing table whose default route is Iface.
	Table int
	// HandshakeMaxAge is how old the tunnel's latest handshake may be when
	// an agent VM boots (api.AgentEgressHandshakeMaxAgeSec).
	HandshakeMaxAge time.Duration
	// Proxy is the gateway's politeness proxy. When set, every agent VM is
	// registered with it before boot, and a VM it cannot register does not
	// boot (ADR-20261117185346 §3–§4). Nil while the gateway forwards agent
	// traffic directly.
	Proxy AgentEgressProxy
	// ProxyRenewEvery is how often live registrations are renewed; it must
	// be well under the proxy's registration TTL.
	ProxyRenewEvery time.Duration
}

// The agent rules sit ahead of the static-egress exception (100) and the
// tenant tunnel's source rules (10000+).
const (
	agentEgressLookupPriority      = "95"
	agentEgressUnreachablePriority = "96"
)

// errAgentEgressUnavailable refuses a boot while the agent tunnel cannot
// carry the VM; the caller retries (ADR-20261117185346 §3).
var errAgentEgressUnavailable = errors.New("agent egress gateway unavailable")

// agentEgressState is the Manager's agent tunnel: its config and the
// addresses its VMs hold. addrs and starting are guarded by Manager.mu.
type agentEgressState struct {
	cfg *AgentEgressConfig
	// addrs maps each held agent address to its instance.
	addrs map[netip.Addr]string
	// starting holds the rules of agent VMs booting, before they are live.
	starting map[string]netns.AgentEgressRule
	// registered are the addresses the proxy holds a registration for.
	registered map[netip.Addr]agentEgressRegistration
	// regMu orders every registration call, so a late renewal or removal
	// never lands after the address has moved to another VM. It is taken
	// before Manager.mu, never inside it.
	regMu sync.Mutex
	// unregister forgets a released address at the proxy, then frees it.
	unregister func(netip.Addr)
	// now is the clock the handshake age is read against; nil is time.Now.
	now func() time.Time
}

func (s *agentEgressState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// SetAgentEgress turns the agent tunnel on for this Manager. Without it,
// sandbox sessions keep the tenant path.
func (m *Manager) SetAgentEgress(cfg AgentEgressConfig) error {
	switch {
	case strings.TrimSpace(cfg.Iface) == "":
		return errors.New("fcvm: agent egress needs its interface")
	case !cfg.Slice.IsValid() || !cfg.Slice.Addr().Is4() || cfg.Slice.Bits() > 28 || cfg.Slice.Masked() != cfg.Slice:
		return fmt.Errorf("fcvm: agent egress slice %s must be an IPv4 network of at most /28", cfg.Slice)
	case cfg.Table <= 0:
		return errors.New("fcvm: agent egress needs its routing table")
	case cfg.HandshakeMaxAge <= 0:
		return errors.New("fcvm: agent egress needs a handshake age bound")
	case cfg.Proxy != nil && cfg.ProxyRenewEvery <= 0:
		return errors.New("fcvm: agent egress proxy needs a renewal interval")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &m.agentEgress
	s.cfg = &cfg
	s.addrs = map[netip.Addr]string{}
	s.starting = map[string]netns.AgentEgressRule{}
	s.registered = map[netip.Addr]agentEgressRegistration{}
	s.unregister = m.unregisterAgentEgress
	return nil
}

// agentEgressOn reports whether sandbox sessions leave through the agent
// tunnel.
func (m *Manager) agentEgressOn() bool { return m.agentEgress.cfg != nil }

// agentEgressVM is whether a wake's VM leaves through the agent tunnel: a
// sandbox session with egress. A session without network has none, and an
// agent run reaches its model over vsock only.
func agentEgressVM(req WakeRequest) bool {
	return req.sandboxID != "" && (req.sandboxNetwork == api.SandboxNetworkRegistries || req.sandboxNetwork == api.SandboxNetworkWeb)
}

// prepareAgentEgressWake gives a booting agent VM its address and installs
// its rules before the boot. The returned func undoes it when the wake fails.
func (m *Manager) prepareAgentEgressWake(ctx context.Context, req WakeRequest, lease Lease) (func(), error) {
	if !agentEgressVM(req) || !m.agentEgressOn() {
		return func() {}, nil
	}
	cfg := m.agentEgress.cfg
	if m.hostRenderer == nil {
		return nil, errors.New("agent egress requires the host policy renderer")
	}
	if !lease.HostIP.Is4() {
		return nil, errors.New("agent egress requires an IPv4 lease")
	}
	policy := netns.ActiveHostPolicyForRender()
	if policy == nil || policy.BridgeName == "" {
		return nil, errors.New("agent egress requires the host egress policy")
	}
	if err := m.agentEgressHandshakeFresh(ctx, cfg); err != nil {
		return nil, fmt.Errorf("%w: %w", errAgentEgressUnavailable, err)
	}
	if err := m.ensureAgentEgressRouting(ctx, policy.BridgeName, cfg.Table); err != nil {
		return nil, err
	}
	m.mu.Lock()
	addr, ok := m.agentEgress.allocateLocked(req.Instance)
	if ok {
		m.agentEgress.starting[req.Instance] = netns.AgentEgressRule{PerVMHostIP: lease.HostIP, AgentIP: addr,
			AccountID: req.AccountID, SandboxID: req.sandboxID}
	}
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: slice %s is full", errAgentEgressUnavailable, cfg.Slice)
	}
	undo := func() {
		m.mu.Lock()
		delete(m.agentEgress.starting, req.Instance)
		m.agentEgress.releaseLocked(req.Instance)
		m.mu.Unlock()
		_ = m.renderHostSMTPAllowlistRules(context.WithoutCancel(ctx), false)
	}
	if err := m.registerAgentEgress(ctx, addr, req.Instance, req.AccountID, req.sandboxID); err != nil {
		undo()
		return nil, fmt.Errorf("%w: %w", errAgentEgressUnavailable, err)
	}
	if err := m.renderHostSMTPAllowlistRules(ctx, false); err != nil {
		undo()
		return nil, fmt.Errorf("install agent egress before boot: %w", err)
	}
	return undo, nil
}

// agentEgressStartedLocked moves a booted VM's rule from starting onto the
// instance.
func (m *Manager) agentEgressStartedLocked(inst *Instance) {
	if m.agentEgress.cfg == nil {
		return
	}
	if r, ok := m.agentEgress.starting[inst.Lease.Instance]; ok {
		inst.agentEgressIP = r.AgentIP
		delete(m.agentEgress.starting, inst.Lease.Instance)
	}
}

// agentEgressRulesLocked are the rules of every live and booting agent VM,
// sorted by host address.
func (m *Manager) agentEgressRulesLocked() []netns.AgentEgressRule {
	if m.agentEgress.cfg == nil {
		return nil
	}
	var rules []netns.AgentEgressRule
	for _, inst := range m.live {
		if inst.agentEgressIP.IsValid() && inst.Lease.HostIP.Is4() {
			rules = append(rules, netns.AgentEgressRule{PerVMHostIP: inst.Lease.HostIP, AgentIP: inst.agentEgressIP,
				AccountID: inst.AccountID, SandboxID: inst.SandboxID})
		}
	}
	for id, r := range m.agentEgress.starting {
		if m.live[id] == nil {
			rules = append(rules, r)
		}
	}
	slices.SortFunc(rules, func(a, b netns.AgentEgressRule) int { return a.PerVMHostIP.Compare(b.PerVMHostIP) })
	return rules
}

// allocateLocked gives instance the lowest free address of the slice: not
// the network address, the node's own (the first host) or the broadcast.
func (s *agentEgressState) allocateLocked(instance string) (netip.Addr, bool) {
	for addr, owner := range s.addrs {
		if owner == instance {
			return addr, true
		}
	}
	slice := s.cfg.Slice
	last := lastAddr(slice)
	for a := slice.Addr().Next().Next(); a.IsValid() && a.Less(last); a = a.Next() {
		if _, held := s.addrs[a]; !held {
			s.addrs[a] = instance
			return a, true
		}
	}
	return netip.Addr{}, false
}

// releaseLocked frees instance's address. With a proxy, the address stays
// held, ownerless, until the proxy has forgotten it, so it never reaches the
// next VM while the old registration stands.
func (s *agentEgressState) releaseLocked(instance string) {
	for addr, owner := range s.addrs {
		if owner != instance {
			continue
		}
		delete(s.registered, addr)
		if s.cfg == nil || s.cfg.Proxy == nil || s.unregister == nil {
			delete(s.addrs, addr)
			continue
		}
		s.addrs[addr] = ""
		go s.unregister(addr)
	}
}

// lastAddr is a v4 prefix's broadcast address.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= (uint32(1) << (32 - p.Bits())) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// agentEgressHandshakeFresh checks the tunnel's latest handshake is younger
// than the bound: a tunnel that has not shaken hands cannot carry the VM.
func (m *Manager) agentEgressHandshakeFresh(ctx context.Context, cfg *AgentEgressConfig) error {
	if m.captureRunner == nil {
		return errors.New("agent egress requires a tunnel probe")
	}
	raw, err := m.captureRunner.RunCapture(ctx, []string{"wg", "show", cfg.Iface, "latest-handshakes"})
	if err != nil {
		return fmt.Errorf("read %s handshakes: %w", cfg.Iface, err)
	}
	latest, err := latestHandshake(string(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.Iface, err)
	}
	if age := m.agentEgress.clock().Sub(latest); age > cfg.HandshakeMaxAge {
		return fmt.Errorf("%s last shook hands %s ago", cfg.Iface, age.Round(time.Second))
	}
	return nil
}

// latestHandshake is the newest handshake in `wg show <if>
// latest-handshakes` output: one "<public key>\t<unix seconds>" per peer.
func latestHandshake(out string) (time.Time, error) {
	var newest int64
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err == nil && sec > newest {
			newest = sec
		}
	}
	if newest == 0 {
		return time.Time{}, errors.New("no handshake yet")
	}
	return time.Unix(newest, 0), nil
}

// ensureAgentEgressRouting installs the two policy rules for marked traffic:
// look up the agent table, and if it has no route, drop rather than fall
// through to the tenant tunnel or the main table.
func (m *Manager) ensureAgentEgressRouting(ctx context.Context, bridge string, table int) error {
	if m.captureRunner == nil {
		return errors.New("agent egress routing requires a host rule probe")
	}
	raw, err := m.captureRunner.RunCapture(ctx, []string{"ip", "-4", "rule", "show"})
	if err != nil {
		return fmt.Errorf("inspect agent egress routing: %w", err)
	}
	mark := fmt.Sprintf("%#x/%#x", netns.AgentEgressRoutingMark, netns.AgentEgressRoutingMark)
	have := map[string]bool{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		values := map[string]string{}
		for i := 1; i+1 < len(fields); i += 2 {
			values[fields[i]] = fields[i+1]
		}
		if values["iif"] != bridge || values["fwmark"] != mark {
			continue
		}
		switch {
		case fields[0] == agentEgressLookupPriority+":" && values["lookup"] == strconv.Itoa(table):
			have["lookup"] = true
		case fields[0] == agentEgressUnreachablePriority+":" && slices.Contains(fields, "unreachable"):
			have["unreachable"] = true
		}
	}
	if !have["lookup"] {
		if err := m.run.Run(ctx, []string{"ip", "-4", "rule", "add", "priority", agentEgressLookupPriority,
			"iif", bridge, "fwmark", mark, "lookup", strconv.Itoa(table)}); err != nil {
			return fmt.Errorf("install agent egress routing: %w", err)
		}
	}
	if !have["unreachable"] {
		if err := m.run.Run(ctx, []string{"ip", "-4", "rule", "add", "priority", agentEgressUnreachablePriority,
			"iif", bridge, "fwmark", mark, "unreachable"}); err != nil {
			return fmt.Errorf("install agent egress fail-closed rule: %w", err)
		}
	}
	return nil
}
