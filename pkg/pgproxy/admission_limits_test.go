//go:build !no_pg

// Bug hunt 34: a handshake slot was held for 15 s by a client that sent
// nothing, so a few sources could fill every slot and lock every database on
// the node out. A slot now lasts StartupTimeout until the startup message,
// and the platform's own networks skip the caps.
package pgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func serveTestProxy(t *testing.T, cfg Config) string {
	t.Helper()
	cfg.TLS, cfg.HostSuffix, cfg.DecoySecret = selfSignedTLS(t), testSuffix, []byte(strings.Repeat("k", 32))
	p, err := New(cfg, &mapCredentials{}, &countingWaker{}, tcpDialer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	go func() { _ = p.Serve(ctx, ln) }()
	return ln.Addr().String()
}

func closedWithin(c net.Conn, d time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(d))
	_, err := c.Read(make([]byte, 1))
	var netErr net.Error
	return errors.Is(err, io.EOF) || err != nil && !(errors.As(err, &netErr) && netErr.Timeout())
}

func TestProxyClosesIdleClientsAtTheStartupTimeout(t *testing.T) {
	addr := serveTestProxy(t, Config{StartupTimeout: 300 * time.Millisecond, HandshakeTimeout: 10 * time.Second})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if !closedWithin(c, 2*time.Second) {
		t.Fatal("a client that sent nothing kept its handshake slot past the startup timeout")
	}
}

func TestProxyTrustedSourcesSkipTheHandshakeCaps(t *testing.T) {
	addr := serveTestProxy(t, Config{HandshakeTimeout: 5 * time.Second, MaxHandshakesPerSource: 1, MaxHandshakes: 1,
		TrustedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for range 3 {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	time.Sleep(100 * time.Millisecond)
	for i, c := range conns {
		if closedWithin(c, 200*time.Millisecond) {
			t.Fatalf("trusted connection %d was refused by the handshake caps", i)
		}
	}
}
