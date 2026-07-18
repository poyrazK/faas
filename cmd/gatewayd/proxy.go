// gatewayd dashboard proxy (spec §14 M7.5, ADR-011).
//
// gatewayd is the only public listener (spec §11). For the M7.5
// dashboard and the future OAuth callback we add a thin path-prefix
// switch in front of gateway.Handler: anything starting with
// /dashboard/* or /oauth/* reverse-proxies to apid's loopback listener
// (default 127.0.0.1:8081). Everything else falls through to the
// existing host-routed wake/proxy path.
//
// apid binds loopback-only, so this proxy is the only way external
// traffic reaches the dashboard — preserving the §11 invariant.
//
// Webhook path (/webhooks/github) lands in slice 7 and follows the
// same shape but with HMAC edge-verification before the proxy hop.
package main

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// dashboardProxy wraps next so /dashboard/* and /oauth/* requests
// reverse-proxy to apid's loopback listener. The proxy is path-prefix
// only — it doesn't touch Host headers — because apid's loopback mux
// doesn't key off Host (gatewayd already does the host→app routing
// for traffic that reaches the proxy via the apps domain).
//
// target is the parsed loopback URL of apid (e.g.
// http://127.0.0.1:8081). It's stored so we build a fresh
// httputil.ReverseProxy per request — the stdlib proxy keeps no
// per-request state worth reusing, and rebuilding avoids any chance
// of a stale Director closure.
type dashboardProxy struct {
	target *url.URL
	next   http.Handler
	log    *slog.Logger
}

// newDashboardProxy parses target and returns the wrapping handler.
// If target is empty or unparseable, the wrapper is disabled and every
// request falls through to next — useful for unit tests.
func newDashboardProxy(target string, next http.Handler, log *slog.Logger) http.Handler {
	if target == "" || log == nil {
		return next
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Warn("dashboard proxy target invalid; passing through", "target", target, "err", err)
		return next
	}
	log.Info("dashboard proxy armed", "target", u.String())
	return &dashboardProxy{target: u, next: next, log: log}
}

// ServeHTTP routes /dashboard/* and /oauth/* to apid. The rest falls
// through to next (gateway.Handler's normal wake/rate-limit/proxy
// flow).
func (d *dashboardProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isDashboardPath(r.URL.Path) {
		d.proxyToApid(w, r)
		return
	}
	d.next.ServeHTTP(w, r)
}

// isDashboardPath returns true for the prefixes gatewayd forwards to
// apid. Keep the list small — anything outside these falls through
// to the wake/proxy path (and would 404 there for legitimate
// dashboard traffic, which is a bug we'll catch immediately in tests).
//
// Review finding #6: the bare HasPrefix("/dashboard") matched
// "/dashboard.zip" and "/dashboards" too. Tighten to exact-match
// /dashboard + /dashboard/ + the /dashboard/ subtree; /oauth/ was
// already correctly anchored by the trailing slash.
func isDashboardPath(p string) bool {
	const dashboardRoot = "/dashboard"
	if p == dashboardRoot || p == dashboardRoot+"/" {
		return true
	}
	if strings.HasPrefix(p, dashboardRoot+"/") {
		return true
	}
	return strings.HasPrefix(p, "/oauth/")
}

// proxyToApid builds a one-shot httputil.ReverseProxy and serves the
// request through it. We strip X-Forwarded-* headers so apid sees the
// originating client, not the gateway hop, and ensure x-faas-request-id
// is present (gateway.Handler does this for the wake path; the
// dashboard proxy bypasses it, so we mint one here).
func (d *dashboardProxy) proxyToApid(w http.ResponseWriter, r *http.Request) {
	r.Header.Del("X-Forwarded-For")
	r.Header.Del("X-Forwarded-Proto")
	r.Header.Del("X-Forwarded-Host")
	if r.Header.Get("x-faas-request-id") == "" {
		r.Header.Set("x-faas-request-id", middleware.NewRequestID())
	}
	r.Host = d.target.Host

	pxy := httputil.NewSingleHostReverseProxy(d.target)
	// On upstream dial failure (apid not running yet) emit a clean
	// 503 problem instead of the stdlib's bare "EOF".
	pxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
		d.log.Error("dashboard proxy upstream error", "path", logsanitize.Field(r.URL.Path), "err", err)
		rw.Header().Set("Content-Type", "application/problem+json")
		rw.WriteHeader(http.StatusServiceUnavailable)
		_, _ = rw.Write([]byte(`{"type":"about:blank","title":"apid_unavailable","status":503,"detail":"apid is not reachable on the loopback listener"}`))
	}
	pxy.ServeHTTP(w, r)
}
