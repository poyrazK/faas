// Package securitytxt serves Gregale's RFC 9116 security contact document.
package securitytxt

import "net/http"

// Content is the public security contact document. Keep the canonical URL
// stable: security tooling discovers this document before any product API.
const Content = "Contact: mailto:security@gregale.dev\n" +
	"Contact: https://github.com/poyrazK/faas/security/advisories/new\n" +
	"Expires: 2027-12-31T23:59:59z\n" +
	"Preferred-Languages: en\n" +
	"Canonical: https://docs.gregale.dev/.well-known/security.txt\n" +
	"Encryption: https://docs.gregale.dev/security/pgp.asc\n" +
	"Acknowledgments: https://docs.gregale.dev/security/acknowledgments\n" +
	"Policy: https://docs.gregale.dev/security\n"

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
