package vmmdgrpc

// adr: 20261122140000 — vmmd sends an HTTP/1.1 app's requests to its
// persistent stream bridge in binary HTTP (pkg/binhop) instead of h2c.

import (
	"context"
	"net"
	"net/http"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/binhop"
)

// bridgeBinaryHTTPEnv tells a bridge to serve binary HTTP beside h2c
// (cmd/vmmd-stream-bridge/binary_http.go).
const bridgeBinaryHTTPEnv = "FAAS_BRIDGE_BINARY_HTTP"

// bridgeTransport is what an entry's client sends through: the h2c
// transport or the binary HTTP one.
type bridgeTransport interface {
	http.RoundTripper
	CloseIdleConnections()
}

// bridgeBinaryIdleTimeout keeps an idle binary HTTP connection for half the
// bridge server's idle timeout (api.StreamBridgeIdleTimeout), as the edge does for
// gatewayd-internal: vmmd never reuses a connection the bridge has closed,
// which would fail a request that cannot be sent again.
const bridgeBinaryIdleTimeout = api.StreamBridgeIdleTimeout / 2

// streamBridgeBinaryTransport sends one request at a time over each unix
// connection to a bridge. Every connection passes the same peer check as
// the h2c transport's (verifyBridgePeer: the instance's bridge uid), and
// response heads and trailers are capped at the h2c transport's 1 MiB
// header list.
type streamBridgeBinaryTransport struct {
	t    *binhop.Transport
	dial func(context.Context) (net.Conn, error)
}

func newStreamBridgeBinaryTransport(sockPath string, id bridgeIdentity) *streamBridgeBinaryTransport {
	return &streamBridgeBinaryTransport{
		// The guest sees what vmmd's h2c client sent it: the same default
		// User-Agent and Accept-Encoding, and gzip it asked for is decoded.
		t: binhop.NewTransport(bridgeBinaryIdleTimeout, int(api.DefaultMaxHeaderBytes), binhop.DefaultMaxIdle).WithHTTP2ClientDefaults(),
		dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", sockPath)
			if err != nil {
				return nil, err
			}
			if err := id.verifyBridgePeer(conn); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}
}

func (b *streamBridgeBinaryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return b.t.RoundTrip(req, b.dial)
}

func (b *streamBridgeBinaryTransport) CloseIdleConnections() { b.t.CloseIdle() }

// startEnv is the environment entry's bridge starts with. It marks entry
// binary when the bridge is told to serve binary HTTP, which connectEntry
// then sends it.
func (m *streamBridgeManager) startEnv(req *vmmdpb.ForwardHTTPRequestInit, entry *streamBridgeEntry) []string {
	env := m.bridgeEnv(req, entry.target)
	if m.useBinaryHTTP(entry) {
		env = append(env, bridgeBinaryHTTPEnv+"=1")
		entry.binary = true
	}
	return env
}

// useBinaryHTTP reports whether a bridge vmmd is about to start speaks
// binary HTTP: the switch is on and the bridge frames HTTP/1.1 to its
// guest. HTTP/2 and gRPC apps keep h2c end to end (full duplex, trailers).
// A bridge adopted from a predecessor keeps h2c: every bridge serves it, and
// the adoption record carries no framing.
func (m *streamBridgeManager) useBinaryHTTP(entry *streamBridgeEntry) bool {
	return m.binaryHTTP.Load() && entry.protocol == "h1"
}
