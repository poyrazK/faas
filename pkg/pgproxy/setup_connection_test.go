//go:build !no_pg

package pgproxy

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

func TestBackendHandshakeDeadlineClosesDirectAndPooledStreams(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		for _, phase := range []string{"authentication", "before_ready"} {
			t.Run(map[bool]string{false: "direct", true: "pooled"}[pooled]+"/"+phase, func(t *testing.T) {
				backend, closed := setupBackend(t, phase)
				id := uuid.NewString()
				v, err := pgscram.NewVerifier("test-password", pgscram.DefaultIterations)
				if err != nil {
					t.Fatal(err)
				}
				creds := &mapCredentials{verifiers: map[string]pgscram.Verifier{id + "|reader": v}}
				p, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)), HandshakeTimeout: time.Second, WakeTimeout: 40 * time.Millisecond}, creds, setupWaker{}, setupDialer(func(context.Context, string, string) (io.ReadWriteCloser, error) { return backend, nil }), nil)
				if err != nil {
					t.Fatal(err)
				}
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				defer ln.Close()
				go func() { _ = p.Serve(ctx, ln) }()
				h := &harness{proxyAddr: ln.Addr().String()}
				host := id
				if pooled {
					host += "-pooler"
				}
				host += "." + testSuffix
				clientCtx, stopClient := context.WithTimeout(t.Context(), 2*time.Second)
				defer stopClient()
				if conn, err := h.connect(clientCtx, host, "reader", "test-password", "gregale", "require", nil); err == nil {
					_ = conn.Close(clientCtx)
					t.Fatal("stalled backend handshake succeeded")
				}
				select {
				case <-closed:
				case <-time.After(250 * time.Millisecond):
					t.Fatal("proxy left the timed-out backend stream open")
				}
			})
		}
	}
}
