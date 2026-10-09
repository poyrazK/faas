package guestprofiling

import (
	"context"
	"net/http"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// WithRoute associates sampled Go CPU with a static declared method/pattern,
// e.g. "GET /orders/{id}". Never pass URL paths containing customer values.
// Gregale admits only labels in the host-owned explicit route contract.
func WithRoute(ctx context.Context, route string, work func(context.Context)) {
	if !api.ValidProfileRoute(route) || route == api.ProfileUnattributedRoute {
		route = ""
	}
	pprof.Do(ctx, pprof.Labels(api.ProfileRouteLabel, route), work)
}

// RouteHandler wraps a handler without copying request bodies or headers.
func RouteHandler(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WithRouteRequest(r.Context(), route, func(ctx context.Context) { next.ServeHTTP(w, r.WithContext(ctx)) })
	})
}

// ServeMux labels CPU with the matched Go 1.22+ ServeMux pattern. Install it
// after registering routes and use it as the server's Handler. It dispatches
// through mux so Request.Pattern, PathValue, redirects and HTTP behavior remain
// intact. Patterns must still match Gregale's host-declared route contract.
// Legacy GODEBUG=httpmuxgo121=1 routing is not supported.
func ServeMux(mux *http.ServeMux) http.Handler {
	legacy := false
	for _, setting := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if value, ok := strings.CutPrefix(setting, "httpmuxgo121="); ok {
			legacy = value == "1"
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := ""
		if !legacy {
			_, pattern := mux.Handler(r)
			route = serveMuxRoute(r.Method, pattern)
		}
		WithRouteRequest(r.Context(), route, func(ctx context.Context) {
			mux.ServeHTTP(w, r.WithContext(ctx))
		})
	})
}

// The pattern's host and optional method are routing syntax, not label values.
// Use the actual request method (including HEAD matches against GET patterns).
// An empty match clears inherited attribution for 404/405 responses.
func serveMuxRoute(method, pattern string) string {
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = rest
	}
	if slash := strings.IndexByte(pattern, '/'); slash >= 0 {
		pattern = pattern[slash:]
	} else {
		return ""
	}
	route := method + " " + pattern
	if !api.ValidProfileRoute(route) {
		return ""
	}
	return route
}

// WithoutRoute clears inherited attribution for background goroutines.
func WithoutRoute(ctx context.Context, work func(context.Context)) { WithRoute(ctx, "", work) }
