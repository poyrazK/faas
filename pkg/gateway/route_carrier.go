package gateway

import (
	"context"
	"net/http"
	"sync"
)

type observedRouteCarrierKey struct{}

// ObservedRouteCarrier receives the request's bounded route label (method plus
// declared or identifier-inferred path) from the gateway handler. A wrapper
// outside the handler cannot see context values the handler adds, so it plants
// a carrier before calling the handler. The app errors recorder fingerprints
// on it: it used to read a context key nothing set and fell back to the raw
// URL, so every distinct path became its own issue (production-us hunt #9).
type ObservedRouteCarrier struct {
	mu    sync.Mutex
	route string
}

// WithObservedRouteCarrier returns r carrying a fresh carrier.
func WithObservedRouteCarrier(r *http.Request) (*http.Request, *ObservedRouteCarrier) {
	carrier := &ObservedRouteCarrier{}
	return r.WithContext(context.WithValue(r.Context(), observedRouteCarrierKey{}, carrier)), carrier
}

// Route returns the label the handler recorded, or "" when the request never
// reached app routing.
func (c *ObservedRouteCarrier) Route() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.route
}

func (c *ObservedRouteCarrier) set(route string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.route = route
	c.mu.Unlock()
}

// ObservedRouteCarrierFrom returns the carrier planted on ctx, or nil.
func ObservedRouteCarrierFrom(ctx context.Context) *ObservedRouteCarrier {
	carrier, _ := ctx.Value(observedRouteCarrierKey{}).(*ObservedRouteCarrier)
	return carrier
}

// ObservedRouteLabel is the bounded label for a request the handler never
// labelled: identifiers in the path are inferred, and an overlong or unsafe
// path collapses to the overflow label.
func ObservedRouteLabel(method, path string) string {
	return observedRouteLabel(method, inferredObservedPath(path))
}
