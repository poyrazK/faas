// adr: 20261031193635
package gateway

import (
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

// Slice 6c: the bridge object relay's name resolves to this node's bridge
// address, authoritatively, and never rides along to the upstream resolver.
func TestServiceDiscoveryDNSAnswersObjectRelayName(t *testing.T) {
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.WithObjectRelayName("Objects.Gregale.")
	for _, name := range []string{"objects.gregale.", "OBJECTS.GREGALE."} {
		req := new(dns.Msg)
		req.SetQuestion(name, dns.TypeA)
		w := &serviceDiscoveryResponseWriter{}
		h.ServeDNS(w, req)
		if w.msg == nil || len(w.msg.Answer) != 1 || !w.msg.Authoritative {
			t.Fatalf("%s: response = %v", name, w.msg)
		}
		if a, ok := w.msg.Answer[0].(*dns.A); !ok || a.A.String() != "10.100.0.1" {
			t.Fatalf("%s: answer = %v, want the bridge address", name, w.msg.Answer[0])
		}
	}
	mixed := new(dns.Msg)
	mixed.SetQuestion("objects.gregale.", dns.TypeA)
	mixed.Question = append(mixed.Question, dns.Question{Name: "example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	w := &serviceDiscoveryResponseWriter{}
	h.ServeDNS(w, mixed)
	if w.msg == nil || w.msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("mixed question = %v, want FORMERR", w.msg)
	}
	for _, name := range []string{"x.objects.gregale.", "objects.gregale.evil.", "gregale."} {
		if h.objectRelayName(name) {
			t.Errorf("%s claimed as the relay name", name)
		}
	}
	off, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if off.objectRelayName("objects.gregale.") {
		t.Fatal("a handler without the relay claimed its name")
	}
}
