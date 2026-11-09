package domaindns_test

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/apid/domaindns"
)

func dnsServer(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", packet.LocalAddr().String())
	if err != nil {
		_ = packet.Close()
		t.Fatal(err)
	}
	udp := &dns.Server{PacketConn: packet, Handler: handler}
	tcp := &dns.Server{Listener: listener, Handler: handler}
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	t.Cleanup(func() { _ = udp.Shutdown(); _ = tcp.Shutdown() })
	return packet.LocalAddr().String()
}

func TestWildcardPermissionHonorsIssuewildAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name             string
		records          []string
		permits, unknown bool
	}{
		{"wildcard denied", []string{`0 issue "letsencrypt.org"`, `0 issuewild ";"`}, false, false},
		{"wildcard overrides issue", []string{`0 issue ";"`, `0 issuewild "letsencrypt.org"`}, true, false},
		{"fallback to issue", []string{`0 issue "letsencrypt.org; validationmethods=dns-01"`}, true, false},
		{"selected methods denied", []string{`0 issue "letsencrypt.org"`, `0 issuewild "letsencrypt.org; validationmethods=http-01"`}, false, false},
		{"account uncertain", []string{`0 issuewild "letsencrypt.org; accounturi=https://acme.example/acct/1"`}, false, true},
		{"critical tag", []string{`0 issuewild "letsencrypt.org"`, `128 unknown "value"`}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domaindns.LetsEncryptWildcardPermission(tc.records, []string{"dns-01"})
			if got.Permits != tc.permits || got.Unknown != tc.unknown {
				t.Fatalf("wildcard permission=%+v", got)
			}
		})
	}
}

func TestLookupCAAQueriesRealRecordsAndFollowsInheritance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rrs     map[string]string
		want    []string
		wantErr bool
	}{
		{"direct", map[string]string{"app.example.com.": `app.example.com. 60 IN CAA 0 issue "digicert.com"`}, []string{`0 issue "digicert.com"`}, false},
		{"parent", map[string]string{"example.com.": `example.com. 60 IN CAA 0 issue "letsencrypt.org"`}, []string{`0 issue "letsencrypt.org"`}, false},
		{"alias", map[string]string{"app.example.com.": `app.example.com. 60 IN CNAME edge.customer.net.`, "edge.customer.net.": `edge.customer.net. 60 IN CAA 0 issue ";"`}, []string{`0 issue ";"`}, false},
		{"alias without CAA uses original parent", map[string]string{"app.example.com.": `app.example.com. 60 IN CNAME edge.customer.net.`, "customer.net.": `customer.net. 60 IN CAA 0 issue ";"`, "example.com.": `example.com. 60 IN CAA 0 issue "letsencrypt.org"`}, []string{`0 issue "letsencrypt.org"`}, false},
		{"missing", nil, nil, false},
		{"alias loop", map[string]string{"app.example.com.": `app.example.com. 60 IN CNAME other.example.com.`, "other.example.com.": `other.example.com. 60 IN CNAME app.example.com.`}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := dnsServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
				if req.Question[0].Qtype != dns.TypeCAA {
					t.Error("query did not request CAA")
				}
				response := new(dns.Msg).SetReply(req)
				if raw := tc.rrs[req.Question[0].Name]; raw != "" {
					rr, err := dns.NewRR(raw)
					if err != nil {
						t.Error(err)
					} else {
						response.Answer = []dns.RR{rr}
					}
				}
				_ = w.WriteMsg(response)
			})
			got, err := domaindns.LookupCAAAt(t.Context(), "app.example.com", []string{server})
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("records=%v err=%v, want %v error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestLookupCAATCPFallbackAndResolverFailure(t *testing.T) {
	broken := dnsServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
		_ = w.WriteMsg(new(dns.Msg).SetRcode(req, dns.RcodeServerFailure))
	})
	if _, err := domaindns.LookupCAAAt(t.Context(), "example.com", []string{broken}); err == nil {
		t.Fatal("SERVFAIL was treated as absent CAA")
	}
	server := dnsServer(t, func(w dns.ResponseWriter, req *dns.Msg) {
		response := new(dns.Msg).SetReply(req)
		if w.RemoteAddr().Network() == "udp" {
			response.Truncated = true
		} else {
			rr, _ := dns.NewRR(`example.com. 60 IN CAA 0 issue "letsencrypt.org"`)
			response.Answer = []dns.RR{rr}
		}
		_ = w.WriteMsg(response)
	})
	got, err := domaindns.LookupCAAAt(t.Context(), "example.com", []string{broken, server})
	if err != nil || len(got) != 1 {
		t.Fatalf("TCP/resolver fallback: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := domaindns.LookupCAAAt(ctx, "example.com", []string{server}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestPermitsLetsEncryptConcreteCertificates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []string
		want    bool
	}{
		{"absent", nil, true},
		{"lets encrypt", []string{`0 issue "letsencrypt.org"`}, true},
		{"different CA", []string{`0 issue "digicert.com"`}, false},
		{"deny all", []string{`0 issue ";"`}, false},
		{"empty issuer", []string{`0 issue ""`}, false},
		{"alternative issuer", []string{`0 issue "digicert.com"`, `0 issue "letsencrypt.org"`}, true},
		{"wildcard cannot override exact denial", []string{`0 issue ";"`, `0 issuewild "letsencrypt.org"`}, false},
		{"only wildcard constrained", []string{`0 issuewild ";"`}, true},
		{"reporting only", []string{`0 iodef "mailto:security@example.com"`}, true},
		{"unknown critical tag", []string{`128 future "value"`}, false},
		{"unknown noncritical tag", []string{`0 future "value"`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := domaindns.PermitsLetsEncrypt(tc.records)
			if got != tc.want {
				t.Fatalf("permitted=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestLetsEncryptPermissionHonorsIssuerRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name             string
		records, methods []string
		allow, unknown   bool
	}{
		{"HTTP mode rejects DNS only", []string{`0 issue "letsencrypt.org; validationmethods=dns-01"`}, []string{"http-01", "tls-alpn-01"}, false, false},
		{"legacy DNS mode", []string{`0 issue "letsencrypt.org; validationmethods=dns-01"`}, []string{"dns-01"}, true, false},
		{"HTTP allowed alternative", []string{`0 issue "letsencrypt.org; validationmethods=dns-01,http-01"`}, []string{"http-01"}, true, false},
		{"ALPN mode", []string{`0 issue "letsencrypt.org; validationmethods=tls-alpn-01"`}, []string{"tls-alpn-01"}, true, false},
		{"account needs confirmation", []string{`0 issue "letsencrypt.org; accounturi=https://acme.example/acct/123"`}, []string{"http-01"}, false, true},
		{"unrestricted record wins", []string{`0 issue "letsencrypt.org; accounturi=https://acme.example/acct/123"`, `0 issue "letsencrypt.org"`}, []string{"http-01"}, true, false},
		{"unsupported method denies despite account", []string{`0 issue "letsencrypt.org; accounturi=https://acme.example/acct/123; validationmethods=dns-01"`}, []string{"http-01"}, false, false},
		{"duplicate parameters", []string{`0 issue "letsencrypt.org; validationmethods=http-01; validationmethods=dns-01"`}, []string{"http-01"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domaindns.LetsEncryptPermission(tc.records, tc.methods)
			if got.Permits != tc.allow || got.Unknown != tc.unknown {
				t.Fatalf("permission=%+v, want allowed=%v unknown=%v", got, tc.allow, tc.unknown)
			}
		})
	}
}
