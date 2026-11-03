package gateway

import (
	"context"
	"net/http"
)

// Route labels go to routes the app serves (ADR-20261026143210). A request
// whose route has no label yet carries it as pending; the response decides:
// the route is admitted when the app itself answered with a status that
// shows the route exists. Paths the app does not serve (internet scanners'
// 404s), malformed requests and gateway-authored refusals never use one of
// the app's bounded labels. Declared routes (OpenAPI, gregale.yaml) are
// admitted on sight.

// routeLabelMethods are the methods a route label may carry; any other method
// is folded into __route_other__ so made-up methods cannot fill the set.
var routeLabelMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {}, http.MethodPut: {},
	http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
}

// routeLabelMethodAllowed reports whether method may carry a route label.
func routeLabelMethodAllowed(method string) bool {
	_, ok := routeLabelMethods[method]
	return ok
}

// statusEstablishesRoute reports whether an app-authored status shows the
// requested route exists: any 2xx, 3xx or 5xx except 501, or a 4xx other
// than the ones that say the request named nothing the app serves or could
// not be read (400, 401, 403, 404, 405, 410, 414, 431).
func statusEstablishesRoute(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusGone, http.StatusRequestURITooLong,
		http.StatusRequestHeaderFieldsTooLarge, http.StatusNotImplemented:
		return false
	}
	return status >= 200 && status < 600
}

// lookup returns route's label when it is already admitted.
func (s *routeLabelSet) lookup(route string) (string, bool) {
	switch route {
	case reservedRouteLabelEmpty, otherRouteLabel:
		return route, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.admitted[route]
	if ok {
		// A label in use is not idle: keep it out of reach of replacement.
		entry.lastSeen = s.now().UnixNano()
		s.admitted[route] = entry
	}
	return route, ok
}

// labelBeforeResponse returns route's label when it is already admitted or
// declared, and "" while the response has yet to decide.
func (s *routeLabelSet) labelBeforeResponse(route string, declared bool) string {
	if label, ok := s.lookup(route); ok {
		return label
	}
	if declared {
		return s.admitDeclared(route)
	}
	return ""
}

// pendingRouteLabel is a request's route whose label waits for the response.
// metrics records whether the label also names per-route series.
type pendingRouteLabel struct {
	route   string
	set     *routeLabelSet
	metrics bool
}

type pendingRouteLabelKey struct{}

func withPendingRouteLabel(r *http.Request, route string, set *routeLabelSet, metrics bool) *http.Request {
	if r == nil || set == nil || route == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), pendingRouteLabelKey{}, pendingRouteLabel{route: route, set: set, metrics: metrics}))
}

// resolvePendingRouteLabel admits a pending route when the app's response
// established it and returns the request's label (the route, or
// __route_other__) and whether that label names per-route series. The label
// is "" when the request carried no pending route.
func resolvePendingRouteLabel(r *http.Request, status int) (string, bool) {
	if r == nil {
		return "", false
	}
	pending, ok := r.Context().Value(pendingRouteLabelKey{}).(pendingRouteLabel)
	if !ok {
		return "", false
	}
	if label, ok := pending.set.lookup(pending.route); ok {
		return label, pending.metrics
	}
	if _, guestResponded := FirstByteFrom(r); guestResponded && statusEstablishesRoute(status) {
		return pending.set.admit(pending.route), pending.metrics
	}
	return otherRouteLabel, pending.metrics
}
