// adr: 20261117185346 §4 — the politeness proxy names a connection's
// destination from its SNI or Host without decrypting, serves only VMs a
// node registered in its own slice, and refuses past a cap, a blocklisted
// domain or a non-public destination.
package agentproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func clientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		c := tls.Client(a, &tls.Config{ServerName: serverName, InsecureSkipVerify: serverName == ""})
		_ = c.Handshake()
	}()
	_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
	var buf bytes.Buffer
	chunk := make([]byte, 4096)
	for {
		n, err := b.Read(chunk)
		buf.Write(chunk[:n])
		if err != nil || (buf.Len() > 5 && buf.Len() >= 5+(int(buf.Bytes()[3])<<8|int(buf.Bytes()[4]))) {
			break
		}
	}
	_ = a.Close()
	_ = b.Close()
	return buf.Bytes()
}

func TestPeekTLS(t *testing.T) {
	hello := clientHello(t, "docs.example.com")
	name, raw, err := peekTLS(bytes.NewReader(hello))
	if err != nil || name != "docs.example.com" || !bytes.Equal(raw, hello) {
		t.Fatalf("peekTLS = %q, %d bytes, %v", name, len(raw), err)
	}
	// Split across two records, as a large hello may be.
	body := hello[5:]
	split := append([]byte{0x16, hello[1], hello[2], 0, 40}, body[:40]...)
	rest := body[40:]
	split = append(split, 0x16, hello[1], hello[2], byte(len(rest)>>8), byte(len(rest)))
	split = append(split, rest...)
	if name, _, err := peekTLS(bytes.NewReader(split)); err != nil || name != "docs.example.com" {
		t.Fatalf("split hello = %q, %v", name, err)
	}
	if _, _, err := peekTLS(bytes.NewReader(clientHello(t, ""))); err == nil {
		t.Fatal("a hello without SNI was named")
	}
	for _, bad := range [][]byte{[]byte("GET / HTTP/1.1\r\n"), {0x16, 3, 1, 0, 2, 1, 0}, {0x16, 3, 1, 0xff, 0xff}} {
		if _, _, err := peekTLS(bytes.NewReader(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPeekHTTP(t *testing.T) {
	req := "GET /a HTTP/1.1\r\nUser-Agent: x\r\nhost: Example.com:8080\r\n\r\nbody"
	host, raw, err := peekHTTP(strings.NewReader(req))
	if err != nil || host != "Example.com" || !strings.HasPrefix(req, string(raw)) {
		t.Fatalf("peekHTTP = %q %q %v", host, raw, err)
	}
	if _, _, err := peekHTTP(strings.NewReader("GET / HTTP/1.0\r\n\r\n")); err == nil {
		t.Fatal("no Host accepted")
	}
	if _, _, err := peekHTTP(strings.NewReader("GET / HTTP/1.1\r\nX: " + strings.Repeat("a", 9000) + "\r\n\r\n")); err == nil {
		t.Fatal("an oversized head accepted")
	}
}

func testRegistry(now *time.Time) *Registry {
	return NewRegistry(Slices{Tunnel: netip.MustParsePrefix("10.78.0.0/16"), NodePrefix: 22}, time.Minute, func() time.Time { return *now })
}

func TestRegistry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := testRegistry(&now)
	node, vm := netip.MustParseAddr("10.78.8.1"), netip.MustParseAddr("10.78.8.9")
	if _, err := r.Register(node, vm, "acct", "sb"); err != nil {
		t.Fatal(err)
	}
	if reg, ok := r.Lookup(vm); !ok || reg.AccountID != "acct" || reg.Node != node {
		t.Fatalf("lookup = %+v %v", reg, ok)
	}
	for name, tc := range map[string][2]string{
		"another node's slice": {"10.78.4.1", "10.78.8.10"},
		"the node itself":      {"10.78.8.1", "10.78.8.1"},
		"not a node":           {"10.78.8.9", "10.78.8.10"},
		"the gateway's slice":  {"10.78.0.1", "10.78.0.5"},
		"outside the tunnel":   {"10.79.8.1", "10.79.8.9"},
	} {
		if _, err := r.Register(netip.MustParseAddr(tc[0]), netip.MustParseAddr(tc[1]), "acct", "sb"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := r.Unregister(netip.MustParseAddr("10.78.4.1"), vm); err == nil {
		t.Fatal("another node unregistered a VM")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := r.Lookup(vm); ok {
		t.Fatal("an expired registration served")
	}
}

func TestLimiterCaps(t *testing.T) {
	l := newLimiter(Caps{PerVM: 2, PerAccountDomain: 2, PerDomainFleet: 3})
	a, b := netip.MustParseAddr("10.78.8.2"), netip.MustParseAddr("10.78.8.3")
	r1, _ := l.acquire(a, "acct", "example.com")
	r2, _ := l.acquire(a, "acct", "example.org")
	if _, why := l.acquire(a, "acct", "example.net"); why != OutcomeCapVM {
		t.Fatalf("third from one VM = %q", why)
	}
	r3, _ := l.acquire(b, "acct", "example.com")
	if _, why := l.acquire(b, "acct", "example.com"); why != OutcomeCapAccountDomain {
		t.Fatalf("third for one account and domain = %q", why)
	}
	r4, _ := l.acquire(b, "other", "example.com")
	if _, why := l.acquire(netip.MustParseAddr("10.78.8.4"), "third", "example.com"); why != OutcomeCapDomainFleet {
		t.Fatalf("fourth for one domain = %q", why)
	}
	for _, r := range []func(){r1, r2, r3, r4, r4} {
		r()
	}
	if len(l.vm)+len(l.accountDomain)+len(l.domain) != 0 {
		t.Fatalf("released counts remain: %v %v %v", l.vm, l.accountDomain, l.domain)
	}
}

func TestBlocklist(t *testing.T) {
	var b Blocklist
	if err := b.Load(strings.NewReader("# complaints\nEvil.example.\n\nads.example.org # tracker\n")); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"evil.example": true, "cdn.evil.example": true, "example": false, "ads.example.org": true, "example.org": false} {
		if b.Blocks(host) != want {
			t.Errorf("Blocks(%s) = %v", host, !want)
		}
	}
	if err := b.Load(strings.NewReader("10.0.0.1\n")); err == nil || b.Len() != 2 {
		t.Fatal("an address was listed, or a failed load replaced the list")
	}
}

// proxyHarness proxies localhost connections, sending them to a local
// upstream that echoes the first line of what it receives.
func proxyHarness(t *testing.T, dst netip.AddrPort, caps Caps, block string) (dial func() net.Conn, registry *Registry) {
	t.Helper()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				_, _ = io.WriteString(c, "upstream saw "+line)
			}()
		}
	}()
	now := time.Now()
	registry = testRegistry(&now)
	registry.regs[netip.MustParseAddr("127.0.0.1")] = Registration{Addr: netip.MustParseAddr("127.0.0.1"), AccountID: "acct",
		SandboxID: "sb", Expires: now.Add(time.Hour)}
	blocklist := &Blocklist{}
	_ = blocklist.Load(strings.NewReader(block))
	p := &Proxy{Registry: registry, Blocklist: blocklist, Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		OriginalDestination: func(net.Conn) (netip.AddrPort, error) { return dst, nil },
		Dial: func(ctx context.Context, _ netip.Addr, _ netip.AddrPort) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", upstream.Addr().String())
		},
		PeekTimeout: 2 * time.Second}
	p.Configure(caps)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Serve(ctx, ln) }()
	return func() net.Conn {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		return c
	}, registry
}

func roundTrip(t *testing.T, c net.Conn, req string) string {
	t.Helper()
	defer c.Close()
	_, _ = io.WriteString(c, req)
	out, _ := io.ReadAll(c)
	return string(out)
}

func TestProxyCarriesARegisteredVMsHTTP(t *testing.T) {
	public := netip.MustParseAddrPort("93.184.216.34:80")
	dial, registry := proxyHarness(t, public, Caps{PerVM: 4, PerAccountDomain: 4, PerDomainFleet: 4}, "blocked.example\n")
	if got := roundTrip(t, dial(), "GET / HTTP/1.1\r\nHost: www.example.com\r\n\r\n"); got != "upstream saw GET / HTTP/1.1\r\n" {
		t.Fatalf("proxied = %q", got)
	}
	for name, req := range map[string]string{
		"blocklisted": "GET / HTTP/1.1\r\nHost: cdn.blocked.example\r\n\r\n",
		"an address":  "GET / HTTP/1.1\r\nHost: 93.184.216.34\r\n\r\n",
		"no host":     "GET / HTTP/1.0\r\n\r\n",
	} {
		if got := roundTrip(t, dial(), req); got != "" {
			t.Errorf("%s: proxied %q", name, got)
		}
	}
	registry.mu.Lock()
	registry.regs = map[netip.Addr]Registration{}
	registry.mu.Unlock()
	if got := roundTrip(t, dial(), "GET / HTTP/1.1\r\nHost: www.example.com\r\n\r\n"); got != "" {
		t.Fatalf("unregistered VM proxied %q", got)
	}
}

func TestProxyRefusesPastACapAndNonPublicDestinations(t *testing.T) {
	dial, _ := proxyHarness(t, netip.MustParseAddrPort("93.184.216.34:80"), Caps{PerVM: 0, PerAccountDomain: 4, PerDomainFleet: 4}, "")
	if got := roundTrip(t, dial(), "GET / HTTP/1.1\r\nHost: www.example.com\r\n\r\n"); got != "" {
		t.Fatalf("over the VM cap proxied %q", got)
	}
	for _, dst := range []string{"10.0.0.5:80", "169.254.169.254:80", "93.184.216.34:22", "100.64.0.1:443"} {
		dial, _ := proxyHarness(t, netip.MustParseAddrPort(dst), Caps{PerVM: 4, PerAccountDomain: 4, PerDomainFleet: 4}, "")
		if got := roundTrip(t, dial(), "GET / HTTP/1.1\r\nHost: www.example.com\r\n\r\n"); got != "" {
			t.Errorf("%s: proxied %q", dst, got)
		}
	}
}

func TestRegistrationAPI(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	reg := testRegistry(&now)
	h := Handler(reg)
	call := func(method, peer, addr, body string) int {
		r := httptest.NewRequest(method, "/v1/registrations/"+addr, strings.NewReader(body))
		r.RemoteAddr = peer + ":40000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := call(http.MethodPut, "10.78.8.1", "10.78.8.9", `{"account_id":"acct","sandbox_id":"sb"}`); code != http.StatusOK {
		t.Fatalf("register = %d", code)
	}
	if _, ok := reg.Lookup(netip.MustParseAddr("10.78.8.9")); !ok {
		t.Fatal("not registered")
	}
	if code := call(http.MethodPut, "10.78.4.1", "10.78.8.9", `{"account_id":"acct","sandbox_id":"sb"}`); code != http.StatusForbidden {
		t.Fatalf("another node = %d", code)
	}
	if code := call(http.MethodPut, "10.78.8.1", "10.78.8.9", `{"account_id":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty account = %d", code)
	}
	if code := call(http.MethodDelete, "10.78.8.1", "10.78.8.9", ""); code != http.StatusNoContent {
		t.Fatalf("unregister = %d", code)
	}
	if _, ok := reg.Lookup(netip.MustParseAddr("10.78.8.9")); ok {
		t.Fatal("still registered")
	}
}
