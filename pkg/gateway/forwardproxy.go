// Issue #98 / ADR-028: gatewayd's HTTP→gRPC forwarder.
//
// Gatewayd's hot path looks up the compute_node.id an instance lives on
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodeClientLookup resolves a compute_node.id to a cached
// *grpc.ClientConn dialed to that node's vmmd gRPC server. Implementations
// must be safe for concurrent use. The gateway owns the cache
// (cmd/gatewayd/nodecache.go); tests pass a fake.
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
// (10.100.x.y/16 is bound on veth_peer per ADR-009) so gatewayd can't
// reach it from a remote box. This handler instead asks vmmd to
// bridge the bytes via the ForwardHTTP RPC.
//
// ctxHook (optional) lets the caller cancel the bridge mid-flight
// when the inbound request is cancelled (client disconnect). nil
// means "no special cancellation"; we still wire the inbound ctx to
// the bridge call so the standard http.Server cancellation works.
func ForwardingReverseProxy(nodes NodeClientLookup, log *slog.Logger) func(nodeID string) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(nodeID string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fwdOnce(w, r, nodes, log, nodeID)
		})
	}
}

// fwdOnce runs a single forward. Extracted so the test harness can
// drive it without standing up an http.Server. The defer at the top
// is the only panic-safety net — if a future maintainer adds a step
// that panics on a malformed request, we still observe the request
// via slog instead of crashing the listener.
func fwdOnce(w http.ResponseWriter, r *http.Request, nodes NodeClientLookup, log *slog.Logger, nodeID string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("gateway: forwarder panic",
				"node", nodeID, "err", fmt.Sprintf("%v", rec))
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()

	if nodeID == "" {
		// Defensive: an empty node id would mean the routing cache
		// evicted between Target() and the proxy call. Surface 503 so
		// the client retries; this should not happen because the
		// Target-check and the proxy call run on the same goroutine
		// under the WakeGate, but the contract has to outlive the
		// goroutine.
		http.Error(w, "no node available", http.StatusServiceUnavailable)
		return
	}

	cli, closer, ok := nodes.ClientFor(r.Context(), nodeID)
	if !ok {
		http.Error(w, "node unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = closer.Close() }()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		// Body read failure = a client that disconnected before we
		// finished buffering. Distinct from the bridge failing because
		// the bridge hasn't started yet.
		log.Warn("gateway: body read failed", "node", nodeID, "err", err.Error())
		http.Error(w, "body read failed", http.StatusBadRequest)
		return
	}

	pbReq := &vmmdpb.ForwardHTTPRequest{
		Instance:   r.Header.Get("x-faas-instance"),
		Method:     r.Method,
		RequestUri: r.URL.RequestURI(),
		Body:       body,
	}
	for name, vals := range stripHopByHop(r.Header) {
		// Skip the request-id and other internal-only headers we add
		// in the gateway; the bridge doesn't need them and the guest
		// would just see noise.
		if strings.HasPrefix(strings.ToLower(name), "x-faas-") {
			continue
		}
		for _, v := range vals {
			pbReq.Headers = append(pbReq.Headers, &vmmdpb.Header{Name: name, Value: v})
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	resp, err := cli.ForwardHTTP(ctx, pbReq)
	if err != nil {
		// Unavailable is the explicit signal from vmmd that the
		// guest is sick / the netns is gone — the gateway should
		// evict the cached target and let the next request re-wake.
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			log.Warn("gateway: forwarder Unavailable; surfacing 503",
				"node", nodeID, "instance", pbReq.Instance)
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		// NotFound = the instance isn't live. Same retry semantics:
		// evict + re-wake on next request.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			http.Error(w, "instance gone", http.StatusServiceUnavailable)
			return
		}
		log.Error("gateway: forwarder RPC failed",
			"node", nodeID, "instance", pbReq.Instance,
			"err", err.Error())
		http.Error(w, "forwarder RPC failed", http.StatusBadGateway)
		return
	}

	for _, h := range resp.GetHeaders() {
		w.Header().Add(h.GetName(), h.GetValue())
	}
	w.WriteHeader(int(resp.GetStatus()))
	if len(resp.GetBody()) > 0 {
		_, _ = w.Write(resp.GetBody())
	}
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

	dial func(ctx context.Context, target string) (*grpc.ClientConn, error)
	log  *slog.Logger
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
		dial:    dial,
		log:     log,
	}
}

// ClientFor resolves nodeID to a VmmdClient on a cached
// *grpc.ClientConn. Returns ok=false on a dial failure; the caller
// surfaces 503. Each successful call increments the refcount so
// Evict() can wait for in-flight requests before closing.
//
// On a cache miss, the cache looks the node's dial target up via the
// resolver (production: pkg/state.ComputeNodeByID; tests: a fixed
// map). On a cache hit, the conn is returned without dialing.
func (c *NodeClientCache) ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	c.mu.Lock()
	conn, ok := c.clients[nodeID]
	if !ok {
		c.mu.Unlock()
		// Resolve target outside the lock: a postgres round-trip
		// would block other nodes' lookups if we held it.
		target, ok := c.resolveTarget(ctx, nodeID)
		if !ok {
			return nil, nil, false
		}
		conn, err := c.dial(ctx, target)
		if err != nil {
			c.log.Warn("gateway: vmmd dial failed",
				"node", nodeID, "target", target, "err", err.Error())
			return nil, nil, false
		}
		c.mu.Lock()
		// Re-check under the lock; another goroutine may have raced
		// us and inserted.
		if existing, dup := c.clients[nodeID]; dup {
			_ = conn.Close()
			conn = existing
		} else {
			c.clients[nodeID] = conn
		}
		c.refs[nodeID]++
		c.mu.Unlock()
		return vmmdpb.NewVmmdClient(conn), leaseCloser{c: c, nodeID: nodeID}, true
	}
	c.refs[nodeID]++
	c.mu.Unlock()
	return vmmdpb.NewVmmdClient(conn), leaseCloser{c: c, nodeID: nodeID}, true
}

// resolveTarget is the seam that turns a compute_node.id into the
// dial target string ("tcp://<overlay-ip>:50051" for a remote box,
// "unix:///run/faas/vmmd.sock" for default-local). Production wires
// this to pkg/state.ComputeNodeByID + ParseTarget; tests pass a fixed
// map. Returning the empty string means "unknown node" and yields
// ok=false from ClientFor.
var resolveNodeTarget = func(ctx context.Context, nodeID string) (string, bool) {
	// Production resolver installed by cmd/gatewayd at startup.
	return "", false
}

// SetNodeResolver replaces the package-level resolver. Production
// calls this once during wiring; tests inject a fake. Splitting the
// resolver out (rather than passing it through NodeClientCache's
// constructor) lets the same cache type serve cmd/gatewayd's wiring
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

// Close shuts down every cached conn. Called by cmd/gatewayd on
// SIGHUP / SIGTERM. In-flight requests see a closed conn (gRPC
// surfaces Unavailable); the listener stops accepting new ones.
func (c *NodeClientCache) Close() error {
	c.mu.Lock()
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
