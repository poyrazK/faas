// adr: 373 — DNS-gated egress; 20261104160000 — grant owner fences.
package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// rc.324 refused every builder's DNS grants and the resolver said nothing. A
// refused grant is a warning, logged once per guest per interval because a
// guest retries a lookup whose answer it cannot reach.
func TestServiceDiscoveryDNSWarnsOnceWhenTheGrantIsRefused(t *testing.T) {
	upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "104.16.212.131")}
		_ = w.WriteMsg(resp)
	}))
	var logs bytes.Buffer
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{upstream}, slog.New(slog.NewJSONHandler(&logs, nil)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.WithResolvedEgressPreparation(func(context.Context, string, string) (ResolvedEgressHook, error) {
		return func(context.Context, string, string, []netip.Addr, time.Duration) error {
			return errors.New("vmmd refused the DNS grant")
		}, nil
	})
	resolver := startServiceDNSFixture(t, h)
	for range 3 {
		if resp := queryServiceDNSFixture(t, "udp", resolver, 0); resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
			t.Fatalf("refused grant changed the answer: %v", resp)
		}
	}
	if got := strings.Count(logs.String(), "guest DNS answer not registered for egress"); got != 1 {
		t.Fatalf("warnings = %d, want 1:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), `"name":"allowed.example"`) || !strings.Contains(logs.String(), "vmmd refused the DNS grant") {
		t.Fatalf("warning lacks the name or the refusal:\n%s", logs.String())
	}
	if !h.egressWarns.admit("grant", "127.0.0.1:53", time.Now().Add(guestDNSWarnInterval)) {
		t.Fatal("the guest's next interval was not admitted")
	}
}
