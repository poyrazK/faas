package main

// adr: 20261103163954 — a compute edge's per-client-address limits (§4).
//
// With no proxy in front, what Cloudflare absorbed reaches the node, so the
// edge bounds what one client address may take: open connections, new
// connections a second, TLS handshakes in progress and requests a second.
// It also holds a request body to a minimum rate and caps the requests the
// edge forwards to the control plane. The values are in pkg/api/limits.go.
// Off unless FAAS_EDGE_CLIENT_LIMITS=1.

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/clientlimit"
	"github.com/onebox-faas/faas/pkg/gateway"
)

const (
	edgeClientLimitsEnv = "FAAS_EDGE_CLIENT_LIMITS"
	// edgeProxyCIDRsEnv lists Cloudflare's ranges on an edge with no front:
	// a host moved back behind Cloudflare, the flood fallback, arrives from a
	// few of its addresses, each carrying many clients, so the per-address
	// connection limits skip them. Their clients are limited per request by
	// CF-Connecting-IP, which the edge trusts only with the zone's client
	// certificate.
	edgeProxyCIDRsEnv = "FAAS_EDGE_PROXY_CIDRS"
)

// edgeClientLimits switches the per-address limits on. The request rate
// applies on any front, keyed on the client address the gateway trusts
// (CF-Connecting-IP behind Cloudflare). The connection and handshake limits
// apply only to a TLS listener with no front: behind Cloudflare a
// connection's peer is one of Cloudflare's addresses, not a client. refused
// counts every refusal by reason; it must be set before the limits serve.
type edgeClientLimits struct {
	on      bool
	proxies []netip.Prefix
	refused *prometheus.CounterVec
}

func edgeClientLimitsFromEnv(mode publicEdgeMode, getenv func(string) string) (edgeClientLimits, error) {
	switch v := strings.TrimSpace(getenv(edgeClientLimitsEnv)); v {
	case "", "0":
		return edgeClientLimits{}, nil
	case "1":
		if mode != edgeModeCompute {
			return edgeClientLimits{}, fmt.Errorf("gatewayd-public: %s is for a compute edge (%s=%s)", edgeClientLimitsEnv, publicEdgeModeEnv, edgeModeCompute)
		}
		proxies, err := parseEdgeProxyCIDRs(getenv(edgeProxyCIDRsEnv))
		if err != nil {
			return edgeClientLimits{}, err
		}
		return edgeClientLimits{on: true, proxies: proxies}, nil
	default:
		return edgeClientLimits{}, fmt.Errorf("gatewayd-public: %s=%q, want 0 or 1", edgeClientLimitsEnv, v)
	}
}

// parseEdgeProxyCIDRs reads a comma-separated prefix list, empty for none.
func parseEdgeProxyCIDRs(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.Split(v, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		p, err := netip.ParsePrefix(field)
		if err != nil || p != p.Masked() {
			return nil, fmt.Errorf("gatewayd-public: %s entry %q is not a network prefix", edgeProxyCIDRsEnv, field)
		}
		out = append(out, p)
	}
	return out, nil
}

func (l edgeClientLimits) count(reason string) { l.refused.WithLabelValues(reason).Inc() }

// requests limits each client address's request rate in front of h.
func (l edgeClientLimits) requests(h http.Handler, trusted []netip.Prefix) http.Handler {
	if !l.on {
		return h
	}
	limits := clientlimit.RequestLimits{
		PerSecond:       api.EdgeClientRequestsPerSecondPerAddr,
		Burst:           api.EdgeClientRequestsBurstPerAddr,
		TrackedAddrsMax: api.EdgeClientTrackedAddrsMax,
	}
	clientAddr := func(r *http.Request) (netip.Addr, bool) {
		ip, _ := gateway.CanonicalForwardingContext(r, trusted)
		addr, err := netip.ParseAddr(ip)
		return addr, err == nil
	}
	return clientlimit.Requests(h, limits, clientAddr, l.count, nil)
}

// body ends a request whose body arrives slower than the minimum rate. gRPC
// client streams may idle, so they are exempt.
func (l edgeClientLimits) body(h http.Handler) http.Handler {
	if !l.on {
		return h
	}
	return clientlimit.BodyRate(h, api.EdgeClientBodyMinBytesPerWindow, api.EdgeClientBodyRateWindow, isGRPC, l.count)
}

// forwards caps the requests this edge forwards to the control plane at once.
func (l edgeClientLimits) forwards(h http.Handler) http.Handler {
	if !l.on || h == nil {
		return h
	}
	return clientlimit.InFlight(h, api.EdgeControlPlaneForwardsInFlightMax, l.count)
}

// isGRPC reports whether r is a gRPC call, whose client stream may idle.
func isGRPC(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")
}

// tlsListener serves TLS on raw. With the limits on and no front, raw's
// connections are limited per address before TLS, and handshakes complete
// through a gate sized to the CPU before the server sees them, ending with
// ctx; failed counts each failed handshake there.
func (l edgeClientLimits) tlsListener(ctx context.Context, raw net.Listener, cfg *tls.Config, direct bool, failed func(net.Addr, error)) net.Listener {
	if !l.on || !direct {
		return tls.NewListener(raw, cfg)
	}
	limited := clientlimit.NewListener(raw, clientlimit.ConnLimits{
		MaxOpenPerAddr:  api.EdgeClientOpenConnsPerAddrMax,
		NewPerSecond:    api.EdgeClientNewConnsPerSecondPerAddr,
		NewBurst:        api.EdgeClientNewConnsBurstPerAddr,
		TrackedAddrsMax: api.EdgeClientTrackedAddrsMax,
		Exempt:          l.proxies,
	}, l.count, nil)
	return clientlimit.NewHandshakeGate(ctx, tls.NewListener(limited, cfg),
		api.EdgeTLSHandshakesInFlightPerCPU*runtime.GOMAXPROCS(0), api.EdgeTLSHandshakeTimeout, l.count, failed)
}
