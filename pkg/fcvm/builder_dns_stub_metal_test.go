//go:build metal && linux

package fcvm_test

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/onebox-faas/faas/pkg/netns"
)

// builderDNSUpstream is where the stub relays builder DNS. Production builder
// guests reach the registries through gatewayd-internal's resolver.
const builderDNSUpstream = "1.1.1.1:53"

// startBuilderDNSStub serves DNS on the host bridge IP for builder guests.
// Every guest query is pinned to HostBridgeIP:53 (GuestDNSPinCommands,
// ADR-361), which gatewayd-internal serves in production. The builder
// acceptance test starts no gatewayd, so without this stub the guest's
// registry DNS preflight timed out and no build ran (PR #7593 metal run,
// 2026-11-19). It binds only a free port: a real resolver already listening
// there is left alone.
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
		if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
			network = "tcp"
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
	t.Logf("builder DNS stub on %s relaying to %s", addr, builderDNSUpstream)
}
