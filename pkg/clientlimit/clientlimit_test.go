// adr: 20261103163954
package clientlimit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestKey(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{"203.0.113.9", "203.0.113.9/32"},
		{"::ffff:203.0.113.9", "203.0.113.9/32"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
	} {
		if got := Key(netip.MustParseAddr(tc.addr)).String(); got != tc.want {
			t.Errorf("Key(%s) = %s, want %s", tc.addr, got, tc.want)
		}
	}
}

// fakeClock is a settable clock for the token buckets.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestTableSpendsAndRefills(t *testing.T) {
	clock := newFakeClock()
	tab := newTable(1, 2, 10, clock.Now)
	key := Key(netip.MustParseAddr("203.0.113.9"))
	for i, want := range []bool{true, true, false} {
		if ok, _ := tab.take(key); ok != want {
			t.Fatalf("take %d = %v, want %v", i, ok, want)
		}
	}
	clock.advance(time.Second)
	if ok, _ := tab.take(key); !ok {
		t.Fatal("a second later the bucket had no token")
	}
	other := Key(netip.MustParseAddr("198.51.100.1"))
	if ok, _ := tab.take(other); !ok {
		t.Fatal("another address shared the first one's bucket")
	}
}

func TestTableStaysWithinItsBound(t *testing.T) {
	clock := newFakeClock()
	tab := newTable(1, 1, 2, clock.Now)
	a, b, c, d := Key(netip.MustParseAddr("192.0.2.1")), Key(netip.MustParseAddr("192.0.2.2")),
		Key(netip.MustParseAddr("192.0.2.3")), Key(netip.MustParseAddr("192.0.2.4"))
	tab.take(a)
	tab.take(b)
	// Full and nothing idle: c and d share the overflow bucket.
	if ok, _ := tab.take(c); !ok {
		t.Fatal("the overflow bucket started empty")
	}
	if ok, _ := tab.take(d); ok {
		t.Fatal("a fourth address got its own bucket past the bound")
	}
	if got := tab.size(); got != 2 {
		t.Fatalf("tracked %d addresses, want the bound 2", got)
	}
	// Once a and b are idle again, a sweep makes room.
	clock.advance(2 * time.Second)
	if ok, _ := tab.take(c); !ok {
		t.Fatal("no room after the tracked addresses went idle")
	}
	if got := tab.size(); got != 1 {
		t.Fatalf("tracked %d addresses after the sweep, want 1", got)
	}
}

// acceptAll serves ln's connections into a channel until it fails.
func acceptAll(t *testing.T, ln net.Listener) <-chan net.Conn {
	t.Helper()
	conns := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return conns
}

// refusals records refusal reasons.
type refusals struct {
	mu      sync.Mutex
	reasons []string
}

func (r *refusals) add(reason string) {
	r.mu.Lock()
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
}

func (r *refusals) wait(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		got := slices.Clone(r.reasons)
		r.mu.Unlock()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("refusals = %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// refusedByPeer dials addr and reports whether the other side reset or
// closed the connection. The reset can reach the client before its connect
// returns, which macOS reports as a failed dial.
func refusedByPeer(t *testing.T, addr string) bool {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return errors.Is(err, syscall.ECONNRESET)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

func TestListenerLimitsOpenConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	ln := NewListener(inner, ConnLimits{MaxOpenPerAddr: 2, NewPerSecond: 1000, NewBurst: 1000, TrackedAddrsMax: 10}, refused.add, nil)
	conns := acceptAll(t, ln)
	dial := func() net.Conn {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	dial()
	dial()
	first, second := <-conns, <-conns
	if !refusedByPeer(t, inner.Addr().String()) {
		t.Fatal("a third connection from the address was served")
	}
	refused.wait(t, ReasonOpenConnections)

	_ = first.Close()
	_ = second
	dial()
	select {
	case <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("a connection after one closed was not served")
	}
}

// A proxy's connections each carry many clients, so its addresses are
// neither limited nor tracked.
func TestListenerExemptsProxyAddresses(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	ln := NewListener(inner, ConnLimits{MaxOpenPerAddr: 1, NewPerSecond: 1, NewBurst: 1, TrackedAddrsMax: 10,
		Exempt: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}, refused.add, nil)
	conns := acceptAll(t, ln)
	for range 5 {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		select {
		case <-conns:
		case <-time.After(5 * time.Second):
			t.Fatal("a connection from an exempt address was not served")
		}
	}
	if got := len(ln.table.entries); got != 0 {
		t.Fatalf("exempt address tracked: %d entries", got)
	}
}

func TestListenerLimitsConnectionRate(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	var refused refusals
	ln := NewListener(inner, ConnLimits{MaxOpenPerAddr: 100, NewPerSecond: 1, NewBurst: 2, TrackedAddrsMax: 10}, refused.add, clock.Now)
	conns := acceptAll(t, ln)
	for range 2 {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		<-conns
	}
	if !refusedByPeer(t, inner.Addr().String()) {
		t.Fatal("a connection past the rate was served")
	}
	refused.wait(t, ReasonConnectionRate)
}

func testServerTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "edge"},
		DNSNames:     []string{"shop.gregale.dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func TestHandshakeGateBoundsHandshakesInProgress(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	failures := make(chan error, 4)
	gate := NewHandshakeGate(t.Context(), tls.NewListener(inner, testServerTLS(t)), 1, 300*time.Millisecond, refused.add,
		func(_ net.Addr, err error) { failures <- err })
	conns := acceptAll(t, gate)
	addr := inner.Addr().String()

	// A client that never sends its hello holds the only slot.
	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(gate.slots) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.slots) != 1 {
		t.Fatal("the stalled handshake never took the slot")
	}
	clientTLS := &tls.Config{InsecureSkipVerify: true, ServerName: "shop.gregale.dev"} //nolint:gosec // the test's own self-signed server
	if c, err := tls.Dial("tcp", addr, clientTLS); err == nil {
		_ = c.Close()
		t.Fatal("a handshake past the bound completed")
	}
	refused.wait(t, ReasonHandshakes)

	// The stalled handshake times out, fails and frees the slot.
	select {
	case <-failures:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled handshake never failed")
	}
	c, err := tls.Dial("tcp", addr, clientTLS)
	if err != nil {
		t.Fatalf("a handshake after the slot freed: %v", err)
	}
	defer c.Close()
	select {
	case served := <-conns:
		tc, ok := served.(*tls.Conn)
		if !ok || !tc.ConnectionState().HandshakeComplete {
			t.Fatalf("Accept returned %T before its handshake completed", served)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the completed connection never reached Accept")
	}
}

func TestRequestsLimitEachAddress(t *testing.T) {
	clock := newFakeClock()
	var refused refusals
	h := Requests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		RequestLimits{PerSecond: 1, Burst: 2, TrackedAddrsMax: 10},
		func(r *http.Request) (netip.Addr, bool) {
			a, err := netip.ParseAddr(r.Header.Get("Test-Client"))
			return a, err == nil
		}, refused.add, clock.Now)
	do := func(client string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://shop.gregale.dev/", nil)
		if client != "" {
			req.Header.Set("Test-Client", client)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := range 2 {
		if got := do("203.0.113.9").Code; got != http.StatusNoContent {
			t.Fatalf("request %d = %d, want it served", i, got)
		}
	}
	rec := do("203.0.113.9")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("third request = %d (Retry-After %q), want 429 with Retry-After 1", rec.Code, rec.Header().Get("Retry-After"))
	}
	var problem struct {
		Code     string `json:"code"`
		Limit    int64  `json:"limit"`
		Observed int64  `json:"observed"`
		DocsURL  string `json:"docs_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem body: %v: %s", err, rec.Body)
	}
	if problem.Code != "rate_limited" || problem.Limit != 2 || problem.Observed != 2 || problem.DocsURL == "" {
		t.Fatalf("problem = %+v, want rate_limited with limit 2, observed 2 and a docs link", problem)
	}
	refused.wait(t, ReasonRequestRate)
	if got := do("198.51.100.1").Code; got != http.StatusNoContent {
		t.Fatalf("another address = %d, want it served", got)
	}
	if got := do("").Code; got != http.StatusNoContent {
		t.Fatalf("a request with no client address = %d, want it served", got)
	}
	clock.advance(time.Second)
	if got := do("203.0.113.9").Code; got != http.StatusNoContent {
		t.Fatalf("a second later = %d, want it served", got)
	}
}
