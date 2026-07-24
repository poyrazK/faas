package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgRouter adapts state.Store to gateway.Router: it resolves a request hostname
// to its routing app. gatewayd only ever READS these tables — apid owns apps and
// domains, schedd owns instances (CLAUDE.md §Component ownership).
type pgRouter struct {
	store state.Store
	// appsSuffix is the ".apps.DOMAIN" suffix (leading dot). A host under it is a
	// platform subdomain whose label is the app slug; anything else is a custom
	// domain resolved through the domains table.
	appsSuffix string
}

var _ gateway.Router = pgRouter{}

// ResolveHost implements gateway.Router. A missing/unverified/deleted route is a
// clean ok=false (404); only an actual store failure returns a non-nil error.
func (r pgRouter) ResolveHost(ctx context.Context, host string) (gateway.App, bool, error) {
	if slug, ok := r.slugFor(host); ok {
		app, err := r.store.AppBySlug(ctx, slug)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.App{}, false, nil
		}
		if err != nil {
			return gateway.App{}, false, err
		}
		return r.toApp(ctx, app)
	}

	// Custom domain: must exist AND be verified before we route to it (spec §7).
	dom, err := r.store.DomainByName(ctx, host)
	if errors.Is(err, state.ErrNotFound) {
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	if !dom.Verified() {
		return gateway.App{}, false, nil
	}
	app, err := r.store.AppByID(ctx, dom.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	return r.toApp(ctx, app)
}

// slugFor returns the app slug for a platform-subdomain host, or ok=false when
// the host is a custom domain (or the suffix is unconfigured). It rejects
// multi-label prefixes (only "slug.apps.DOMAIN" routes, not "x.slug.apps.…").
func (r pgRouter) slugFor(host string) (string, bool) {
	if r.appsSuffix == "" {
		return "", false
	}
	label, ok := strings.CutSuffix(host, r.appsSuffix)
	if !ok || label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

// toApp joins the app to its account's plan (the plan lives on the account, not
// the app) and filters out deleted apps.
func (r pgRouter) toApp(ctx context.Context, app state.App) (gateway.App, bool, error) {
	if app.Status == state.AppDeleted {
		return gateway.App{}, false, nil
	}
	acct, err := r.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return gateway.App{}, false, err
	}
	return gateway.App{ID: app.ID, Plan: acct.Plan}, true, nil
}

// appsSuffix normalizes a bare apps domain ("apps.example.com") into the
// leading-dot suffix form pgRouter/gateway compare against (".apps.example.com").
// Empty in → empty out (custom-domain-only routing).
func appsSuffix(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return ""
	}
	if domain[0] != '.' {
		domain = "." + domain
	}
	return domain
}

// invalidator is the slice of gateway.PGBackend the notify loop drives. Declared
// here so the loop is testable with a fake.
//
// Issue #168 widened the eviction surface: every instance_changed
// notification carries the instance_id schedd owns (pkg/sched/engine.go's
// emitInstanceChanged), and the gateway uses that to drop exactly one
// entry from the per-app targetSet. EvictTarget (legacy wholesale drop) is
// kept on the interface as a fallback when the payload is malformed.
type invalidator interface {
	EvictInstance(appID, instanceID string)
	FlushRoutes()
}

// watchInvalidations subscribes to the pg_notify channels that affect routing
// and keeps the backend's caches coherent (spec §4.1). It runs until ctx is
// cancelled; a subscription error is logged and the daemon keeps serving from
// cache (a brief staleness window is preferable to crashing the edge).
//
// F-11: switched from db.Subscribe to SubscribeWithReconnect. The old code
// exited cleanly the moment the LISTEN conn died, leaving the edge live but
// with stale caches forever. The reconnect wrapper keeps the subscribe alive
// across pg restarts. The single log-and-return on initial-acquire failure
// remains — boot-time DB outage is a different signal.
func watchInvalidations(ctx context.Context, pool *pgxpool.Pool, inv invalidator, log *slog.Logger) {
	channels := []string{db.NotifyInstanceChanged, db.NotifyAppChanged, db.NotifyDomainChanged}
	notif, err := db.SubscribeWithReconnect(ctx, pool, channels, log)
	if err != nil {
		log.Error("gatewayd: subscribe invalidations", "err", err)
		return
	}
	// Reconnect wrapper owns its own cancel via the deferred goroutine.
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-notif:
			if !ok {
				// Defensive — wrapper keeps open until ctx cancels.
				return
			}
			handleInvalidation(inv, n, log)
		}
	}
}

// handleInvalidation applies a single notification to the caches. instance
// changes evict one entry from that app's targetSet (issue #168);
// app/domain changes flush the route caches wholesale (one-box scale,
// spec §4.3).
//
// Issue #168: the pg_notify payload now also carries instance_id (the
// schedd-engine's emitInstanceChanged emits it next to app_id). The
// listener uses that to drop exactly one entry from the per-app cache,
// leaving any siblings routable.
//
// Cache-self-destruct guard (issue #168): the wake flow emits TWO
// notifications per successful wake — WAKING/COLD_BOOTING right after
// CreateInstance (engine.go:375) and RUNNING after vmmd boot succeeds
// (engine.go:574 → 1277). PGBackend.Admit runs the gRPC RPC between
// these two emissions and adds the Target to the cache; without the
// state filter below, the RUNNING notification would evict the
// Target we just added, defeating the cache and creating a thundering
// herd under sustained load. Only evict on terminal-ish states where
// the instance has actually left the routable set.
//
// A malformed payload that omits either app_id or instance_id is
// logged-and-dropped — better to over-evict (next request re-admits)
// than to crash the edge loop.
func handleInvalidation(inv invalidator, n db.Notification, log *slog.Logger) {
	switch n.Channel {
	case db.NotifyInstanceChanged:
		var p struct {
			AppID      string `json:"app_id"`
			InstanceID string `json:"instance_id"`
			State      string `json:"state"`
			WakeID     string `json:"wake_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil || p.AppID == "" || p.InstanceID == "" {
			log.Warn("gatewayd: bad instance_changed payload", "payload", n.Payload)
			return
		}
		// Lifecycle states (waking/cold_booting/running) leave the cache
		// alone — the entry is either pending admit (the WAKING emission
		// arrives BEFORE the gateway's RPC returns, no Target yet) or
		// already healthy (the RUNNING emission arrives AFTER
		// PGBackend.Admit has seeded the Target). Terminal-ish states
		// evict the entry so the next request re-admits.
		switch p.State {
		case "stopped", "failed", "parked", "snapshotting":
			inv.EvictInstance(p.AppID, p.InstanceID)
		}
	case db.NotifyAppChanged, db.NotifyDomainChanged:
		inv.FlushRoutes()
	}
}
