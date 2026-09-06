// Package gateway — internal_proxy.go is the public→internal reverse
// proxy that lives at the centre of the Tier A7 edge split (ADR-070).
//
// gatewayd-public forwards every customer request to gatewayd-internal
// over the same-box unix socket at /run/faas/gatewayd-internal.sock
// (ADR-015/018). We use http.Transport.RoundTrip with a custom
// DialContext that dials the unix socket — the standard transport
// gives us body handling, idle-conn pooling, request timeouts, and
// the canonical "client.Do" semantics for free. The unix-socket
// dialer is a single line (`net.Dialer.DialContext(ctx, "unix", path)`).
//
// Same-box only in v1.0; cross-box mTLS is Gate-B. The dialer is
// injected via InternalDialer so Gate-B can swap in a TLS dialer
// without touching the proxy.
//
// Trust model (security):
//
// gatewayd-public is the only hop the customer can reach. The
// internal daemon reads X-Forwarded-For to enforce per-IP rate
// limits (ADR-070). If we APPENDED the customer's XFF header
// we would trust every customer to write their own per-IP key — a
// trivial bypass. We therefore STRIP the inbound XFF and re-add
// only the public daemon's own RemoteAddr. The chain is
// `customer-=public-remote-addr` going in; the internal daemon
// trusts exactly one hop. X-Forwarded-Proto is preserved (the
// public hop is the only TLS terminator; downstream hops are
// plaintext unix).
//
// Hop-by-hop headers (RFC 7230 §6.1) are stripped on both request
// and response — match the forwardproxy.go list so the test suite
// that asserts equality stays green.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// InternalDialer is the seam the public daemon wires to reach a
// gatewayd-internal replica. The default unix-socket wiring is
// NewUnixSocketDialer("/run/faas/gatewayd-internal.sock").
//
// Note: the dialer may pool connections internally; the returned
// net.Conn is owned by the proxy for the duration of the request
// and closed by the transport when the body read completes.
type InternalDialer interface {
	DialContext(ctx context.Context, target string) (net.Conn, error)
}

// ErrNoComputeCapacity is returned by a dynamic internal dialer when the
// control plane has no reachable compute gateway. It is deliberately distinct
// from a malformed proxy configuration: the control plane is still healthy,
// but the data plane has no capacity for this request.
var ErrNoComputeCapacity = errors.New("no compute capacity")

// NewUnixSocketDialer returns an InternalDialer that dials a unix
// socket at `path`. The dialer ignores the target string the caller
// passes — every request routes to the same socket in the v1.0
// same-box shape. Future Gate-B work passes a target-encoded replica
// selector and picks a different socket per replica.
//
// ACL: the daemon runs as faas:faas (per the systemd unit). The
// internal daemon chmods the socket 0660 on bind, so the dialer
// only needs to be in the `faas` group — there's no per-process
// umask dance.
func NewUnixSocketDialer(path string) InternalDialer {
	return &unixSocketDialer{path: path}
}

type unixSocketDialer struct {
	path string
}

func (d *unixSocketDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	var dnet net.Dialer
	return dnet.DialContext(ctx, "unix", d.path)
}

// NewTCPDialer returns an InternalDialer for a private gatewayd-internal
// listener. The address is fixed at construction time so requests cannot
// smuggle a customer-controlled destination through the reverse proxy.
// Deployments use this only for the split-box control-plane → compute hop;
// the nftables policy restricts the listener to the rendered control-plane
// CIDRs.
func NewTCPDialer(address string) InternalDialer {
	return &tcpDialer{address: address}
}

type tcpDialer struct {
	address string
}

func (d *tcpDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	var dnet net.Dialer
	return dnet.DialContext(ctx, "tcp", d.address)
}

// InternalReverseProxy is the public→internal forwarder. It wraps
// http.Transport with a DialContext that dials the unix socket, and
// applies the load-bearing header rewrites (XFF strip-and-rebuild,
// hop-by-hop strip, X-Forwarded-Proto) before the request hits the
// wire.
//
// The zero value is unusable; construct via NewInternalReverseProxy.
type InternalReverseProxy struct {
	Dialer      InternalDialer
	Target      *url.URL
	Transport   http.RoundTripper
	Logger      *slog.Logger
	DialTimeout time.Duration
	// Drain (issue #587 / PR-A) is the per-request WaitGroup-backed
	// drain tracker shared with Handler + TraceHandler + the
	// control mux. nil = drain disabled. Wired via WithInFlightTracker
	// from cmd/gatewayd-public/main.go so the same tracker the
	// handler waits on covers the forwarder too — without this,
	// a request that's already handed off to the proxy but is
	// still piping bytes upstream would be invisible to the drain.
	Drain *drain.Tracker
}

// WithInFlightTracker installs the per-request drain tracker (see
// Handler.WithInFlightTracker for the full contract). Returns the
// proxy for fluent chaining.
func (p *InternalReverseProxy) WithInFlightTracker(tracker *drain.Tracker) *InternalReverseProxy {
	p.Drain = tracker
	return p
}

// NewInternalReverseProxy returns a wired InternalReverseProxy. The
// dialer and target are required; the rest default to safe
// production values (10 s dial timeout, 30 s response header
// timeout, slog.Default()).
//
// useH2C switches the upstream transport to H2C (HTTP/2 cleartext)
// for the in-process unix-socket hop. Issue #675: default is true so
// the Tier A7 production path negotiates H2 end-to-end with the
// gatewayd-internal h2c.NewHandler wrapper. Set to false to fall
// back to the legacy HTTP/1.1 transport (e.g. for rollback via the
// FAAS_INTERNAL_H2C env knob in cmd/gatewayd-public/main.go).
//
// The returned proxy is safe for concurrent use on the public
// listener hot path.
func NewInternalReverseProxy(dialer InternalDialer, target *url.URL, log *slog.Logger, useH2C bool) *InternalReverseProxy {
	if log == nil {
		log = slog.Default()
	}
	p := &InternalReverseProxy{
		Dialer:      dialer,
		Target:      target,
		Logger:      log,
		DialTimeout: 10 * time.Second,
	}
	p.Transport = p.buildTransport(dialer, useH2C)
	return p
}

// buildTransport constructs the appropriate transport with the
// proxy's current DialTimeout wired in. Issue #687: the timeout
// scopes only the dial step (transport.DialContext closure), not
// the inbound RoundTrip context. Tests can mutate p.DialTimeout
// before calling ServeHTTP, then re-call buildTransport to pick
// up the new value — keeps the public API small (no
// WithDialTimeout setter) while still allowing tests to pin the
// dial-timeout-scope regression.
func (p *InternalReverseProxy) buildTransport(dialer InternalDialer, useH2C bool) http.RoundTripper {
	if useH2C {
		return newInternalProxyH2CTransport(dialer, p.DialTimeout)
	}
	return newInternalProxyTransport(dialer, p.DialTimeout)
}

// newInternalProxyTransport returns an *http.Transport whose
// DialContext is the unix-socket dialer. The transport is the
// single seam the proxy uses; standard *http.Client semantics
// cover request body, hop-by-hop connection close, idle-conn
// reuse, and response timeout.
//
// dialTimeout scopes ONLY the dial step (issue #687): the
// DialContext closure wraps the caller's ctx with WithTimeout
// so a slow upstream socket aborts at the deadline, but the
// subsequent RoundTrip runs on the inbound request context
// (customer deadline or server ReadTimeout). Pre-fix the
// deadline wrapped the entire RoundTrip and prematurely cut
// cold-wake streaming responses.
func newInternalProxyTransport(dialer InternalDialer, dialTimeout time.Duration) *http.Transport {
	return &http.Transport{
		// Disable the env-driven HTTP proxy; this is a local hop.
		Proxy: nil,
		// DialContext is the load-bearing override — every request
		// dials via the unix socket (or whatever InternalDialer
		// returns). The transport's idle pool keys by the dialer's
		// identity (the unix socket path) so the same in-flight
		// conn is reused across requests.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialWithTimeout(ctx, dialer, dialTimeout)
		},
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Disable HTTP/2 — the unix-socket hop is HTTP/1.1 only.
		// Issue #675: this is the legacy HTTP/1.1 path; for the
		// production Tier A7 hop use newInternalProxyH2CTransport
		// instead. Kept here so the legacy e2e harness path (which
		// expects HTTP/1.1) keeps working without any knob.
		ForceAttemptHTTP2: false,
	}
}

// newInternalProxyH2CTransport returns an http2.Transport that
// dials the unix socket via the supplied InternalDialer. H2C
// (HTTP/2 cleartext) is the only way to negotiate HTTP/2 over the
// plaintext unix socket — the stdlib http.Transport refuses to do
// H2 without TLS even when ForceAttemptHTTP2 is true.
//
// The server side of this hop (gatewayd-internal) sets
// srv.Protocols.SetUnencryptedHTTP2(true) in cmd/gatewayd-internal/run.go
// so the negotiation is symmetric. Together they carry H2 frames in
// both directions on the same in-process socket that used to be
// HTTP/1.1.
//
// Streaming responses (app.StreamingEnabled=true) keep working
// because the customer handler writes its own chunked framing
// downstream of this hop — H2C is transparent to the streaming
// path. The handler-to-guest leg stays HTTP/1.1 plaintext per the
// issue #675 decision to keep streaming on HTTP/1.1.
//
// Connection lifetime mirrors the HTTP/1.1 transport above so an
// operator toggling between the two transports via FAAS_INTERNAL_H2C
// doesn't see different idle-conn behaviour. ReadIdleTimeout +
// PingTimeout are H2-specific keepalive parameters that have no
// HTTP/1.1 equivalent.
func newInternalProxyH2CTransport(dialer InternalDialer, dialTimeout time.Duration) http.RoundTripper {
	t := &http2.Transport{
		// AllowHTTP is the load-bearing flag — without it http2
		// refuses to dial plain HTTP and falls back to TLS-or-error.
		AllowHTTP: true,
		// DialTLS is the seam the http2 library uses to acquire a
		// net.Conn. We ignore network/addr/cfg and route everything
		// through the unix-socket InternalDialer. The context passed
		// here is the request context (caller's deadline) — we
		// propagate it to the dialer so cancellation works. The
		// dialTimeout (issue #687) scopes only the dial step; the
		// RoundTrip body copy runs on the inbound request context
		// (customer deadline). Without this contextcheck flagged
		// the closure (golangci-lint v2.4 contextcheck rule).
		DialTLSContext: func(ctx context.Context, _ string, _ string, _ *tls.Config) (net.Conn, error) {
			return dialWithTimeout(ctx, dialer, dialTimeout)
		},
		// Issue #691: bind http2.Transport defaults so a future Go
		// stdlib default change can't widen a closed-but-reused
		// connection window. IdleConnTimeout mirrors the H1
		// transport at newInternalProxyTransport:149 so toggling
		// FAAS_INTERNAL_H2C keeps idle-conn behaviour symmetric.
		IdleConnTimeout: 90 * time.Second,
		ReadIdleTimeout: 30 * time.Second,
		PingTimeout:     15 * time.Second,
		// ADR-127 §D2 (Layer 9) — outbound H2C client transport
		// pins. Mirrors the same pins on the other two H2C
		// transports (newGuestH2CTransport in
		// cmd/vmmd-stream-bridge/h2c_terminator.go and
		// newStreamBridgeH2CTransport in
		// pkg/vmmdgrpc/forward.go). See those sites for
		// per-field rationale.
		MaxReadFrameSize:           1 << 20, // 1 MiB
		MaxHeaderListSize:          1 << 20, // 1 MiB
		StrictMaxConcurrentStreams: true,
	}
	return t
}

// dialWithTimeout calls dialer.DialContext with ctx wrapped by
// WithTimeout(dialTimeout) when dialTimeout > 0. Extracted so the
// dial-timeout wrap pattern lives in one place — the two transport
// constructors share the same shape (issue #687 PR-3 review
// finding #4) and any future change to the timeout semantics
// (e.g. a min-deadline with the inbound ctx) only needs to update
// this function. Returns the dialer's net.Conn (or error).
//
// ADR-093 / PR-C: when the inbound ctx carries a Budget, the dial
// step takes a reqbudget.WithOverhead reservation
// (DefaultOverheadGRPC, 5 ms) so the next downstream hop starts
// with less declared budget than the inbound dial had. The
// reservation is only the budget's bookkeeping — it doesn't add
// wall-clock latency. When no Budget is attached, the dial ctx is
// the inbound ctx unchanged (no overhead).
func dialWithTimeout(ctx context.Context, dialer InternalDialer, dialTimeout time.Duration) (net.Conn, error) {
	if b, ok := reqbudget.FromContext(ctx); ok {
		var cancel context.CancelFunc
		ctx, cancel, _ = b.WithOverhead(ctx, "grpc-dial", reqbudget.DefaultOverheadGRPC)
		defer cancel()
	}
	if dialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dialTimeout)
		defer cancel()
	}
	return dialer.DialContext(ctx, "")
}

// ServeHTTP implements http.Handler. It rewrites the inbound
// request to the target URL, dials via Transport (which dials the
// unix socket), and pipes the response back to the inbound writer.
//
// On dial failure: 502 Bad Gateway. On upstream error: propagated
// unchanged.
func (p *InternalReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Drain tracker (issue #587 / PR-A): a request that's
	// handed off to the proxy is "in flight" from the daemon's
	// perspective until this ServeHTTP returns.
	//
	// Correct shape is `defer tracker.Begin("http")()`:
	// Begin runs IMMEDIATELY at the defer statement
	// (increments the WaitGroup); the Done closure is what
	// gets deferred.
	//
	// The WRONG shape `defer func(){ tracker.Begin(...)() }()`
	// evaluates the entire closure body at function return —
	// Begin and Done both fire then, so the tracker never sees
	// a slot held during the proxy's RoundTrip. Under load,
	// the gateway's drain then `OutcomeClean`s immediately and
	// force-cuts in-flight requests — the exact regression
	// PR-A's WaitGroup was meant to fix.
	//
	// Begin is NOT nil-safe (it's a method on *Tracker); guard
	// explicitly when the drain is absent so the e2e harness
	// and unit tests still compile.
	done := func() {}
	if p.Drain != nil {
		done = p.Drain.Begin("http")
	}
	defer done()

	if p.Dialer == nil || p.Target == nil {
		// Wiring bug — log at ERROR because the customer sees the
		// failure on the public listener.
		p.logger().Error("internal proxy not configured",
			"has_dialer", p.Dialer != nil,
			"has_target", p.Target != nil)
		http.Error(w, "internal proxy not configured", http.StatusBadGateway)
		return
	}
	streamCtx, detachBudget, touch, cancelStream := newStreamSession(r.Context(), 0, streamIdleTimeout)
	defer cancelStream()
	outReq := r.Clone(streamCtx)
	outReq.URL.Scheme = p.Target.Scheme
	outReq.URL.Host = p.Target.Host
	// Keep the inbound Host as the routing key. gatewayd-internal
	// resolves the app from r.Host (handler.go ServeHTTP → backend
	// Lookup), so rewriting it to the internal target name makes
	// every request 404 with "no app is routed to
	// \"gatewayd-internal\"". The URL.Host above is what the
	// transport dials (the unix-socket dialer ignores it anyway);
	// the Host header must stay the customer-facing hostname.
	outReq.Host = r.Host
	outReq.RequestURI = "" // required for outgoing client requests
	// Strip hop-by-hop in place (no second map alloc). The strip
	// is correct for plain HTTP (RFC 7230 §6.1) — but it would
	// destroy the Connection: Upgrade + Upgrade: <token>
	// handshake for inbound WebSocket / h2c / MQTT-over-WS
	// requests before gatewayd-internal's Upgrade detector ever
	// sees them (issue #676 / ADR-080). Skip the strip when the
	// request is an upgrade; the detector lives in pkg/gateway
	// (upgrade.go) and is shared with Handler.ServeHTTP so the
	// two sides of the public→internal hop agree on the
	// case-insensitive RFC 7230 §3.2 parse.
	if !isUpgradeRequest(r) {
		stripHopByHopInPlace(outReq.Header)
	}
	// XFF trust: strip the inbound chain (the customer could
	// forge any IP) and re-add only the public daemon's RemoteAddr.
	// The internal daemon sees exactly one trusted hop.
	outReq.Header.Del("X-Forwarded-For")
	if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		outReq.Header.Set("X-Forwarded-For", clientIP)
	}
	// ADR-119 round-2 (peer-review #1): do NOT strip inbound
	// Authorization. The strip was load-bearing for internal_only
	// (so a customer who set Authorization: Bearer foo could
	// not reach applyIngressInternalSvc over the public TLS hop),
	// but the strip was unconditional — it killed every existing
	// customer bearer/basic/require_authn gate on the Tier-A7
	// gatewayd-public → gatewayd-internal hop. The fix is to
	// leave the header intact: the customer-facing auth gates
	// at handler.go (enforcePublicAuthBearer, enforcePublicAuthBasic,
	// requireAuthnKey) consume the inbound Authorization header
	// for their own purpose, and the internal_only gate
	// (applyIngressInternalSvc) only fires when the app's
	// public_auth_mode is 'internal_only'. The two cases don't
	// collide: a customer with a bearer/basic/require_authn app
	// never reaches applyIngressInternalSvc; a customer with an
	// internal_only app never has a customer-facing auth gate
	// (the mode is mutually exclusive).
	//
	// Why the original "strip" was wrong:
	// The strip assumed the internal_only gate applies to every
	// request, but it's gated on public_auth_mode='internal_only'
	// which is mutually exclusive with bearer/basic/require_authn.
	// Stripping on every hop broke the four pre-existing
	// customer-facing gates. The defensive counter
	// gateway_internal_auth_match_total{outcome="bypass_stripped"}
	// (peer-review #6) is also dead as a result — see the
	// metric removal commit. The strip is removed entirely.
	//
	// The internal_only gate is now reachable only from the
	// unix-socket dial path (handled by the cmd-side wiring:
	// gatewayd-public is NOT a unix-socket dialer; it uses
	// its own TLS listener + proxies to gatewayd-internal).
	// For the unix-socket dial path, schedd attaches its own
	// JWT (see pkg/sched/configure_internal_svc.go) and the
	// gate verifies it. There is no scenario where a customer
	// header survives the public hop AND leaks into the gate.
	_ = r // suppress unused-name if the comment is removed
	if r.TLS != nil {
		outReq.Header.Set("X-Forwarded-Proto", "https")
	} else {
		outReq.Header.Set("X-Forwarded-Proto", "http")
	}
	if outReq.Body != nil {
		outReq.Body = &activityReadCloser{ReadCloser: outReq.Body, activity: touch}
	}
	// RoundTrip runs on a detachable stream context (issue #687). The
	// dial step is bounded by p.DialTimeout via the transport's
	// DialContext/DialTLSContext closure; once a long-lived response
	// commits its headers, the ordinary request budget is detached and
	// body copy is bounded by activity plus the stream idle timeout.
	//
	// resp.Body ownership transferred to copyResponseBody — it
	// owns the close (defer in the read goroutine + the cancel
	// arm). The bodyclose lint rule can't see across function
	// boundaries, so annotate explicitly.
	//nolint:bodyclose
	resp, err := p.Transport.RoundTrip(outReq)
	if err != nil {
		// Distinguish "upstream unreachable" (502) from "client
		// cancelled" (the latter is a normal close, don't log
		// at WARN).
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return
		}
		// Our own request budget firing is not an upstream fault.
		// gatewayd-public installs the reqbudget middleware
		// (cmd/gatewayd-public/main.go, Default=RequestBudgetDefault),
		// so a slow-but-healthy upstream trips this deadline while
		// still doing the work — under a 1000-concurrency burst the
		// compute layer returned 200 for 99% of requests whose
		// RoundTrip had already been cut here and reported as 502.
		//
		// Writing the 502 also pre-empted the middleware: it only
		// synthesises its 504 when the inner handler wrote nothing
		// (pkg/reqbudget/middleware.go, `case bw.wrote` wins), so
		// every budget expiry was additionally recorded as
		// outcome="set" instead of "exceeded" — under-reporting
		// request_budget_exceeded_total. Emitting the canonical
		// envelope here keeps the customer-visible status honest;
		// the metric attribution is fixed separately since the
		// middleware still observes bw.wrote.
		if requestBudgetExpired(streamCtx) {
			p.logger().Warn("internal round-trip exceeded request budget",
				"target", p.Target.String(),
				"err", err)
			writeRequestBudgetExceeded(w)
			return
		}
		p.logger().Warn("internal round-trip failed",
			"target", p.Target.String(),
			"err", err)
		if errors.Is(err, ErrNoComputeCapacity) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "compute capacity unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "bad gateway: internal round-trip failed", http.StatusBadGateway)
		return
	}
	// copyResponseBody owns Body.Close (issue #687: closes it on
	// ctx cancel to release the H2C stream window).
	// Copy headers + body to the inbound writer. Strip hop-by-hop
	// in place on the response (RFC 7230 §6.1) — the internal
	// daemon may have set Connection: close and we don't want to
	// leak that to the customer.
	for k, vv := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Body copy bound to ctx — a hung upstream pins only the
	// in-flight goroutine, not the listener.
	if isLongLivedResponse(resp.StatusCode, resp.Header) {
		detachBudget()
		touch()
	}
	if _, err := copyResponseBodyWithActivity(streamCtx, w, resp.Body, touch); err != nil && !errors.Is(err, context.Canceled) {
		p.logger().Warn("internal body copy failed",
			"target", p.Target.String(),
			"err", err)
	}
}

// stripHopByHopInPlace deletes the hop-by-hop headers (RFC 7230
// §6.1) from h in place. Avoids the double-alloc of Clone +
// rebuild.
func stripHopByHopInPlace(h http.Header) {
	for k := range h {
		if isHopByHop(k) {
			delete(h, k)
		}
	}
}

// isHopByHop returns true for the headers in hopByHopHeaders
// (case-insensitive per RFC 7230 §3.2).
func isHopByHop(h string) bool {
	for _, x := range hopByHopHeaders {
		if strings.EqualFold(h, x) {
			return true
		}
	}
	return false
}

// copyResponseBody drains src to dst, returning on ctx cancel or
// io.EOF. Long-lived callers pass a detachable stream context so
// the initial request budget ends at response headers while client
// cancellation and the stream idle timeout remain active.
//
// Issue #687 (PR-3 review follow-up): three things matter here.
// (1) Per-Write Flush so SSE / chunked-JSON customers see flushes
// at chunk boundaries, not at the 32 KiB io.Copy buffer boundary.
// The Flush type assertion is a no-op for non-http.ResponseWriter
// dst (test *strings.Builder, *bytes.Buffer) so the non-flusher
// path is unchanged.
// (2) On ctx cancel, close src so the goroutine's blocked Read
// returns an error and exits. The H2C connection's stream window
// is released instead of being held open by a stuck reader
// (internal_proxy.go:289-292 in the pre-fix shape).
// (3) Wait on the goroutine's done channel after cancel so the
// goroutine-exit assertion in TestCopyResponseBody_CancelReleasesStreamWindow
// is deterministic — bounded by one final Read latency.
//
// The function owns src's lifecycle: defer Body.Close in the
// caller is redundant after this change.
func copyResponseBody(ctx context.Context, dst io.Writer, src io.ReadCloser) (int64, error) {
	return copyResponseBodyWithActivity(ctx, dst, src, nil)
}

func copyResponseBodyWithActivity(ctx context.Context, dst io.Writer, src io.ReadCloser, activity func()) (int64, error) {
	flusher, _ := dst.(http.Flusher) // ok if dst isn't an http.ResponseWriter
	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		// 32 KiB — same as http.Server's default. Larger buffers
		// cost memory without measurably improving throughput for
		// the customer-app traffic shape (most responses are
		// sub-MiB JSON / HTML).
		//
		// Issue #687 PR-3 review finding: defers run LIFO. The
		// panic-recovery defer MUST be registered BEFORE the
		// src.Close defer so it fires FIRST (during the unwind)
		// — otherwise a panic in dst.Write / flusher.Flush would
		// run src.Close first (closing the body without ever
		// notifying the cancel arm) and the cancel arm's `<-done`
		// would block forever. Register the recover defer first;
		// the close defer runs last so the goroutine still exits.
		//
		// `total` is hoisted above both defers so the recover
		// defer can reference it (Go's closure capture is by
		// reference; the variable must be in scope at defer
		// registration time, not just at defer execution time).
		var total int64
		defer func() {
			if r := recover(); r != nil {
				// Best-effort send so the cancel arm's `<-done`
				// doesn't hang. `done` is buffered with capacity 1
				// so this never blocks. We swallow the panic
				// (don't re-raise) so the goroutine exits cleanly
				// — a re-panic in a goroutine surfaces to the
				// runtime as a crash, and the caller is already
				// out of options (the underlying writer panicked,
				// the stream is broken; nothing useful to do).
				select {
				case done <- result{total, fmt.Errorf("copyResponseBody: panic during copy: %v", r)}:
				default:
				}
			}
		}()
		defer func() { _ = src.Close() }() // ensure goroutine exit even on panic
		buf := make([]byte, 32*1024)
		for {
			n, rErr := src.Read(buf)
			if n > 0 {
				// Honour io.Writer contract: a Write may return
				// fewer than len(p) bytes AND nil error. Loop on
				// the unwritten tail until the full chunk is
				// drained (matching io.Copy's shape). Without this,
				// total overcounts the bytes the customer actually
				// receives when a partial-write dst is passed.
				//
				// Issue #687 PR-3 review finding #2: the previous
				// shape used `total += int64(n)` with `n` being the
				// Read count, not the Write count — diverging from
				// io.Copy semantics on partial writes.
				written := 0
				for written < n {
					nw, wErr := dst.Write(buf[written:n])
					written += nw
					total += int64(nw)
					if wErr != nil {
						done <- result{total, wErr}
						return
					}
					if nw == 0 {
						// Write made no progress but returned nil —
						// io.Copy treats this as the next-read loop
						// unblocking; we follow suit and break out
						// of the partial-write retry, letting the
						// next Read refill the buffer. The bytes
						// already written are counted; the ones not
						// yet written will appear in the next Read.
						break
					}
				}
				if activity != nil && written > 0 {
					activity()
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rErr != nil {
				// Match io.Copy semantics: io.EOF on a clean read
				// is success (the source has signalled end-of-stream
				// without corruption), not an error. Any other
				// error propagates so copyResponseBody callers see
				// upstream-side read failures.
				if rErr == io.EOF {
					done <- result{total, nil}
				} else {
					done <- result{total, rErr}
				}
				return
			}
		}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-ctx.Done():
		// Close src so the goroutine's Read returns; the upstream
		// unix socket sees the FIN and the H2C stream window is
		// released. The second <-done makes the goroutine-exit
		// assertion in tests deterministic.
		_ = src.Close()
		<-done
		return 0, ctx.Err()
	}
}

type activityReadCloser struct {
	io.ReadCloser
	activity func()
}

func (r *activityReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.activity != nil {
		r.activity()
	}
	return n, err
}

func isLongLivedResponse(statusCode int, h http.Header) bool {
	if statusCode != http.StatusSwitchingProtocols && (statusCode < http.StatusOK || statusCode >= http.StatusBadRequest) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(h.Get(api.StreamingStatusHeader))) {
	case string(api.StreamingStatusStreaming), string(api.StreamingStatusUpgradeBypass):
		return true
	default:
		return false
	}
}

func (p *InternalReverseProxy) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}
