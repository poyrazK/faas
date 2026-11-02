//go:build !no_pg

// adr: 1027 — unauthenticated clients are bounded: a source past its cap of
// in-flight handshakes is closed at once, and its slots free up again.
package pgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProxyCapsHandshakesPerSource(t *testing.T) {
	p, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)),
		HandshakeTimeout: 5 * time.Second, MaxHandshakesPerSource: 2},
		&mapCredentials{}, &countingWaker{}, tcpDialer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Serve(ctx, ln) }()
	defer func() { _ = ln.Close() }()

	dial := func() net.Conn {
		t.Helper()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// closedSoon reports whether the proxy closed the connection within d.
	closedSoon := func(c net.Conn, d time.Duration) bool {
		_ = c.SetReadDeadline(time.Now().Add(d))
		_, err := c.Read(make([]byte, 1))
		var netErr net.Error
		return errors.Is(err, io.EOF) || err != nil && !(errors.As(err, &netErr) && netErr.Timeout())
	}
	// Two clients sit in their handshake, sending nothing.
	first, second := dial(), dial()
	defer func() { _ = first.Close(); _ = second.Close() }()
	time.Sleep(100 * time.Millisecond)
	third := dial()
	defer func() { _ = third.Close() }()
	if !closedSoon(third, time.Second) {
		t.Fatal("a third in-flight handshake from one source was let in")
	}
	if closedSoon(first, 200*time.Millisecond) {
		t.Fatal("a handshake within the cap was cut off")
	}
	// Once one gives up, its slot is free again.
	_ = first.Close()
	time.Sleep(100 * time.Millisecond)
	fourth := dial()
	defer func() { _ = fourth.Close() }()
	if closedSoon(fourth, 300*time.Millisecond) {
		t.Fatal("a freed slot was not reusable")
	}
}
