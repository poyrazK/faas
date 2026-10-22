package ciapi

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
)

// HideDisabledRoutes runs before route matching and authentication so disabled
// CI routes have the same response as an unregistered API route, including
// method mismatches and paths that ServeMux would otherwise redirect.
func HideDisabledRoutes(config func() ci.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const root = "/v1/internal/ci"
		path := r.URL.Path
		if path == root || strings.HasPrefix(path, root+"/") {
			cfg := config()
			enabled := cfg.Enabled
			if strings.HasPrefix(path, root+"/runtime/") {
				// Existing guest grants can finish bounded output/stop recovery
				// with admission off. Their transport and proof gates still apply.
				enabled = cfg.TransportEnabled
			}
			if !enabled {
				w.Header().Set("Cache-Control", "no-store")
				httpkit.NoSuchRoute(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
