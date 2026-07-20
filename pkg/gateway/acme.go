// :80 ACME mux + :80 → :443 redirect (spec §4.1).
//
// The :80 listener serves two unrelated workloads from one port:
//
//  1. ACME HTTP-01 challenges at /.well-known/acme-challenge/* — these MUST
//     stay on plain HTTP because Let's Encrypt's HTTP-01 solver only fetches
//     from :80. The certmagic HTTPChallengeHandler routes these by path.
//  2. Everything else gets a 308 redirect to https://<host><uri> so browsers
//     upgrade cleanly without us double-handling the request.
//
// Why 308 not 301: 301 is a "Moved Permanently" but historically allowed
// method downgrade (POST → GET). 308 is the standards-correct "Permanent
// Redirect" that preserves the original method. Spec §4.1 says "redirect to
// HTTPS"; 308 is the right status for that.
//
// Mount with http.Server.Addr = ":80" in cmd/gatewayd/main.go when TLS is
// enabled. When Disabled, :80 is unbound and the mux is never constructed.
package gateway

import (
	"net/http"
	"strings"
)

// acmeChallengePrefix is the well-known prefix RFC 8555 mandates for the
// HTTP-01 challenge. CertMagic matches on this path prefix internally;
// we only need to know it for the redirect-everything-else logic.
const acmeChallengePrefix = "/.well-known/acme-challenge/"

// NewACMEMux returns an http.Handler that:
//
//   - routes acmeChallengePrefix + <token> to challengeHandler
//   - 308-redirects everything else to https://<host><uri>
//
// challengeHandler is typically certmagic's HTTPChallengeHandler; pass nil
// and the mux will skip ACME dispatch (useful for tests + for the disabled
// path). When nil, the mux is a pure redirect handler — main.go should still
// mount it on :80 so the redirect can serve even without TLS (it just won't
// reach the ACME handler).
func NewACMEMux(challengeHandler http.Handler) http.Handler {
	if challengeHandler == nil {
		challengeHandler = http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mux.HandleFunc(acmeChallengePrefix, func(w http.ResponseWriter, r *http.Request) {
		challengeHandler.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ACME challenges: dispatch. Anything else: 308 to https://<host><uri>.
		if strings.HasPrefix(r.URL.Path, acmeChallengePrefix) {
			mux.ServeHTTP(w, r)
			return
		}
		host := hostname(r.Host)
		if host == "" {
			// No host header — refuse rather than redirect to a malformed URL.
			// The empty-host case is a protocol violation by the client; 400
			// is the spec-blessed response.
			http.Error(w, "missing host header", http.StatusBadRequest)
			return
		}
		target := "https://" + host + r.URL.RequestURI()
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}