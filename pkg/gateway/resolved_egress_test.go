// adr: 370 — DNS-gated egress hook in the bridge resolver.
package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestAnswerAddresses(t *testing.T) {
	resp := new(dns.Msg)
	resp.Answer = []dns.RR{
		&dns.CNAME{Hdr: dns.RR_Header{Name: "api.example.", Rrtype: dns.TypeCNAME, Ttl: 30}, Target: "edge.example."},
		&dns.A{Hdr: dns.RR_Header{Name: "edge.example.", Rrtype: dns.TypeA, Ttl: 300}, A: net.ParseIP("198.51.100.10")},
		&dns.AAAA{Hdr: dns.RR_Header{Name: "edge.example.", Rrtype: dns.TypeAAAA, Ttl: 120}, AAAA: net.ParseIP("2001:db8::1")},
	}
	addrs, ttl := answerAddresses(resp)
	if len(addrs) != 2 || addrs[0] != netip.MustParseAddr("198.51.100.10") || addrs[1] != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("addrs = %v", addrs)
	}
	if ttl != 120*time.Second {
		t.Fatalf("ttl = %s, want the smallest address TTL (CNAME ignored)", ttl)
	}
	if addrs, _ := answerAddresses(new(dns.Msg)); len(addrs) != 0 {
		t.Fatalf("empty answer = %v", addrs)
	}
}

// The hook sees the forwarded answer's addresses before the reply goes out,
// and a hook error still returns the answer.
func TestServiceDiscoveryDNSReportsForwardedAnswers(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("203.0.113.9")}}
		_ = w.WriteMsg(resp)
	})}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() { _ = upstream.Shutdown() })

	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{pc.LocalAddr().String()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	type call struct {
		remote string
		addrs  []netip.Addr
		ttl    time.Duration
	}
	var calls []call
	hookErr := errors.New("vmmd down")
	h.WithResolvedEgressHook(func(_ context.Context, remote string, addrs []netip.Addr, ttl time.Duration) error {
		calls = append(calls, call{remote, addrs, ttl})
		return hookErr
	})
	req := new(dns.Msg)
	req.SetQuestion("api.example.", dns.TypeA)
	w := &serviceDiscoveryResponseWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.100.0.7"), Port: 40000}}
	h.ServeDNS(w, req)
	if len(calls) != 1 || calls[0].remote != "10.100.0.7:40000" || len(calls[0].addrs) != 1 ||
		calls[0].addrs[0] != netip.MustParseAddr("203.0.113.9") || calls[0].ttl != time.Minute {
		t.Fatalf("hook calls = %+v", calls)
	}
	if w.msg == nil || len(w.msg.Answer) != 1 {
		t.Fatalf("answer not returned after a hook error: %#v", w.msg)
	}
}
