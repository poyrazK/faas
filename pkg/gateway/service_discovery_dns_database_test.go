// adr: 1027
package gateway

import (
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const testDatabaseName = "0b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e.db.gregale.test."

func databaseRouteHandler(t *testing.T) *ServiceDiscoveryDNSHandler {
	t.Helper()
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.WithDatabaseRoute(".DB.Gregale.Test.", netip.MustParseAddr("198.18.0.1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestServiceDiscoveryDNSAnswersDatabaseRoute(t *testing.T) {
	h := databaseRouteHandler(t)
	for _, name := range []string{testDatabaseName, "0B7D3C5E-8F43-4C7A-9D0E-5A1F2B3C4D5E.DB.GREGALE.TEST."} {
		req := new(dns.Msg)
		req.SetQuestion(name, dns.TypeA)
		w := &serviceDiscoveryResponseWriter{}
		h.ServeDNS(w, req)
		if w.msg == nil || len(w.msg.Answer) != 1 || !w.msg.Authoritative {
			t.Fatalf("%s: response = %v", name, w.msg)
		}
		a, ok := w.msg.Answer[0].(*dns.A)
		if !ok || a.A.String() != "198.18.0.1" || a.Hdr.Ttl != 60 {
			t.Fatalf("%s: answer = %v, want 198.18.0.1 for 60 s", name, w.msg.Answer[0])
		}
	}

	req := new(dns.Msg)
	req.SetQuestion(testDatabaseName, dns.TypeAAAA)
	w := &serviceDiscoveryResponseWriter{}
	h.ServeDNS(w, req)
	if w.msg == nil || len(w.msg.Answer) != 0 || w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("AAAA = %v, want an empty authoritative answer so clients stay on IPv4", w.msg)
	}

	// A database name never rides along with an external one to upstream.
	mixed := new(dns.Msg)
	mixed.SetQuestion(testDatabaseName, dns.TypeA)
	mixed.Question = append(mixed.Question, dns.Question{Name: "example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	w = &serviceDiscoveryResponseWriter{}
	h.ServeDNS(w, mixed)
	if w.msg == nil || w.msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("mixed question = %v, want FORMERR", w.msg)
	}
}

func TestServiceDiscoveryDNSDatabaseRouteClaimsOnlyOneLabel(t *testing.T) {
	h := databaseRouteHandler(t)
	for _, name := range []string{"db.gregale.test.", "a.b.db.gregale.test.", "x.db.gregale.test.evil.", "x.gregale.test.", "-x.db.gregale.test."} {
		if h.databaseRouteName(name) {
			t.Errorf("%s claimed as a database name", name)
		}
	}
	off, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if off.databaseRouteName(testDatabaseName) {
		t.Fatal("a handler without the route claimed a database name")
	}
	for name, suffix := range map[string]string{"empty": "", "bad label": "db.gre_gale.test", "dot only": "."} {
		if _, err := off.WithDatabaseRoute(suffix, netip.MustParseAddr("198.18.0.1"), time.Minute); err == nil {
			t.Errorf("%s suffix accepted", name)
		}
	}
	if _, err := off.WithDatabaseRoute("db.gregale.test", netip.MustParseAddr("fd00::1"), time.Minute); err == nil {
		t.Error("IPv6 route address accepted")
	}
}
