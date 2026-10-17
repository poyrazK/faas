// adr: 1349
package gateway

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

func TestRehearsalNetworkDNSBeforeRuntimePublication(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var forwarded atomic.Int32
	upstream := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		forwarded.Add(1)
		msg := new(dns.Msg)
		msg.SetReply(req)
		_ = w.WriteMsg(msg)
	})}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() { _ = upstream.Shutdown() })
	newHandler := func() *ServiceDiscoveryDNSHandler {
		t.Helper()
		h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{packet.LocalAddr().String()}, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	query := new(dns.Msg)
	query.SetQuestion("external.example.test.", dns.TypeA)
	ordinary := &serviceDiscoveryResponseWriter{}
	newHandler().ServeDNS(ordinary, query)
	if ordinary.msg == nil || ordinary.msg.Rcode != dns.RcodeSuccess || forwarded.Load() != 1 {
		t.Fatal("ordinary startup DNS now requires runtime identity")
	}
	private := newHandler().WithPrivateServiceNamesOnly()
	blocked := &serviceDiscoveryResponseWriter{}
	private.ServeDNS(blocked, query)
	if blocked.msg == nil || blocked.msg.Rcode != dns.RcodeRefused || forwarded.Load() != 1 {
		t.Fatal("unpublished contained caller reached public DNS")
	}
	query.SetQuestion("worker.svc.gregale.", dns.TypeA)
	allowed := &serviceDiscoveryResponseWriter{}
	private.ServeDNS(allowed, query)
	if allowed.msg == nil || len(allowed.msg.Answer) != 1 || forwarded.Load() != 1 {
		t.Fatal("private startup DNS requires runtime identity")
	}
}

func TestRehearsalNetworkDNSRefusesExternalAndAliasQuestions(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"public.example.", dns.RcodeRefused},
		{"billing.internal.", dns.RcodeRefused},
		{"worker.svc.gregale.", dns.RcodeSuccess},
	} {
		t.Run(tc.name+dns.RcodeToString[tc.want], func(t *testing.T) {
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{"127.0.0.1:1"}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithPrivateServiceNamesOnly()
			h.WithServiceAddressLookup(func(context.Context, string, string) (netip.Addr, bool) {
				t.Error("contained caller used service-address path")
				return netip.MustParseAddr("198.18.0.1"), true
			})
			req := new(dns.Msg)
			req.SetQuestion(tc.name, dns.TypeA)
			w := &serviceDiscoveryResponseWriter{}
			h.ServeDNS(w, req)
			if w.msg == nil || w.msg.Rcode != tc.want {
				t.Fatalf("response: %+v", w.msg)
			}
			if tc.want == dns.RcodeSuccess && (len(w.msg.Answer) != 1 || w.msg.Answer[0].(*dns.A).A.String() != "10.100.0.1") {
				t.Fatal("private request did not use bridge proxy")
			}
		})
	}
}
