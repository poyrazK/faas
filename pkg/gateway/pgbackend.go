package gateway

import (
	"context"
	"log/slog"
	"sync"
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

// PGBackend is gatewayd's production Backend (spec §4.1): a host→app routing
// cache over Postgres plus schedd over gRPC for wakes. It replaces the M4-era
// unwiredBackend once schedd's gRPC surface (ADR-018) is up.
//
// Two caches, populated on different paths:
//
//   - routes/apps: host→app_id (RouteCache, spec §4.1 10k LRU) and app_id→App
//     (plan). Filled on a Lookup miss via Router; wholesale-reset on an
//     app/domain change (Reset / FlushRoutes).
//   - targets: app_id→instance addr. Filled ONLY by a successful Wake (which
//     carries the addr schedd just brought up) and evicted on any
//     instance_changed notification. Target() is the ctx-less hot path, so it
//     must be a pure in-memory read — hence a cache the notify loop keeps fresh
//     rather than a per-request DB hit.
type PGBackend struct {
	router Router
	sched  Scheduler
	log    *slog.Logger

	routes *RouteCache // host -> app_id (LRU)

	appsMu sync.RWMutex
	apps   map[string]App // app_id -> App (plan)

	tgtMu sync.RWMutex
	// targets is the hot-path app_id -> instance addr (host:port) cache.
	targets map[string]string
	// addrInstance reverses addr -> instance_id so the last_request_at flush can
	// attribute a touch (keyed by the addr the handler proxied to) back to the
	// instance row schedd owns (spec §4.1, ADR-018).
	addrInstance map[string]string
}

// compile-time assertion PGBackend satisfies the edge seam.
var _ Backend = (*PGBackend)(nil)

// NewPGBackend wires the production backend. log may be nil (slog default).
func NewPGBackend(router Router, sched Scheduler, log *slog.Logger) *PGBackend {
	if log == nil {
		log = slog.Default()
	}
	return &PGBackend{
		router:       router,
		sched:        sched,
		log:          log,
		routes:       NewRouteCache(RouteCacheCap),
		apps:         map[string]App{},
		targets:      map[string]string{},
		addrInstance: map[string]string{},
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

// Target returns the cached instance address for appID, or ("", false) when no
// wake has populated it yet (the handler then blocks on Wake). This is the hot
// path: a pure in-memory read, no ctx, no DB.
func (b *PGBackend) Target(appID string) (string, bool) {
	b.tgtMu.RLock()
	addr, ok := b.targets[appID]
	b.tgtMu.RUnlock()
	return addr, ok && addr != ""
}

// Wake blocks while schedd admits + dispatches an instance (restore or cold
// boot) and caches the address it returns so the handler's follow-up Target
// hits without waiting for the instance_changed notification round-trip. The
// error preserves schedd's *api.Problem so writeWakeError maps it directly.
func (b *PGBackend) Wake(ctx context.Context, appID string) error {
	instanceID, addr, err := b.sched.Wake(ctx, appID)
	if err != nil {
		return err
	}
	if addr != "" {
		b.tgtMu.Lock()
		b.targets[appID] = addr
		if instanceID != "" {
			b.addrInstance[addr] = instanceID
		}
		b.tgtMu.Unlock()
	}
	return nil
}

// EvictTarget drops the cached address for appID. gatewayd calls this on every
// instance_changed notification (running or parked): a parked/destroyed
// instance must never be proxied to, and a state change means the next request
// should re-resolve via an idempotent Wake (which re-seeds the cache). At
// one-box scale the extra Wake round-trip is negligible.
func (b *PGBackend) EvictTarget(appID string) {
	b.tgtMu.Lock()
	if addr, ok := b.targets[appID]; ok {
		delete(b.addrInstance, addr)
	}
	delete(b.targets, appID)
	b.tgtMu.Unlock()
}

// InstanceIDForAddr resolves the instance an addr was last woken as, so the
// last_request_at flush can attribute touches (spec §4.1). Returns ok=false once
// the target has been evicted (the instance parked); the flush drops those.
func (b *PGBackend) InstanceIDForAddr(addr string) (string, bool) {
	b.tgtMu.RLock()
	id, ok := b.addrInstance[addr]
	b.tgtMu.RUnlock()
	return id, ok
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
