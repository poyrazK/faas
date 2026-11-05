package api

// RouteDiscoverableStatus reports whether a response proves that its route
// exists in the app. A client error does not: scanner probes the app answers
// 404, and requests an edge rule refuses (401, 403, 429) before they reach the
// app, carry a route too, so `inspect` listed blocked probes among an app's
// discovered API paths (Hunt 12 H12-17). Success, redirect and server-error
// answers come from a route the app has.
func RouteDiscoverableStatus(status int) bool {
	return status < 400 || status >= 500
}

// RouteDiscoverableCodeRegex is RouteDiscoverableStatus as a PromQL matcher
// for the gateway's three-digit `code` label.
const RouteDiscoverableCodeRegex = `[1235]..`
