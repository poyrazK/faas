// adr: 170
package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

type serviceDiscoveryResponseWriter struct {
	msg    *dns.Msg
	remote net.Addr
}

func (w *serviceDiscoveryResponseWriter) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (w *serviceDiscoveryResponseWriter) RemoteAddr() net.Addr        { return w.remote }
func (w *serviceDiscoveryResponseWriter) WriteMsg(msg *dns.Msg) error { w.msg = msg; return nil }
func (w *serviceDiscoveryResponseWriter) Write([]byte) (int, error)   { return 0, nil }
func (w *serviceDiscoveryResponseWriter) Close() error                { return nil }
func (w *serviceDiscoveryResponseWriter) TsigStatus() error           { return nil }
func (w *serviceDiscoveryResponseWriter) TsigTimersOnly(bool)         {}
func (w *serviceDiscoveryResponseWriter) Hijack()                     {}

func TestServiceDiscoveryDNSAnswersPrivateNames(t *testing.T) {
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := new(dns.Msg)
	req.SetQuestion("orders.svc.gregale.", dns.TypeA)
	w := &serviceDiscoveryResponseWriter{}
	h.ServeDNS(w, req)
	if w.msg == nil || len(w.msg.Answer) != 1 {
		t.Fatalf("answers = %#v, want one A record", w.msg)
	}
	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok || a.A.String() != "10.100.0.1" {
		t.Fatalf("answer = %#v, want 10.100.0.1", w.msg.Answer[0])
	}
	if !w.msg.Authoritative {
		t.Fatal("private answer is not authoritative")
	}
}

func TestServiceDiscoveryDNSDoesNotClaimExternalNames(t *testing.T) {
	if got := serviceDiscoveryName("orders.example.com."); got != "" {
		t.Fatalf("external name parsed as %q", got)
	}
	if got := serviceDiscoveryName("orders.api.svc.gregale."); got != "" {
		t.Fatalf("multi-label service parsed as %q", got)
	}
	if got := serviceAliasName("BILLING.INTERNAL."); got != "billing" {
		t.Fatalf("alias = %q, want billing", got)
	}
	for _, name := range []string{"billing.other.internal.", "-billing.internal.", "internal."} {
		if got := serviceAliasName(name); got != "" {
			t.Errorf("invalid alias %q parsed as %q", name, got)
		}
	}
}

func TestServiceDiscoveryDNSOnlyClaimsBoundAliases(t *testing.T) {
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
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET},
			A:   net.ParseIP("192.0.2.5").To4(),
		})
		_ = w.WriteMsg(msg)
	})}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() { _ = upstream.Shutdown() })

	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{packet.LocalAddr().String()}, nil,
		func(_ context.Context, remote string) (string, error) {
			if remote != "10.100.0.5:54321" {
				t.Errorf("remote = %q", remote)
			}
			return "caller", nil
		},
		func(_ context.Context, caller, service string) (bool, error) {
			return caller == "caller" && service == "billing", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	query := func(names ...string) *dns.Msg {
		t.Helper()
		req := new(dns.Msg)
		req.SetQuestion(names[0], dns.TypeA)
		for _, name := range names[1:] {
			req.Question = append(req.Question, dns.Question{Name: name, Qtype: dns.TypeA, Qclass: dns.ClassINET})
		}
		w := &serviceDiscoveryResponseWriter{remote: &net.UDPAddr{IP: net.ParseIP("10.100.0.5"), Port: 54321}}
		h.ServeDNS(w, req)
		if w.msg == nil {
			t.Fatal("no DNS response")
		}
		return w.msg
	}
	bound := query("billing.internal.")
	if !bound.Authoritative || len(bound.Answer) != 1 || bound.Answer[0].(*dns.A).A.String() != "10.100.0.1" {
		t.Fatalf("bound alias response = %#v", bound)
	}
	for _, name := range []string{"identity.internal.", "printer.office.internal."} {
		unbound := query(name)
		if unbound.Authoritative || len(unbound.Answer) != 1 || unbound.Answer[0].(*dns.A).A.String() != "192.0.2.5" {
			t.Fatalf("unbound %q response = %#v", name, unbound)
		}
	}
	before := forwarded.Load()
	mixed := query("billing.internal.", "identity.internal.")
	if mixed.Rcode != dns.RcodeFormatError || forwarded.Load() != before {
		t.Fatalf("mixed response = %#v, forwarded %d -> %d", mixed, before, forwarded.Load())
	}
	if forwarded.Load() != 2 {
		t.Fatalf("forwarded queries = %d, want 2", forwarded.Load())
	}
	h.resolveCaller = func(context.Context, string) (string, error) { return "", nil }
	unknown := query("billing.internal.")
	if unknown.Authoritative || len(unknown.Answer) != 1 || unknown.Answer[0].(*dns.A).A.String() != "192.0.2.5" {
		t.Fatalf("unknown caller received private alias: %#v", unknown)
	}
}

func TestServiceDiscoveryDNSAliasLookupFailureIsNotForwarded(t *testing.T) {
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil,
		func(context.Context, string) (string, error) { return "caller", nil },
		func(context.Context, string, string) (bool, error) { return false, errors.New("store unavailable") })
	if err != nil {
		t.Fatal(err)
	}
	req := new(dns.Msg)
	req.SetQuestion("billing.internal.", dns.TypeA)
	w := &serviceDiscoveryResponseWriter{}
	h.ServeDNS(w, req)
	if w.msg == nil || w.msg.Rcode != dns.RcodeServerFailure {
		t.Fatalf("response = %#v, want SERVFAIL", w.msg)
	}
}
