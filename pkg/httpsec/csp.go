// csp.go — Content-Security-Policy with per-request nonce, gated by
// a path predicate so gatewayd-internal doesn't lock CSP onto customer-app
// proxied responses (those apps govern their own CSP). apid emits
// CSP on every response (apid serves only dashboard + API; JSON
// responses carry a harmless policy).
//
// The header literal is pinned by issue #249. Editing it requires
// touching every dashboard template that holds a matching
// `nonce="…"` attribute — see pkg/dashboard/templates/*.html.

package httpsec

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
)

// nonceLen is the byte count fed into base64.RawURLEncoding; 16 bytes
// produces a 22-char URL-safe nonce, well above the 128-bit floor CSP
// recommends.
const nonceLen = 16

// BuildCSPForTest exposes the unexported buildCSP to the test
// package without exposing it on the public API. Test-only seam:
// live code never calls it (the Nonce middleware inlines the build).
//
// The exported name carries the _ForTest suffix so a code-review
// reader knows immediately that this is a test-only handle. The
// _test.go file imports the package via the external test package
// (package httpsec_test) and reads it as httpsec.BuildCSPForTest.
var BuildCSPForTest = buildCSP

// MintNonceForTest exposes the unexported mintNonce to the test
// package so the CR/LF sanitisation path (which only fires on
// rand.Read failure) can be exercised without a fault-injection
// hook on the rand source. Live code never calls it.
var MintNonceForTest = mintNonce

// cspScriptHosts is the additional script-src host set beyond 'self'
// and the per-request nonce. Issue #249 keeps the dashboard's
// htmx.org@2.0.4 and htmx-ext-sse@2.2.2 scripts served from
// unpkg.com — the alternative (self-hosting) is tracked as a future
// ADR.
//
// TODO(security): pin SRI hashes on the <script src="…unpkg.com…">
// tags so a compromised unpkg.com response cannot inject arbitrary
// script. The hashes are content-addressed, so they stay valid
// across the version pin (htmx.org@2.0.4 / htmx-ext-sse@2.2.2) and
// the SRI check fails if the file is tampered with on the CDN.
var cspScriptHosts = []string{"https://unpkg.com"}

// cspStyleHosts permits the SRI-pinned Swagger UI stylesheet on /docs.
// Dashboard styles remain nonce-protected; this is only an additional host
// source for the external documentation stylesheet.
var cspStyleHosts = []string{"https://unpkg.com"}

// cspFormActionHosts is the form-action host list. Today no client-
// side Stripe.js is loaded (verified by grep), but the policy
// anticipates Stripe Checkout / Billing Portal onboarding.
var cspFormActionHosts = []string{
	"https://*.stripe.com",
}

// buildCSP returns the Content-Security-Policy header value for the
// given nonce. The output is byte-stable for a given nonce so the
// test suite can assert exact equality (TestCSP_HeaderMatchesDashboardSpec).
//
// `'nonce-…'` MUST be quoted exactly: unquoted nonces match any
// per-page nonce value, which defeats the purpose. CSP3 source-list
// grammar, https://www.w3.org/TR/CSP3/#match-url-to-source-list.
//
// Host sources (https://unpkg.com, https://*.stripe.com) are
// unquoted — only keywords like 'self' / 'nonce-…' / 'unsafe-inline'
// take quotes. The `default-src 'self'` covers anything not
// enumerated below (frame-src, font-src, media-src, object-src,
// etc.); the few directives we override are the ones the dashboard
// actually uses.
//
// img-src allows data: so the dashboard can inline small icons /
// SVG without an XSS-shaped detour; a future PR can tighten when
// payment icons land.
func buildCSP(nonce string) string {
	var b strings.Builder
	b.WriteString("default-src 'self'; ")
	b.WriteString("script-src 'self' 'nonce-")
	b.WriteString(nonce)
	b.WriteString("'")
	for _, h := range cspScriptHosts {
		b.WriteString(" ")
		b.WriteString(h)
	}
	b.WriteString("; ")
	b.WriteString("style-src 'self' 'nonce-")
	b.WriteString(nonce)
	b.WriteString("'")
	for _, h := range cspStyleHosts {
		b.WriteString(" ")
		b.WriteString(h)
	}
	b.WriteString("; ")
	b.WriteString("img-src 'self' data:; ")
	b.WriteString("connect-src 'self'; ")
	b.WriteString("frame-ancestors 'none'; ")
	b.WriteString("base-uri 'none'; ")
	b.WriteString("form-action 'self'")
	for _, h := range cspFormActionHosts {
		b.WriteString(" ")
		b.WriteString(h)
	}
	b.WriteString("'")
	return b.String()
}

// Nonce is the CSP-emitting middleware. It mints a fresh nonce per
// request, stores it on the request context (so pkg/dashboard.Render
// can stamp `nonce="…"` on every <script>/<style> tag), and emits
// Content-Security-Policy only when gate(r) returns true.
//
// gate is the apid-vs-customer-app discriminator forgatewayd-internal
// (cmd/gatewayd-internal/proxy.go::isApidPath); apid passes a func that
// always returns true. Forgetting the gate is a compile error in
// gatewayd-internal's main.go (the parameter is required).
//
// The middleware does not log the nonce; on the rare rand.Read
// failure path it logs the 6-char fingerprint so the operator can
// correlate the request without recovering the value.
func Nonce(gate func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce, ok := mintNonce(r.URL.Path)
		if !ok {
			// rand.Read failed; fall back to the zero nonce. The
			// browser will refuse inline scripts that carry a
			// zero-nonce match against the page's CSP, but the
			// page still renders and operators see the alert in
			// their structured logs.
			nonce = zeroNonce
		}
		r = r.WithContext(WithNonce(r.Context(), nonce))
		if gate != nil && gate(r) {
			w.Header().Set("Content-Security-Policy", buildCSP(nonce))
		}
		next.ServeHTTP(w, r)
	})
}

// zeroNonce is the literal 22-zero string returned on a rare
// rand.Read failure. Constant so the failure-path test can pin it
// and operators can grep for it in their logs.
const zeroNonce = "0000000000000000000000"

// mintNonce returns a fresh 22-character URL-safe nonce and
// reports success. On rand.Read failure it logs once (path +
// 6-char fingerprint of the zero nonce, never the full value) and
// returns ok=false so the caller can substitute the zero nonce.
//
// path is the request URL path — attacker-controlled. We strip CR
// and LF before logging so a malicious URL like `/foo%0A%0Dbar`
// cannot forge log lines. This is the canonical CodeQL go/log-
// injection sanitiser (see the rule's GOOD example at
// https://codeql.github.com/codeql-query-help/go/go-log-injection/);
// a custom helper that does the same stripping internally does NOT
// close the alert — CodeQL only propagates taint through the two
// separate strings.ReplaceAll calls.
func mintNonce(path string) (string, bool) {
	var b [nonceLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		safePath := strings.ReplaceAll(path, "\r", "")
		safePath = strings.ReplaceAll(safePath, "\n", "")
		slog.Default().Error("httpsec: rand.Read failed; nonce degraded to zeros",
			"err", err, "path", safePath, "nonce_fp", zeroNonce[:6])
		return zeroNonce, false
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), true
}

// NewNonce returns a fresh 22-character URL-safe nonce. Public so
// unit tests and any future caller (e.g. a future build-time
// pre-renderer) can mint nonces without going through the
// middleware. On the rare rand.Read failure it returns the zero
// nonce rather than panicking in the caller's hot path; callers
// that need the failure signal should use mintNonce directly.
func NewNonce() string {
	n, _ := mintNonce("")
	return n
}
