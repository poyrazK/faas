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
type invalidator interface {
	EvictTarget(appID string)
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
// changes evict just that app's target; app/domain changes flush the route
// caches wholesale (one-box scale, spec §4.3).
func handleInvalidation(inv invalidator, n db.Notification, log *slog.Logger) {
	switch n.Channel {
	case db.NotifyInstanceChanged:
		var p struct {
			AppID string `json:"app_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil || p.AppID == "" {
			log.Warn("gatewayd: bad instance_changed payload", "payload", n.Payload)
			return
		}
		inv.EvictTarget(p.AppID)
	case db.NotifyAppChanged, db.NotifyDomainChanged:
		inv.FlushRoutes()
	}
}
