// gatewayd → apid loopback proxy (spec §11 single-public-listener
// invariant, ADR-011).
//
// gatewayd is the only public listener. apid binds loopback-only.
// For every public surface apid serves we add a thin path-prefix
// switch in front of gateway.Handler: anything matching isApidPath
// reverse-proxies to apid's loopback listener (default
// 127.0.0.1:8081). Everything else falls through to the existing
// host-routed wake/proxy path.
//
// isApidPath covers the full apid public surface:
//   - /dashboard, /dashboard/ and the /dashboard/ subtree (M7.5
//     dashboard, ADR-011)
//   - /oauth/* (OAuth callbacks)
//   - /v1/* (the §4.2 REST API surface — apps, deployments,
//     domains, crons, keys, secrets, usage, webhooks, SSE events)
//   - /login, /login/, /login/*, /auth/verify, /auth/verify/*,
//     /logout, /logout/, /logout/* (magic-link + session auth)
//   - /status, /status/, /status/* (spec §12 public status page)
//   - /healthz (loopback infra probe — required for the CD health
//     check in deploy/digitalocean/bootstrap.sh and the
//     cd-digitalocean.yml post-deploy smoke test)
//
// apid binds loopback-only, so this proxy is the only way external
// traffic reaches any of those routes — preserving the §11
// invariant. Per-route auth (api.AuthLimit, dashboard session
// middleware) is applied at apid; gatewayd just forwards.
//
// Webhook paths (/webhooks/github, /v1/webhooks/stripe) live in
// sibling wrappers (githubdProxy, stripeProxy) that run *before*
// this one — they need edge HMAC verification before forwarding,
// which plain reverse-proxying would skip.
package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// apidProxy wraps next so requests matching isApidPath
// reverse-proxy to apid's loopback listener. The proxy is
// path-prefix only — it doesn't touch Host headers — because apid's
// loopback mux doesn't key off Host (gatewayd already does the
// host→app routing for traffic that reaches the proxy via the apps
// domain).
//
// target is the parsed loopback URL of apid (e.g.
// http://127.0.0.1:8081). It's stored so we build a fresh
// httputil.ReverseProxy per request — the stdlib proxy keeps no
// per-request state worth reusing, and rebuilding avoids any chance
// of a stale Director closure.
type apidProxy struct {
	target *url.URL
	next   http.Handler
	log    *slog.Logger
}

// newApidProxy parses target and returns the wrapping handler.
// If target is empty or unparseable, the wrapper is disabled and
// every request falls through to next — useful for unit tests.
func newApidProxy(target string, next http.Handler, log *slog.Logger) http.Handler {
	if target == "" || log == nil {
		return next
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Warn("apid proxy target invalid; passing through", "target", target, "err", err)
		return next
	}
	log.Info("apid proxy armed", "target", u.String())
	return &apidProxy{target: u, next: next, log: log}
}

// ServeHTTP routes isApidPath requests to apid. The rest falls
// through to next (gateway.Handler's normal wake/rate-limit/proxy
// flow).
func (a *apidProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isApidPath(r.URL.Path) {
		a.proxyToApid(w, r)
		return
	}
	a.next.ServeHTTP(w, r)
}

// hasApidPrefix reports whether p begins with prefix anchored at
// the trailing slash — p matches if it is exactly prefix, or
// prefix followed by "/", or prefix followed by "/" and then more
// path. This prevents accidental shadowing like "/v1.zip" matching
// "/v1" — review finding #6 from the dashboard era.
func hasApidPrefix(p, prefix string) bool {
	if p == prefix || p == prefix+"/" {
		return true
	}
	return strings.HasPrefix(p, prefix+"/")
}

// isApidPath returns true for the prefixes gatewayd forwards to
// apid. Keep the list exhaustive for the apid public surface
// (issue #85) — anything outside falls through to the wake/proxy
// path (which 404s for legitimate apid traffic, so missing entries
// are loud bugs we'll catch immediately in tests).
//
// Anchor discipline (hasApidPrefix): each anchored entry matches
// exact + the trailing-slash subtree. Bare HasPrefix(prefix) would
// also match prefix + arbitrary junk (e.g. "/v1.zip" or
// "/loginfoo"), which would silently steal customer-app paths —
// review finding #6.
//
// NOTE: this means customer apps cannot expose routes starting with
// /v1/, /dashboard/, /oauth/, /login/, /auth/verify/, /logout/,
// /status/, /healthz, or /cli-auth. /v1/ in particular is a permanent
// API reservation; customer-facing docs should call this out (issue
// #85 follow-up). /cli-auth is the device-code approval page
// (spec §2.2) — same single-host reverse proxy handles it, no
// rewrite needed.
func isApidPath(p string) bool {
	// Anchored roots: each matched as exact + "/" subtree.
	for _, root := range []string{
		apidRootV1,
		apidRootDashboard,
		apidRootLogin,
		apidRootAuthVerify,
		apidRootLogout,
		apidRootStatus,
		apidRootHealthz,
		apidRootCliAuth,
	} {
		if hasApidPrefix(p, root) {
			return true
		}
	}
	// /oauth/* — only the subtree form. Deliberately no exact
	// /oauth match: apid has no /oauth route today (only
	// /oauth/callback is mounted), so a bare /oauth request would
	// 404 on apid's mux either way. Pinning this in tests
	// ({"/oauth", false}) defends against an accidental future
	// expansion that would steal what should be a 404 path.
	return strings.HasPrefix(p, apidRootOAuthPrefix)
}

// Anchored root paths used by isApidPath. Lifted to constants so
// the path table reads as data and goconst stops nagging (one of
// these appears four times in the matcher alone).
const (
	apidRootV1          = "/v1"
	apidRootDashboard   = "/dashboard"
	apidRootOAuthPrefix = "/oauth/"
	apidRootLogin       = "/login"
	apidRootAuthVerify  = "/auth/verify"
	apidRootLogout      = "/logout"
	apidRootStatus      = "/status"
	apidRootHealthz     = "/healthz"
	apidRootCliAuth     = "/cli-auth"
)

// proxyToApid builds a one-shot httputil.ReverseProxy and serves
// the request through it.
//
// Header policy lives entirely inside the Rewrite callback so all
// per-hop mutation is co-located:
//
//   - We strip X-Forwarded-Proto and X-Forwarded-Host (apid binds
//     loopback; protocol headers would mislead scheme detection).
//   - We pin X-Forwarded-For to the real client IP from
//     pr.In.RemoteAddr's host (gatewayd is the single public
//     listener, so pr.In.RemoteAddr here is the originating
//     customer). apid trusts X-Forwarded-For only when its own
//     RemoteAddr is loopback, so a customer-injected X-Forwarded-For
//     cannot reach apid in a position to be trusted — issue #89.
//   - We mint x-faas-request-id (gateway.Handler does this for the
//     wake path; the apid proxy bypasses it, so we mint here).
func (a *apidProxy) proxyToApid(w http.ResponseWriter, r *http.Request) {
	// Rebind Host to the upstream target — must happen before
	// stdlib's hop. Director-level work, not per-hop mutation.
	r.Host = a.target.Host

	pxy := &httputil.ReverseProxy{
		// Rewrite hook: stdlib's default ReverseProxy sets
		// X-Forwarded-For from r.RemoteAddr at the bottom of its
		// serve path and appends to any prior value, producing a
		// multi-hop chain. apid's defaultClientIP predicate
		// (issue #89) treats a multi-hop chain as untrusted and
		// falls back to the loopback host, which collapses every
		// customer's bucket. By providing a Rewrite callback,
		// stdlib strips the four forwarding headers itself and
		// delegates to us — the values we write here are the only
		// ones that reach apid.
		//
		// We construct ReverseProxy directly (not via
		// NewSingleHostReverseProxy) because stdlib requires
		// exactly one of Director or Rewrite to be set — adding
		// Rewrite on top of the Director that
		// NewSingleHostReverseProxy wired would crash with
		// "ReverseProxy must have exactly one of Director or
		// Rewrite set". SetURL rewrites the outbound URL to the
		// loopback target (httputil.NewSingleHostReverseProxy
		// doc, "use ReverseProxy directly with a Rewrite function.
		// The ProxyRequest SetURL method may be used to route the
		// outbound request").
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(a.target)
			// Strip X-Forwarded-For / -Proto / -Host. We rewrite
			// them ourselves below; the Del calls are belt-and-braces
			// in case the inbound request already had them set.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Forwarded-Host")
			// Pin X-Forwarded-For to the real client IP from
			// pr.In's RemoteAddr — the gatewayd edge sees the
			// customer's IP before the loopback hop. We
			// overwrite (rather than append) so apid sees
			// exactly one value, the contract its
			// defaultClientIP predicate relies on (issue #89).
			if host, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil && host != "" {
				pr.Out.Header.Set("X-Forwarded-For", host)
			}
			// Mint x-faas-request-id (gateway.Handler does this
			// for the wake path; the apid proxy bypasses it, so
			// we mint here). Co-located with the other header
			// mutations so all per-hop writes live in one place.
			if pr.Out.Header.Get("x-faas-request-id") == "" {
				pr.Out.Header.Set("x-faas-request-id", middleware.NewRequestID())
			}
		},
	}
	// On upstream dial failure (apid not running yet) emit a clean
	// 503 problem instead of the stdlib's bare "EOF".
	pxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
		a.log.Error("apid proxy upstream error", "path", logsanitize.Field(r.URL.Path), "err", err)
		rw.Header().Set("Content-Type", "application/problem+json")
		rw.WriteHeader(http.StatusServiceUnavailable)
		_, _ = rw.Write([]byte(`{"type":"about:blank","title":"apid_unavailable","status":503,"detail":"apid is not reachable on the loopback listener"}`))
	}
	pxy.ServeHTTP(w, r)
}
