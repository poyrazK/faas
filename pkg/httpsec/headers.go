// headers.go — the five static hardening headers, set on every
// response (JSON, HTML, SSE, problem docs alike). Mounted at the
// outermost wrapper of both gatewayd-internal's publicHandler and apid's
// server.handler() return.
//
// Headers and values are pinned by issue #249; do not relax them
// without an ADR. CSP lives in csp.go because it depends on a
// per-request nonce.

package httpsec

import (
	"net/http"
	"sync"
)

// Header values are package-level constants so the test suite can pin
// them byte-for-byte without copy-pasting the literal. Editing these
// literals is the only way to change the platform's posture.
const (
	HeaderStrictTransportSecurity = "Strict-Transport-Security"
	HeaderXFrameOptions           = "X-Frame-Options"
	HeaderXContentTypeOptions     = "X-Content-Type-Options"
	HeaderReferrerPolicy          = "Referrer-Policy"
	HeaderPermissionsPolicy       = "Permissions-Policy"

	// ValueHSTSMaxAge is 1 year. spec §11 + issue #249. No `preload`
	// until the §11 policy review finalises.
	ValueHSTSMaxAge = "max-age=31536000; includeSubDomains"

	ValueXFrameOptions       = "DENY"
	ValueXContentTypeOptions = "nosniff"
	ValueReferrerPolicy      = "strict-origin-when-cross-origin"
	ValuePermissionsPolicy   = "camera=(), microphone=(), geolocation=(), usb=(), payment=()"
)

// HSTSEnabled gates Strict-Transport-Security. Default true; flipped
// to false by cmd/{apid,gatewayd-internal,gatewayd-public}/main.go when
// FAAS_HSTS_ENABLED=false
// is set (dev mode). RFC 6797 §7.2 says UAs ignore HSTS on plain HTTP,
// so the env knob is purely cosmetic — production TLS listeners always
// emit it.
//
// Package-level var (not constructor arg) so the test suite can flip
// it without re-plumbing every Middleware call. SetHSTSEnabled protects
// reads and writes with hstsMu, so the gateway runtime-config watcher can
// safely apply an operator change while requests are in flight.
var HSTSEnabled = true
var hstsMu sync.RWMutex

// SetHSTSEnabled is the explicit setter called from cmd/*/main.go's
// run() after env parsing. Avoids the "is this var exported? is it
// mutated by tests?" ambiguity of a bare exported bool.
func SetHSTSEnabled(enabled bool) {
	hstsMu.Lock()
	HSTSEnabled = enabled
	hstsMu.Unlock()
}

func hstsEnabled() bool {
	hstsMu.RLock()
	enabled := HSTSEnabled
	hstsMu.RUnlock()
	return enabled
}

// Static is the middleware that emits the five static hardening
// headers on every response. It MUST be mounted at the outermost
// wrapper of the public listener so the headers reach the wire
// regardless of which inner handler responded (dashboard render,
// reverse-proxy passthrough, problem-doc, SSE).
//
// Order of Set does not matter (Set overwrites; idempotent under
// re-mount). The middleware does not wrap the ResponseWriter, so
// downstream code that calls WriteHeader(N) flushes both this
// middleware's headers and the handler's Content-Type.
func Static(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set(HeaderXFrameOptions, ValueXFrameOptions)
		h.Set(HeaderXContentTypeOptions, ValueXContentTypeOptions)
		h.Set(HeaderReferrerPolicy, ValueReferrerPolicy)
		h.Set(HeaderPermissionsPolicy, ValuePermissionsPolicy)
		if hstsEnabled() {
			h.Set(HeaderStrictTransportSecurity, ValueHSTSMaxAge)
		}
		next.ServeHTTP(w, r)
	})
}
