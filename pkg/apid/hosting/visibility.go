package hosting

import (
	"net/http"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

// HideDisabled runs before routing, authentication and CORS, so disabled
// hosting cannot be discovered through auth errors, method probes or redirects.
// The inner guards still fence admission if the runtime flag changes in flight.
func (s Service) HideDisabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const root = "/v1/internal/hosting"
		p := path.Clean(r.URL.Path)
		if (p == root || strings.HasPrefix(p, root+"/")) && !s.Host.HostingEnabled() {
			w.Header().Set("Cache-Control", "no-store")
			httpkit.NoSuchRoute(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
