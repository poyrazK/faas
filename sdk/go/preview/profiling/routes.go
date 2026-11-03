package profiling

import (
	"context"
	"net/http"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	// routeLabel is the pprof label Gregale attributes CPU by.
	routeLabel = "gregale_route"
	// unattributedRoute is reserved for CPU outside any declared route.
	unattributedRoute     = "[unattributed]"
	routeMaxLabelBytes    = 256
	routeMaxLabels        = 50
	routeMaxLabeledCounts = int64(1000000000)
)

// validRoute reports whether route is a label Gregale accepts: a method and
// a path pattern, never a URL with customer values.
func validRoute(route string) bool {
	if route == "" || route == unattributedRoute {
		return true
	}
	if len(route) > routeMaxLabelBytes || !utf8.ValidString(route) || strings.IndexFunc(route, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return false
	}
	method, path, ok := strings.Cut(route, " ")
	if !ok || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " ?#") {
		return false
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	}
	return false
}

// WithRoute associates sampled Go CPU with a static declared method/pattern,
// e.g. "GET /orders/{id}". Never pass URL paths containing customer values.
// Gregale admits only labels in the host-owned explicit route contract.
func WithRoute(ctx context.Context, route string, work func(context.Context)) {
	if !validRoute(route) || route == unattributedRoute {
		route = ""
	}
	pprof.Do(ctx, pprof.Labels(routeLabel, route), work)
}

// WithoutRoute clears inherited attribution for background goroutines.
func WithoutRoute(ctx context.Context, work func(context.Context)) { WithRoute(ctx, "", work) }

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
	if !validRoute(route) {
		return ""
	}
	return route
}

// routeRequestReport counts request entries per route over one CPU window,
// so Gregale can report CPU per request.
type routeRequestReport struct {
	FromUnixNano  int64            `json:"from_unix_nano"`
	UntilUnixNano int64            `json:"until_unix_nano"`
	Complete      bool             `json:"complete"`
	Routes        map[string]int64 `json:"routes"`
}

type countedRouteRequestKey struct{}

var countingRouteRequests atomic.Bool

var routeRequests struct {
	sync.Mutex
	active *routeRequestReport
}

// WithRouteRequest labels one request and counts its entry while the Go CPU
// collector is active. Use WithRoute for execution that is not a request.
// Nested wrappers share the request context to avoid counting the same entry
// twice. Instrument the final matched route once, not multiple router layers.
func WithRouteRequest(ctx context.Context, route string, work func(context.Context)) {
	if route != "" && route != unattributedRoute && validRoute(route) {
		if counted, _ := ctx.Value(countedRouteRequestKey{}).(bool); !counted {
			countRouteRequest(route)
			ctx = context.WithValue(ctx, countedRouteRequestKey{}, true)
		}
	}
	WithRoute(ctx, route, work)
}

func startRouteRequestWindow() {
	routeRequests.Lock()
	defer routeRequests.Unlock()
	routeRequests.active = &routeRequestReport{FromUnixNano: time.Now().UnixNano(), Complete: true, Routes: map[string]int64{}}
	countingRouteRequests.Store(true)
}

func stopRouteRequestWindow() *routeRequestReport {
	countingRouteRequests.Store(false)
	routeRequests.Lock()
	defer routeRequests.Unlock()
	report := routeRequests.active
	routeRequests.active = nil
	if report != nil {
		report.UntilUnixNano = time.Now().UnixNano()
	}
	return report
}

func countRouteRequest(route string) {
	if !countingRouteRequests.Load() {
		return
	}
	entry := time.Now().UnixNano()
	routeRequests.Lock()
	defer routeRequests.Unlock()
	report := routeRequests.active
	if report == nil || entry < report.FromUnixNano {
		return
	}
	count, exists := report.Routes[route]
	if (!exists && len(report.Routes) >= routeMaxLabels) || count >= routeMaxLabeledCounts {
		report.Complete = false
		return
	}
	report.Routes[route] = count + 1
}
