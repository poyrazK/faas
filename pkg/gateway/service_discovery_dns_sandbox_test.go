// adr: 20261027090013 §6 — the registries-mode resolver forwards one A or
// AAAA question for an allowed registry name, reports only those answers to
// the DNS-gated egress hook, and refuses every other query, private service
// names included.
package gateway

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSandboxRegistriesResolverAnswersAllowedNamesOnly(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var forwarded atomic.Int32
	upstream := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		forwarded.Add(1)
		msg := new(dns.Msg)
		msg.SetReply(req)
		msg.Answer = append(msg.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("192.0.2.7").To4(),
		})
		_ = w.WriteMsg(msg)
	})}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() { _ = upstream.Shutdown() })

	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{packet.LocalAddr().String()}, nil,
		func(context.Context, string) (string, error) { return "caller", nil },
		func(context.Context, string, string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	var resolved []string
	h.WithAllowedNamesOnly(api.SandboxRegistryHost).
		WithResolvedEgressHook(func(_ context.Context, _, name string, addrs []netip.Addr, _ time.Duration) error {
			resolved = append(resolved, name)
			return nil
		})
	query := func(qtype uint16, names ...string) *dns.Msg {
		t.Helper()
		req := new(dns.Msg)
		req.SetQuestion(names[0], qtype)
		for _, name := range names[1:] {
			req.Question = append(req.Question, dns.Question{Name: name, Qtype: qtype, Qclass: dns.ClassINET})
		}
		w := &serviceDiscoveryResponseWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.100.0.9"), Port: 40000}}
		h.ServeDNS(w, req)
		if w.msg == nil {
			t.Fatal("no DNS response")
		}
		return w.msg
	}
	for _, name := range []string{"registry.npmjs.org.", "files.pythonhosted.org.", "GitHub.com.", "objects.githubusercontent.com."} {
		if resp := query(dns.TypeA, name); resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
			t.Errorf("%s: response = %#v", name, resp)
		}
	}
	if forwarded.Load() != 4 || len(resolved) != 4 {
		t.Fatalf("forwarded %d, reported %d; want 4 each", forwarded.Load(), len(resolved))
	}
	for _, tc := range []struct {
		qtype uint16
		names []string
	}{
		{dns.TypeA, []string{"example.com."}},
		{dns.TypeA, []string{"evilgithub.com."}},
		{dns.TypeA, []string{"github.com.evil.net."}},
		{dns.TypeA, []string{"billing.svc.gregale."}},
		{dns.TypeA, []string{"billing.internal."}},
		{dns.TypeTXT, []string{"github.com."}},
		{dns.TypeA, []string{"github.com.", "example.com."}},
		{dns.TypeA, []string{"github.com.", "pypi.org."}},
	} {
		if resp := query(tc.qtype, tc.names...); resp.Rcode != dns.RcodeRefused || len(resp.Answer) != 0 {
			t.Errorf("%v: response = %#v, want REFUSED", tc.names, resp)
		}
	}
	if forwarded.Load() != 4 || len(resolved) != 4 {
		t.Fatalf("a refused query was forwarded or reported: %d, %d", forwarded.Load(), len(resolved))
	}
}
