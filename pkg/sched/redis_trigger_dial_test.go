// adr: 20261101104512 — a `tls: true` Redis Streams trigger dials TLS through
// the egress-guarded dialer; go-redis ignored TLSConfig once a custom Dialer
// was set, so AUTH went out in plaintext.
package sched_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestRedisTriggerOptionsDialTLSWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		useTLS bool
	}{{"tls", true}, {"plain", false}} {
		t.Run(tc.name, func(t *testing.T) {
			opts := sched.XRedisTriggerOptions("redis.example.test:6380", tc.useTLS)
			if opts.Dialer == nil {
				t.Fatal("a Redis trigger client has no egress-guarded dialer")
			}
			if got := opts.TLSConfig != nil; got != tc.useTLS {
				t.Fatalf("TLSConfig set = %v, want %v", got, tc.useTLS)
			}
			if tc.useTLS && opts.TLSConfig.MinVersion < tls.VersionTLS12 {
				t.Fatalf("TLS minimum version %x, want TLS 1.2 or later", opts.TLSConfig.MinVersion)
			}
		})
	}
}

func TestRedisTriggerDialerHandshakesTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	var plainDialer net.Dialer
	dial := sched.XRedisTLSDial(plainDialer.DialContext, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "example.com"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		t.Fatalf("dialer returned %T, want a TLS connection", conn)
	}
	if !tlsConn.ConnectionState().HandshakeComplete {
		t.Fatal("the dialer returned before the TLS handshake completed")
	}
}

// A server that does not speak TLS fails the dial: the client never writes
// AUTH in plaintext to it.
func TestRedisTriggerDialerRefusesAPlaintextServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 512)
		n, _ := conn.Read(buf)
		received <- buf[:n]
		_, _ = conn.Write([]byte("-ERR not tls\r\n"))
	}()
	var plainDialer net.Dialer
	dial := sched.XRedisTLSDial(plainDialer.DialContext, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "redis.example.test"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if conn, err := dial(ctx, "tcp", listener.Addr().String()); err == nil {
		_ = conn.Close()
		t.Fatal("a TLS trigger connected to a plaintext server")
	}
	select {
	case first := <-received:
		// A TLS ClientHello starts with the handshake record type 0x16.
		if len(first) == 0 || first[0] != 0x16 {
			t.Fatalf("first bytes on the wire %q, want a TLS ClientHello", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server never received the client's first bytes")
	}
}
