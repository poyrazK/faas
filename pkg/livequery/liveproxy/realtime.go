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

// EndpointRouter routes a seal's key through a realtime endpoint's current
// live routes.
func EndpointRouter(routes func() []realtime.LiveRoute) Router {
	return func(_ context.Context, kid string) (Route, error) {
		for _, r := range routes() {
			if r.KID == kid {
				return Route{Database: r.Database, URL: r.URL}, nil
			}
		}
		return Route{}, ErrNoRoute
	}
}
