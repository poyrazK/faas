package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Router is the Postgres-backed routing seam PGBackend reads through. It is the
// narrow slice of the state.Store the edge needs to resolve a hostname to its
// app; cmd/gatewayd adapts state.Store to it. Keeping it gateway-local (rather
// than importing pkg/state here) keeps the hot request path unit-testable with
// a fake and keeps this package's dependency surface to pkg/api only.
type Router interface {
	// ResolveHost maps a request hostname (lowercased, port-stripped) to its
	// routing app. ok=false means "no app is routed here" (a 404, not an error);
	// a non-nil error means the lookup itself failed (Postgres down) and the
	// caller should surface it as a 404 without caching.
	ResolveHost(ctx context.Context, host string) (app App, ok bool, err error)
}

// targetSet (issue #168) is the per-app list of routable instances the
// gateway holds. Members are unique by InstanceID; Pick uses an atomic
// round-robin cursor so the hot path is allocation-free even with multiple
// instances in the set. The set is mutated under PGBackend.tgtMu.
//
// Concurrency model:
//   - `next` is the atomic round-robin cursor; it monotonically increments
//     on every Pick. Modulo the current length yields the picked slot.
//   - `entries` is read-only inside Pick (RLock); mutation happens under
//     Lock (addTarget / EvictInstance).
type targetSet struct {
	next    atomic.Uint64
	entries []Target
}

// add appends a new Target to the set, replacing any existing entry with
// the same InstanceID. Callers must hold tgtMu (Lock).
func (s *targetSet) add(t Target) {
	if t.NodeID == "" || t.InstanceID == "" {
		return
	}
	for i, e := range s.entries {
		if e.InstanceID == t.InstanceID {
			// Re-admission of a known instance — overwrite in place.
			s.entries[i] = t
			return
		}
	}
	s.entries = append(s.entries, t)
}

// remove drops the entry whose InstanceID matches. Returns the new slice
// length. Callers must hold tgtMu (Lock).
func (s *targetSet) remove(instanceID string) int {
	for i, e := range s.entries {
		if e.InstanceID == instanceID {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return len(s.entries)
		}
	}
	return len(s.entries)
}

// pick returns one Target via atomic round-robin. Callers must hold tgtMu
// (RLock). Empty set → ok=false.
func (s *targetSet) pick() (Target, bool) {
	if len(s.entries) == 0 {
		return Target{}, false
	}
	idx := s.next.Add(1) - 1
	return s.entries[int(idx%uint64(len(s.entries)))], true
}

// PGBackend is gatewayd's production Backend (spec §4.1, issue #168): a
// host→app routing cache over Postgres plus schedd over gRPC for
// per-instance admission. Replaces the M4-era unwiredBackend once schedd's
// gRPC surface (ADR-018) is up.
//
// Two caches, populated on different paths:
//
//   - routes/apps: host→app_id (RouteCache, spec §4.1 10k LRU) and app_id→App
//     (plan). Filled on a Lookup miss via Router; wholesale-reset on an
//     app/domain change (Reset / FlushRoutes).
//   - targets: app_id → *targetSet. Filled by Admit (issue #168) when
//     schedd returns a fresh instance, and mutated by EvictInstance when
//     an instance_changed notification says a specific instance parked.
//     Pick is the ctx-less hot path, so it must be a pure in-memory read —
//     the notify loop + the admit path keep it fresh rather than per-request
//     DB hits.
type PGBackend struct {
	router Router
	sched  Scheduler
	log    *slog.Logger

	routes *RouteCache // host -> app_id (LRU)

	appsMu sync.RWMutex
	apps   map[string]App // app_id -> App (plan)

	tgtMu sync.RWMutex
	// targets is the hot-path app_id → *targetSet cache. Each targetSet
	// holds 1..max_concurrency Targets, picked round-robin on every
	// request (issue #168).
	targets map[string]*targetSet
}

// compile-time assertion PGBackend satisfies the edge seam.
var _ Backend = (*PGBackend)(nil)

// NewPGBackend wires the production backend. log may be nil (slog default).
func NewPGBackend(router Router, sched Scheduler, log *slog.Logger) *PGBackend {
	if log == nil {
		log = slog.Default()
	}
	return &PGBackend{
		router:  router,
		sched:   sched,
		log:     log,
		routes:  NewRouteCache(RouteCacheCap),
		apps:    map[string]App{},
		targets: map[string]*targetSet{},
	}
}

// RouteCacheCap is the host→app_id cache ceiling (spec §4.1: 10,000 routes).
const RouteCacheCap = 10_000

// Lookup resolves a hostname to its app, cache-first (spec §4.1). A cache miss
// is one indexed Postgres lookup through the Router; the result is memoized in
// both the route (host→app_id) and app (app_id→plan) caches. A Router error or
// an unknown host both yield ok=false so the handler writes a 404.
func (b *PGBackend) Lookup(ctx context.Context, host string) (App, bool) {
	if appID, ok := b.routes.Get(host); ok {
		if app, ok := b.getApp(appID); ok {
			return app, true
		}
	}
	app, ok, err := b.router.ResolveHost(ctx, host)
	if err != nil {
		b.log.Warn("gateway: route lookup failed", "host", host, "err", err)
		return App{}, false
	}
	if !ok {
		return App{}, false
	}
	b.routes.Put(host, app.ID)
	b.putApp(app)
	return app, true
}

// Pick returns one routable Target for appID via atomic round-robin
// (issue #168). Returns ("", false) when the cache is empty (no wake has
// populated it yet, or every cached instance was evicted). The handler
// must ensure capacity before calling Pick so this only returns false on
// the rare eviction race.
func (b *PGBackend) Pick(appID string) (Target, bool) {
	b.tgtMu.RLock()
	set := b.targets[appID]
	if set == nil {
		b.tgtMu.RUnlock()
		return Target{}, false
	}
	t, ok := set.pick()
	b.tgtMu.RUnlock()
	return t, ok
}

// HealthyCount returns the number of routable Targets currently cached for
// appID (issue #168). Drives the WakeGate's shouldWake predicate: stop
// admitting once we're at the plan's effective max_concurrency.
func (b *PGBackend) HealthyCount(appID string) int {
	b.tgtMu.RLock()
	set := b.targets[appID]
	if set == nil {
		b.tgtMu.RUnlock()
		return 0
	}
	n := len(set.entries)
	b.tgtMu.RUnlock()
	return n
}

// Admit asks schedd to admit ONE additional instance for appID (issue #168).
// On the admitted path the new Target is added to the per-app targetSet
// (dedup by InstanceID). On the at-capacity path the engine's typed result
// is passed through (wakeID empty, err nil). On a real failure (RAM
// headroom, chooser, store) the error is preserved — schedd lifts them to
// *api.Problem at the wire boundary.
//
// Fan-out invariant (issue #168): HealthyCount < maxConcurrency is enforced
// atomically inside this method. Concurrent callers serialize on tgtMu so a
// burst of N requests cannot collectively exceed the cap. Schedd also
// enforces the cap via its per-app ledger, but that round-trip is expensive
// — the gateway-side check is the cheap fast path that keeps the RPC count
// ≤ maxConcurrency per burst.
func (b *PGBackend) Admit(ctx context.Context, appID string, maxConcurrency int) (string, bool, error) {
	// Cheap fast path: refuse before we spend a gRPC round-trip.
	b.tgtMu.Lock()
	set := b.targets[appID]
	if set != nil && len(set.entries) >= maxConcurrency {
		b.tgtMu.Unlock()
		return "", true, nil
	}
	b.tgtMu.Unlock()

	instanceID, nodeID, wakeID, atCapacity, err := b.sched.AdmitInstance(ctx, appID)
	if err != nil {
		return "", false, err
	}
	if atCapacity {
		return "", true, nil
	}
	if nodeID == "" || instanceID == "" {
		// schedd returned a successful admit with empty ids. This is
		// an internal-server-error class event — the wire contract
		// says instance_id/node_id are populated on the admitted
		// path. Lift to *api.Problem so writeWakeError surfaces a
		// descriptive 5xx instead of the generic "wake failed" 503.
		return "", false, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "schedd admit returned empty ids",
			fmt.Sprintf("instance=%q node=%q wake=%q", instanceID, nodeID, wakeID))
	}
	b.tgtMu.Lock()
	set = b.targets[appID]
	if set == nil {
		set = &targetSet{}
		b.targets[appID] = set
	}
	set.add(Target{
		NodeID:     nodeID,
		InstanceID: instanceID,
		WakeID:     wakeID,
		AddedAt:    time.Now(),
	})
	b.tgtMu.Unlock()
	return wakeID, false, nil
}

// EvictInstance drops a specific instance from its app's targetSet (issue
// #168). The instance_changed notification loop calls this with the
// instance_id from the pg_notify payload; only that single entry is
// removed, leaving any other instances in the set routable.
func (b *PGBackend) EvictInstance(appID, instanceID string) {
	if appID == "" || instanceID == "" {
		return
	}
	b.tgtMu.Lock()
	set := b.targets[appID]
	if set == nil {
		b.tgtMu.Unlock()
		return
	}
	if set.remove(instanceID) == 0 {
		delete(b.targets, appID)
	}
	b.tgtMu.Unlock()
}

// EvictTarget drops ALL cached targets for appID (legacy contract). Kept
// for callers that don't yet parse the instance_id from the
// instance_changed payload — it under-evicts nothing because the next
// request will Pick from what's left and miss if everything's gone,
// then re-admit. New code should prefer EvictInstance.
func (b *PGBackend) EvictTarget(appID string) {
	b.tgtMu.Lock()
	delete(b.targets, appID)
	b.tgtMu.Unlock()
}

// FlushRoutes clears the host→app and app→plan caches. gatewayd calls this on
// an app_changed / domain_changed notification so a renamed slug, plan change,
// or deleted app is re-resolved (or 404'd) on the next request.
func (b *PGBackend) FlushRoutes() {
	b.routes.Reset()
	b.appsMu.Lock()
	b.apps = map[string]App{}
	b.appsMu.Unlock()
}

func (b *PGBackend) getApp(appID string) (App, bool) {
	b.appsMu.RLock()
	app, ok := b.apps[appID]
	b.appsMu.RUnlock()
	return app, ok
}

func (b *PGBackend) putApp(app App) {
	b.appsMu.Lock()
	b.apps[app.ID] = app
	b.appsMu.Unlock()
}
