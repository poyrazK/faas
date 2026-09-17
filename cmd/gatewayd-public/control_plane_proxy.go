package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// controlPlaneProxy keeps the API surface on the control-plane host. App
// routes continue to the compute gateway, but a compute outage can never turn
// /v1, dashboard, auth, or health traffic into a gateway 502.
type controlPlaneProxy struct {
	target              *url.URL
	next                http.Handler
	proxy               *httputil.ReverseProxy
	githubProxy         *httputil.ReverseProxy
	log                 *slog.Logger
	appsDomain          string
	trustedIngressCIDRs []netip.Prefix
}

func newControlPlaneProxy(rawTarget string, next http.Handler, log *slog.Logger, trustedIngressCIDRs ...netip.Prefix) (http.Handler, error) {
	target, err := url.Parse(rawTarget)
	if err != nil || target.Scheme == "" || target.Host == "" || target.Path != "" {
		return nil, fmt.Errorf("control-plane API target must be an absolute URL, got %q", rawTarget)
	}
	if log == nil {
		log = slog.Default()
	}

	appsDomain := strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN"))
	if appsDomain == "" {
		appsDomain = "gregale.dev"
	}
	p := &controlPlaneProxy{
		target: target, next: next, log: log, appsDomain: appsDomain,
		trustedIngressCIDRs: append([]netip.Prefix(nil), trustedIngressCIDRs...),
	}
	p.proxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.Host = req.In.Host
			// Keep the edge's correlation id as a singleton across the
			// public→apid hop. The outer boundary normally stamps it;
			// this explicit Set also keeps direct proxy use deterministic.
			req.Out.Header.Set(api.RequestIDHeader, req.In.Header.Get(api.RequestIDHeader))
			req.Out.Header.Del("X-Forwarded-For")
			req.Out.Header.Del("X-Forwarded-Host")
			req.Out.Header.Del("X-Forwarded-Proto")
			clientIP, proto := gateway.CanonicalForwardingContext(req.In, p.trustedIngressCIDRs)
			if clientIP != "" {
				req.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Out.Header.Set("X-Forwarded-Proto", proto)
		},
		ModifyResponse: func(resp *http.Response) error {
			// The outer gatewayd-public middleware owns these headers.
			httpsec.StripStaticHeaders(resp.Header)
			// httputil.ReverseProxy copies response headers with Add.
			// Drop apid's copy so the edge's already-stamped value is
			// the only customer-visible X-Faas-Request-ID.
			resp.Header.Del(api.RequestIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("control-plane API upstream unavailable", "path", r.URL.Path, "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "control_plane_unavailable", "Control plane unavailable", "the control-plane API is not reachable"))
		},
	}
	githubTargetText := strings.TrimSpace(os.Getenv("FAAS_GITHUBD_LOOPBACK"))
	if githubTargetText == "" {
		githubTargetText = "http://127.0.0.1:8083"
	}
	githubTarget, githubErr := url.Parse(githubTargetText)
	if githubErr != nil || githubTarget.Scheme == "" || githubTarget.Host == "" || githubTarget.Path != "" {
		return nil, fmt.Errorf("githubd target must be an absolute URL, got %q", githubTargetText)
	}
	p.githubProxy = &httputil.ReverseProxy{
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(githubTarget)
			req.Out.Host = req.In.Host
			req.Out.Header.Set(api.RequestIDHeader, req.In.Header.Get(api.RequestIDHeader))
			req.Out.Header.Del("X-Forwarded-For")
			req.Out.Header.Del("X-Forwarded-Host")
			req.Out.Header.Del("X-Forwarded-Proto")
			clientIP, proto := gateway.CanonicalForwardingContext(req.In, p.trustedIngressCIDRs)
			if clientIP != "" {
				req.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Out.Header.Set("X-Forwarded-Proto", proto)
		},
		ModifyResponse: func(resp *http.Response) error {
			httpsec.StripStaticHeaders(resp.Header)
			resp.Header.Del(api.RequestIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("githubd upstream unavailable", "path", r.URL.Path, "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "github_integration_unavailable", "GitHub integration unavailable", "the GitHub webhook receiver is not reachable"))
		},
	}
	return p, nil
}

func (p *controlPlaneProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Stamp the public boundary even when this handler is exercised without
	// gatewayd-public's outer middleware (unit tests and embedded callers).
	// The same value is forwarded to apid, whose middleware reuses it.
	requestID := r.Header.Get(api.RequestIDHeader)
	if requestID == "" {
		requestID = middleware.NewRequestID()
	}
	r.Header.Set(api.RequestIDHeader, requestID)
	w.Header().Set(api.RequestIDHeader, requestID)

	// githubd is loopback-only on the control-plane host. Route the exact
	// public webhook endpoint here before the generic compute data plane; app
	// and custom-domain workloads may still own the same path on their hosts.
	// githubd remains the authority for method, body-limit, signature, and
	// delivery de-duplication checks.
	if r.URL.Path == "/webhooks/github" && isPlatformHealthHost(r.Host, p.appsDomain) {
		p.githubProxy.ServeHTTP(w, r)
		return
	}
	// Prometheus consumes this registry-backed service-discovery endpoint only
	// over apid's loopback listener. Do not let the public control-plane proxy
	// turn it into an externally reachable API route.
	if r.URL.Path == "/v1/internal/metrics/targets" ||
		r.URL.Path == "/v1/internal/metrics/promtail-targets" {
		http.NotFound(w, r)
		return
	}
	// The platform API has no public Prometheus surface. Customer app hosts,
	// however, may legitimately implement /metrics themselves, so keep those
	// requests on the ordinary compute data plane.
	if r.URL.Path == "/metrics" && isPlatformHealthHost(r.Host, p.appsDomain) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Not found", "the requested API route does not exist"))
		return
	}
	if r.URL.Path == apid.ApidRootHealthz || r.URL.Path == apid.ApidRootReadyz {
		if isPlatformHealthHost(r.Host, p.appsDomain) {
			p.proxy.ServeHTTP(w, r)
			return
		}
		p.next.ServeHTTP(w, r)
		return
	}
	if apid.IsApidPath(r.URL.Path) && !isComputeOwnedGatewayPath(r.URL.Path) {
		p.proxy.ServeHTTP(w, r)
		return
	}
	p.next.ServeHTTP(w, r)
}

// isPlatformHealthHost scopes the public platform probe to the apex/API host
// (plus direct loopback/IP probes used by rollout tooling). Wildcard app and
// custom-domain Hosts must route /healthz through the app data plane.
func isPlatformHealthHost(rawHost, appsDomain string) bool {
	host := hostWithoutPort(rawHost)
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(appsDomain)), ".")
	if host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	return domain != "" && (host == domain || host == "api."+domain)
}

func hostWithoutPort(rawHost string) string {
	rawHost = strings.TrimSpace(rawHost)
	if host, _, err := net.SplitHostPort(rawHost); err == nil {
		return strings.TrimSuffix(strings.ToLower(host), ".")
	}
	return strings.TrimSuffix(strings.ToLower(strings.Trim(rawHost, "[]")), ".")
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
