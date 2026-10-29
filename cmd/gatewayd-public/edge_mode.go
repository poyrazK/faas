package main

// Compute-node app edge (ADR-1343).
//
// With FAAS_PUBLIC_EDGE_MODE=compute, gatewayd-public runs on a compute node
// behind a local Cloudflare Tunnel connector and serves app hosts only:
//
//	cloudflared (dials out) → 127.0.0.1:8090 here → this node's
//	gatewayd-internal socket (the compute pool only while it is unhealthy)
//
// Platform hosts and custom domains stay on the control plane. Requests that
// need control-plane credentials or sockets — public object-storage reads,
// uploads and the customer OTLP endpoint — are forwarded to the control
// plane's gatewayd-public, so a compute node holds no storage credentials and
// no apid sockets. The mode is off by default.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/role"
)

const (
	// publicEdgeModeEnv selects where this gatewayd-public serves:
	// unset on the control plane, "compute" on a compute-node edge.
	publicEdgeModeEnv = "FAAS_PUBLIC_EDGE_MODE"
	// publicEdgeControlPlaneTargetEnv is the control plane's private
	// gatewayd-public listener that a compute edge forwards
	// credential-bound requests to, e.g. http://10.0.0.1:8090.
	publicEdgeControlPlaneTargetEnv = "FAAS_PUBLIC_EDGE_CONTROL_PLANE_TARGET"
	// publicPrivateListenEnv adds a second listener for the same public
	// handler, on the control plane's private address, so compute edges can
	// reach it. Unset means no private listener.
	publicPrivateListenEnv = "FAAS_PUBLIC_PRIVATE_LISTEN_ADDR"
	// otelTracesPath is the customer OTLP endpoint, which needs apid's
	// Auth and spans-writer sockets.
	otelTracesPath = "/v1/otel/v1/traces"
	// localGatewayCheckEvery is how often a compute edge re-checks its own
	// node's gatewayd-internal.
	localGatewayCheckEvery = 2 * time.Second
)

type publicEdgeMode string

const (
	edgeModeControlPlane publicEdgeMode = ""
	edgeModeCompute      publicEdgeMode = "compute"
)

// parsePublicEdgeMode rejects unknown values so a typo cannot silently start
// a control-plane edge on a compute node.
func parsePublicEdgeMode(raw string) (publicEdgeMode, error) {
	switch mode := publicEdgeMode(strings.TrimSpace(raw)); mode {
	case edgeModeControlPlane, edgeModeCompute:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want unset or %q", publicEdgeModeEnv, raw, edgeModeCompute)
	}
}

// requireRole exposes the normal gateway's literal guard to the unit-template
// source cross-check. The optional compute edge has its own service identity.
func (m publicEdgeMode) requireRole(observed role.Role) error {
	if m == edgeModeCompute {
		return role.Require("gatewayd-public-edge", observed,
			role.RoleSingleBox, role.RoleControlPlane, role.RoleComputeOnly)
	}
	return role.Require("gatewayd-public", observed,
		role.RoleSingleBox, role.RoleControlPlane)
}

// edgeMetrics are the compute-edge series, served on the control listener.
type edgeMetrics struct {
	registry    *prometheus.Registry
	fallbacks   prometheus.Counter
	misdirected prometheus.Counter
	localReady  prometheus.Gauge
	nodeDrained prometheus.Gauge
}

func newEdgeMetrics() *edgeMetrics {
	m := &edgeMetrics{
		registry: prometheus.NewRegistry(),
		fallbacks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gatewayd_public_edge_local_fallback_total",
			Help: "Upstream dials a compute-node edge sent to the compute pool because its own node's gatewayd-internal was unhealthy or refused the dial (ADR-1343).",
		}),
		misdirected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gatewayd_public_edge_misdirected_total",
			Help: "Requests a compute-node edge answered with 421 because the host is not an app host (ADR-1343).",
		}),
		localReady: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gatewayd_public_edge_local_gateway_ready",
			Help: "1 while a compute-node edge routes to its own node's gatewayd-internal, 0 while it uses the compute pool (ADR-1343).",
		}),
		nodeDrained: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gatewayd_public_edge_node_drained",
			Help: "1 while this compute node is drained in the registry and its edge sends every request to the other nodes (ADR-1343 §6).",
		}),
	}
	m.registry.MustRegister(m.fallbacks, m.misdirected, m.localReady, m.nodeDrained)
	return m
}

// localFirstDialer sends upstream connections to this node's gatewayd-internal
// and uses the compute pool only while the local gateway fails its health
// check or refuses a dial. Every node's gatewayd-internal can reach every
// node's vmmd, so the local hop loses nothing (ADR-1343 §2).
//
// It also withdraws the local path while this node is drained in the
// registry (ADR-1343 §6): the tunnel still delivers requests to this edge,
// but none of them adds load to the node being drained. That is a planned
// route, not a fallback, so it does not count as one.
type localFirstDialer struct {
	local    gateway.InternalDialer
	fallback gateway.InternalDialer
	target   *url.URL
	healthy  atomic.Bool
	drained  atomic.Bool
	metrics  *edgeMetrics
	log      *slog.Logger

	// nodeName and inventory enable the drain withdrawal; either empty
	// leaves routing to the health check alone.
	nodeName  string
	inventory nodeInventory
}

// nodeInventory reports a compute node's registry state: whether it is
// active, how many nodes are, and whether the registry could be read.
type nodeInventory interface {
	nodeStatus(ctx context.Context, name string) (active bool, activeNodes int, known bool)
}

func newLocalFirstDialer(local, fallback gateway.InternalDialer, target *url.URL, metrics *edgeMetrics, log *slog.Logger) *localFirstDialer {
	return &localFirstDialer{local: local, fallback: fallback, target: target, metrics: metrics, log: log}
}

// withDrainWatch makes the dialer withdraw the local path while nodeName is
// drained in inventory.
func (d *localFirstDialer) withDrainWatch(nodeName string, inventory nodeInventory) *localFirstDialer {
	d.nodeName = nodeName
	d.inventory = inventory
	return d
}

func (d *localFirstDialer) DialContext(ctx context.Context, target string) (net.Conn, error) {
	if d.healthy.Load() {
		conn, err := d.local.DialContext(ctx, target)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		d.log.Warn("gatewayd-public: local gatewayd-internal refused the dial; using the compute pool", "err", err)
	} else if d.drained.Load() {
		conn, err := d.fallback.DialContext(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("compute edge: node drained and compute pool failed: %w", err)
		}
		return conn, nil
	}
	d.metrics.fallbacks.Inc()
	conn, err := d.fallback.DialContext(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("compute edge: local gateway unavailable and compute pool failed: %w", err)
	}
	return conn, nil
}

// checkLocal runs one health check of the local gateway and records it.
func (d *localFirstDialer) checkLocal(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, localGatewayCheckEvery)
	err := checkInternalGateway(checkCtx, d.local, d.target)
	cancel()
	drained := d.checkDrained(ctx)
	if wasDrained := d.drained.Swap(drained); wasDrained != drained {
		if drained {
			d.log.Info("gatewayd-public: this node is drained; sending every request to the other compute nodes", "node", d.nodeName)
		} else {
			d.log.Info("gatewayd-public: this node is active again; routing locally when healthy", "node", d.nodeName)
		}
	}
	if drained {
		d.metrics.nodeDrained.Set(1)
	} else {
		d.metrics.nodeDrained.Set(0)
	}
	ready := err == nil && !drained
	if was := d.healthy.Swap(ready); was != ready && !drained {
		if ready {
			d.log.Info("gatewayd-public: local gatewayd-internal healthy; routing locally")
		} else {
			d.log.Warn("gatewayd-public: local gatewayd-internal unhealthy; routing to the compute pool", "err", err)
		}
	}
	if ready {
		d.metrics.localReady.Set(1)
	} else {
		d.metrics.localReady.Set(0)
	}
}

// checkDrained reports whether this node is drained: known to the registry
// as inactive while other nodes are active. A registry that cannot be read,
// or one with no other active node, never withdraws the local path, so a
// drain can only move traffic somewhere that can take it.
func (d *localFirstDialer) checkDrained(ctx context.Context) bool {
	if d.nodeName == "" || d.inventory == nil {
		return false
	}
	statusCtx, cancel := context.WithTimeout(ctx, localGatewayCheckEvery)
	defer cancel()
	active, activeNodes, known := d.inventory.nodeStatus(statusCtx, d.nodeName)
	return known && !active && activeNodes > 0
}

// run checks the local gateway until ctx ends. The first check runs at once,
// so an edge that starts before its gateway uses the pool until it is up.
func (d *localFirstDialer) run(ctx context.Context) {
	d.checkLocal(ctx)
	tick := time.NewTicker(localGatewayCheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.checkLocal(ctx)
		}
	}
}

// isAppHost reports whether host is an app host: one or more labels in front
// of an apps domain (or an alias, or the deployment-preview domain), and not
// a platform host (apex, api., operations., an IP literal) or the storage
// host s3 of the platform domain.
func isAppHost(host string, domains hostidentity.Domains) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	if domains.Apps == "" || apid.IsPlatformHost(h, domains.Platform) || h == "s3."+domains.Platform {
		return false
	}
	for _, domain := range append(domains.AppDomains(), domains.Deploy) {
		if label, ok := strings.CutSuffix(h, "."+domain); ok && label != "" {
			return true
		}
	}
	return false
}

// appHostsOnly answers every non-app host with 421. The DNS split keeps them
// away from a compute edge; this keeps the edge from ever becoming a path to
// the control plane's API if a record is wrong.
func appHostsOnly(next http.Handler, domains hostidentity.Domains, misdirected prometheus.Counter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAppHost(r.Host, domains) {
			next.ServeHTTP(w, r)
			return
		}
		misdirected.Inc()
		api.WriteProblem(w, api.NewProblem(http.StatusMisdirectedRequest, "misdirected_request",
			"Misdirected request", "this edge serves app hosts only"))
	})
}

// cloudflareClientIP makes CF-Connecting-IP the only forwarded client address
// when the direct peer is the local tunnel connector. Cloudflare appends to a
// client-supplied X-Forwarded-For, and CanonicalForwardingContext ignores a
// comma list, so without this every such client would appear as 127.0.0.1.
func cloudflareClientIP(next http.Handler, trusted []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" && gateway.TrustedIngressPeer(r, trusted) {
			if addr, err := netip.ParseAddr(v); err == nil {
				r.Header.Set("X-Forwarded-For", addr.Unmap().String())
			}
		}
		next.ServeHTTP(w, r)
	})
}

// newControlPlaneForwarder proxies credential-bound requests to the control
// plane's private gatewayd-public listener. It keeps the app Host, the
// request ID and one canonical client address, which the control plane
// trusts from compute-node peers. An empty target fails closed with 503.
func newControlPlaneForwarder(target string, trusted []netip.Prefix, log *slog.Logger) (http.Handler, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "control_plane_unavailable",
				"Control plane unavailable", "this edge has no control-plane target for this request"))
		}), nil
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%s must be an http(s) URL with no path, got %q", publicEdgeControlPlaneTargetEnv, target)
	}
	u.Path = ""
	return &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(u)
			req.Out.Host = req.In.Host
			req.Out.Header.Set(api.RequestIDHeader, req.In.Header.Get(api.RequestIDHeader))
			req.Out.Header.Del("X-Forwarded-For")
			req.Out.Header.Del("X-Forwarded-Host")
			req.Out.Header.Del("X-Forwarded-Proto")
			clientIP, proto := gateway.CanonicalForwardingContext(req.In, trusted)
			if clientIP != "" {
				req.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Out.Header.Set("X-Forwarded-Proto", proto)
		},
		ModifyResponse: func(resp *http.Response) error {
			// The control plane's edge already set these; this edge's outer
			// wrapper sets them again.
			httpsec.StripStaticHeaders(resp.Header)
			resp.Header.Del(api.RequestIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("gatewayd-public: control-plane edge unavailable", "path", r.URL.Path, "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "control_plane_unavailable",
				"Control plane unavailable", "the control-plane edge is not reachable"))
		},
	}, nil
}

// computeEdgeRoot puts the credential-bound object-storage routes in front of
// next: upload routes and their receipts, then public bucket reads, are
// forwarded; everything else goes to next.
func computeEdgeRoot(next, forward http.Handler, store objectstorage.PublicReadStore, domains hostidentity.Domains, log *slog.Logger) (http.Handler, error) {
	publicRead, err := objectstorage.NewPublicReadForwarder(objectstorage.PublicReadForwardConfig{
		Store: store, AppsDomain: domains.Apps, Forward: forward, Next: next, Log: log,
	})
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if objectstorage.IsUploadPath(r.URL.Path) {
			forward.ServeHTTP(w, r)
			return
		}
		publicRead.ServeHTTP(w, r)
	}), nil
}

// requireComputeEdgeConfig checks what a compute edge cannot run without.
func requireComputeEdgeConfig(getenv func(string) string) error {
	if strings.TrimSpace(getenv("FAAS_APPS_DOMAIN")) == "" {
		return errors.New("gatewayd-public: compute edge mode needs FAAS_APPS_DOMAIN, the only hosts it serves")
	}
	return nil
}
