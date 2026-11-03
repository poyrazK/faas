package gateway

import (
	"context"
	"net/http"
)

// publicRequestPath is the path the client sent: ServeHTTP records it before
// any rewrite rule changes r.URL.Path. A request that never reached that
// point (unit tests calling a gate directly) uses r.URL.Path.
func publicRequestPath(r *http.Request) string {
	if route, ok := r.Context().Value(lifecycleRequestRouteKey{}).(lifecycleRequestRoute); ok && route.path != "" {
		return route.path
	}
	return r.URL.Path
}

// matchEdgeGate picks an access gate's rule (ip, geo, jwt, throttle, waf,
// challenge) for the path the client sent and, when a rewrite rule changed
// the path and nothing matched there, for the rewritten path (ADR-091
// amendment 2026-11-02, review round 8). Rewrite runs before these gates, and
// they used to match only the rewritten path: a jwt rule on /api/** stopped
// applying once a rewrite took /api away. Declared routes and the request
// deadline already use the client's path; the gates now do too, and a gate
// written for the rewritten path keeps applying.
func matchEdgeGate[T any](r *http.Request, match func(ctx context.Context, path string) *T) *T {
	ctx := r.Context()
	public := publicRequestPath(r)
	if rule := match(ctx, public); rule != nil {
		return rule
	}
	if r.URL.Path != public {
		return match(ctx, r.URL.Path)
	}
	return nil
}
