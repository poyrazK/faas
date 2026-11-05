// Per-gateway bound on the per-route Prometheus series (ADR-20261024154324).
//
// ADR-093 bounds one app to 50 route labels, but every Hobby+ app now opts
// in by default, so the series a gateway exported grew with every app it had
// served since its last restart. routeSeriesBudget bounds them twice:
//
//   - at most maxRoutes (app, route) pairs carry live series on this
//     gateway; a pair beyond it is not exported and counts as rejected;
//   - an app's series are deleted once it has gone idleAfter without a
//     request here, which frees its pairs. Its next request re-creates
//     them; the counters restart, which rate() and increase() absorb.
//
// The routeLabelSet still admits and keeps each app's route labels, so a
// re-created series carries the same label, and request telemetry is not
// affected. Series are deleted by exact label values, never by partial
// match: a partial match holds the vector's write lock while it scans every
// series and would stall the hot path.
package gateway

import (
	"sync"
	"sync/atomic"
	"time"
)

// routeSeriesClasses are the histogram classes PreInstantiateAppRoute
// creates for every admitted route; eviction deletes them with the rest.
var routeSeriesClasses = []string{"2xx", "3xx", "4xx", "5xx"}

type routeSeriesBudget struct {
	maxRoutes atomic.Int64
	idleAfter atomic.Int64 // nanoseconds
	routes    atomic.Int64 // live (app, route) pairs
	apps      sync.Map     // appID → *routeSeriesApp
}

// routeSeriesApp is one app's live series on this gateway. mu guards
// evicted and routes; lastSeen is read without it so the sweeper can skip
// busy apps cheaply.
type routeSeriesApp struct {
	lastSeen atomic.Int64 // unix nanoseconds of the latest emission
	mu       sync.Mutex
	evicted  bool
	routes   map[string]map[routeSeriesKey]struct{}
}

// routeSeriesKey is one (plan, code, class) combination a route emitted,
// enough to delete each of its counter and histogram series exactly.
type routeSeriesKey struct{ plan, code, class string }

func newRouteSeriesBudget(maxRoutes int, idleAfter time.Duration) *routeSeriesBudget {
	b := &routeSeriesBudget{}
	b.configure(maxRoutes, idleAfter)
	return b
}

// configure sets the bound; non-positive values keep the current ones.
func (b *routeSeriesBudget) configure(maxRoutes int, idleAfter time.Duration) {
	if maxRoutes > 0 {
		b.maxRoutes.Store(int64(maxRoutes))
	}
	if idleAfter > 0 {
		b.idleAfter.Store(int64(idleAfter))
	}
}

// admit reports whether route's series may be emitted for appID and whether
// the route is new to this app's live set (the caller pre-instantiates it).
func (b *routeSeriesBudget) admit(appID string, key routeSeriesKey, route string, now time.Time) (ok, fresh bool) {
	for {
		e := b.app(appID)
		// Publish lastSeen before taking mu: a sweeper that already holds mu
		// re-reads it after marking the app evicted, so either it sees this
		// emission and keeps the app or this call sees evicted and retries.
		e.lastSeen.Store(now.UnixNano())
		e.mu.Lock()
		if e.evicted {
			e.mu.Unlock()
			b.apps.CompareAndDelete(appID, e)
			continue
		}
		keys, known := e.routes[route]
		if !known {
			if !b.reserve() {
				e.mu.Unlock()
				return false, false
			}
			keys = make(map[routeSeriesKey]struct{}, 2)
			e.routes[route] = keys
		}
		keys[key] = struct{}{}
		e.mu.Unlock()
		return true, !known
	}
}

func (b *routeSeriesBudget) app(appID string) *routeSeriesApp {
	if v, ok := b.apps.Load(appID); ok {
		return v.(*routeSeriesApp)
	}
	v, _ := b.apps.LoadOrStore(appID, &routeSeriesApp{routes: make(map[string]map[routeSeriesKey]struct{})})
	return v.(*routeSeriesApp)
}

// reserve takes one pair from the budget, or reports that none is left.
func (b *routeSeriesBudget) reserve() bool {
	for {
		n := b.routes.Load()
		if n >= b.maxRoutes.Load() {
			return false
		}
		if b.routes.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// sweep drops every app idle since now-idleAfter. drop runs under the app's
// lock, so a concurrent emission for it waits and then starts a fresh entry
// whose series are created after the deletion. It returns the apps dropped.
func (b *routeSeriesBudget) sweep(now time.Time, drop func(appID string, routes map[string]map[routeSeriesKey]struct{})) int {
	cutoff := now.Add(-time.Duration(b.idleAfter.Load())).UnixNano()
	dropped := 0
	b.apps.Range(func(k, v any) bool {
		e := v.(*routeSeriesApp)
		if e.lastSeen.Load() >= cutoff {
			return true
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.evicted = true
		if e.lastSeen.Load() >= cutoff {
			e.evicted = false
			return true
		}
		drop(k.(string), e.routes)
		b.routes.Add(-int64(len(e.routes)))
		e.routes = nil
		b.apps.CompareAndDelete(k, e)
		dropped++
		return true
	})
	return dropped
}

// dropRoute deletes one route's series for appID, freeing its pair, when the
// app's route label set replaced it (ADR-20261026143210 amendment 1). drop
// runs under the app's lock like sweep. It reports whether the route had
// live series.
func (b *routeSeriesBudget) dropRoute(appID, route string, drop func(appID string, routes map[string]map[routeSeriesKey]struct{})) bool {
	v, ok := b.apps.Load(appID)
	if !ok {
		return false
	}
	e := v.(*routeSeriesApp)
	e.mu.Lock()
	defer e.mu.Unlock()
	keys, known := e.routes[route]
	if e.evicted || !known {
		return false
	}
	drop(appID, map[string]map[routeSeriesKey]struct{}{route: keys})
	delete(e.routes, route)
	b.routes.Add(-1)
	return true
}

// live reports the (app, route) pairs with series on this gateway.
func (b *routeSeriesBudget) live() int64 { return b.routes.Load() }

// limit reports the configured maximum number of live pairs.
func (b *routeSeriesBudget) limit() int64 { return b.maxRoutes.Load() }
