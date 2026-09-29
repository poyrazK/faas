// adr: 370 — guest DNS blocklist.
package gateway

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestDNSBlocklistMatch(t *testing.T) {
	b := NewDNSBlocklist(map[string]string{"evil.example": "c2", " Bad.Example. ": "malware"})
	for _, tc := range []struct {
		name, category string
		blocked        bool
	}{
		{"xmr-eu1.nanopool.org.", "miner", true},
		{"NANOPOOL.ORG", "miner", true},
		{"notnanopool.org.", "", false},
		{"a.b.evil.example.", "c2", true},
		{"bad.example", "malware", true},
		{"example.", "", false},
		{"api.stripe.com.", "", false},
	} {
		category, blocked := b.Match(tc.name)
		if blocked != tc.blocked || category != tc.category {
			t.Errorf("Match(%q) = %q, %v; want %q, %v", tc.name, category, blocked, tc.category, tc.blocked)
		}
	}
	var nilList *DNSBlocklist
	if _, blocked := nilList.Match("nanopool.org"); blocked {
		t.Fatal("a nil blocklist must block nothing")
	}
}

func TestParseDNSBlocklist(t *testing.T) {
	got, err := parseDNSBlocklist(strings.NewReader("# threat feed\n\nc2.example malware\nphish.example\n"))
	if err != nil || got["c2.example"] != "malware" || got["phish.example"] != "operator" || len(got) != 2 {
		t.Fatalf("parse = %v, %v", got, err)
	}
	if _, err := parseDNSBlocklist(strings.NewReader("localhost\n")); err == nil {
		t.Fatal("a bare label must be rejected")
	}
}

// A blocked name is answered NXDOMAIN without reaching the upstream, and the
// callback sees its category.
func TestServiceDiscoveryDNSRefusesBlockedNames(t *testing.T) {
	upstream := "127.0.0.1:1" // nothing listens; forwarding would SERVFAIL
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{upstream}, nil,
		func(context.Context, string) (string, error) { return "app-1", nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	var categories []string
	h.WithBlocklist(NewDNSBlocklist(nil), func(c string) { categories = append(categories, c) })
	req := new(dns.Msg)
	req.SetQuestion("pool.supportxmr.com.", dns.TypeA)
	w := &serviceDiscoveryResponseWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.100.0.5"), Port: 5353}}
	h.ServeDNS(w, req)
	if w.msg == nil || w.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("response = %#v, want NXDOMAIN", w.msg)
	}
	if len(categories) != 1 || categories[0] != "miner" {
		t.Fatalf("onBlocked calls = %v, want [miner]", categories)
	}
}
