// Issue #98 / ADR-028: gatewayd-internal's HTTP→gRPC forwarder.
//
// gatewayd-internal's hot path looks up the compute_node.id an instance lives on
// (cached in PGBackend.targets after Wake) and forwards the inbound HTTP
// request to vmmd's ForwardHTTP RPC. vmmd then nsenter's the per-instance
// netns and dials netns.GuestIP:netns.AppPort on the inner side (see
// pkg/vmmdgrpc/forward.go). This file is the gateway-side half of that
// bridge.
//
// Why HTTP→gRPC and not direct HTTP to the overlay: vmmd's only listener
// is its gRPC server (issue #95), so reusing it for the bridge keeps the
// transport stack flat. Adding a second listener per vmmd box would
// double the surface for the §11 auth model (unix-socket mode 0660 +
// group-`faas` for v1.0, mTLS for multi-host) and we already have mTLS
// from issue #95 — forwarding over the same gRPC channel reuses the
// certs, the overlay dial (pkg/wire.DialContext), and the auth shape.
//
// Per-node client caching: each compute_node gets one *grpc.ClientConn
// cached for the process lifetime. The first request to a node pays the
// dial cost (≈30-80 ms on the Lima arm64 nested-KVM guest, plus mTLS
// handshake); subsequent requests hit the cached conn. Eviction is the
// LISTEN/NOTIFY channel `compute_node_changed` which the cache listens
// on; an admin DELETE /v1/compute-nodes or a stale-heartbeat
// deactivation drops the cached conn so the next request re-dials
// against the fresh node row.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	evts "github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type syntheticInvocationContextKey struct{}

// WithSyntheticInvocation marks an internal scheduler-to-runner request.
// Platform-owned invocation metadata may cross the vmmd HTTP bridge only for
// this context; ordinary customer requests keep the x-faas-* strip policy.
func WithSyntheticInvocation(ctx context.Context) context.Context {
	return context.WithValue(ctx, syntheticInvocationContextKey{}, true)
}

func isSyntheticInvocation(ctx context.Context) bool {
	v, _ := ctx.Value(syntheticInvocationContextKey{}).(bool)
	return v
}

// NodeClientLookup resolves a compute_node.id to a cached
// *grpc.ClientConn dialed to that node's vmmd gRPC server. Implementations
// must be safe for concurrent use. The gateway owns the cache
// (cmd/gatewayd-internal/nodecache.go); tests pass a fake.
type NodeClientLookup interface {
	// ClientFor returns a *grpc.ClientConn for the named compute node
	// and a close function the caller MUST call when done with it.
	// ok=false when the node is unknown to the cache (admin DELETE'd
	// it, never registered, or just deactivated). The handler surfaces
	// 503 in that case.
	ClientFor(ctx context.Context, nodeID string) (cli vmmdpb.VmmdClient, closer io.Closer, ok bool)
}

// hopByHopHeaders are stripped from the request before forwarding
// (RFC 7230 §6.1). They have meaning only on the inbound hop and
// re-injecting them inside the bridge confuses the guest (e.g.
// Connection: close would make the guest reply, then close, then the
// response would be truncated on the next request). Keep this list in
// one place — handler/forwardproxy/cert-manager all need the same set.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// stripHopByHop drops the headers above from h in place. We mutate a
// shallow copy so the inbound request (already observed by middleware
// that reads headers) is not surprised by a missing field.
func stripHopByHop(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range hopByHopHeaders {
		out.Del(k)
	}
	return out
}

// ForwardingReverseProxy returns an http.Handler that forwards r to
// the vmmd that owns the instance the node id routes to. It is the
// post-#98 / ADR-028 replacement for defaultProxy: defaultProxy
// assumed `addr = host_ip:8080` and dialed the inner side directly;
// the inner side is reachable only from inside the vmmd host's netns
// (10.100.x.y/16 is bound on veth_peer per ADR-009) so gatewayd-internal can't
// reach it from a remote box. This handler instead asks vmmd to
// bridge the bytes via the bidi ForwardHTTPStream RPC.
//
// ctxHook (optional) lets the caller cancel the bridge mid-flight
// when the inbound request is cancelled (client disconnect). nil
// means "no special cancellation"; we still wire the inbound ctx to
// the bridge call so the standard http.Server cancellation works.
//
// PR-C (issue #460 / ADR-053): the returned closure receives the
// full Target so the per-deployment override port (Target.Port) can
// be stamped onto ForwardHTTPRequestInit.port. vmmd's bridge resolves
// port=0 to netns.AppPort (8080) so legacy cached targets keep
// working bit-for-bit.
//
// PR-D / ADR-047: the legacy unary ForwardHTTP RPC was removed. The
// streaming RPC is the only bridge today — see fwdStreamOnce below.
//
// Deprecated: PR-C (issue #517 / ADR-064) added the events-aware
// sibling; the legacy 3-arg signature exists only to keep the
// pre-PR-C caller surface compiling. Gateway production wiring
// has moved to ForwardingReverseProxyWithEvents. A future cleanup
// PR will drop this wrapper.
func ForwardingReverseProxy(nodes NodeClientLookup, log *slog.Logger) func(t Target) http.Handler {
	return ForwardingReverseProxyWithEvents(nodes, log, nil)
}

// ForwardingReverseProxyWithEvents (issue #517 / PR-C / ADR-064) is
// the events-aware variant of ForwardingReverseProxy. The Platform
// emits wake.proxy_first_byte on the first downstream byte (the
// Response Init frame's WriteHeader). nil events opts out (legacy
// callers and the test corpus).
func ForwardingReverseProxyWithEvents(nodes NodeClientLookup, log *slog.Logger, events *evts.Platform) func(t Target) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(t Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Stamp the proxy start so the wake.proxy_first_byte
			// emit can compute latency_ms = first_byte - start.
			// The metric is the customer-facing "how long until
			// the upstream answered"; pre-PR-C the gateway had
			// no introspection here.
			ctx := contextWithProxyStart(r.Context(), time.Now())
			fwdOnceWithEvents(w, r.WithContext(ctx), nodes, log, t, events)
		})
	}
}

// proxyStartKey is the unexported context key for the proxy start
// timestamp. Used by wake.proxy_first_byte to compute latency_ms
// without re-deriving it from the Accept timestamp (PR-A).
type proxyStartKey struct{}

// contextWithProxyStart returns a copy of ctx with the proxy start
// timestamp stored under proxyStartKey.
func contextWithProxyStart(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, proxyStartKey{}, t)
}

// proxyStartFromContext returns the proxy start timestamp stored
// on ctx, or a zero time if the stamp is missing (the legacy
// fwdOnce path, which doesn't thread the events seam).
func proxyStartFromContext(ctx context.Context) time.Time {
	if v, ok := ctx.Value(proxyStartKey{}).(time.Time); ok {
		return v
	}
	return time.Time{}
}

// fwdOnce runs a single forward. Extracted so the test harness can
// drive it without standing up an http.Server. The defer at the top
// is the only panic-safety net — if a future maintainer adds a step
// that panics on a malformed request, we still observe the request
// via slog instead of crashing the listener.
//
// Deprecated: PR-C (issue #517 / ADR-064) added the events-aware
// sibling; this 5-arg signature exists only to keep the pre-PR-C
// test corpus compiling. New code should call fwdOnceWithEvents
// directly. A future cleanup PR will drop this wrapper.
//
// PR-D / ADR-047: this is now a thin wrapper that delegates to
// fwdStreamOnce (the bidi ForwardHTTPStream RPC is the only bridge
// today). The buffered unary path was removed; the streaming path
// handles small/short responses correctly because the bridge pipes
// bytes through Go's bufio and never enforces a latency floor.
//
//nolint:unused // removed-from-API 5-arg wrapper; kept for the pre-PR-C test corpus. Future cleanup PR drops it.
func fwdOnce(w http.ResponseWriter, r *http.Request, nodes NodeClientLookup, log *slog.Logger, t Target) {
	fwdOnceWithEvents(w, r, nodes, log, t, nil)
}

// fwdOnceWithEvents (issue #517 / PR-C / ADR-064) is the
// events-aware variant of fwdOnce. The events seam threads
// through to fwdStreamOnce so wake.proxy_first_byte can be
// emitted on the first downstream byte (the Response Init
// frame's WriteHeader). nil opts out (pre-PR-C fixtures + the
// legacy fwdOnce entry).
func fwdOnceWithEvents(w http.ResponseWriter, r *http.Request, nodes NodeClientLookup, log *slog.Logger, t Target, events *evts.Platform) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("gateway: forwarder panic",
				"node", t.NodeID, "err", fmt.Sprintf("%v", rec))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()

	if t.NodeID == "" {
		// Defensive: an empty node id would mean the routing cache
		// evicted between Target() and the proxy call. Surface 503 so
		// the client retries; this should not happen because the
		// Target-check and the proxy call run on the same goroutine
		// under the WakeGate, but the contract has to outlive the
		// goroutine.
		http.Error(w, "no node available", http.StatusServiceUnavailable)
		return
	}

	cli, closer, ok := nodes.ClientFor(r.Context(), t.NodeID)
	if !ok {
		markStaleTarget(r.Context())
		http.Error(w, "node unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = closer.Close() }()

	// PR-D / ADR-047: the streaming RPC is the only bridge today.
	// The Handler still stamps `x-faas-stream: true` on the inbound
	// request (handler.go:setupStreamingWriter) as the load-bearing
	// signal for the vmmd-side cap-lift; the gateway forwarder
	// strips it before bridging (see the x-faas-* skip below) so
	// the guest never observes the internal signal.
	fwdStreamOnceWithEvents(w, r, cli, log, t, events)
}

// fwdStreamOnce is the streaming counterpart of fwdOnce (issue
// #471 PR-B + PR-C / ADR-047). It opens a bidi
// ForwardHTTPStream, sends the request init frame, streams the
// request body in 8 KiB chunks, and reads response frames back
// into the statusRecorder (which fires the per-flush
// egressSink.RecordResponseBytes deltas via onFlush).
//
// Why this is a separate function and not a branch in fwdOnce:
// the request body is no longer buffered — it's pipelined into
// the bidi stream as it arrives. A streaming client that
// uploads 50 MB via chunked transfer no longer hits the 25 MiB
// cap (the cap-lift to 100 MB applies on the vmmd side). A
// client disconnect tears down the bridge cleanly via the
// inbound request context.
//
// Error mapping mirrors fwdOnce (Unavailable → 503, NotFound →
// 503, anything else → 502). The Handler's statusRecorder
// translates 4xx/5xx into "no body bytes to meter" so the
// e2e quota AC stays correct under error paths.
//
// Deprecated: PR-C (issue #517 / ADR-064) added the events-aware
// sibling; this 5-arg signature exists only to keep the pre-PR-C
// test corpus compiling. New code should call
// fwdStreamOnceWithEvents directly. A future cleanup PR will drop
// this wrapper.
//
//nolint:unused // deprecated 5-arg wrapper; kept for the pre-PR-C test corpus. Future cleanup PR drops it.
func fwdStreamOnce(w http.ResponseWriter, r *http.Request, cli vmmdpb.VmmdClient, log *slog.Logger, t Target) {
	fwdStreamOnceWithEvents(w, r, cli, log, t, nil)
}

// fwdStreamOnceWithEvents (issue #517 / PR-C / ADR-064) is the
// events-aware variant of fwdStreamOnce. wake.proxy_first_byte is
// emitted on the first downstream byte (the Response Init
// frame's WriteHeader). nil events opts out (pre-PR-C fixtures).
//
// ADR-093 / PR-C: when the inbound request carries an end-to-end
// budget, the 910 s stream session is bounded by the budget's
// remaining time (min(parentRemaining, 910s)). The 910 s ceiling is
// unchanged — the budget can only shorten it. When no Budget is
// attached to ctx, the legacy context.WithTimeout(910s) ceiling is
// preserved.
func fwdStreamOnceWithEvents(w http.ResponseWriter, r *http.Request, cli vmmdpb.VmmdClient, log *slog.Logger, t Target, events *evts.Platform) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if b, ok := reqbudget.FromContext(r.Context()); ok {
		ctx, cancel, _ = b.WithCeiling(r.Context(), 910*time.Second)
	} else {
		ctx, cancel = context.WithTimeout(r.Context(), 910*time.Second)
	}
	defer cancel()

	// ADR-124: read the customer's per-app wire-protocol choice
	// (stamped on r by Handler.ServeHTTP as x-faas-protocol at the
	// site x-faas-stream is stamped today). The value is recorded
	// for observability in this PR — the actual wire on the
	// public↔internal and internal↔guest hops is governed by the
	// process-global FAAS_INTERNAL_H2C and FAAS_STREAM_BRIDGE_VERSION
	// knobs, not by per-app state (see ADR-124 §"Architecture" §"Out
	// of scope" §17 G19 for the bridge-side termination ADR that
	// would let customer protocol choice reach the guest's :8080).
	// The hop the gatewayd-internal forwarder controls is the gRPC
	// bidi ForwardHTTPStream; vmmd-stream-bridge then re-frames to
	// HTTP/1.1 plaintext today. This means end-to-end framing
	// (customer→guest) for `app_protocol=grpc/http2` is filed as
	// G19 (out of scope for this PR); the customer-visible
	// effect of this PR is **metadata plumbing + plan gating +
	// observability + header-stamp on the inbound request**, NOT a
	// transport switch on the bridge.
	protocol := r.Header.Get("x-faas-protocol")
	if protocol == "" {
		protocol = "http1"
	}
	if log.Enabled(r.Context(), slog.LevelDebug) {
		log.Debug("gateway: framing selection",
			"node", t.NodeID,
			"app", r.Header.Get("x-faas-app"),
			"app_protocol", protocol)
	}

	stream, err := cli.ForwardHTTPStream(ctx)
	if err != nil {
		if handleForwardRequestCancellation(w, r, true) {
			return
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			markStaleTarget(r.Context())
		}
		log.Error("gateway: forwarder stream open failed",
			"node", t.NodeID, "err", err.Error())
		http.Error(w, "forwarder stream open failed", http.StatusBadGateway)
		return
	}

	// Build the init frame from the inbound request headers.
	// Hop-by-hop and x-faas-* headers are stripped (mirrors
	// fwdOnce); everything else is forwarded as-is. The body
	// is NOT included — it streams in via the body-copy
	// goroutine below.
	//
	// PR-C (issue #463 / ADR-069 / ADR-071 / PR-C §5): the
	// per-request sidecar-port override (stamped by the
	// handler when the routing-key resolver picks a sidecar)
	// takes precedence over Target.Port. The handler can't
	// mutate Target.Port on the per-target cursor (the
	// target set is shared across all instances of a
	// deployment), so the override rides the request context
	// and the forwarder reads it here. Port=0 means "no
	// override" — the forwarder falls back to Target.Port
	// (the main workload's port).
	port := uint32(t.Port)
	if sidecarPort := SidecarPortFrom(r); sidecarPort != 0 {
		port = uint32(sidecarPort)
	}
	init := &vmmdpb.ForwardHTTPRequestInit{
		Instance:   r.Header.Get("x-faas-instance"),
		Method:     r.Method,
		RequestUri: r.URL.RequestURI(),
		Port:       port,
		Stream:     true,
	}
	for name, vals := range stripHopByHop(r.Header) {
		if strings.HasPrefix(strings.ToLower(name), "x-faas-") &&
			(!isSyntheticInvocation(r.Context()) || !strings.EqualFold(name, "x-faas-invocation-id")) {
			continue
		}
		for _, v := range vals {
			init.Headers = append(init.Headers, &vmmdpb.Header{Name: name, Value: v})
		}
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{
		Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: init},
	}); err != nil {
		if handleForwardRequestCancellation(w, r, true) {
			return
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			markStaleTarget(r.Context())
		}
		log.Error("gateway: forwarder stream init send failed",
			"node", t.NodeID, "err", err.Error())
		http.Error(w, "forwarder stream init failed", http.StatusBadGateway)
		return
	}

	// Body-copy goroutine: stream r.Body → stream in 8 KiB
	// chunks. The first error (client disconnect, send
	// failure) wins; the receiver goroutine below surfaces
	// it via sendErr so the bidi close is clean.
	//
	// Cancellation: when the derived stream context is cancelled
	// (client disconnect, gateway-side deadline, or upstream
	// receiver failure), the goroutine exits promptly via the
	// ctxReader closing r.Body. Without this, a client that uploads 1 byte
	// per second and then disconnects would leave the
	// goroutine blocked on r.Body.Read until the gateway's
	// http.Server.ReadTimeout fires — that's a goroutine
	// leak visible in production dashboards. (Issue #471
	// review F3 fix.)
	bodyErrCh := make(chan error, 1)
	go func() {
		cr, stopReader := newCtxReader(ctx, r.Body)
		defer stopReader()
		buf := make([]byte, 8*1024)
		for {
			n, err := cr.Read(buf)
			if n > 0 {
				if serr := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{
					Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{
						BodyChunk: append([]byte(nil), buf[:n]...),
					},
				}); serr != nil {
					bodyErrCh <- serr
					return
				}
			}
			if errors.Is(err, io.EOF) {
				_ = stream.CloseSend()
				bodyErrCh <- nil
				return
			}
			if err != nil {
				_ = stream.CloseSend()
				bodyErrCh <- err
				return
			}
		}
	}()

	// Receiver loop: read frames and pipe into w. The first
	// frame is ForwardHTTPResponseInit (status + headers);
	// subsequent frames are body_chunk bytes that go
	// straight to w.Write (which the statusRecorder
	// intercepts to fire maybeFlush → onFlush).
	wroteHeader := false
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Cancel the derived stream context so the request body is
			// closed and the body goroutine can finish before return.
			cancel()
			<-bodyErrCh
			if handleForwardRequestCancellation(w, r, !wroteHeader) {
				return
			}
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
				markStaleTarget(r.Context())
				log.Warn("gateway: forwarder stream Unavailable; surfacing 503",
					"node", t.NodeID)
				http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				return
			}
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				markStaleTarget(r.Context())
				http.Error(w, "instance gone", http.StatusServiceUnavailable)
				return
			}
			log.Error("gateway: forwarder stream Recv failed",
				"node", t.NodeID, "err", err.Error())
			http.Error(w, "forwarder stream failed", http.StatusBadGateway)
			return
		}
		if init := frame.GetInit(); init != nil && !wroteHeader {
			recordForwardedFirstByte(r.Context())
			for _, h := range init.GetHeaders() {
				w.Header().Add(h.GetName(), h.GetValue())
			}
			w.WriteHeader(int(init.GetStatus()))
			wroteHeader = true
			// issue #517 / PR-C / ADR-064 — emit
			// wake.proxy_first_byte on the first downstream
			// byte. The wake_id is the per-wake correlation
			// handle minted by the engine (Target.WakeID — the
			// gateway-fanout cache sets it from the
			// AdmitInstanceResponse on the wake). requestID
			// is the inbound x-faas-request-id minted by the
			// gateway edge. latency_ms is the gap from the
			// proxy start (stamped on the request context by
			// the closure) to the first byte. nil opts out
			// (pre-PR-C fixtures).
			//
			// `evs` aliases the parameter so the package
			// name `evts.ProxyFirstByte` is still reachable
			// inside the if-block (the local parameter
			// otherwise shadows the package).
			if evs := events; evs != nil {
				started := proxyStartFromContext(r.Context())
				evs.Emit(r.Context(), evts.ProxyFirstByte{
					EmitAt:     time.Now().UTC(),
					WakeID:     t.WakeID,
					AppID:      r.Header.Get("x-faas-app"),
					RequestID:  r.Header.Get("x-faas-request-id"),
					InstanceID: t.InstanceID,
					NodeID:     t.NodeID,
					LatencyMs:  time.Since(started).Milliseconds(),
				})
			}
			continue
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			if _, werr := w.Write(chunk); werr != nil {
				// Client disconnect mid-stream. The
				// receiver stops reading frames. The
				// body-copy goroutine is cancelled via
				// the ctxReader (F3 fix) so it exits
				// within the stream-teardown window;
				// drain bodyErrCh so the handler
				// doesn't return while the goroutine
				// is still unwinding.
				log.Debug("gateway: forwarder stream client write failed",
					"node", t.NodeID, "err", werr.Error())
				cancel()
				<-bodyErrCh
				return
			}
		}
	}

	// Wait for the body goroutine to drain so we can
	// distinguish a clean bidi close from a client-disconnect
	// (which surfaces as a Send error).
	<-bodyErrCh
}

// rawStreamSessionDeadline is the wall-clock ceiling for a single
// raw-bytes Upgrade session (issue #676 / ADR-080). WebSocket
// sessions and long-poll clients are long-lived by design — a
// chat-completion agent WS can hold a connection for hours. The
// deadline is intentionally generous (24h) so customers don't see
// forced reconnects during a normal session; the per-connection
// inbound body cap (api.RawStreamMaxRequestBytes, 100 MiB) is the
// load-bearing memory bound on the gateway side. The bridge
// (cmd/vmmd-raw-bridge) is unbounded on the session body because
// the inbound cap is enforced at the init frame.
const rawStreamSessionDeadline = 24 * time.Hour

// rawStreamOnceWithEvents (issue #676 / ADR-080) is the events-aware
// raw-bytes forwarder for WebSocket / h2c / MQTT-over-WS / long-poll
// Upgrade traffic. Where fwdStreamOnceWithEvents parses the inbound
// request into Method/URL/Headers and reconstructs the response
// from a parsed init + chunked body, rawStreamOnceWithEvents carries
// bytes verbatim — gatewayd-internal → vmmd ForwardRawStream →
// vmmd-raw-bridge → guest netns TCP socket — and back. This
// preserves the Connection: Upgrade + Upgrade: <token> handshake
// that the plain-HTTP forwarder's hop-by-hop strip would destroy.
//
// The shape mirrors fwdStreamOnceWithEvents: NodeClientLookup
// resolution (handled by the caller, ForwardingRawReverseProxy),
// a ctxReader-based body-copy goroutine, a receiver loop that
// writes the init frame's status + headers then streams body
// chunks via Write+Flush, and the same bodyErrCh drain on exit.
//
// Why a Flush per Write:
//
//   - The inbound transport is H2C (gRPC-gatewayd-internal); H2 has
//     no "raw mode" but DATA frames are just bytes. Every Flush
//     emits a DATA frame to the customer so a WS server's slow
//     trickle (1 byte per 100 ms) reaches the customer within the
//     flush interval rather than the 32 KiB io.Copy buffer.
//
// Why a 24h session deadline:
//
//   - WS sessions are long-lived by design. The deadline is the
//     memory bound on the gateway-side goroutine pair, not a
//     customer-visible timeout. A customer who exceeds 24h simply
//     reconnects; the metrics layer (gateway_ws_session_duration_seconds)
//     records the disconnect.
//
// nil events opts out (legacy callers and the test corpus).
//
// ADR-093 / PR-C: when the inbound request carries an end-to-end
// budget, the 24 h raw-stream session is bounded by the budget's
// remaining time (min(parentRemaining, rawStreamSessionDeadline)).
// The 24 h ceiling is unchanged — the budget can only shorten it.
// When no Budget is attached to ctx, the legacy
// context.WithTimeout(rawStreamSessionDeadline) ceiling is preserved.
func rawStreamOnceWithEvents(w http.ResponseWriter, r *http.Request, cli vmmdpb.VmmdClient, log *slog.Logger, t Target, events *evts.Platform, sink *egresssink.EgressSink) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if b, ok := reqbudget.FromContext(r.Context()); ok {
		ctx, cancel, _ = b.WithCeiling(r.Context(), rawStreamSessionDeadline)
	} else {
		ctx, cancel = context.WithTimeout(r.Context(), rawStreamSessionDeadline)
	}
	defer cancel()

	// Issue #676 / ADR-080 follow-up, PR-B: instrument the WS
	// observability surface (gateway_ws_* Prometheus series).
	// The (plan, metrics) pair is stamped onto the request
	// context at the cmd/gatewayd-internal three-input gate
	// (pkg/gateway/handler.go:2899) via withWSContext so the
	// forwarder doesn't need a wider public signature. The
	// outcome variable is captured by reference in the four
	// `return` branches below — each sets it before returning,
	// and the deferred ObserveWSSessionDuration fires on the
	// way out with the resolved label.
	plan, metrics := wsContextFrom(r.Context())
	sessionStart := time.Now()
	if metrics != nil {
		metrics.IncWSSessionStart(string(plan))
	}
	var wsOutcome WSOutcome
	defer func() {
		if metrics == nil {
			return
		}
		// Default to client_disconnect if no branch set the
		// outcome — covers the common case where the function
		// exits via the normal bidi close (no explicit
		// `return` path sets wsOutcome). The receiver loop's
		// EOF branch is the only path that relies on this
		// default; everything else sets wsOutcome before
		// returning.
		if wsOutcome == "" {
			wsOutcome = WSOutcomeClientDisconnect
		}
		metrics.DecWSSessionEnd(string(plan))
		metrics.ObserveWSSessionDuration(string(plan), wsOutcome, time.Since(sessionStart))
	}()

	stream, err := cli.ForwardRawStream(ctx)
	if err != nil {
		wsOutcome = WSOutcomeInitFailed
		log.Error("gateway: raw forwarder stream open failed",
			"node", t.NodeID, "err", err.Error())
		http.Error(w, "raw forwarder stream open failed", http.StatusBadGateway)
		return
	}

	// Init frame: instance + port + per-request body cap. The
	// bridge (pkg/vmmdgrpc/forward.go) clamps MaxRequestBytes
	// DOWN to api.RawStreamMaxRequestBytes (PR-1 review-fix #2)
	// so a misconfigured caller cannot grow the cap past the
	// limit. The gateway-side forwarder just stamps the constant
	// unchanged — the clamp is at the trust boundary.
	//
	// Headers are NOT included in the init frame; the raw bridge
	// expects the inbound HTTP request to arrive as a body_chunk
	// stream (the very first chunk is the request line + headers,
	// the rest is the request body). This is the load-bearing
	// difference from fwdStreamOnceWithEvents: the wire carries
	// the bytes the client wrote, including the Connection +
	// Upgrade headers the hop-by-hop strip would otherwise drop.
	port := uint32(t.Port)
	if sidecarPort := SidecarPortFrom(r); sidecarPort != 0 {
		port = uint32(sidecarPort)
	}
	init := &vmmdpb.ForwardRawRequestInit{
		Instance:        r.Header.Get("x-faas-instance"),
		Port:            port,
		MaxRequestBytes: api.RawStreamMaxRequestBytes,
	}
	if err := stream.Send(&vmmdpb.ForwardRawRequest{
		Frame: &vmmdpb.ForwardRawRequest_Init{Init: init},
	}); err != nil {
		wsOutcome = WSOutcomeInitFailed
		log.Error("gateway: raw forwarder stream init send failed",
			"node", t.NodeID, "err", err.Error())
		http.Error(w, "raw forwarder stream init failed", http.StatusBadGateway)
		return
	}

	// Body-copy goroutine: stream r.Body → stream in 8 KiB
	// chunks. The first error wins; the receiver loop below
	// surfaces it via bodyErrCh so the bidi close is clean.
	//
	// Cancellation: when the derived stream context is cancelled
	// (client disconnect, gateway-side deadline, or upstream
	// receiver failure), the goroutine exits promptly via the
	// ctxReader closing r.Body. Same F3 fix as fwdStreamOnceWithEvents.
	//
	// Issue #676 / ADR-080 follow-up, PR-B: the tx byte counter
	// increments per `stream.Send` (the bytes flowing
	// customer→guest). The counter is increment-after-send so a
	// half-send that the receiver catches surfaces as the
	// upstream_unavailable outcome rather than a missing-byte
	// observation. Prometheus counters are atomic, so the
	// concurrent tx/rx increment from the body goroutine +
	// receiver loop is race-free without a mutex.
	bodyErrCh := make(chan error, 1)
	go func() {
		cr, stopReader := newCtxReader(ctx, r.Body)
		defer stopReader()
		buf := make([]byte, 8*1024)
		for {
			n, err := cr.Read(buf)
			if n > 0 {
				if serr := stream.Send(&vmmdpb.ForwardRawRequest{
					Frame: &vmmdpb.ForwardRawRequest_BodyChunk{
						BodyChunk: append([]byte(nil), buf[:n]...),
					},
				}); serr != nil {
					// A Send failure on the request
					// side is an upstream-availability
					// issue (the bridge closed the
					// bidi stream because the guest
					// went away, the per-instance
					// netns was torn down, or the
					// vmmd process crashed). Without
					// this label, the defer's default
					// of WSOutcomeClientDisconnect
					// would race-mislabel the
					// histogram (PR-B code review
					// finding #2: the body goroutine
					// can record wsOutcome before the
					// receiver loop sees the
					// corresponding Recv error, and
					// the latter would otherwise
					// overwrite it with the same
					// WSOutcomeUpstreamUnavailable).
					// Both goroutines writing the
					// same constant is benign — the
					// race is only on the *value*,
					// not on the observability
					// contract.
					wsOutcome = WSOutcomeUpstreamUnavailable
					bodyErrCh <- serr
					return
				}
				if metrics != nil {
					metrics.AddWSSessionBytes(string(plan), WSDirectionTx, int64(n))
				}
			}
			if errors.Is(err, io.EOF) {
				_ = stream.CloseSend()
				bodyErrCh <- nil
				return
			}
			if err != nil {
				_ = stream.CloseSend()
				bodyErrCh <- err
				return
			}
		}
	}()

	// Receiver loop: read frames and pipe into w. The first
	// frame is ForwardRawResponseInit (status + headers + error);
	// subsequent frames are body_chunk bytes that go straight
	// to w.Write + Flush (the statusRecorder intercepts Write
	// to fire maybeFlush → onFlush; the explicit Flush on the
	// raw path guarantees the H2C DATA frame emits even when
	// the chunk is below the 32 KiB maybeFlush threshold).
	wroteHeader := false
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Cancel the derived stream context so the request body is
			// closed and the body goroutine can finish before return.
			cancel()
			<-bodyErrCh
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
				wsOutcome = WSOutcomeUpstreamUnavailable
				log.Warn("gateway: raw forwarder stream Unavailable; surfacing 503",
					"node", t.NodeID)
				http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				return
			}
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				wsOutcome = WSOutcomeUpstreamUnavailable
				http.Error(w, "instance gone", http.StatusServiceUnavailable)
				return
			}
			wsOutcome = WSOutcomeUpstreamUnavailable
			log.Error("gateway: raw forwarder stream Recv failed",
				"node", t.NodeID, "err", err.Error())
			http.Error(w, "raw forwarder stream failed", http.StatusBadGateway)
			return
		}
		if init := frame.GetInit(); init != nil && !wroteHeader {
			recordForwardedFirstByte(r.Context())
			for _, h := range init.GetHeaders() {
				w.Header().Add(h.GetName(), h.GetValue())
			}
			w.WriteHeader(int(init.GetStatus()))
			wroteHeader = true
			// Mirror fwdStreamOnceWithEvents: emit
			// wake.proxy_first_byte on the first downstream
			// byte. nil events opts out (legacy callers and
			// the test corpus).
			if evs := events; evs != nil {
				started := proxyStartFromContext(r.Context())
				evs.Emit(r.Context(), evts.ProxyFirstByte{
					EmitAt:     time.Now().UTC(),
					WakeID:     t.WakeID,
					AppID:      r.Header.Get("x-faas-app"),
					RequestID:  r.Header.Get("x-faas-request-id"),
					InstanceID: t.InstanceID,
					NodeID:     t.NodeID,
					LatencyMs:  time.Since(started).Milliseconds(),
				})
			}
			// If the init carries an error string (the bridge
			// failed to dial the guest and wrote a synthetic
			// 502 with the dial error in the body), surface
			// that as the response body so a customer reading
			// `init.error != ''` sees the cause. The status
			// was already written above; the body is the
			// last write before the receiver loop exits.
			if init.Error != "" {
				// Bridge dial failure is an upstream-
				// availability issue — the bridge wrote
				// the synthetic 502 from inside the
				// per-instance netns because the guest
				// refused the connect. Without this
				// label, the defer's default of
				// WSOutcomeClientDisconnect would tag
				// the histogram as customer-side churn
				// (PR-B code review finding #1:
				// polluting the
				// rate(gateway_ws_session_duration_seconds{outcome="client_disconnect"})
				// panel when the bridge is the source of
				// the failure).
				wsOutcome = WSOutcomeUpstreamUnavailable
				errBody := []byte(init.Error + "\n")
				if _, werr := w.Write(errBody); werr == nil && sink != nil {
					// Init-error body is part of the
					// per-instance egress ring too:
					// the bridge wrote the synthetic
					// 502 from inside the per-instance
					// netns, so the bytes count toward
					// the same usage_minutes.tx_bytes
					// bucket the body_chunks do.
					sink.RecordResponseBytes(t.InstanceID, int64(len(errBody)))
				}
				flushSafe(w)
			}
			continue
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			// Issue #676 / ADR-080 follow-up, PR-B: rx byte
			// counter increments per chunk received
			// (guest→customer). Counter is increment-before-
			// write so the gauge / counter pair stays
			// self-consistent even if w.Write fails — the
			// bytes DID arrive from the guest, the
			// client_disconnect outcome label is for the
			// fact that they didn't reach the customer.
			if metrics != nil {
				metrics.AddWSSessionBytes(string(plan), WSDirectionRx, int64(len(chunk)))
			}
			if _, werr := w.Write(chunk); werr != nil {
				// Client disconnect mid-stream. The
				// receiver stops reading frames. The
				// body-copy goroutine is cancelled via
				// the ctxReader (F3 fix) so it exits
				// within the stream-teardown window;
				// drain bodyErrCh so the handler
				// doesn't return while the goroutine
				// is still unwinding.
				wsOutcome = WSOutcomeClientDisconnect
				log.Debug("gateway: raw forwarder stream client write failed",
					"node", t.NodeID, "err", werr.Error())
				cancel()
				<-bodyErrCh
				return
			}
			// Egress ring: every raw-stream byte the gateway
			// forwards to the client counts toward this
			// instance's usage_minutes.tx_bytes bucket.
			// RecordResponseBytes is nil-safe on a nil
			// receiver and on an empty InstanceID (skip),
			// so the production path and the unit-test
			// corpus both call it unconditionally and the
			// guard lives at the sink boundary.
			if sink != nil {
				sink.RecordResponseBytes(t.InstanceID, int64(len(chunk)))
			}
			// Flush per Write so the H2C transport emits
			// a DATA frame on every body_chunk. The
			// statusRecorder's maybeFlush only fires at
			// the 32 KiB threshold or the periodic timer;
			// a WS frame of 256 bytes would otherwise
			// buffer in the gateway until one of those
			// triggers fired.
			flushSafe(w)
		}
	}

	// Wait for the body goroutine to drain so we can
	// distinguish a clean bidi close from a client-disconnect
	// (which surfaces as a Send error).
	<-bodyErrCh
	// wsOutcome stays at WSOutcomeClientDisconnect (the defer's
	// default) for the clean bidi close. A future enhancement
	// could inspect bodyErrCh's final value to distinguish
	// client-initiated FIN from server-initiated close; for
	// PR-B the customer-side churn signal is sufficient.
}

// flushSafe is a recover-guarded wrapper around http.Flusher.Flush
// (via http.NewResponseController) for the raw-bytes Upgrade path.
// The inbound transport is H2C in production (a real Flusher), but
// the test corpus uses httptest.NewRecorder which is NOT a Flusher.
// http.NewResponseController(w).Flush() panics on a non-Flusher
// transport; recover() turns the panic into a no-op so the test
// corpus keeps working without per-test transport plumbing. The
// recover is per-call so a panic in production code surfaces
// correctly on the next Flush.
func flushSafe(w http.ResponseWriter) {
	defer func() {
		_ = recover() // non-Flusher transport: degrade to buffered Write
	}()
	_ = http.NewResponseController(w).Flush()
}

// ForwardingRawReverseProxy returns an http.Handler factory that
// drives the raw-bytes bridge (issue #676 / ADR-080). Where
// ForwardingReverseProxy routes plain HTTP through
// fwdOnceWithEvents → fwdStreamOnceWithEvents → ForwardHTTPStream,
// this factory routes Upgrade traffic through rawStreamOnceWithEvents
// → ForwardRawStream. The handler.ServeHTTP call site decides which
// factory's output to invoke (per the isUpgradeRequest detector);
// the factories share the same NodeClientLookup so the underlying
// gRPC channel is reused regardless of which RPC is in flight.
//
// sink is the per-instance egress ring (see pkg/gateway/egresssink)
// that the raw path's bytes flow into via
// rawStreamOnceWithEvents's RecordResponseBytes hook — the legacy
// setupStreamingWriter wrap at handler.go:2836-2838 is gated by
// !isUpgradeRequest(r), so the raw path does not install that
// onFlush hook itself. nil sink opts out (the test corpus and any
// pre-egress-ring callers); RecordResponseBytes is nil-safe.
func ForwardingRawReverseProxy(nodes NodeClientLookup, log *slog.Logger, sink *egresssink.EgressSink) func(t Target) http.Handler {
	return ForwardingRawReverseProxyWithEvents(nodes, log, nil, sink)
}

// ForwardingRawReverseProxyWithEvents is the events-aware variant
// of ForwardingRawReverseProxy (issue #676 / ADR-080). nil events
// opts out (pre-PR-C fixtures and the unit-test corpus).
func ForwardingRawReverseProxyWithEvents(nodes NodeClientLookup, log *slog.Logger, events *evts.Platform, sink *egresssink.EgressSink) func(t Target) http.Handler {
	return ForwardingRawReverseProxyWithEventsAndDrain(nodes, log, events, sink, nil)
}

// ForwardingRawReverseProxyWithEventsAndDrain (issue #587 / PR-A)
// is the drain-aware variant. The drain tracker, when non-nil, is
// held for the duration of the raw-stream pump so the gateway's
// graceful-shutdown drain waits for hijacked Upgrade pumps (which
// run outside any ServeHTTP envelope) instead of force-closing the
// conn on TimeoutStopSec=30s.
//
// The tracker is held by the closure returned for each Target via
// `tracker.Begin("upgrade")()` at the top of the inner handler.
// Storing the Done closure on conn-scoped state isn't possible
// here because the hijacker lives inside rawStreamOnceWithEvents;
// the simplest correct invariant is "the closure fires when this
// http.HandlerFunc returns, regardless of whether ServeHTTP has
// returned to net/http". The closure is the canonical
// defer-done pattern: it captures the per-request drain slot and
// releases it when the inner function exits.
//
// nil tracker = pre-PR-A behaviour, preserved for unit tests
// and the e2e harness.
func ForwardingRawReverseProxyWithEventsAndDrain(nodes NodeClientLookup, log *slog.Logger, events *evts.Platform, sink *egresssink.EgressSink, tracker *drain.Tracker) func(t Target) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(t Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Drain tracker: holds the per-raw-pump drain slot
			// for the FULL lifetime of the hijacked Upgrade pump
			// (which outlives ServeHTTP's envelope — the bidi
			// gRPC stream keeps the conn open past this
			// function return).
			//
			// Correct shape is `defer tracker.Begin("upgrade")()`:
			//   - Begin("upgrade") runs IMMEDIATELY at the defer
			//     statement (increments the WaitGroup).
			//   - The Done closure is what gets deferred.
			//
			// The WRONG shape `defer func(){ tracker.Begin(...)() }()`
			// evaluates the entire closure body at function return
			// — Begin+Done both fire then, so the tracker never
			// sees a slot held during the pump.
			//
			// Begin is NOT nil-safe (it's a method on *Tracker), so
			// guard explicitly when the tracker is absent (e2e
			// harness, unit tests). When tracker == nil, returning
			// the no-op closure let the same `defer ...()` shape
			// be used at the call site without an if-defender
			// outside the defer statement itself.
			done := func() {}
			if tracker != nil {
				done = tracker.Begin("upgrade")
			}
			defer done()
			ctx := contextWithProxyStart(r.Context(), time.Now())
			cli, closer, ok := nodes.ClientFor(r.Context(), t.NodeID)
			if !ok {
				http.Error(w, "node unavailable", http.StatusServiceUnavailable)
				return
			}
			defer func() { _ = closer.Close() }()
			rawStreamOnceWithEvents(w, r.WithContext(ctx), cli, log, t, events, sink)
		})
	}
}

// ctxReader is a context-aware io.Reader wrapper used by the
// stream body-copy goroutines. Request bodies normally implement
// io.Closer, so newCtxReader watches the derived stream context and
// closes them to unblock a real network read. The pre-read check handles
// already-cancelled contexts without touching the body and avoids a
// helper goroutine allocation for every body chunk.
//
// Issue #471 review F3: the body goroutine still exits when an inbound
// HTTP request is cancelled because the server closes its Request.Body.
// The previous implementation created an additional goroutine and
// channel for every Read call, which amplified allocations and goroutine
// pressure for large uploads.
type ctxReader struct {
	r        io.Reader
	ctx      context.Context
	closer   io.Closer
	done     chan struct{}
	stopOnce sync.Once
}

// newCtxReader attaches cancellation to a closable reader. The returned stop
// function must be called when the owning goroutine exits so the watcher does
// not outlive a completed request body.
func newCtxReader(ctx context.Context, r io.Reader) (*ctxReader, func()) {
	cr := &ctxReader{r: r, ctx: ctx}
	closer, ok := r.(io.Closer)
	if !ok {
		return cr, func() {}
	}
	cr.closer = closer
	cr.done = make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-cr.done:
		}
	}()
	return cr, cr.stop
}

func (cr *ctxReader) stop() {
	if cr.done != nil {
		cr.stopOnce.Do(func() { close(cr.done) })
	}
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	if cr == nil || cr.r == nil {
		return 0, io.EOF
	}
	if cr.ctx != nil {
		select {
		case <-cr.ctx.Done():
			return 0, cr.ctx.Err()
		default:
		}
	}
	n, err := cr.r.Read(p)
	if cr.ctx != nil {
		if ctxErr := cr.ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
	}
	return n, err
}

// NodeClientCache is the production implementation of NodeClientLookup.
// It caches one *grpc.ClientConn per compute_node.id, dialed lazily on
// first use. Eviction: a per-cache subscribe call drops entries when
// the underlying compute_nodes row changes (admin update) or is
// deactivated (heartbeat watchdog). Closer-on-each-call lets the cache
// track outstanding leases — used in tests to assert the cache is
// touched exactly once per request.
type NodeClientCache struct {
	mu      sync.Mutex
	clients map[string]*grpc.ClientConn // nodeID -> conn
	// refcount lets us close the conn once the last lease is released,
	// avoiding an idle conn lingering for a node that just got drained.
	refs map[string]int
	// dialing coalesces the first-use resolver + gRPC dial for a node.
	// Without this map, a burst that lands on a new node makes every
	// request perform the same Postgres target lookup and start a
	// duplicate connection before the cache re-check wins.
	dialing map[string]*nodeDialCall

	dial func(ctx context.Context, target string) (*grpc.ClientConn, error)
	log  *slog.Logger
}

type nodeDialCall struct {
	done        chan struct{}
	invalidated bool
}

// NewNodeClientCache wires a cache with the given dialer (production:
// pkg/wire.DialContext; tests: a fake that returns an in-process
// bufconn). log may be nil.
func NewNodeClientCache(dial func(ctx context.Context, target string) (*grpc.ClientConn, error), log *slog.Logger) *NodeClientCache {
	if log == nil {
		log = slog.Default()
	}
	return &NodeClientCache{
		clients: map[string]*grpc.ClientConn{},
		refs:    map[string]int{},
		dialing: map[string]*nodeDialCall{},
		dial:    dial,
		log:     log,
	}
}

// ClientFor resolves nodeID to a VmmdClient on a cached
// *grpc.ClientConn. Returns ok=false on a dial failure; the caller
// surfaces 503. Each successful call increments the refcount so
// Evict() can wait for in-flight requests before closing.
//
// On a cache miss, one caller looks the node's dial target up via the
// resolver (production: pkg/state.ComputeNodeByID; tests: a fixed
// map) and establishes the connection. Concurrent callers wait for
// that same dial and then lease the resulting connection.
func (c *NodeClientCache) ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if c == nil || nodeID == "" {
		return nil, nil, false
	}
	c.mu.Lock()
	if c.clients == nil {
		c.clients = map[string]*grpc.ClientConn{}
	}
	if c.refs == nil {
		c.refs = map[string]int{}
	}
	conn, ok := c.clients[nodeID]
	if ok {
		c.refs[nodeID]++
		c.mu.Unlock()
		return vmmdpb.NewVmmdClient(conn), leaseCloser{c: c, nodeID: nodeID}, true
	}
	if c.dialing == nil {
		c.dialing = map[string]*nodeDialCall{}
	}
	if call, exists := c.dialing[nodeID]; exists {
		c.mu.Unlock()
		select {
		case <-call.done:
			c.mu.Lock()
			conn, ok := c.clients[nodeID]
			if ok {
				c.refs[nodeID]++
			}
			c.mu.Unlock()
			if !ok {
				return nil, nil, false
			}
			return vmmdpb.NewVmmdClient(conn), leaseCloser{c: c, nodeID: nodeID}, true
		case <-ctx.Done():
			return nil, nil, false
		}
	}
	call := &nodeDialCall{done: make(chan struct{})}
	c.dialing[nodeID] = call
	c.mu.Unlock()

	// Resolve and dial outside the cache lock: resolver/database work
	// must not block hits for other nodes.
	target, ok := c.resolveTarget(ctx, nodeID)
	if !ok {
		c.finishNodeDial(nodeID, call, nil)
		return nil, nil, false
	}
	if c.dial == nil {
		c.finishNodeDial(nodeID, call, nil)
		return nil, nil, false
	}
	conn, err := c.dial(ctx, target)
	if err != nil {
		if c.log != nil {
			c.log.Warn("gateway: vmmd dial failed",
				"node", nodeID, "target", target, "err", err.Error())
		}
		c.finishNodeDial(nodeID, call, nil)
		return nil, nil, false
	}
	if !c.finishNodeDial(nodeID, call, conn) {
		// The node was evicted or the cache was closed while the dial
		// was in flight. The dial result was not published and is now
		// owned by this caller.
		if conn != nil {
			_ = conn.Close()
		}
		return nil, nil, false
	}
	return vmmdpb.NewVmmdClient(conn), leaseCloser{c: c, nodeID: nodeID}, true
}

// finishNodeDial publishes a successful connection and wakes all waiters.
// It returns false when an eviction/close invalidated the in-flight dial.
func (c *NodeClientCache) finishNodeDial(nodeID string, call *nodeDialCall, conn *grpc.ClientConn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dialing != nil {
		delete(c.dialing, nodeID)
	}
	if call.invalidated || conn == nil {
		close(call.done)
		return false
	}
	if c.clients == nil {
		c.clients = map[string]*grpc.ClientConn{}
	}
	if c.refs == nil {
		c.refs = map[string]int{}
	}
	c.clients[nodeID] = conn
	// The leader took its lease before starting the external work.
	// Keep that reference while publishing the connection.
	c.refs[nodeID] = 1
	close(call.done)
	return true
}

// resolveTarget is the seam that turns a compute_node.id into the
// dial target string ("tcp://<overlay-ip>:50051" for a remote box,
// "unix:///run/faas/vmmd.sock" for default-local). Production wires
// this to pkg/state.ComputeNodeByID + ParseTarget; tests pass a fixed
// map. Returning the empty string means "unknown node" and yields
// ok=false from ClientFor.
var resolveNodeTarget = func(ctx context.Context, nodeID string) (string, bool) {
	// Production resolver installed by cmd/gatewayd-internal/at startup.
	return "", false
}

// SetNodeResolver replaces the package-level resolver. Production
// calls this once during wiring; tests inject a fake. Splitting the
// resolver out (rather than passing it through NodeClientCache's
// constructor) lets the same cache type serve cmd/gatewayd-internal's wiring
// without dragging state.Store into the gateway package — pkg/gateway
// stays pkg/api-only by design (CLAUDE.md ownership).
func SetNodeResolver(fn func(ctx context.Context, nodeID string) (string, bool)) {
	resolveNodeTarget = fn
}

func (c *NodeClientCache) resolveTarget(ctx context.Context, nodeID string) (string, bool) {
	return resolveNodeTarget(ctx, nodeID)
}

// Evict drops the cached conn for nodeID. Safe to call from a
// LISTEN/NOTIFY goroutine; concurrent in-flight ClientFor calls
// finish on their existing refcount before the conn is closed.
func (c *NodeClientCache) Evict(nodeID string) {
	c.mu.Lock()
	if c.dialing != nil {
		if call, ok := c.dialing[nodeID]; ok {
			call.invalidated = true
		}
	}
	conn, ok := c.clients[nodeID]
	if !ok {
		c.mu.Unlock()
		return
	}
	refs := c.refs[nodeID]
	delete(c.clients, nodeID)
	delete(c.refs, nodeID)
	c.mu.Unlock()
	if refs == 0 {
		_ = conn.Close()
	}
}

// Close shuts down every cached conn. Called by cmd/gatewayd-internal/on
// SIGHUP / SIGTERM. In-flight requests see a closed conn (gRPC
// surfaces Unavailable); the listener stops accepting new ones.
func (c *NodeClientCache) Close() error {
	c.mu.Lock()
	for _, call := range c.dialing {
		call.invalidated = true
	}
	conns := c.clients
	c.clients = map[string]*grpc.ClientConn{}
	c.refs = map[string]int{}
	c.mu.Unlock()
	var errs []error
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// leaseCloser decrements the per-node refcount and closes the conn
// when the last lease is released. The connection itself lives in
// the cache so a subsequent request skips the dial.
type leaseCloser struct {
	c      *NodeClientCache
	nodeID string
}

func (l leaseCloser) Close() error {
	l.c.mu.Lock()
	l.c.refs[l.nodeID]--
	l.c.mu.Unlock()
	// Refcount-driven close happens on Evict; the conn stays cached
	// for the next caller as long as the row stays alive.
	return nil
}
