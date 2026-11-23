package gateway

import "net/http"

// Cloudflare caches responses in front of the public edge (static file types
// by default, anything an app marks public), keyed by URL alone. A response to
// a request an access control decided would then reach clients that control
// refuses: an address an ip rule or the ingress allowlist denies, a country a
// geo rule refuses, a caller without the JWT or key a gate verified (ADR-091
// amendment 2026-11-02). Such a response carries both targeted cache headers
// (RFC 9213) set to private; Cloudflare honours them and they do not change
// how the browser caches.
const (
	cloudflareCDNCacheControlHeader = "Cloudflare-Cdn-Cache-Control" // canonical spelling of Cloudflare-CDN-Cache-Control
	cdnCacheControlHeader           = "Cdn-Cache-Control"            // canonical spelling of CDN-Cache-Control
)

// keepFromSharedCaches marks the response being written through w as one no
// shared cache may store, because an access control decided its request.
func keepFromSharedCaches(w http.ResponseWriter) {
	if rec, ok := w.(*statusRecorder); ok {
		rec.sharedCachePrivate = true
	}
}

// markSharedCachePrivate sets the targeted cache headers, replacing any
// value the app sent.
func markSharedCachePrivate(h http.Header) {
	h.Set(cloudflareCDNCacheControlHeader, "private")
	h.Set(cdnCacheControlHeader, "private")
}

// declaresCachePolicy reports whether the app (or an edge header rule) said
// how the response may be cached. Without a policy Cloudflare applies its own
// defaults: it stored a static-extension path (/me.pdf, .css, .js, .png, …)
// for four hours keyed by URL alone, so one user's cookie-authenticated
// response reached the next user and an app's 404 outlived the object that
// replaced it (prod 2026-11-19). A response without a policy is therefore
// kept from shared caches; an app opts into edge caching by sending one.
func declaresCachePolicy(h http.Header) bool {
	return h.Get("Cache-Control") != "" || h.Get(cdnCacheControlHeader) != "" || h.Get(cloudflareCDNCacheControlHeader) != ""
}

// carriesVerifiedCredential reports whether a gate verified a credential for
// the request, so its answer may depend on who asked.
func (a Authenticated) carriesVerifiedCredential() bool {
	return a.APIKeyID != "" || a.ConsumerID != "" || a.JWTSubject != "" || a.PlatformTenantID != ""
}
