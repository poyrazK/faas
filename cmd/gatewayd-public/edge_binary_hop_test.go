// adr: 20261110081523
package main

import (
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// FAAS_EDGE_INTERNAL_HOP=binary takes effect only on a compute edge with
// per-node keep-alive, and offers the local upstream a hop to the socket.
func TestConfigureBinaryHop(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	newLFD := func() *localFirstDialer {
		return newLocalFirstDialer(gateway.NewUnixSocketDialer("/nonexistent/internal.sock"), nil, target, newEdgeMetrics(), quiet)
	}
	for _, tc := range []struct {
		name      string
		value     string
		compute   bool
		keepAlive bool
		wantHop   bool
		wantErr   string
	}{
		{name: "unset", compute: true, keepAlive: true},
		{name: "http1", value: "http1", compute: true, keepAlive: true},
		{name: "binary", value: "binary", compute: true, keepAlive: true, wantHop: true},
		{name: "binary without per-node keep-alive", value: "binary", compute: true},
		{name: "binary on a control-plane edge", value: "binary", wantErr: "for a compute edge"},
		{name: "not a hop", value: "h2c", compute: true, wantErr: edgeInternalHopEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lfd *localFirstDialer
			var dialer gateway.InternalDialer = gateway.NewUnixSocketDialer("/nonexistent/internal.sock")
			if tc.compute {
				lfd = newLFD()
				dialer = lfd
			}
			proxy := gateway.NewInternalReverseProxy(dialer, target, quiet, false)
			if tc.keepAlive && !proxy.WithUpstreamKeepAlive() {
				t.Fatal("per-node keep-alive did not take effect")
			}
			err := configureBinaryHop(proxy, lfd, newEdgeMetrics(), func(k string) string {
				if k == edgeInternalHopEnv {
					return tc.value
				}
				return ""
			}, quiet)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := lfd != nil && lfd.hop != nil; got != tc.wantHop {
				t.Fatalf("hop offered = %v, want %v", got, tc.wantHop)
			}
		})
	}
}
