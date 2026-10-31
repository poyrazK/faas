package main

import (
	"net/http"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// computePrivatePath reports whether p belongs to gatewayd-internal's private
// surface: realtimed's unauthenticated management API, which apid reaches on
// a compute node directly (/v1/internal/realtime/...), and the compute
// metrics scrape (/v1/internal/metrics). gatewayd-internal serves both on the
// same mux as app traffic, ahead of every app's own routes, so the edge must
// never forward them, on any host: an app subdomain request for
// /v1/internal/realtime/internal/connections reached realtimed and listed
// every tenant's connections. An app cannot serve these paths itself anyway.
func computePrivatePath(p string) bool {
	clean := path.Clean("/" + p)
	return clean == "/v1/internal/realtime" ||
		strings.HasPrefix(clean, "/v1/internal/realtime/") ||
		clean == "/v1/internal/metrics"
}

// refuseComputePrivatePaths answers 404 for computePrivatePath requests
// before they reach the control-plane proxy or the compute edge.
func refuseComputePrivatePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if computePrivatePath(r.URL.Path) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Not found", "the requested API route does not exist"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
