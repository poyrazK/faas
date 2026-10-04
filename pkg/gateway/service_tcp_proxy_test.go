// adr: 530
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

var testServiceBlock = netip.MustParsePrefix("198.19.0.0/16")

type tcpTestProvider struct {
	mu        sync.Mutex
	endpoints map[string][]ServiceEndpoint
	reads     int
}

func (p *tcpTestProvider) ServiceEndpoints(_ context.Context, appID string) (ServiceEndpointsSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	return ServiceEndpointsSnapshot{AppID: appID, Endpoints: append([]ServiceEndpoint(nil), p.endpoints[appID]...)}, nil
}

func (p *tcpTestProvider) set(appID string, endpoints ...ServiceEndpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endpoints[appID] = endpoints
}

type tcpTestHarness struct {
	provider *tcpTestProvider
	proxy    *ServiceTCPProxy
	targets  map[netip.Addr]ServiceTCPTarget
	authzErr error
	scope    *api.ServiceCallScope
	wakes    int
	onWake   func()
	release  string
	dead     map[string]bool // instance IDs whose guest dial fails

	mu   sync.Mutex
	hops []Target
	idle []time.Duration
}

func newTCPTestHarness(t *testing.T) *tcpTestHarness {
	t.Helper()
	h := &tcpTestHarness{
		provider: &tcpTestProvider{endpoints: map[string][]ServiceEndpoint{}},
		targets: map[netip.Addr]ServiceTCPTarget{
			netip.MustParseAddr("198.19.0.7"): {AppID: "db", AccountID: "acct", Plan: api.PlanPro, TCPPorts: []int{5432, 443}, IdleTimeout: 5 * time.Minute},
		},
		dead: map[string]bool{},
	}
	services := NewServiceProxy(ServiceProxyConfig{
		Provider: h.provider,
		Authorize: func(_ context.Context, callerAppID, targetAppID string) (ServiceCaller, error) {
			if h.authzErr != nil {
				return ServiceCaller{}, h.authzErr
			}
			return ServiceCaller{AppID: callerAppID, AccountID: "acct", CallScope: h.scope}, nil
		},
		ResolveCallerIdentity: func(_ context.Context, remoteAddr string) (string, string, error) {
			switch remoteAddr {
			case "caller":
				return "api", "api-dep", nil
			case "broken":
				return "", "", ErrServiceProxyUnavailable
			}
			return "", "", nil
		},
		ResolveRelease: func(_ context.Context, _, _, _, _ string) (string, string, error) {
			if h.release == "gone" {
				return "", "", ErrReleaseGone
			}
			return "", h.release, nil
		},
		Wake: func(context.Context, string) error {
			h.wakes++
			if h.onWake != nil {
				h.onWake()
			}
			return nil
		},
	})
	proxy, err := NewServiceTCPProxy(ServiceTCPProxyConfig{
		Services: services,
		Resolve: func(_ context.Context, callerAppID string, address netip.Addr) (ServiceTCPTarget, bool, error) {
			if address == netip.MustParseAddr("198.19.0.9") {
				return ServiceTCPTarget{}, false, ErrServiceTCPTargetUnavailable
			}
			target, ok := h.targets[address]
			return target, ok, nil
		},
		Forward: func(_ context.Context, conn net.Conn, target Target, idle time.Duration) error {
			h.mu.Lock()
			h.hops = append(h.hops, target)
			h.idle = append(h.idle, idle)
			h.mu.Unlock()
			if h.dead[target.InstanceID] {
				return fmt.Errorf("%w: refused", ErrTCPGuestUnreachable)
			}
			return nil
		},
		OriginalDestination: func(conn net.Conn) (netip.AddrPort, error) {
			return conn.(*tcpTestConn).dst, nil
		},
		AddressCIDR:        testServiceBlock,
		ReservedPorts:      api.ServiceTCPReservedPorts(),
		SessionsPerAccount: func(plan api.Plan) int { return plan.ServiceTCPSessionsPerAccount() },
		MaxSessions:        8,
		WakeTimeout:        time.Second,
	})
	if err != nil {
		t.Fatalf("NewServiceTCPProxy: %v", err)
	}
	h.proxy = proxy
	return h
}

// tcpTestConn carries the caller's source identity and the dialed address.
type tcpTestConn struct {
	net.Conn
	remote string
	dst    netip.AddrPort
}

type tcpTestAddr string

func (a tcpTestAddr) Network() string { return "tcp" }
func (a tcpTestAddr) String() string  { return string(a) }

func (c *tcpTestConn) RemoteAddr() net.Addr { return tcpTestAddr(c.remote) }

func dialService(t *testing.T, remote, dst string) *tcpTestConn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return &tcpTestConn{Conn: server, remote: remote, dst: netip.MustParseAddrPort(dst)}
}

func (h *tcpTestHarness) serve(t *testing.T, remote, dst string) (string, string) {
	t.Helper()
	outcome, reason, _ := h.proxy.serve(context.Background(), dialService(t, remote, dst), nil)
	return outcome, reason
}

func TestServiceTCPProxyRefusals(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		dst    string
		setup  func(*tcpTestHarness)
		want   string
	}{
		{name: "outside the service block", remote: "caller", dst: "10.100.0.1:10082", want: "not_service_address"},
		{name: "HTTP mesh port", remote: "caller", dst: "198.19.0.7:10081", want: "reserved_port"},
		{name: "declared but reserved :443", remote: "caller", dst: "198.19.0.7:443", want: "reserved_port"},
		{name: "unknown source", remote: "stranger", dst: "198.19.0.7:5432", want: "unknown_caller"},
		{name: "identity lookup failure", remote: "broken", dst: "198.19.0.7:5432", want: "identity_unavailable"},
		{name: "address not in the caller's account", remote: "caller", dst: "198.19.0.8:5432", want: "unknown_service"},
		{name: "target in maintenance", remote: "caller", dst: "198.19.0.9:5432", want: "target_unavailable"},
		{name: "undeclared port", remote: "caller", dst: "198.19.0.7:22", want: "undeclared_port"},
		{name: "cross-account or tenant denial", remote: "caller", dst: "198.19.0.7:5432",
			setup: func(h *tcpTestHarness) { h.authzErr = ErrServiceProxyDenied }, want: "denied"},
		{name: "undeclared binding", remote: "caller", dst: "198.19.0.7:5432",
			setup: func(h *tcpTestHarness) { h.authzErr = ErrServiceProxyBindingDenied }, want: "binding_denied"},
		{name: "target caller allowlist", remote: "caller", dst: "198.19.0.7:5432",
			setup: func(h *tcpTestHarness) { h.authzErr = ErrServiceProxyCallerDenied }, want: "caller_denied"},
		{name: "method/path scoped caller", remote: "caller", dst: "198.19.0.7:5432",
			setup: func(h *tcpTestHarness) { h.scope = &api.ServiceCallScope{} }, want: "call_scope"},
		{name: "release graph gone", remote: "caller", dst: "198.19.0.7:5432",
			setup: func(h *tcpTestHarness) { h.release = "gone" }, want: "release"},
		{name: "no replica after wake", remote: "caller", dst: "198.19.0.7:5432", want: "no_replica"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTCPTestHarness(t)
			if tt.setup != nil {
				tt.setup(h)
			}
			if _, reason := h.serve(t, tt.remote, tt.dst); reason != tt.want {
				t.Fatalf("reason = %q, want %q", reason, tt.want)
			}
			if tt.want != "no_replica" && len(h.hops) != 0 {
				t.Fatalf("refused connection reached a guest: %+v", h.hops)
			}
		})
	}
}

// A parked target is woken and the session lands on the declared port with
// the target plan's idle timeout.
func TestServiceTCPProxyWakesParkedTarget(t *testing.T) {
	h := newTCPTestHarness(t)
	h.onWake = func() {
		h.provider.set("db", ServiceEndpoint{InstanceID: "i1", NodeID: "n1", DeploymentID: "d1", Port: 8080})
	}
	outcome, reason := h.serve(t, "caller", "198.19.0.7:5432")
	if reason != "" || outcome != "success" {
		t.Fatalf("outcome=%q reason=%q, want success", outcome, reason)
	}
	if h.wakes != 1 {
		t.Fatalf("wakes = %d, want 1", h.wakes)
	}
	if len(h.hops) != 1 || h.hops[0].InstanceID != "i1" || h.hops[0].Port != 5432 || h.idle[0] != 5*time.Minute {
		t.Fatalf("hop = %+v idle=%v, want instance i1 on guest port 5432 with a 5m idle timeout", h.hops, h.idle)
	}
}

// A replica that cannot be dialed is benched, the lease is dropped, and the
// connection is retried once on another replica without waking.
func TestServiceTCPProxyRetriesUnreachableReplica(t *testing.T) {
	h := newTCPTestHarness(t)
	h.provider.set("db",
		ServiceEndpoint{InstanceID: "stale", NodeID: "n1", Port: 8080},
		ServiceEndpoint{InstanceID: "live", NodeID: "n2", Port: 8080},
	)
	h.dead["stale"] = true
	// Start the round-robin on the stale replica.
	h.proxy.cfg.Services.next["db"] = 0
	outcome, reason := h.serve(t, "caller", "198.19.0.7:5432")
	if reason != "" || outcome != "success" {
		t.Fatalf("outcome=%q reason=%q, want success after one retry", outcome, reason)
	}
	if len(h.hops) != 2 || h.hops[0].InstanceID != "stale" || h.hops[1].InstanceID != "live" {
		t.Fatalf("hops = %+v, want stale then live", h.hops)
	}
	if h.wakes != 0 {
		t.Fatalf("retry woke the target %d times", h.wakes)
	}
	if h.provider.reads < 2 {
		t.Fatalf("endpoint lease was not refreshed after the failed dial (reads=%d)", h.provider.reads)
	}

	h.dead["live"] = true
	if _, reason := h.serve(t, "caller", "198.19.0.7:5432"); reason != "guest_unreachable" && reason != "no_replica" {
		t.Fatalf("all replicas unreachable: reason = %q", reason)
	}
}

// A project release pin restricts the session to the pinned deployment.
func TestServiceTCPProxyHonoursReleasePin(t *testing.T) {
	h := newTCPTestHarness(t)
	h.release = "d2"
	h.provider.set("db",
		ServiceEndpoint{InstanceID: "old", NodeID: "n1", DeploymentID: "d1", Port: 8080},
		ServiceEndpoint{InstanceID: "pinned", NodeID: "n1", DeploymentID: "d2", Port: 8080},
	)
	if outcome, reason := h.serve(t, "caller", "198.19.0.7:5432"); outcome != "success" || reason != "" {
		t.Fatalf("outcome=%q reason=%q", outcome, reason)
	}
	if len(h.hops) != 1 || h.hops[0].InstanceID != "pinned" {
		t.Fatalf("hops = %+v, want the pinned deployment", h.hops)
	}
}

func TestServiceTCPAccountSessions(t *testing.T) {
	s := &serviceTCPAccountSessions{current: map[string]int{}}
	first, ok := s.acquire("acct", 2)
	if !ok {
		t.Fatal("first session refused")
	}
	if _, ok := s.acquire("acct", 2); !ok {
		t.Fatal("second session refused")
	}
	if _, ok := s.acquire("acct", 2); ok {
		t.Fatal("third session admitted over a cap of 2")
	}
	if _, ok := s.acquire("other", 2); !ok {
		t.Fatal("another account was limited by this one")
	}
	first()
	first() // idempotent
	if _, ok := s.acquire("acct", 2); !ok {
		t.Fatal("released slot was not reusable")
	}
	if _, ok := s.acquire("acct", 0); ok {
		t.Fatal("a zero cap admitted a session")
	}
	if _, ok := s.acquire("", 5); ok {
		t.Fatal("an empty account admitted a session")
	}
}

func TestNewServiceTCPProxyRejectsMissingWiring(t *testing.T) {
	if _, err := NewServiceTCPProxy(ServiceTCPProxyConfig{}); err == nil {
		t.Fatal("empty config accepted")
	}
	services := NewServiceProxy(ServiceProxyConfig{
		Provider:  &tcpTestProvider{},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
	})
	_, err := NewServiceTCPProxy(ServiceTCPProxyConfig{Services: services})
	if err == nil {
		t.Fatal("a proxy without source-address identity was accepted")
	}
}

func TestServiceTCPProxyServeConnEnforcesGlobalCap(t *testing.T) {
	h := newTCPTestHarness(t)
	for i := 0; i < cap(h.proxy.slots); i++ {
		h.proxy.slots <- struct{}{}
	}
	conn := dialService(t, "caller", "198.19.0.7:5432")
	h.proxy.ServeConn(context.Background(), conn)
	if len(h.hops) != 0 {
		t.Fatal("a session beyond the node cap was forwarded")
	}
	if _, err := conn.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("refused connection was left open: write err = %v", err)
	}
}

func TestNewServiceTCPProxyValidatesConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServiceTCPProxyConfig)
	}{
		{"no resolver", func(c *ServiceTCPProxyConfig) { c.Resolve = nil }},
		{"no forwarder", func(c *ServiceTCPProxyConfig) { c.Forward = nil }},
		{"no destination lookup", func(c *ServiceTCPProxyConfig) { c.OriginalDestination = nil }},
		{"no session cap", func(c *ServiceTCPProxyConfig) { c.SessionsPerAccount = nil }},
		{"no address block", func(c *ServiceTCPProxyConfig) { c.AddressCIDR = netip.Prefix{} }},
		{"IPv6 address block", func(c *ServiceTCPProxyConfig) { c.AddressCIDR = netip.MustParsePrefix("fd00::/64") }},
		{"zero node cap", func(c *ServiceTCPProxyConfig) { c.MaxSessions = 0 }},
		{"zero wake timeout", func(c *ServiceTCPProxyConfig) { c.WakeTimeout = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTCPTestHarness(t).proxy.cfg
			tt.mutate(&cfg)
			if _, err := NewServiceTCPProxy(cfg); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	cfg := newTCPTestHarness(t).proxy.cfg
	cfg.Log = nil
	if p, err := NewServiceTCPProxy(cfg); err != nil || p.cfg.Log == nil {
		t.Fatalf("valid config without a logger: proxy=%v err=%v", p, err)
	}
}

// tcpTestListener stamps every accepted connection with a fixed caller
// identity and dialed address, standing in for the bridge DNAT.
type tcpTestListener struct {
	net.Listener
	remote string
	dst    netip.AddrPort
}

func (l tcpTestListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &tcpTestConn{Conn: conn, remote: l.remote, dst: l.dst}, nil
}

func TestServiceTCPProxyServeForwardsAndShutsDown(t *testing.T) {
	h := newTCPTestHarness(t)
	h.provider.set("db", ServiceEndpoint{InstanceID: "i1", NodeID: "n1", DeploymentID: "d1", Port: 8080})
	registry := prometheus.NewRegistry()
	h.proxy.cfg.Metrics = tcpmetrics.New(registry, "test_service")
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tcpTestListener{Listener: inner, remote: "caller", dst: netip.MustParseAddrPort("198.19.0.7:5432")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.proxy.Serve(ctx, ln) }()

	client, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	// The fake forwarder returns at once, so the proxy closes the session.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("session was not closed after forwarding: %v", err)
	}
	if got := counterValue(t, registry, "test_service_tcp_sessions_completed_total", "success"); got != 1 {
		t.Fatalf("completed{success} = %v, want 1", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestServiceTCPProxyServeReportsListenerFailure(t *testing.T) {
	h := newTCPTestHarness(t)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = inner.Close()
	if err := h.proxy.Serve(context.Background(), inner); err == nil {
		t.Fatal("Serve on a closed listener returned nil")
	}
}

// A session whose context ends while it is being served is recorded as
// canceled, not as the forwarder's outcome.
func TestServiceTCPProxyServeConnRecordsCancellation(t *testing.T) {
	h := newTCPTestHarness(t)
	h.provider.set("db", ServiceEndpoint{InstanceID: "i1", NodeID: "n1", DeploymentID: "d1", Port: 8080})
	registry := prometheus.NewRegistry()
	h.proxy.cfg.Metrics = tcpmetrics.New(registry, "test_service")
	ctx, cancel := context.WithCancel(context.Background())
	forward := h.proxy.cfg.Forward
	h.proxy.cfg.Forward = func(ctx context.Context, conn net.Conn, target Target, idle time.Duration) error {
		cancel()
		return forward(ctx, conn, target, idle)
	}
	h.proxy.ServeConn(ctx, dialService(t, "caller", "198.19.0.7:5432"))
	if got := counterValue(t, registry, "test_service_tcp_sessions_completed_total", "canceled"); got != 1 {
		t.Fatalf("completed{canceled} = %v, want 1", got)
	}
}
