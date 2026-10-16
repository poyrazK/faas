package ciruntime

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/ci"
)

// SecureTransport accepts direct TLS or the deployment's local TLS terminator.
// Forwarded headers from non-loopback peers are never evidence of HTTPS.
func SecureTransport(origin string, enabled bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !enabled || !ci.ValidRuntimeOrigin(origin) || !secureRequest(r, origin) {
			writeError(w, ci.ErrRuntimeAuthority)
			return
		}
		next(w, r)
	}
}

func secureRequest(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(r.Host, u.Host) {
		return false
	}
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback() && len(r.Header.Values("X-Forwarded-Proto")) == 1 && r.Header.Get("X-Forwarded-Proto") == "https"
}
