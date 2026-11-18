package fcvm

// adr: 20261117185346 §2–§3 — a sandbox session with egress gets its own
// agent tunnel address and rules before it boots, and fails closed: a stale
// tunnel, a full slice or a failed render refuses the boot and leaves
// nothing behind.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// agentProbe answers `wg show` and `ip rule show` by command.
type agentProbe struct {
	mu         sync.Mutex
	handshakes string
	rules      string
	err        error
}

func (p *agentProbe) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	if argv[0] == "wg" {
		return []byte(p.handshakes), nil
	}
	return []byte(p.rules), nil
}

func agentEgressTestManager(t *testing.T, slice string) (*Manager, *fakeRunner, *agentProbe, *fakeHostRenderer, time.Time) {
	t.Helper()
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	runner := &fakeRunner{}
	now := time.Unix(1_800_000_000, 0)
	probe := &agentProbe{handshakes: fmt.Sprintf("gwkey=\t%d\n", now.Add(-30*time.Second).Unix())}
	m := newTestManager(runner, &fakeVMM{}).WithCaptureRunner(probe)
	renderer := &fakeHostRenderer{}
	m.SetHostRenderer(renderer)
	if err := m.SetAgentEgress(AgentEgressConfig{Iface: "wg-agent", Slice: netip.MustParsePrefix(slice), Table: 51821,
		HandshakeMaxAge: 180 * time.Second}); err != nil {
		t.Fatal(err)
	}
	m.agentEgress.now = func() time.Time { return now }
	return m, runner, probe, renderer, now
}

func agentWake(id string) (WakeRequest, Lease) {
	host := netip.MustParseAddr("10.100.0." + strings.TrimPrefix(id, "vm-"))
	return WakeRequest{Instance: id, AccountID: "acct", sandboxID: "sb-" + id, sandboxNetwork: api.SandboxNetworkWeb},
		Lease{Instance: id, HostIP: host}
}

func TestAgentEgressGivesEachSessionItsAddressBeforeBoot(t *testing.T) {
	m, runner, _, _, _ := agentEgressTestManager(t, "10.78.8.0/22")
	var undos []func()
	for _, id := range []string{"vm-9", "vm-10"} {
		req, lease := agentWake(id)
		undo, err := m.prepareAgentEgressWake(t.Context(), req, lease)
		if err != nil {
			t.Fatal(err)
		}
		undos = append(undos, undo)
	}
	p := netns.ActiveHostPolicyForRender()
	if p.AgentEgressIface != "wg-agent" || len(p.AgentEgressRules) != 2 ||
		p.AgentEgressRules[0].AgentIP.String() != "10.78.8.2" || p.AgentEgressRules[1].AgentIP.String() != "10.78.8.3" ||
		p.AgentEgressRules[0].PerVMHostIP.String() != "10.100.0.9" || p.AgentEgressRules[0].SandboxID != "sb-vm-9" {
		t.Fatalf("rules before boot = %+v", p.AgentEgressRules)
	}
	for _, want := range []string{"priority 95 iif br-tenants fwmark 0x20000000/0x20000000 lookup 51821",
		"priority 96 iif br-tenants fwmark 0x20000000/0x20000000 unreachable"} {
		if !runner.ran(want) {
			t.Errorf("missing routing rule %q: %v", want, runner.commands)
		}
	}
	// The first booted: its rule moves onto the instance and stays.
	m.mu.Lock()
	inst := &Instance{Lease: Lease{Instance: "vm-9", HostIP: netip.MustParseAddr("10.100.0.9")}, AccountID: "acct", SandboxID: "sb-vm-9"}
	m.agentEgressStartedLocked(inst)
	m.live["vm-9"] = inst
	m.mu.Unlock()
	if inst.agentEgressIP.String() != "10.78.8.2" {
		t.Fatalf("booted VM's address = %v", inst.agentEgressIP)
	}
	// The second failed to boot: its address comes back.
	undos[1]()
	if rules := netns.ActiveHostPolicyForRender().AgentEgressRules; len(rules) != 1 || rules[0].PerVMHostIP.String() != "10.100.0.9" {
		t.Fatalf("after a failed boot = %+v", rules)
	}
	req, lease := agentWake("vm-11")
	if _, err := m.prepareAgentEgressWake(t.Context(), req, lease); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	reused := m.agentEgress.starting["vm-11"].AgentIP.String()
	m.agentEgress.releaseLocked("vm-9")
	_, held := m.agentEgress.addrs[netip.MustParseAddr("10.78.8.2")]
	m.mu.Unlock()
	if reused != "10.78.8.3" || held {
		t.Fatalf("reused %s, teardown left the address held: %v", reused, held)
	}
}

func TestAgentEgressFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Manager, *agentProbe, *fakeHostRenderer, time.Time)
		want   string
	}{
		{"stale handshake", func(_ *Manager, p *agentProbe, _ *fakeHostRenderer, now time.Time) {
			p.handshakes = fmt.Sprintf("gwkey=\t%d\n", now.Add(-10*time.Minute).Unix())
		}, "last shook hands"},
		{"never shook hands", func(_ *Manager, p *agentProbe, _ *fakeHostRenderer, _ time.Time) { p.handshakes = "gwkey=\t0\n" }, "no handshake"},
		{"tunnel probe fails", func(_ *Manager, p *agentProbe, _ *fakeHostRenderer, _ time.Time) {
			p.err = errors.New("no such device")
		}, "handshakes"},
		{"render fails", func(_ *Manager, _ *agentProbe, r *fakeHostRenderer, _ time.Time) { r.renderErr = errors.New("nft") }, "install agent egress"},
		{"slice full", func(m *Manager, _ *agentProbe, _ *fakeHostRenderer, _ time.Time) {
			m.agentEgress.cfg.Slice = netip.MustParsePrefix("10.78.8.0/30") // the node's address and one VM's
			m.agentEgress.addrs[netip.MustParseAddr("10.78.8.2")] = "other"
		}, "is full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, probe, renderer, now := agentEgressTestManager(t, "10.78.8.0/22")
			tc.mutate(m, probe, renderer, now)
			req, lease := agentWake("vm-9")
			_, err := m.prepareAgentEgressWake(t.Context(), req, lease)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.agentEgress.starting) != 0 || m.agentEgress.addrs[netip.MustParseAddr("10.78.8.2")] == "vm-9" {
				t.Fatal("a refused boot kept its address or rule")
			}
		})
	}
}

func TestAgentEgressLeavesOtherVMsAlone(t *testing.T) {
	m, runner, _, _, _ := agentEgressTestManager(t, "10.78.8.0/22")
	for name, req := range map[string]WakeRequest{
		"app":                {Instance: "vm-1", AppID: "app"},
		"session no network": {Instance: "vm-2", sandboxID: "sb", sandboxNetwork: api.SandboxNetworkNone},
	} {
		if _, err := m.prepareAgentEgressWake(t.Context(), req, Lease{Instance: req.Instance, HostIP: netip.MustParseAddr("10.100.0.1")}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(runner.commands) != 0 || len(netns.ActiveHostPolicyForRender().AgentEgressRules) != 0 {
		t.Fatal("a VM without agent egress touched the agent tunnel")
	}
	off := newTestManager(&fakeRunner{}, &fakeVMM{})
	req, lease := agentWake("vm-9")
	if _, err := off.prepareAgentEgressWake(t.Context(), req, lease); err != nil {
		t.Fatalf("switch off: %v", err)
	}
}

func TestAgentEgressConfigAndHandshakeParsing(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	for _, bad := range []AgentEgressConfig{
		{Slice: netip.MustParsePrefix("10.78.8.0/22"), Table: 1, HandshakeMaxAge: time.Second},
		{Iface: "wg-agent", Slice: netip.MustParsePrefix("10.78.8.0/30"), Table: 1, HandshakeMaxAge: time.Second},
		{Iface: "wg-agent", Slice: netip.MustParsePrefix("10.78.8.1/22"), Table: 1, HandshakeMaxAge: time.Second},
		{Iface: "wg-agent", Slice: netip.MustParsePrefix("10.78.8.0/22"), HandshakeMaxAge: time.Second},
		{Iface: "wg-agent", Slice: netip.MustParsePrefix("10.78.8.0/22"), Table: 1},
	} {
		if err := m.SetAgentEgress(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	got, err := latestHandshake("a=\t100\nb=\t250\nc=\t0\n")
	if err != nil || got.Unix() != 250 {
		t.Fatalf("latest = %v, %v", got, err)
	}
	if last := lastAddr(netip.MustParsePrefix("10.78.8.0/22")); last.String() != "10.78.11.255" {
		t.Fatalf("broadcast = %s", last)
	}
}

// fakeAgentProxy records registration calls; failRegister refuses them.
type fakeAgentProxy struct {
	mu           sync.Mutex
	calls        []string
	failRegister bool
	unregistered chan netip.Addr
}

func (p *fakeAgentProxy) Register(_ context.Context, addr netip.Addr, accountID, sandboxID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "PUT "+addr.String()+" "+accountID+" "+sandboxID)
	if p.failRegister {
		return errors.New("proxy unreachable")
	}
	return nil
}

func (p *fakeAgentProxy) Unregister(_ context.Context, addr netip.Addr) error {
	p.mu.Lock()
	p.calls = append(p.calls, "DELETE "+addr.String())
	p.mu.Unlock()
	if p.unregistered != nil {
		p.unregistered <- addr
	}
	return nil
}

func (p *fakeAgentProxy) log() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func withAgentProxy(t *testing.T, m *Manager, p *fakeAgentProxy) {
	t.Helper()
	cfg := *m.agentEgress.cfg
	cfg.Proxy, cfg.ProxyRenewEvery = p, time.Minute
	now := m.agentEgress.now
	if err := m.SetAgentEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m.agentEgress.now = now
}

// adr: 20261117185346 §4 — vmmd registers each agent VM with the proxy
// before it boots, and the address stays held until the proxy has
// forgotten it, so the next VM never inherits the old identity.
func TestAgentEgressRegistersWithTheProxyBeforeBoot(t *testing.T) {
	m, _, _, _, _ := agentEgressTestManager(t, "10.78.8.0/22")
	proxy := &fakeAgentProxy{unregistered: make(chan netip.Addr, 2)}
	withAgentProxy(t, m, proxy)

	req, lease := agentWake("vm-9")
	undo, err := m.prepareAgentEgressWake(t.Context(), req, lease)
	if err != nil {
		t.Fatal(err)
	}
	if got := proxy.log(); len(got) != 1 || got[0] != "PUT 10.78.8.2 acct sb-vm-9" {
		t.Fatalf("calls before boot = %v", got)
	}
	m.mu.Lock()
	_, registered := m.agentEgress.registered[netip.MustParseAddr("10.78.8.2")]
	m.mu.Unlock()
	if !registered {
		t.Fatal("the registration was not recorded for renewal")
	}

	// The boot failed: the address is released, but not reused until the
	// proxy has dropped it.
	m.mu.Lock()
	m.agentEgress.regMu.Lock() // hold the removal back
	m.mu.Unlock()
	undo()
	m.mu.Lock()
	owner, held := m.agentEgress.addrs[netip.MustParseAddr("10.78.8.2")]
	m.mu.Unlock()
	if !held || owner != "" {
		t.Fatalf("released address before removal: held=%v owner=%q", held, owner)
	}
	m.mu.Lock()
	next, ok := m.agentEgress.allocateLocked("vm-10")
	m.agentEgress.releaseLocked("vm-10")
	m.mu.Unlock()
	if !ok || next.String() != "10.78.8.3" {
		t.Fatalf("next VM got %v while the old address awaited removal", next)
	}
	m.agentEgress.regMu.Unlock()
	removed := map[string]bool{}
	for range 2 {
		select {
		case addr := <-proxy.unregistered:
			removed[addr.String()] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("released addresses never removed from the proxy: %v", removed)
		}
	}
	if !removed["10.78.8.2"] || !removed["10.78.8.3"] {
		t.Fatalf("removed %v", removed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		_, held = m.agentEgress.addrs[netip.MustParseAddr("10.78.8.2")]
		m.mu.Unlock()
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the address stayed held after the proxy dropped it")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// adr: 20261117185346 §3 — a VM the proxy cannot register does not boot,
// and leaves no rules behind.
func TestAgentEgressRefusesBootTheProxyCannotRegister(t *testing.T) {
	m, _, _, _, _ := agentEgressTestManager(t, "10.78.8.0/22")
	proxy := &fakeAgentProxy{failRegister: true, unregistered: make(chan netip.Addr, 1)}
	withAgentProxy(t, m, proxy)
	req, lease := agentWake("vm-9")
	_, err := m.prepareAgentEgressWake(t.Context(), req, lease)
	if !errors.Is(err, errAgentEgressUnavailable) {
		t.Fatalf("err = %v, want errAgentEgressUnavailable", err)
	}
	if rules := netns.ActiveHostPolicyForRender().AgentEgressRules; len(rules) != 0 {
		t.Fatalf("rules after a refused boot = %+v", rules)
	}
	select {
	case <-proxy.unregistered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refused VM's address was never cleared at the proxy")
	}
}

// adr: 20261117185346 §4 — live registrations are renewed; a released one
// is not.
func TestAgentEgressRenewsLiveRegistrationsOnly(t *testing.T) {
	m, _, _, _, _ := agentEgressTestManager(t, "10.78.8.0/22")
	proxy := &fakeAgentProxy{unregistered: make(chan netip.Addr, 1)}
	withAgentProxy(t, m, proxy)
	for _, id := range []string{"vm-9", "vm-10"} {
		req, lease := agentWake(id)
		if _, err := m.prepareAgentEgressWake(t.Context(), req, lease); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	delete(m.agentEgress.starting, "vm-10")
	m.agentEgress.releaseLocked("vm-10")
	m.mu.Unlock()
	<-proxy.unregistered
	before := len(proxy.log())
	m.renewAgentEgress(t.Context())
	got := proxy.log()[before:]
	if len(got) != 1 || got[0] != "PUT 10.78.8.2 acct sb-vm-9" {
		t.Fatalf("renewals = %v", got)
	}
}
