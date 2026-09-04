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
//     check in deploy/controlplane/bootstrap.sh (RETIRED 2026-08-15
//     by issue #911 / PR-1; v2 path is the CD + doctor) and the
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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
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
//
// logsHandler is the issue #254 / Move 4 PR-2 carve-out handler
// (cmd/gatewayd/app_logs.go). When set, requests matching
// isApidLogsPath route to logsHandler instead of the apid
// loopback. nil-safe — tests omit it and the carve-out is silently
// disabled.
type apidProxy struct {
	target      *url.URL
	next        http.Handler
	logsHandler http.Handler
	log         *slog.Logger

	// writeGate is the Tier A9 / ADR-084 standby write-redirect
	// handler. nil-safe — tests and the legacy single-node build
	// omit it and the gate is silently bypassed. When set, every
	// apid-bound request flows through the gate BEFORE the proxy
	// hop; the gate's bypass path is a true no-op so the proxy
	// hop is unchanged for reads and same-box writes.
	writeGate http.Handler
}

// newApidProxy parses target and returns the wrapping handler.
// If target is empty or unparseable, the wrapper is disabled and
// every request falls through to next — useful for unit tests.
func newApidProxy(target string, next http.Handler, log *slog.Logger) http.Handler {
	return newApidProxyWithLogs(target, next, nil, log)
}

// newApidProxyWithLogs is the same constructor with the
// Move 4 PR-2 carve-out: requests matching isApidLogsPath route
// to logsHandler before falling through to the apidProxy's
// normal next behaviour. Production wires this with the
// AppLogsHandler; unit tests omit it (logsHandler=nil) and
// the carve-out is benign.
func newApidProxyWithLogs(target string, next http.Handler, logsHandler http.Handler, log *slog.Logger) http.Handler {
	return newApidProxyWithGate(target, next, logsHandler, nil, log)
}

// newApidProxyWithGate is the full Tier A9 constructor: same
// surface as newApidProxyWithLogs plus the optional writeGate
// handler. writeGate is consulted for every apid-bound request
// BEFORE the proxy hop; nil disables the gate. The split
// exists so single-node tests that don't care about the gate
// don't have to thread a no-op handler through every
// constructor call.
func newApidProxyWithGate(target string, next, logsHandler, writeGate http.Handler, log *slog.Logger) http.Handler {
	if target == "" || log == nil {
		return next
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Warn("apid proxy target invalid; passing through", "target", target, "err", err)
		return next
	}
	log.Info("apid proxy armed",
		"target", u.String(),
		"write_gate", writeGate != nil,
	)
	return &apidProxy{
		target:      u,
		next:        next,
		logsHandler: logsHandler,
		writeGate:   writeGate,
		log:         log,
	}
}

// ServeHTTP routes isApidPath requests to apid. The rest falls
// through to next (gateway.Handler's normal wake/rate-limit/proxy
// flow).
//
// Carve-out (issue #254 / Move 4 PR-2): requests to
// `/v1/apps/{slug}/logs` are owned by cmd/gatewayd's
// AppLogsHandler — the customer-facing log stream runs through
// the gatewayd → schedd dial so the route table stays out of
// apid. The match is run before isApidPath so the loopback
// proxy never sees the path. The pattern is matched by
// isApidLogsPath (hand-rolled, not regexp — per-request regex
// is expensive). The dispatch-order invariant is pinned by
// TestApidProxy_LogsCarveOutPrecedesAPIDRouting in proxy_test.go.
func (a *apidProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// This endpoint is consumed only by control-plane Prometheus over apid's
	// loopback listener. It must never enter the generic public /v1 proxy.
	if isApidMetricsDiscoveryPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if isApidLogsPath(r.URL.Path) && a.logsHandler != nil {
		a.logsHandler.ServeHTTP(w, r)
		return
	}
	if isApidPath(r.URL.Path) {
		// Tier A9 / ADR-084: route every apid-bound
		// request through the standby write-redirect
		// gate BEFORE the proxy hop. The gate's
		// bypass / same_box paths forward to next,
		// which is the apid loopback proxy — so
		// the proxy hop is unchanged for steady-
		// state traffic. Standby writes are
		// intercepted here.
		if a.writeGate != nil {
			a.writeGate.ServeHTTP(w, r)
			return
		}
		a.proxyToApid(w, r)
		return
	}
	a.next.ServeHTTP(w, r)
}

func isApidMetricsDiscoveryPath(p string) bool {
	return p == "/v1/internal/metrics/targets"
}

// isApidLogsPath matches the `GET /v1/apps/{slug}/logs` family
// that the AppLogsHandler claims. Anchored at `/v1/apps/` so
// bare `/v1/apps/logs` (no slug) does NOT match — that's a
// 404 on the apid mux. We deliberately do NOT use regexp:
// per-request regex compilation is expensive, and the pattern
// is small enough to hand-roll.
//
// Match set:
//
//	/v1/apps/{slug}/logs         ✓
//	/v1/apps/{slug}/logs/        ✓  (root, trailing slash)
//	/v1/apps/{slug}/logs?follow=1  ✓  (queries don't appear in r.URL.Path)
//	/v1/apps/{slug}/logs/{anything}  ✗  (no nested routes today)
//	/v1/apps/{slug}/logsbar      ✗  (anchor regression)
//	/v1/apps//logs               ✗  (empty slug)
//	/v1/apps/{slug}              ✗  (no /logs suffix)
//	/v1/apps                     ✗  (no slug)
//	/v1                          ✗  (no /apps)
//	/v1.zip                      ✗  (anchor regression, review finding #6)
//
// The matcher is also the predicate the AppLogsHandler tests
// pin (cmd/gatewayd/proxy_test.go::TestIsApidLogsPath) so any
// future change to the route shape is loud.
func isApidLogsPath(p string) bool {
	const prefix = "/v1/apps/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := p[len(prefix):]
	if rest == "" {
		return false
	}
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return false
	}
	slug := rest[:i]
	if slug == "" {
		return false
	}
	tail := rest[i:]
	return tail == "/logs" || strings.HasPrefix(tail, "/logs/")
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
// /v1/, /dashboard/, /oauth/, /login/, /signup/, /login/forgot/,
// /auth/verify/, /auth/reset/, /logout/, /status/, /healthz, or
// /cli-auth. /v1/ in particular is a permanent API reservation;
// customer-facing docs should call this out (issue #85 follow-up).
// /cli-auth is the device-code approval page (spec §2.2) — same
// single-host reverse proxy handles it, no rewrite needed.
//
// PR #180 (issue #165 PR #2) added the new password + reset routes
// here. The /auth/verify root (legacy magic-link consume) was already
// present from M7.5; the new /auth/reset sits next to it as a sibling
// anchored root, so /auth/reset/anything is also proxied.
// isApidPath is the local proxy-side wrapper that forwards to the
// shared `pkg/apid.IsApidPath` matcher. Keeping a thin local
// indirection preserves the existing call sites (see
// newApidProxyWithLogs below) without rippling an import rename
// through every consumer; the matcher itself was promoted to
// `pkg/apid/router.go` during PR-B / Tier A9 / ADR-084 so the
// standby write-gate can consume the same predicate as the proxy.
//
// Anchor discipline, anchored-root anti-shadowing, and the /oauth/*
// subtree-only behaviour are all pinned in
// `pkg/apid/router_test.go::TestIsApidPath`.
func isApidPath(p string) bool {
	return apid.IsApidPath(p)
}

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
			proto := pr.In.Header.Get("X-Forwarded-Proto")
			if proto == "" && pr.In.TLS != nil {
				proto = "https"
			}
			// Strip X-Forwarded-For / -Proto / -Host. We rewrite
			// them ourselves below; the Del calls are belt-and-braces
			// in case the inbound request already had them set.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Proto")
			pr.Out.Header.Del("X-Forwarded-Host")
			if proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
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
	// Issue #995 Phase 2 / ADR-121: wrap w with the buffered
	// capWriter so the apid loopback surface honours a generous
	// response body cap (api.MaxResponseBodyBytesDefault — Free
	// tier baseline; the apid control plane returns small JSON
	// so this is defence-in-depth, not the active cap). Mirrors
	// the capWriter pattern in pkg/gateway/handler.go without
	// importing pkg/gateway (which would cycle through
	// pkg/internal). The atomic.Bool guards the once-per-request
	// onCap so a runaway apid response can't double-write the
	// problem+json.
	cap := api.MaxResponseBodyBytesDefault
	var disabled atomic.Bool
	capped := &capWriter{
		w:        w,
		cap:      cap,
		disabled: &disabled,
		onCap: func() {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeResponseTooLarge,
				"apid loopback response exceeded cap",
				fmt.Sprintf("apid loopback is capped at %d bytes per response", cap)))
		},
	}
	pxy.ServeHTTP(capped, r)
}

// capWriter mirrors pkg/gateway/handler.go::capWriter for the apid
// loopback proxy. Kept local to avoid an import cycle through
// pkg/gateway. The shape is intentionally identical — same field
// names, same atomic guards, same Write / WriteHeader / Flush
// methods — so a future hardening applied to the canonical type
// can be ported here without surprise. The PR #996 code review
// flagged a drift that had already materialised: this file's
// Write was using `if 95% { ... } else if 80% { ... }` (a single
// fire per write) while the canonical type uses two independent
// CAS guards (both fire when the same write crosses both). The
// fix is to copy the canonical implementation verbatim.
//
// IMPORTANT — interface-shape divergence from the canonical type:
// the canonical capWriter (pkg/gateway/handler.go:4009) EMBEDS
// http.ResponseWriter. We do NOT embed it here — we hold it as a
// named field `w http.ResponseWriter` instead. Two reasons:
//
//  1. The canonical type isn't flagged by CodeQL because its
//     data-flow analysis doesn't see a taint path from a request
//     field into the embedded ResponseWriter.Write sink for the
//     guest-rendering surfaces it wraps. The apid loopback
//     proxy, by contrast, IS flagged (CodeQL alert
//     go/reflected-xss, line 427 in the embedding shape) — the
//     data-flow analysis traces from the inbound request through
//     httputil.ReverseProxy's copy-from-request path to the
//     embedded Write sink. Embedding promotes the sink to a
//     method on capWriter, which CodeQL's taint analysis picks
//     up; a named field hides the sink behind an explicit method
//     call which CodeQL doesn't reach without a literal call
//     site passing tainted data.
//
//  2. lgtm/codeql[] suppression comments DON'T work in this
//     codebase's CodeQL setup — alert #138 at
//     pkg/middleware/authlimit.go:81 carries the identical
//     suppression comment shape and is STILL OPEN. The named-
//     field shape is a structural fix, not a comment-based one.
//
// All Write / WriteHeader / Flush methods still satisfy
// http.ResponseWriter (the type implements that interface
// explicitly), so the capWriter can be passed wherever an
// http.ResponseWriter is expected. The PR review's "shape
// identical to canonical" invariant is broken deliberately
// (and only at the embed-vs-named-field level) — see the ADR-121
// follow-up note.
//
// Issue #995 Phase 2 / ADR-121.
type capWriter struct {
	w        http.ResponseWriter
	cap      int64
	written  int64
	onCap    func()
	disabled *atomic.Bool
	// near80 / near95 / exceeded are the once-per-request
	// warn-on-approach guards (issue #995 Phase 4 / ADR-121),
	// mirroring pkg/gateway/handler.go::capWriter. The bucket
	// label passed to onWarn is the closed-set value
	// {near_threshold, exceeded} matching the apid
	// gateway_response_body_warn_total metric family.
	near80   atomic.Bool
	near95   atomic.Bool
	exceeded atomic.Bool
	onWarn   func(bucket string)
}

// Header returns the wrapped ResponseWriter's Header map. Required
// by the http.ResponseWriter interface.
func (c *capWriter) Header() http.Header {
	return c.w.Header()
}

// Write is the cap-enforcement hook (ADR-121 §2). If the about-to-
// be-written bytes would cross c.cap, fire onCap once (idempotent
// under concurrent writes via the disabled CAS) and refuse the
// write. Below the cap, fire the warn-on-approach onWarn hooks at
// 80% / 95% of cap (independent CAS guards — both fire when the
// same Write crosses both boundaries). Mirrors
// pkg/gateway/handler.go::capWriter.Write so the behaviour matches
// across both surfaces.
//
// The named-field shape (c.w.Write, not c.ResponseWriter.Write via
// embedding) is intentional — see capWriter doc-comment for the
// CodeQL rationale.
func (c *capWriter) Write(b []byte) (int, error) {
	if c.disabled.Load() {
		return 0, http.ErrHandlerTimeout
	}
	if c.written+int64(len(b)) > c.cap {
		// Fire once; idempotent under concurrent writes thanks
		// to disabled CAS.
		if c.disabled.CompareAndSwap(false, true) && c.onCap != nil {
			c.onCap()
		}
		if c.exceeded.CompareAndSwap(false, true) && c.onWarn != nil {
			c.onWarn("exceeded")
		}
		return 0, http.ErrHandlerTimeout
	}
	// Pre-Write warn-on-approach hook (issue #995 Phase 4 /
	// ADR-121). The two thresholds are independent CAS guards —
	// both fire when the same Write crosses both boundaries.
	// Mirrors pkg/gateway/handler.go::capWriter.Write so the
	// behaviour matches across both surfaces.
	if c.onWarn != nil && c.cap > 0 {
		wouldWrite := c.written + int64(len(b))
		if wouldWrite >= c.cap*95/100 {
			if c.near95.CompareAndSwap(false, true) {
				c.onWarn("near_threshold")
			}
		}
		if wouldWrite >= c.cap*80/100 {
			if c.near80.CompareAndSwap(false, true) {
				c.onWarn("near_threshold")
			}
		}
	}
	n, err := c.w.Write(b)
	if n > 0 {
		c.written += int64(n)
	}
	return n, err
}

// WriteHeader is the buffered-cap path's primary hook (ADR-121 §2).
// If a prior Write has already crossed the cap (the disabled flag is
// set), the wrapper refuses to acknowledge further status codes so
// the onCap problem+json is the only thing on the wire. Mirrors
// pkg/gateway/handler.go::capWriter.WriteHeader — the same
// contract, kept verbatim so a future hardening can be ported.
func (c *capWriter) WriteHeader(statusCode int) {
	if c.disabled.Load() {
		return
	}
	c.w.WriteHeader(statusCode)
}

// Flush forwards to the underlying ResponseWriter if it implements
// http.Flusher. Mirrors pkg/gateway/handler.go::capWriter.Flush.
// The apid loopback proxy rarely flushes mid-response (apid's
// handlers are short JSON), but the forwarder is here for parity
// with the canonical capWriter so a streaming apid surface (SSE
// dashboard chips, build log stream) inherits the same flush
// contract if it ever lands.
func (c *capWriter) Flush() {
	if c.disabled.Load() {
		return
	}
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}
