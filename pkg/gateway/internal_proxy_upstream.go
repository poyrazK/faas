package gateway

// Keep-alive per upstream for a node-choosing InternalDialer.
//
// A dialer that picks a compute node inside DialContext cannot share the
// proxy's connection pool: http.Transport looks for an idle connection under
// the request's URL host before it dials, so a reused connection skips the
// pick. It could keep reaching a node the registry drained, or the node a
// wake failover retry excluded. An UpstreamPicker names the node first, and
// the transport pools connections under a host derived from that node, so a
// connection is only ever reused for the node the pick chose.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// UpstreamPicker is a node-choosing InternalDialer that names the upstream of
// an attempt before a connection is chosen, so the edge can keep connections
// alive per upstream.
type UpstreamPicker interface {
	InternalDialer
	// PickUpstream returns the upstream for the next attempt. It skips the
	// nodes rec excludes (rec may be nil) and the upstreams whose Key is in
	// failed: a new connection to them could not be opened during this
	// request. It returns an error wrapping ErrNoComputeCapacity when none is
	// left.
	PickUpstream(ctx context.Context, rec *DialRecord, failed []string) (Upstream, error)
}

// Upstream is one destination an UpstreamPicker offers.
type Upstream struct {
	// Key identifies the destination. Connections are pooled per key, so two
	// upstreams may share a key only when they reach the same listener.
	Key string
	// Node is the compute node name a DialRecord records, or "" when the
	// destination has none (the edge then does not retry).
	Node string
	// Dial opens a new connection to the destination.
	Dial func(ctx context.Context) (net.Conn, error)
	// Used, when set, runs once for each request this upstream carries.
	Used func()
}

// upstreamIdleConnTimeout closes an idle edge connection well before
// gatewayd-internal's keep-alive idle timeout, so the edge closes it first and
// a request rarely races the server's close (ADR-1736: HTTP/1.1 has no
// GOAWAY).
const upstreamIdleConnTimeout = time.Duration(api.GatewaydInternalControlIdleTimeoutSecondsDefault) * time.Second / 2

// WithUpstreamKeepAlive keeps connections to gatewayd-internal alive per
// upstream when the proxy's dialer is an UpstreamPicker and its transport is
// the HTTP/1.1 one. It reports whether it did; otherwise the proxy is
// unchanged.
func (p *InternalReverseProxy) WithUpstreamKeepAlive() bool {
	picker, ok := p.Dialer.(UpstreamPicker)
	if !ok {
		return false
	}
	base, ok := p.Transport.(*http.Transport)
	if !ok {
		return false
	}
	p.Transport = newUpstreamTransport(picker, base, p.DialTimeout)
	return true
}

// upstreamTransport sends each request to the upstream its picker names,
// reusing that upstream's idle connections.
type upstreamTransport struct {
	picker      UpstreamPicker
	transport   *http.Transport
	dialTimeout time.Duration
	// upstreams maps a pool host to the upstream its connections reach.
	upstreams sync.Map
}

func newUpstreamTransport(picker UpstreamPicker, base *http.Transport, dialTimeout time.Duration) *upstreamTransport {
	u := &upstreamTransport{picker: picker, dialTimeout: dialTimeout}
	t := base.Clone()
	t.DisableKeepAlives = false
	t.IdleConnTimeout = upstreamIdleConnTimeout
	t.DialContext = u.dial
	u.transport = t
	return u
}

// RoundTrip sends req to the upstream the picker names. It asks for another
// one only when a new connection could not be opened: that fails before any
// request byte is read, so the same body can still go to the next upstream.
// Any other failure is returned, because the request may have reached a node.
func (u *upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	rec := DialRecordFrom(ctx)
	body := holdBody(req.Body)
	var failed []string
	var lastErr error
	for {
		upstream, err := u.picker.PickUpstream(ctx, rec, failed)
		if err == nil && slices.Contains(failed, upstream.Key) {
			err = fmt.Errorf("%w: upstream %q offered again after its dial failed", ErrNoComputeCapacity, upstream.Key)
		}
		if err != nil {
			body.release(true)
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %w", err, lastErr)
			}
			return nil, err
		}
		resp, err := u.transport.RoundTrip(u.attempt(req, upstream, body))
		if err == nil {
			body.release(false)
			rec.RecordDialed(upstream.Node)
			if upstream.Used != nil {
				upstream.Used()
			}
			return resp, nil
		}
		var dialErr *upstreamDialError
		if !errors.As(err, &dialErr) || body.wasRead() || ctx.Err() != nil {
			body.release(true)
			return nil, err
		}
		failed = append(failed, upstream.Key)
		lastErr = err
	}
}

// attempt is req addressed to upstream's pool host. Only the URL changes: the
// Host header stays the customer's, and the body is the held one.
func (u *upstreamTransport) attempt(req *http.Request, upstream Upstream, body *heldBody) *http.Request {
	host := upstreamHost(upstream.Key)
	u.upstreams.LoadOrStore(host, upstream)
	out := req.WithContext(req.Context())
	target := *req.URL
	target.Host = host
	out.URL = &target
	if out.Host == "" {
		out.Host = req.URL.Host
	}
	if body != nil {
		body.rearm()
		out.Body = body
	}
	return out
}

// dial opens a connection for the transport's pool host addr.
func (u *upstreamTransport) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	value, ok := u.upstreams.Load(host)
	if !ok {
		return nil, &upstreamDialError{err: fmt.Errorf("no upstream for pool host %q", host)}
	}
	conn, err := dialWithTimeout(ctx, upstreamDialer(value.(Upstream).Dial), u.dialTimeout)
	if err != nil {
		return nil, &upstreamDialError{err: err}
	}
	return conn, nil
}

// upstreamHost is the URL host the transport pools key's connections under.
func upstreamHost(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "upstream-" + hex.EncodeToString(sum[:8]) + ".invalid"
}

// upstreamDialer adapts an Upstream's Dial to dialWithTimeout.
type upstreamDialer func(context.Context) (net.Conn, error)

func (d upstreamDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return d(ctx)
}

// upstreamDialError marks a failure to open a new connection to an upstream.
type upstreamDialError struct{ err error }

func (e *upstreamDialError) Error() string { return e.err.Error() }
func (e *upstreamDialError) Unwrap() error { return e.err }

// heldBody keeps a request body open across an attempt whose dial failed.
// http.Transport closes the body on every error, but a failed dial reads none
// of it, so the next upstream can still send it. While held, a Close from the
// current attempt is only recorded; release applies it.
type heldBody struct {
	io.ReadCloser
	read atomic.Bool

	mu     sync.Mutex
	held   bool
	closed bool
}

// holdBody wraps body, or returns nil when there is no body to hold.
func holdBody(body io.ReadCloser) *heldBody {
	if body == nil || body == http.NoBody {
		return nil
	}
	return &heldBody{ReadCloser: body, held: true}
}

func (b *heldBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.ReadCloser.Read(p)
}

func (b *heldBody) Close() error {
	b.mu.Lock()
	if b.held {
		b.closed = true
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	return b.ReadCloser.Close()
}

// rearm forgets a Close from an earlier attempt: the transport closes the
// body when a dial fails, and that Close must not reach the attempt that
// follows, whose transport may still be streaming the body.
func (b *heldBody) rearm() {
	b.mu.Lock()
	b.closed = false
	b.mu.Unlock()
}

// wasRead reports whether any attempt read from the body.
func (b *heldBody) wasRead() bool {
	return b != nil && b.read.Load()
}

// release stops holding the body. With closeNow it closes the body; otherwise
// a Close that arrived while held is applied now and later ones pass through.
func (b *heldBody) release(closeNow bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.held = false
	pending := b.closed
	b.mu.Unlock()
	if closeNow || pending {
		_ = b.ReadCloser.Close()
	}
}
