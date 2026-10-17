package liveproxy

import (
	"context"

	"github.com/onebox-faas/faas/pkg/realtime"
)

// ForRealtime gives the proxy the realtime package's interface. Each
// connection reaches only the databases its endpoint routes to, which apid
// lists from the databases bound to the endpoint's app.
func ForRealtime(p *Proxy) realtime.LiveQueries { return realtimeAdapter{p} }

type realtimeAdapter struct{ proxy *Proxy }

func (a realtimeAdapter) Attach(routes func() []realtime.LiveRoute, send func([]byte) error) realtime.LiveSession {
	return a.proxy.Attach(EndpointRouter(routes), send)
}

// EndpointRouter routes through a realtime endpoint's current live routes: a
// seal by its key, an RLS-mode read by its database. Either way only the
// databases bound to the endpoint's app are reachable.
func EndpointRouter(routes func() []realtime.LiveRoute) Router {
	return func(_ context.Context, key RouteKey) (Route, error) {
		for _, r := range routes() {
			if key.KID != "" && r.KID == key.KID || key.KID == "" && key.Database != "" && r.Database == key.Database {
				return Route{Database: r.Database, URL: r.URL}, nil
			}
		}
		return Route{}, ErrNoRoute
	}
}
