package oci

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

// TestAllowIP_Table exercises every deny class the egress guard supports.
// Public IPs must pass; everything else must fail. Adding a new deny class
// to AllowIP requires a row here.
func TestAllowIP_Table(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		// Public — must allow.
		{"public v4", "8.8.8.8", true},
		{"public v6", "2606:4700:4700::1111", true},
		{"github A record", "140.82.112.4", true},
		{"docker hub", "52.5.62.100", true},
		// RFC1918 (v4).
		{"10/8", "10.0.0.5", false},
		{"10/8 large", "10.255.255.254", false},
		{"172.16/12", "172.16.0.1", false},
		{"172.16/12 large", "172.31.255.254", false},
		{"192.168/16", "192.168.1.1", false},
		{"169.254/16 link-local + metadata", "169.254.169.254", false}, // AWS / GCP metadata!
		{"100.64/10 CGN", "100.64.0.1", false},
		// Specials.
		{"v4 unspecified", "0.0.0.0", false},
		{"v4 broadcast", "255.255.255.255", false},
		{"v4 loopback", "127.0.0.1", false},
		{"v4 multicast", "224.0.0.1", false},
		// IPv6 specials.
		{"v6 unspecified", "::", false},
		{"v6 loopback", "::1", false},
		{"v6 link-local", "fe80::1", false},
		{"v6 ULA fc00", "fc00::1", false},
		{"v6 ULA fd00", "fd12:3456:789a::1", false},
		{"v6 multicast", "ff02::1", false},
		// IPv4-mapped IPv6 — most important bypass check.
		{"v4-mapped RFC1918", "::ffff:10.0.0.5", false},
		{"v4-mapped loopback", "::ffff:127.0.0.1", false},
		{"v4-mapped link-local", "::ffff:169.254.169.254", false},
		// nil.
		{"nil ip", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ip net.IP
			if c.ip != "" {
				ip = net.ParseIP(c.ip)
			}
			if ip == nil && c.want {
				t.Fatalf("test setup: bad IP %q", c.ip)
			}
			got := AllowIP(ip)
			if got != c.want {
				t.Errorf("AllowIP(%q) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// TestEgressGuardMatchesHostPolicy asserts the OCI guard's IPv4 deny-list
// stays in sync with pkg/netns.DefaultHostPolicy.ForwardDenyCIDRs. If
// anyone edits one and forgets the other, CI fails here — that's the
// only thing keeping two layers of deny policy consistent.
//
// We compare string form rather than *net.IPNet equality so the test is
// human-readable when it fails.
func TestEgressGuardMatchesHostPolicy(t *testing.T) {
	ociSet := make(map[string]struct{}, len(deniedCIDRs))
	for _, n := range deniedCIDRs {
		ociSet[n.String()] = struct{}{}
	}
	hostSet := make(map[string]struct{}, len(netns.DefaultHostPolicy.ForwardDenyCIDRs))
	for _, c := range netns.DefaultHostPolicy.ForwardDenyCIDRs {
		hostSet[c] = struct{}{}
	}
	for c := range hostSet {
		if _, ok := ociSet[c]; !ok {
			t.Errorf("OCE guard deny-list missing CIDR %s (present in pkg/netns.DefaultHostPolicy)", c)
		}
	}
	for c := range ociSet {
		if _, ok := hostSet[c]; !ok {
			t.Errorf("OCI guard deny-list contains %s not in pkg/netns.DefaultHostPolicy (drift)", c)
		}
	}
}

// fakeResolver implements resolverIface and returns whatever IPs the
// test planted. Used to simulate DNS-rebinding: the host name the caller
// asked for resolves to a private IP the guard should refuse. Production
// wires *net.Resolver; tests inject this stub.
type fakeResolver struct {
	ips []net.IPAddr
	err error
}

func (f *fakeResolver) LookupIPAddr(ctx context.Context, _ string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ips, nil
}

// TestDialContext_DenyRFC1918 exercises the dial-end of the guard end-to-end:
// a fake resolver returns RFC1918 IPs and the dial must fail with ErrEgressDenied
// wrapped in *net.OpError. The http.Transport wires this through errors.Is
// for typed matching.
func TestDialContext_DenyRFC1918(t *testing.T) {
	d := &dialer{
		resolver: &fakeResolver{
			ips: []net.IPAddr{
				{IP: net.ParseIP("10.0.0.7")},
				{IP: net.ParseIP("192.168.1.1")},
			},
		},
	}
	_, err := d.dialContext(context.Background(), "tcp", "registry.example.com:443")
	if err == nil {
		t.Fatal("expected ErrEgressDenied for RFC1918 resolution")
	}
	if !errors.Is(err, ErrEgressDenied) {
		t.Errorf("err = %v, want ErrEgressDenied in chain", err)
	}
	if !strings.Contains(err.Error(), "registry.example.com") {
		t.Errorf("error should name the host; got %v", err)
	}
}

// TestDialContext_DenySMTP — the port-side guard. A dial to :25 must fail
// regardless of where the IP resolves to.
func TestDialContext_DenySMTP(t *testing.T) {
	d := &dialer{
		resolver: &fakeResolver{
			ips: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, // public, doesn't matter
		},
	}
	for _, port := range []string{"25", "465", "587"} {
		_, err := d.dialContext(context.Background(), "tcp", "spammer.example.com:"+port)
		if err == nil {
			t.Errorf("expected ErrEgressDenied for SMTP port %s", port)
			continue
		}
		if !errors.Is(err, ErrEgressDenied) {
			t.Errorf("port %s: err = %v, want ErrEgressDenied", port, err)
		}
	}
}

// TestWithEgressHTTPClient_InstallsTransport is a positive-path smoke
// test: the option wires a guarded *http.Transport without panicking.
// The dial-then-succeeds case can't be unit-tested cheaply because the
// guard refuses loopback — which is what httptest.NewServer uses. The
// production dial path is exercised end-to-end in the //go:build metal
// suite (TestMetalEgressDial_PublicHost — to be added when the metal
// netns harness gains an off-loopback link), and the negative paths
// above prove the deny logic on every deny class.
func TestWithEgressHTTPClient_InstallsTransport(t *testing.T) {
	// Smoke test: the option must not panic and must leave the client
	// configured. A real "dial refused for RFC1918" E2E test belongs in a
	// //go:build metal with a fake listener inside a non-loopback netns.
	c := NewRegistryClient(WithEgressHTTPClient())
	if c.hc == nil {
		t.Fatal("WithEgressHTTPClient did not install an http.Client")
	}
	if c.hc.Timeout == 0 {
		t.Error("http.Client has zero Timeout; would block forever")
	}
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", c.hc.Transport)
	}
	if tr.DialContext == nil {
		t.Error("Transport.DialContext is nil; the guard was not wired")
	}
}

// pin (compiler): ensure httptest is referenced (used elsewhere in the package).
var _ = httptest.NewServer
