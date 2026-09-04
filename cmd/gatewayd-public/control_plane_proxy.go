package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
)

// controlPlaneProxy keeps the API surface on the control-plane host. App
// routes continue to the compute gateway, but a compute outage can never turn
// /v1, dashboard, auth, or health traffic into a gateway 502.
type controlPlaneProxy struct {
	target *url.URL
	next   http.Handler
	proxy  *httputil.ReverseProxy
	log    *slog.Logger
}

func newControlPlaneProxy(rawTarget string, next http.Handler, log *slog.Logger) (http.Handler, error) {
	target, err := url.Parse(rawTarget)
	if err != nil || target.Scheme == "" || target.Host == "" || target.Path != "" {
		return nil, fmt.Errorf("control-plane API target must be an absolute URL, got %q", rawTarget)
	}
	if log == nil {
		log = slog.Default()
	}

	p := &controlPlaneProxy{target: target, next: next, log: log}
	p.proxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.Host = req.In.Host
			req.Out.Header.Del("X-Forwarded-For")
			req.Out.Header.Del("X-Forwarded-Host")
			req.Out.Header.Del("X-Forwarded-Proto")
			if host, _, splitErr := net.SplitHostPort(req.In.RemoteAddr); splitErr == nil && host != "" {
				req.Out.Header.Set("X-Forwarded-For", host)
			}
			if req.In.TLS != nil {
				req.Out.Header.Set("X-Forwarded-Proto", "https")
			} else {
				req.Out.Header.Set("X-Forwarded-Proto", "http")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("control-plane API upstream unavailable", "path", r.URL.Path, "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "control_plane_unavailable", "Control plane unavailable", "the control-plane API is not reachable"))
		},
	}
	return p, nil
}

func (p *controlPlaneProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Prometheus consumes this registry-backed service-discovery endpoint only
	// over apid's loopback listener. Do not let the public control-plane proxy
	// turn it into an externally reachable API route.
	if r.URL.Path == "/v1/internal/metrics/targets" {
		http.NotFound(w, r)
		return
	}
	if apid.IsApidPath(r.URL.Path) && !isComputeOwnedGatewayPath(r.URL.Path) {
		p.proxy.ServeHTTP(w, r)
		return
	}
	p.next.ServeHTTP(w, r)
}

// isComputeOwnedGatewayPath lists the internal gateway endpoints that need
// the compute data plane even though they live under /v1. The scheduler now
// points at the local public gateway, so these paths must bypass the local
// control-plane API proxy and enter the same dynamic compute pool as app
// traffic. The synth handler enforces its own internal-service auth gate on
// the compute side.
func isComputeOwnedGatewayPath(path string) bool {
	return isComputeOwnedLogsPath(path) ||
		path == "/v1/synthesize" ||
		path == "/v1/invocations:dispatch" ||
		path == "/v1/invocations:dispatch_batch"
}

func isComputeOwnedLogsPath(path string) bool {
	const prefix = "/v1/apps/"
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	rest := path[len(prefix):]
	separator := -1
	for i := range rest {
		if rest[i] == '/' {
			separator = i
			break
		}
	}
	if separator <= 0 {
		return false
	}
	tail := rest[separator:]
	return tail == "/logs" || len(tail) > len("/logs/") && tail[:len("/logs/")] == "/logs/"
}
