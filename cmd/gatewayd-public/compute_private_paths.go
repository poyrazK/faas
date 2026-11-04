package main

import (
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// computePrivatePath reports whether p belongs to gatewayd-internal's private
// surface: realtimed's unauthenticated management API, which apid reaches on
// a compute node directly (/v1/internal/realtime/...), and the compute
// metrics scrape (/v1/internal/metrics). gatewayd-internal serves both on the
// same mux as app traffic, ahead of every app's own routes, so the edge must
// never forward them, on any host: an app subdomain request for
// /v1/internal/realtime/internal/connections reached realtimed and listed
// every tenant's connections. An app cannot serve these paths itself anyway.
func computePrivatePath(p string) bool {
	clean := path.Clean("/" + p)
	return clean == "/v1/internal/realtime" ||
		strings.HasPrefix(clean, "/v1/internal/realtime/") ||
		clean == "/v1/internal/metrics"
}

// schedulerPath reports whether p is one of gatewayd-internal's synth routes,
// which wake and invoke any app by ID. gatewayd-internal serves them on the
// same mux as app traffic, on every Host (ADR-20261103090000).
func schedulerPath(p string) bool {
	switch path.Clean("/" + p) {
	case "/v1/synthesize", "/v1/invocations:dispatch", "/v1/invocations:dispatch_batch":
		return true
	}
	return false
}

// schedulerTransportRequest reports whether r came straight from the local
// scheduler, which posts synth requests to this listener over loopback
// (gateway_synth_target tcp://127.0.0.1:8080) with an IP Host. Caddy and
// Cloudflare reach the same listener over loopback too, but always add
// X-Forwarded-For/-Proto/-Host, and the compute edge's direct listener is
// TLS. A Host header alone proves nothing: Caddy's catch-all customer-domain
// site forwards whatever Host the client sends.
func schedulerTransportRequest(r *http.Request) bool {
	if r.TLS != nil {
		return false
	}
	for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "Forwarded", "Via", "CF-Connecting-IP"} {
		if len(r.Header.Values(name)) != 0 {
			return false
		}
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().Unmap().IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || net.ParseIP(host) != nil
}

// refuseComputePrivatePaths answers 404 for computePrivatePath requests, and
// for synth requests that did not come from the local scheduler, before they
// reach the control-plane proxy or the compute edge.
func refuseComputePrivatePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if computePrivatePath(r.URL.Path) || schedulerPath(r.URL.Path) && !schedulerTransportRequest(r) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Not found", "the requested API route does not exist"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
