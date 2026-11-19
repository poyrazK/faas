//go:build metal && linux

package fcvm_test

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
)

// builderDNSUpstream is where the stub relays builder DNS. Production builder
// guests reach the registries through gatewayd-internal's resolver.
const builderDNSUpstream = "1.1.1.1:53"

// builderDNSManagers are the acceptance managers whose guests the stub
// answers. Like gatewayd-internal's resolver, the stub grants each answer's
// addresses to the asking guest: vmmd's DNS-gated egress (ADR-373) drops a
// connection to any address the bridge resolver did not hand that guest.
var builderDNSManagers struct {
	sync.Mutex
	list []*fcvm.Manager
}

// newBuilderAcceptanceManager is fcvm.NewAcceptanceManager whose guests'
// DNS answers the builder DNS stub grants as egress.
func newBuilderAcceptanceManager(t *testing.T) *fcvm.Manager {
	t.Helper()
	m := fcvm.NewAcceptanceManager(t)
	builderDNSManagers.Lock()
	builderDNSManagers.list = append(builderDNSManagers.list, m)
	builderDNSManagers.Unlock()
	t.Cleanup(func() {
		builderDNSManagers.Lock()
		defer builderDNSManagers.Unlock()
		for i, have := range builderDNSManagers.list {
			if have == m {
				builderDNSManagers.list = append(builderDNSManagers.list[:i], builderDNSManagers.list[i+1:]...)
				return
			}
		}
	})
	return m
}

// grantBuilderDNSAnswer does what gatewayd-internal's resolver does after an
// answer: it names the asking guest by its host address and allows the
// answer's addresses for it.
func grantBuilderDNSAnswer(t *testing.T, source netip.Addr, resp *dns.Msg) {
	var addrs []netip.Addr
	ttl := uint32(0)
	name := ""
	for _, rr := range resp.Answer {
		var ip net.IP
		switch r := rr.(type) {
		case *dns.A:
			ip = r.A
		case *dns.AAAA:
			ip = r.AAAA
		default:
			continue
		}
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr.Unmap())
			if ttl == 0 || rr.Header().Ttl < ttl {
				ttl = rr.Header().Ttl
			}
			name = rr.Header().Name
		}
	}
	if len(addrs) == 0 {
		return
	}
	builderDNSManagers.Lock()
	managers := append([]*fcvm.Manager(nil), builderDNSManagers.list...)
	builderDNSManagers.Unlock()
	for _, m := range managers {
		owner, ok := m.ResolveServiceCaller(source)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := m.AllowResolvedEgressNamed(ctx, source, owner, name, addrs, time.Duration(ttl)*time.Second, 0)
		cancel()
		if err != nil {
			t.Logf("builder DNS stub: grant %s to %s: %v", name, source, err)
		}
		return
	}
	t.Logf("builder DNS stub: no live guest at %s for %s", source, name)
}

// startBuilderDNSStub serves DNS on the host bridge IP for builder guests.
// Every guest query is pinned to HostBridgeIP:53 (GuestDNSPinCommands,
// ADR-361), which gatewayd-internal serves in production. The builder
// acceptance test starts no gatewayd, so without this stub the guest's
// registry DNS preflight timed out and no build ran (PR #7593 metal run,
// 2026-11-19); without the egress grant the next HTTPS preflight timed out.
// It binds only a free port: a real resolver already listening there is
// left alone.
func startBuilderDNSStub(t *testing.T) {
	t.Helper()
	addr := net.JoinHostPort(netns.DefaultHostBridgeIP.String(), "53")
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Logf("builder DNS stub not started, %s is taken (a real resolver serves it): %v", addr, err)
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		_ = packet.Close()
		t.Logf("builder DNS stub not started, tcp %s is taken: %v", addr, err)
		return
	}
	relay := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		network := "udp"
		var source netip.Addr
		switch remote := w.RemoteAddr().(type) {
		case *net.TCPAddr:
			network = "tcp"
			source = remote.AddrPort().Addr().Unmap()
		case *net.UDPAddr:
			source = remote.AddrPort().Addr().Unmap()
		}
		client := &dns.Client{Net: network, Timeout: 3 * time.Second}
		resp, _, err := client.Exchange(req, builderDNSUpstream)
		if err == nil && resp.Truncated && network == "udp" {
			resp, _, err = (&dns.Client{Net: "tcp", Timeout: 3 * time.Second}).Exchange(req, builderDNSUpstream)
		}
		if err != nil {
			fail := new(dns.Msg)
			fail.SetRcode(req, dns.RcodeServerFailure)
			_ = w.WriteMsg(fail)
			return
		}
		// Grant before answering, as the production resolver does, so the
		// guest's first connection already finds its address allowed.
		grantBuilderDNSAnswer(t, source, resp)
		_ = w.WriteMsg(resp)
	})
	udp := &dns.Server{PacketConn: packet, Handler: relay}
	tcp := &dns.Server{Listener: listener, Handler: relay}
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
	})
	t.Logf("builder DNS stub on %s relaying to %s and granting answers as egress", addr, builderDNSUpstream)
}
