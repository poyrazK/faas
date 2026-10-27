// Package securitytxt serves Gregale's RFC 9116 security contact document.
package securitytxt

import "net/http"

// Content is the public security contact document. Keep the canonical URLs
// stable: security tooling discovers this document before any product API.
// It is served on the platform hosts and, through the web console's proxy,
// at the apex; .well-known/security.txt in the repository is the same text.
// Every URL here must resolve (issue #2518): add Encryption and
// Acknowledgments only once a public key and an acknowledgments page exist.
const Content = "Contact: mailto:security@gregale.dev\n" +
	"Contact: https://github.com/poyrazK/faas/security/advisories/new\n" +
	"Expires: 2027-12-31T23:59:59z\n" +
	"Preferred-Languages: en\n" +
	"Canonical: https://gregale.dev/.well-known/security.txt\n" +
	"Canonical: https://api.gregale.dev/.well-known/security.txt\n" +
	"Policy: https://gregale.dev/docs/responsible-disclosure\n"

// Handler returns the anonymous GET/HEAD handler for /.well-known/security.txt.
// The response is deliberately static so it remains available while the
// control plane or customer data plane is degraded.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(Content))
		}
	})
}
