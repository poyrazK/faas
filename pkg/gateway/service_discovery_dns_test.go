package gateway

import (
	"net"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

type serviceDiscoveryResponseWriter struct {
	msg *dns.Msg
}

func (w *serviceDiscoveryResponseWriter) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (w *serviceDiscoveryResponseWriter) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (w *serviceDiscoveryResponseWriter) WriteMsg(msg *dns.Msg) error { w.msg = msg; return nil }
func (w *serviceDiscoveryResponseWriter) Write([]byte) (int, error)   { return 0, nil }
func (w *serviceDiscoveryResponseWriter) Close() error                { return nil }
func (w *serviceDiscoveryResponseWriter) TsigStatus() error           { return nil }
func (w *serviceDiscoveryResponseWriter) TsigTimersOnly(bool)         {}
func (w *serviceDiscoveryResponseWriter) Hijack()                     {}

func TestServiceDiscoveryDNSAnswersPrivateNames(t *testing.T) {
	h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), nil, nil)
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
}
