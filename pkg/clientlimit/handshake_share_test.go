package clientlimit

import (
	"crypto/tls"
	"net"
	"net/netip"
	"testing"
	"time"
)

// waitSlots waits until n of gate's slots are taken.
func waitSlots(t *testing.T, gate *HandshakeGate, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(gate.slots) != n && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.slots) != n {
		t.Fatalf("%d slots taken, want %d", len(gate.slots), n)
	}
}

// ADR-20261107180000 (R8-8): one address that never sends a ClientHello may
// hold only its share of the slots, so others still complete handshakes;
// an address in the proxies' ranges gets the proxy share instead.
func TestHandshakeGateCapsEachAddress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		proxies []netip.Prefix
		share   int
	}{
		{name: "client", share: 2},
		{name: "proxy", proxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, share: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var refused refusals
			gate := NewHandshakeGate(t.Context(), tls.NewListener(inner, testServerTLS(t)), 8, 5*time.Second, refused.add,
				func(net.Addr, error) {}).WithPerAddress(2, 3, tc.proxies)
			acceptAll(t, gate)
			addr := inner.Addr().String()
			for range tc.share {
				stalled, err := net.Dial("tcp", addr)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stalled.Close() })
			}
			waitSlots(t, gate, tc.share)
			if !refusedByPeer(t, addr) {
				t.Fatal("a connection past the address's share was served")
			}
			refused.wait(t, ReasonHandshakesPerAddress)
			if len(gate.slots) != tc.share {
				t.Fatalf("%d slots taken, want the share %d; the rest stay free for other addresses", len(gate.slots), tc.share)
			}
		})
	}
}

// A connection that sends no ClientHello gives its slot back after the
// hello timeout, well before the handshake timeout; one that sends it gets
// the whole handshake timeout.
func TestHandshakeGateClientHelloTimeout(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var refused refusals
	failures := make(chan error, 4)
	cfg := ExtendOnClientHello(testServerTLS(t), 10*time.Second)
	gate := NewHandshakeGate(t.Context(), tls.NewListener(inner, cfg), 4, 10*time.Second, refused.add,
		func(_ net.Addr, err error) { failures <- err }).WithClientHelloTimeout(200 * time.Millisecond)
	acceptAll(t, gate)
	addr := inner.Addr().String()

	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	start := time.Now()
	select {
	case <-failures:
		if waited := time.Since(start); waited > 5*time.Second {
			t.Fatalf("the silent connection held its slot %v", waited)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a connection with no ClientHello kept its slot past the hello timeout")
	}

	// A client that sends its hello and then completes slowly is not cut
	// off at the hello timeout.
	clientTLS := &tls.Config{InsecureSkipVerify: true, ServerName: "shop.gregale.dev"} //nolint:gosec // the test's own self-signed server
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	c := tls.Client(&slowConn{Conn: raw, delay: 400 * time.Millisecond}, clientTLS)
	if err := c.Handshake(); err != nil {
		t.Fatalf("a slow handshake past the hello timeout: %v", err)
	}
}

// slowConn delays each write after the first, so the ClientHello goes out at
// once and the rest of the handshake comes late.
type slowConn struct {
	net.Conn
	delay  time.Duration
	writes int
}

func (c *slowConn) Write(p []byte) (int, error) {
	if c.writes > 0 {
		time.Sleep(c.delay)
	}
	c.writes++
	return c.Conn.Write(p)
}
