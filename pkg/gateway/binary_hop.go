package gateway

// adr: 20261110081523 — the compute edge sends the requests it routes to
// its own node's gatewayd-internal in binary HTTP; gatewayd-internal serves
// them on a second unix socket. The client and server live in pkg/binhop,
// which vmmd and the stream bridge share.

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/binhop"
)

// Reasons a request that should have taken the binary hop went over
// HTTP/1.1 instead, the label values of the edge's fallback counter.
const (
	// HopFallbackNoSocket: the hop socket is missing or nothing listens on
	// it, as beside a gatewayd-internal from before the hop.
	HopFallbackNoSocket = "no_socket"
	// HopFallbackDialError: the dial failed some other way.
	HopFallbackDialError = "dial_error"
)

// HopFallbackReasons lists the fallback reasons, so a counter can start
// each at zero.
var HopFallbackReasons = []string{HopFallbackNoSocket, HopFallbackDialError}

// BinaryHopServer serves gatewayd-internal's handler to the edge's binary
// HTTP connections.
type BinaryHopServer = binhop.Server

// hopDialError is a failure to open a hop connection: nothing was sent.
type hopDialError = binhop.DialError

// hopFallbackReason is the fallback counter's label for e.
func hopFallbackReason(e *hopDialError) string {
	if e.NoSocket {
		return HopFallbackNoSocket
	}
	return HopFallbackDialError
}

// binaryHopTransport sends one request at a time over each connection to
// this node's gatewayd-internal, dialing within the request's budget.
type binaryHopTransport struct {
	t           *binhop.Transport
	dialTimeout time.Duration
}

func newBinaryHopTransport(dialTimeout time.Duration) *binaryHopTransport {
	return &binaryHopTransport{
		t:           binhop.NewTransport(upstreamIdleConnTimeout, int(api.DefaultMaxHeaderBytes), binhop.DefaultMaxIdle),
		dialTimeout: dialTimeout,
	}
}

func (b *binaryHopTransport) roundTrip(req *http.Request, dial func(context.Context) (net.Conn, error)) (*http.Response, error) {
	return b.t.RoundTrip(req, func(ctx context.Context) (net.Conn, error) {
		return dialWithTimeout(ctx, upstreamDialer(dial), b.dialTimeout)
	})
}

func (b *binaryHopTransport) closeIdle() { b.t.CloseIdle() }
