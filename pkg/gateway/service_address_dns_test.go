// adr: 482
package gateway

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

func serviceAddressDNS(t *testing.T, lookup ServiceAddressLookup, questions ...dns.Question) *dns.Msg {
	t.Helper()
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lookup != nil {
		h.WithServiceAddressLookup(lookup)
	}
	req := new(dns.Msg)
	req.Question = questions
	req.Id = dns.Id()
	w := &serviceDiscoveryResponseWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.100.0.7"), Port: 40000}}
	h.ServeDNS(w, req)
	if w.msg == nil {
		t.Fatal("no DNS response")
	}
	return w.msg
}

func aAnswer(t *testing.T, msg *dns.Msg, i int) string {
	t.Helper()
	if len(msg.Answer) <= i {
		t.Fatalf("answers = %v, want at least %d", msg.Answer, i+1)
	}
	a, ok := msg.Answer[i].(*dns.A)
	if !ok {
		t.Fatalf("answer %d = %#v, want an A record", i, msg.Answer[i])
	}
	return a.A.String()
}

func TestServiceDiscoveryDNSAnswersServiceAddress(t *testing.T) {
	var gotRemote, gotService string
	lookup := func(_ context.Context, remote, service string) (netip.Addr, bool) {
		gotRemote, gotService = remote, service
		if service == "db" {
			return netip.MustParseAddr("198.19.0.7"), true
		}
		return netip.Addr{}, false
	}
	msg := serviceAddressDNS(t, lookup, dns.Question{Name: "DB.svc.gregale.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	if got := aAnswer(t, msg, 0); got != "198.19.0.7" {
		t.Fatalf("db.svc.gregale = %s, want its service address", got)
	}
	if gotService != "db" || gotRemote != "10.100.0.7:40000" {
		t.Fatalf("lookup saw remote=%q service=%q; want the caller source and the lower-cased label", gotRemote, gotService)
	}
	if !msg.Authoritative {
		t.Fatal("service address answer is not authoritative")
	}
}

// Any doubt keeps today's bridge answer, which always serves HTTP.
func TestServiceDiscoveryDNSFallsBackToBridge(t *testing.T) {
	tests := []struct {
		name   string
		lookup ServiceAddressLookup
	}{
		{name: "no lookup wired"},
		{name: "lookup declines", lookup: func(context.Context, string, string) (netip.Addr, bool) { return netip.Addr{}, false }},
		{name: "IPv6 answer", lookup: func(context.Context, string, string) (netip.Addr, bool) { return netip.MustParseAddr("fd00::7"), true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := serviceAddressDNS(t, tt.lookup, dns.Question{Name: "db.svc.gregale.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
			if got := aAnswer(t, msg, 0); got != "10.100.0.1" {
				t.Fatalf("answer = %s, want the bridge", got)
			}
		})
	}
}

// AAAA stays authoritatively empty: service addresses are IPv4 only, and an
// upstream must never invent a route for a private name.
func TestServiceDiscoveryDNSServiceAddressIsIPv4Only(t *testing.T) {
	lookup := func(context.Context, string, string) (netip.Addr, bool) {
		return netip.MustParseAddr("198.19.0.7"), true
	}
	msg := serviceAddressDNS(t, lookup, dns.Question{Name: "db.svc.gregale.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET})
	if len(msg.Answer) != 0 || !msg.Authoritative {
		t.Fatalf("AAAA response = %v (authoritative=%v), want an empty authoritative answer", msg.Answer, msg.Authoritative)
	}
}
