package guestprofiling

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

type countedRouteRequestKey struct{}

var countingRouteRequests atomic.Bool

var routeRequests struct {
	sync.Mutex
	active *profileproto.RouteRequestReport
}

// WithRouteRequest labels one request and counts its entry while the Go CPU
// collector is active. Use WithRoute for execution that is not a request.
// Nested wrappers share the request context to avoid counting the same entry
// twice. Instrument the final matched route once, not multiple router layers.
func WithRouteRequest(ctx context.Context, route string, work func(context.Context)) {
	if route != "" && route != api.ProfileUnattributedRoute && api.ValidProfileRoute(route) {
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
	routeRequests.active = &profileproto.RouteRequestReport{FromUnixNano: time.Now().UnixNano(), Complete: true, Routes: map[string]int64{}}
	countingRouteRequests.Store(true)
}

func stopRouteRequestWindow() *profileproto.RouteRequestReport {
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
	if (!exists && len(report.Routes) >= api.ProfileRouteMaxLabels) || count >= api.ProfileRouteMaxLabeledRequests {
		report.Complete = false
		return
	}
	report.Routes[route] = count + 1
}
