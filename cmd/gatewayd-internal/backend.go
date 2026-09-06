package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgRouter adapts state.Store to gateway.Router: it resolves a request hostname
// to its routing app. gatewayd only ever READS these tables — apid owns apps and
// domains, schedd owns instances (CLAUDE.md §Component ownership).
type pgRouter struct {
	store state.Store
	// appsSuffix is the configured public suffix in leading-dot form. A host
	// under it is a platform subdomain whose label is the app slug; anything
	// else is a custom domain resolved through the domains table.
	appsSuffix string
	// tenantSurfacesEnabled is the durable runtime gate. Tests and legacy
	// callers may leave it nil, in which case the historical environment
	// accessor remains the fallback.
	tenantSurfacesEnabled func() bool
}

var _ gateway.Router = pgRouter{}

// ResolveHost implements gateway.Router. A missing/unverified/deleted route is a
// clean ok=false (404); only an actual store failure returns a non-nil error.
func (r pgRouter) ResolveHost(ctx context.Context, host string) (gateway.App, bool, error) {
	if slug, ok := r.slugFor(host); ok {
		return r.appBySlug(ctx, slug)
	}
	// Tenant surface (issue #879 / ADR-100 PR-B). Dark behind
	// FAAS_TENANT_SURFACES_ENABLED; a non-surface host or a
	// surface miss falls through to the legacy custom_domains
	// path. resolveTenantSurface owns the parser check + the
	// routing so ResolveHost stays ≤ 50 lines.
	enabled := api.TenantSurfacesEnabled()
	if r.tenantSurfacesEnabled != nil {
		enabled = r.tenantSurfacesEnabled()
	}
	if enabled {
		app, ok, err := r.resolveTenantSurface(ctx, host)
		if err != nil {
			return gateway.App{}, false, err
		}
		if ok {
			return app, true, nil
		}
	}
	return r.customDomain(ctx, host)
}

// appBySlug — slugFor hit branch. Extracted to keep ResolveHost
// ≤ 50 lines (CLAUDE.md convention).
func (r pgRouter) appBySlug(ctx context.Context, slug string) (gateway.App, bool, error) {
	app, err := r.store.AppBySlug(ctx, slug)
	if errors.Is(err, state.ErrNotFound) {
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	return r.toApp(ctx, app)
}

// customDomain — the legacy custom_domains branch (spec §7).
// Must exist AND be verified before we route to it; a deleted
// parent app falls through to a clean 404.
func (r pgRouter) customDomain(ctx context.Context, host string) (gateway.App, bool, error) {
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

// resolveTenantSurface — pgRouter.ResolveHost's tenant-surface branch.
// Pulled out to keep ResolveHost ≤ 50 lines (CLAUDE.md convention).
// Returns (app, true, nil) on a routable, verified hostname on an
// active surface; ({}, false, nil) on a miss, suspended surface,
// unverified hostname, deleted parent app, or cross-account
// mismatch (caller falls through to legacy custom_domains); or
// (zero, false, err) on a store error.
//
// Two security gates the legacy custom_domains path already has
// and this branch must mirror:
//   - hostname.Verified() == true (pre-challenge TXT records
//     must not route; the apid verify handler flips this)
//   - surface.AccountID == app.AccountID (defends against a
//     hypothetical appID re-use race during a soft-delete window:
//     the surface is keyed to an account, the app is keyed to an
//     account, and they must agree before we route)
func (r pgRouter) resolveTenantSurface(ctx context.Context, host string) (gateway.App, bool, error) {
	surface, err := r.store.TenantSurfaceByHostname(ctx, host)
	if errors.Is(err, state.ErrNotFound) {
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	if !surface.Active() {
		// Soft-deleted / suspended surface: route-around, not 404.
		// A suspended surface is a customer-visible state change;
		// the legacy custom_domains path may still own a domain
		// row that pre-dates the surface — fall through to honour it.
		return gateway.App{}, false, nil
	}
	hostname, err := r.store.GetTenantHostnameByName(ctx, host)
	if errors.Is(err, state.ErrNotFound) {
		// Surface row exists but the hostname row was deleted
		// between the two lookups (dns_poller GC; rare). Fall
		// through to legacy; the next request re-joins cleanly.
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	if !hostname.Verified() {
		// Pre-challenge hostname must not route. Mirror the
		// dom.Verified() gate at the custom_domains branch below.
		return gateway.App{}, false, nil
	}
	app, err := r.store.AppByID(ctx, surface.AppID)
	if errors.Is(err, state.ErrNotFound) {
		// App deleted while surface still active — route-around.
		return gateway.App{}, false, nil
	}
	if err != nil {
		return gateway.App{}, false, err
	}
	if surface.AccountID != app.AccountID {
		// Defence in depth: surface→account and app→account must
		// agree. A drift here would be an invariant violation
		// (every surface is created against an app owned by the
		// same account), but we fail closed rather than route.
		return gateway.App{}, false, nil
	}
	return r.toApp(ctx, app)
}

// slugFor returns the app slug for a platform-subdomain host, or ok=false when
// the host is a custom domain (or the suffix is unconfigured). It rejects
// multi-label prefixes (only one app-slug label under the configured suffix).
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

// previewScopeFromHost (issue #272 / ADR-095 PR-B) peels a preview-hostname
// shape `pr-{N}-{parent-slug}.<zone>` into `(number, parent-slug)` so
// the routing layer can resolve it to the preview app row whose slug is
// `pr-{N}-{parent-slug}` (the convention the webhook provisioner uses per
// ADR-094). The parser is shared with pkg/gateway's on-demand cert
// allowlist so the two paths can't drift on what counts as a preview host.
func (r pgRouter) previewScopeFromHost(host string) (number int, slug string, ok bool) {
	return gateway.PreviewScopeFromHost(r.appsSuffix, host)
}

// toApp joins the app to its account's plan (the plan lives on the account, not
// the app) and filters out deleted apps. AccountID is plumbed through to the
// gateway.App so the per-account rate limiter (ADR-040 / issue #292) can key
// the throttle on app.AccountID — production joins always populate it.
// StreamingEnabled (issue #471 / ADR-047) is plumbed through so ServeHTTP
// can decide between the buffered and streamed response path without
// re-reading the apps row from Postgres on every request.
// RequireAuthn (issue #560) is plumbed through so the routing layer can
// enforce per-deployment token gating at the edge — a Pro/Scale customer
// who PATCHes require_authn=true on their app gets the auth check
// applied to every incoming request, with no store hop on the hot path.
// PublicAuth (issue #477 / ADR-079) is plumbed through so ServeHTTP can
// enforce per-app public-URL auth (open|bearer|basic) at the edge — a
// Hobby+ customer who PATCHes public_auth_mode='bearer' or 'basic' on
// their app gets the credential check applied to every incoming request.
// The secretbox-sealed BasicSealed blob is carried on gateway.App so
// the gatewayd basic-auth path can unseal it at boot (and cache the
// unsealed form for 60s + db.NotifyKeyChanged invalidation).
func (r pgRouter) toApp(ctx context.Context, app state.App) (gateway.App, bool, error) {
	if app.Status == state.AppDeleted {
		return gateway.App{}, false, nil
	}
	acct, err := r.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return gateway.App{}, false, err
	}
	return gateway.App{
		ID:               app.ID,
		AccountID:        acct.ID,
		Plan:             acct.Plan,
		MaxConcurrency:   app.MaxConcurrency,
		Slug:             app.Slug,
		StreamingEnabled: app.StreamingEnabled,
		// Issue #676 / ADR-080: per-app raw-bytes Upgrade
		// bridge flag. Plumbed from apps.websocket_enabled
		// through pgRouter.toApp so Handler.ServeHTTP's
		// three-input gate (isUpgradeRequest &&
		// app.WebSocketEnabled && h.rawByNode != nil) can
		// route inbound Connection: Upgrade requests to the
		// raw forwarder. Default false on the App struct
		// matches the apps.websocket_enabled column DEFAULT.
		WebSocketEnabled: app.WebSocketEnabled,
		// ADR-093: per-route observability opt-in. Plumbed
		// from apps.route_metrics_enabled through pgRouter.toApp
		// so Handler.ServeHTTP's routeSetFor (gated on
		// app.RouteMetricsEnabled && h.routeMetricsEnabled) can
		// lazily create the per-app routeLabelSet. Default false
		// on the App struct matches the apps.route_metrics_enabled
		// column DEFAULT (migration 00212).
		RouteMetricsEnabled: app.RouteMetricsEnabled,
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag. Plumbed from apps.maintenance_mode so
		// Handler.ServeHTTP's applyAppsMaintenanceMode can
		// short-circuit WITHOUT re-reading the database. Default
		// false on the App struct matches the apps.maintenance_mode
		// column DEFAULT (migration 00237).
		MaintenanceMode: app.MaintenanceMode,
		RequireAuthn:    app.RequireAuthn,
		// ADR-124: per-app wire-protocol selector (closed-set
		// {http1, http2, grpc}, default 'http1'). Plumbed from
		// apps.app_protocol through pgRouter.toApp so
		// Handler.ServeHTTP's decideProtocol can stamp
		// x-faas-protocol on the request at the site
		// x-faas-stream is stamped today, WITHOUT re-reading
		// the apps row. Empty on legacy never-PATCHed rows
		// is treated as "http1" by decideProtocol, preserving
		// pre-ADR-124 behaviour. Apid enforces the write-time
		// plan gate (CodePlanAppProtocolGrpcNotAllowed) so this
		// side is read-only; sqlc Scan coerces NULL→"" (the
		// apps_app_protocol_chk constraint + NOT NULL DEFAULT
		// guarantee the column is never actually NULL).
		AppProtocol: app.AppProtocol,
		// CORS improvements D1: per-app default CORS
		// plumbed through pgRouter.toApp from the apps
		// row. CORSDefaultEnabled is *bool (tri-state:
		// nil = legacy never-PATCHed, *false = explicit
		// opt-out, *true = opt-in). The pgstore Scan
		// target is always a non-nil pointer on hydrated
		// rows, so the gateway sees false on legacy
		// apps (the opt-in check uses `*a.CORSDefaultEnabled`)
		// and never consults the origins list — making
		// the legacy wake path bit-for-bit unchanged.
		CORSDefaultEnabled: app.CORSDefaultEnabled,
		CORSDefaultOrigins: app.CORSDefaultOrigins,
		PublicAuth: gateway.PublicAuthConfig{
			Mode:        app.PublicAuthMode,
			BasicSealed: app.PublicAuthBasicSealed,
			// ADR-118: hydrate the per-app ingress IP
			// allowlist. The slice is consulted by
			// applyIngressIPAllowlist ONLY when Mode ==
			// publicAuthModeIPAllowlist; for other modes
			// the slice is nil and ignored. Live updates
			// ride on the existing NotifyAppChanged
			// channel — no extra RPC.
			IPAllowlist: app.PublicAuthIPAllowlist,
		},
	}, true, nil
}

// appsSuffix normalizes a bare apps domain ("gregale.dev") into the
// leading-dot suffix form pgRouter/gateway compare against (".gregale.dev").
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
//
// Issue #477 / ADR-079 added InvalidatePublicAuth: the per-app basic-auth
// unsealed-credential cache in gateway.PublicAuthCache. A key_changed
// notification triggers InvalidatePublicAuth; the cache maps
// (appID, sealed-hash) → entry without a per-entry key-tag, so a key
// rotation drops the whole cache. Operators who rotate a key on a busy
// account pay at most one cache rebuild across all public-auth-locked
// apps (acceptable because key rotations are rare).
//
// Issue #561 / ADR-089 PR 3 added ResetEdgeRules: a
// `edge_rule_changed` notification drops the whole edge-rule
// LRU (the cache is per-host; per-rule invalidation would
// require the matcher to know the rule's host key, which the
// notification doesn't carry). The cache is advisory — a stale
// read only widens the hit window, never wrong-routes — so a
// wholesale flush is correct.
//
// Issue #556 / PR-B added RefreshDeploymentWeights: a
// deployment_changed notification reloads the per-deployment weight
// table for the affected app so a `faas traffic set --percent 25`
// takes effect within ~1s without restarting the edge.
type invalidator interface {
	EvictInstance(appID, instanceID string)
	FlushRoutes()
	InvalidatePublicAuth()
	RefreshDeploymentWeights(ctx context.Context, appID string) error
	// RefreshMirrorRules (issue #72 / ADR-125 PR-A3) reloads
	// the per-app mirror rules cache from Postgres on a
	// kind="mirror" notify. Mirrors RefreshDeploymentWeights'
	// shape — the dispatch goroutine reads the cache on the
	// hot path; Refresh is the only writer.
	RefreshMirrorRules(ctx context.Context, appID string) error
	// ResetEdgeRules (ADR-089 PR 3) drops the per-host
	// edge-rule LRU. Wholesale — per-rule invalidation would
	// require the notification payload to carry the host
	// key, which it doesn't. The cache is advisory so a
	// brief staleness window is fine.
	ResetEdgeRules()
	// ResetApp (ADR-091 amendment / §4.1.2.0) drops a single app
	// from the apps LRU when its apps.maintenance_mode (or any
	// other customer-visible column) flips. The companion trigger
	// apps_maintenance_mode_notify (migrations/00225) fires
	// pg_notify('app_changed', NEW.id::text) ONLY when
	// maintenance_mode IS DISTINCT FROM old, so this method is
	// low-volume — it's not on the hot request path. Per-app
	// drop (not wholesale FlushRoutes) because maintenance_mode
	// flips are usually isolated to one app; wholesale FlushRoutes
	// would also evict every other app's entry on every flip.
	ResetApp(appID string)
	// InvalidateResponseCacheByApp (ADR-122 §Decision) drops every
	// kind=cache entry for an app on a per-app column flip (most
	// importantly a deploy — the previous release's body must
	// never serve under the new release's URL).
	InvalidateResponseCacheByApp(appID string)
	// InvalidateResponseCacheAll (ADR-122 §Decision) drops every
	// kind=cache entry on a kind=cache rule mutation. Wholesale —
	// per-rule invalidation would require the rule's match_host
	// in the notify payload, which it doesn't carry.
	InvalidateResponseCacheAll()
	// InvalidateResponseCacheByPath drops only the requested path subset
	// for one app. Malformed globs are logged and ignored by the consumer.
	InvalidateResponseCacheByPath(appID, pathGlob string) error
	// RequestCertForSurface (ADR-100 / issue #879) is the
	// cert-remint goroutine's entry point. A
	// tenant_surface_changed notification (any insert / update /
	// delete on tenant_surfaces OR tenant_hostnames) re-assembles
	// the SAN set and asks the issuer for a fresh cert. The
	// payload is the bare surface uuid; the consumer re-reads
	// the surface + hostnames (defence against notify loss).
	// Errors are logged-and-swallowed: a failed mint re-tries on
	// the next mutation; we never block the notify path on a
	// transient CA failure.
	RequestCertForSurface(ctx context.Context, surfaceID string) error
	// ResetCorsPresets (issue #975 #4 PR-B / ADR-129 D4) drops
	// the per-host edge-rule LRU for the affected account so
	// the next request recompiles and re-fetches the up-to-date
	// preset via state.GetCorsPresetByID. Wholesale reset is
	// correct: the per-rule compile path re-reads the preset
	// on every cache miss, so the post-reset compile produces
	// resolved actions against the latest row. The account_id
	// payload is informational — the cmd-side pg_notify channel
	// is too narrow to do per-account surgical eviction without
	// restructuring the LRU key to include account_id. For the
	// multi-tenant case the worst-case is "another account's
	// request recompiles" — same compile cost as the first
	// request to a host, no incorrect routing.
	ResetCorsPresets(accountID string)
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
	// Issue #477 / ADR-079: append NotifyKeyChanged so a key
	// rotation triggers InvalidatePublicAuth on the
	// basic-auth unsealed-credential cache. The cache maps
	// (appID, sealed-hash) → entry without a per-entry key-tag,
	// so a key rotation drops the whole cache (see
	// PublicAuthCache.InvalidateAll).
	//
	// Issue #556 / PR-B: append NotifyDeploymentChanged so a
	// `faas traffic set --percent N` triggers
	// RefreshDeploymentWeights on the per-app picker.
	channels := []string{
		db.NotifyInstanceChanged,
		db.NotifyAppChanged,
		db.NotifyDomainChanged,
		db.NotifyKeyChanged,
		db.NotifyDeploymentChanged,
		db.NotifyEdgeRuleChanged,
		db.NotifyCachePurge,
		db.NotifyTenantSurfaceChanged,
		db.NotifyCorsPresetChanged,
	}
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
			handleInvalidation(ctx, inv, n, log)
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
func handleInvalidation(ctx context.Context, inv invalidator, n db.Notification, log *slog.Logger) {
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
		//
		// Tier A5 / ADR-066: state='migrating' is also terminal-ish
		// for routing purposes. Phase 1 of the cross-node handoff
		// has Park'd the VM on the source vmmd; the new owner is
		// mid-restore on the destination. Routing traffic to the
		// source node mid-handoff would land on a VM that's about
		// to be destroyed. The gateway-side evicts this entry so
		// the next request re-admits (which goes through the wake
		// path on the destination — see pkg/sched/migration_handoff.go
		// Phase 4's MigrateInstanceOwner which stamps node_id
		// before the gateway's RUNNING emission from the wake
		// path lands).
		switch p.State {
		case "running":
			// RUNNING may have been produced by another gateway process,
			// by a schedd-side wake producer, or by the service desired-count
			// reconciler. The latter can add a replica while this picker
			// already has a healthy sibling, so the normal empty-cache
			// reconciliation is insufficient: merge the authoritative set
			// even when the local cache is non-empty. Keep the older
			// ReconcileLiveTargets fallback for invalidators that predate the
			// explicit refresh seam.
			if refresher, ok := inv.(interface {
				RefreshLiveTargets(context.Context, string) error
			}); ok {
				if err := refresher.RefreshLiveTargets(ctx, p.AppID); err != nil {
					log.Warn("gatewayd: refresh running instance", "app_id", p.AppID, "instance_id", p.InstanceID, "err", err)
				}
			} else if reconciler, ok := inv.(interface {
				ReconcileLiveTargets(context.Context, string) error
			}); ok {
				if err := reconciler.ReconcileLiveTargets(ctx, p.AppID); err != nil {
					log.Warn("gatewayd: reconcile running instance", "app_id", p.AppID, "instance_id", p.InstanceID, "err", err)
				}
			}
		case "stopped", "failed", "parked", "snapshotting", "migrating":
			inv.EvictInstance(p.AppID, p.InstanceID)
		}
	case db.NotifyAppChanged:
		// ADR-091 amendment / §4.1.2.0: a per-app column flip
		// (e.g. apps.maintenance_mode) fires apps_maintenance_mode_notify
		// which emits 'app_changed' with NEW.id as the payload.
		// Drop ONLY that app from the apps LRU — not the route
		// cache — because the next Backend.Lookup will re-read
		// the apps row and pick up the new column value. This
		// arm is also the load-bearing destination of any future
		// per-app column triggers (e.g. apps.streaming_enabled
		// flips, apps.public_auth_mode rotations). Until those
		// land, the only fired trigger is apps_maintenance_mode_notify.
		// Wholesale FlushRoutes() used to be the only behaviour;
		// we keep that for NotifyDomainChanged because a custom
		// domain's host→app mapping change affects the route
		// resolver wholesale, not per-app.
		if n.Payload != "" {
			inv.ResetApp(n.Payload)
			// ADR-122 §Decision: drop kind=cache entries for
			// the affected app. A deploy or a per-app column
			// flip must not let the previous release's body
			// serve under the new release's URL — the cache
			// key's DeploymentID is empty in v1, so the only
			// way to fence deploys is a per-app drop. Per-app
			// (not wholesale) so an isolated app flip doesn't
			// evict every other app's entries.
			inv.InvalidateResponseCacheByApp(n.Payload)
		} else {
			// Defensive: a missing payload on the existing
			// channel (e.g. a row delete or a future trigger
			// without a NEW.id payload) falls back to wholesale
			// FlushRoutes — same posture as the legacy arm.
			inv.FlushRoutes()
			// Wholesale cache drop on a malformed payload —
			// safer than guessing the affected app.
			inv.InvalidateResponseCacheAll()
		}
	case db.NotifyDomainChanged:
		inv.FlushRoutes()
	case db.NotifyKeyChanged:
		// Issue #477 / ADR-079. A key rotation across the
		// platform could change which api_keys resolve
		// against which apps for both the require_authn
		// (issue #560) and public_auth bearer (issue #477)
		// gates. The auth chain itself reads through Postgres
		// on every request, so the auth-side effect of a
		// key rotation is bounded by the auth chain's own
		// cache (pkg/auth's connection-pool TTL). The
		// public-auth cache (basic-auth unsealed creds,
		// 60s TTL) needs an explicit invalidation because
		// it doesn't read through Postgres on the hot
		// path. The payload (key_id) is intentionally
		// dropped: the cache maps (appID, sealed-hash) →
		// entry without a per-entry key-tag, so a key
		// rotation drops the whole cache. Operators who
		// rotate a key on a busy account pay at most one
		// cache rebuild across all public-auth-locked
		// apps (acceptable because key rotations are
		// rare; the next request re-unseals cleanly).
		inv.InvalidatePublicAuth()
	case db.NotifyDeploymentChanged:
		// Issue #556 / PR-B. A `faas traffic set --percent N`
		// updates deployments.traffic_percent under FOR UPDATE
		// and emits this notification. The gateway-side
		// appPicker holds an in-memory weight table per app;
		// RefreshDeploymentWeights reloads it from Postgres
		// so the next Pick reflects the new ratio within ~1s.
		// Empty / malformed payloads are logged-and-dropped —
		// the picker retains its current weights until the
		// next valid event (better to over-stale than to
		// crash the edge loop). A non-nil refresh error is
		// logged-and-continued: a brief staleness window is
		// preferable to crashing the notify path.
		//
		// Issue #72 / ADR-125 PR-A3: the same channel carries
		// mirror-rule notifications via a `kind="mirror"`
		// discriminant (PR-A2 emits this from
		// cmd/apid/handlers_mirrors.go). Both flows write the
		// SAME channel — NotifyDeploymentChanged — but consume
		// DIFFERENT discriminators. Branch on `kind` here; an
		// empty `kind` (legacy / traffic-split) hits the
		// upstream weight refresh.
		var p struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
			Kind         string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil || p.AppID == "" {
			log.Warn("gatewayd: bad deployment_changed payload", "payload", n.Payload)
			return
		}
		switch p.Kind {
		case "mirror":
			// Mirror rule mutation: refresh the per-app
			// mirror rules cache so the dispatch fanout
			// sees the new rule within ~1s of the apid
			// write. RefreshMirrorRules is safe to call
			// even when the app has no rules (no-op
			// allocation beyond the cache write).
			if err := inv.RefreshMirrorRules(ctx, p.AppID); err != nil {
				log.Warn("gatewayd: refresh mirror rules failed", "app", p.AppID, "err", err)
			}
		default:
			// v1 cache lookup happens before target selection, so the
			// deployment dimension is currently empty. Fence rollout and
			// traffic changes with an app-wide cache purge.
			inv.InvalidateResponseCacheByApp(p.AppID)
			if err := inv.RefreshDeploymentWeights(ctx, p.AppID); err != nil {
				log.Warn("gatewayd: refresh deployment weights failed", "app", p.AppID, "err", err)
			}
		}
	case db.NotifyCachePurge:
		var p struct {
			AppID    string `json:"app_id"`
			PathGlob string `json:"path_glob"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil || p.AppID == "" {
			log.Warn("gatewayd: bad cache purge payload", "payload", n.Payload)
			return
		}
		if err := inv.InvalidateResponseCacheByPath(p.AppID, p.PathGlob); err != nil {
			log.Warn("gatewayd: cache purge failed", "app", p.AppID, "path", p.PathGlob, "err", err)
		}
	case db.NotifyEdgeRuleChanged:
		// Issue #561 / ADR-089 PR 3. A create / update /
		// delete on any edge_rule row emits this notification
		// (cmd/apid/handlers_edge_rules.go:256/455/511).
		// Wholesale Reset — the cache is advisory and a
		// stale read only widens the hit window (the
		// gateway.App cross-account check catches a
		// deleted target). The payload (app_id, rule_id)
		// is intentionally dropped: the cache is per-host
		// keyed, and we'd need the rule's match_host to
		// surgical-evict. Wholesale flush is cheaper and
		// correct.
		inv.ResetEdgeRules()
		// ADR-122 §Decision: drop the kind=cache store on
		// the same notification. A new rule might apply to
		// a path the store already populated under a
		// deleted rule; a deleted rule might have populated
		// entries that no rule now matches; a mutated rule
		// might have changed max_age / stale_if_error. All
		// three are covered by InvalidateAll.
		inv.InvalidateResponseCacheAll()
	case db.NotifyTenantSurfaceChanged:
		// ADR-100 / issue #879: any mutation on
		// tenant_surfaces or tenant_hostnames (insert /
		// update / delete) emits this notification with the
		// bare surface uuid as payload. The cert-remint
		// goroutine re-reads the surface + verified hostnames
		// (defence against notify loss — the trigger at
		// migrations/00243 fires on every relevant row
		// change, including the verified_at flip), then asks
		// the issuer for a fresh cert. The SAN set is
		// deterministic (sort-by-hostname) so re-mints
		// against the same verified set produce identical
		// (primary, sans) tuples.
		//
		// Errors are logged-and-swallowed: a failed mint
		// re-tries on the next mutation; we never block the
		// notify path on a transient CA failure.
		if err := inv.RequestCertForSurface(ctx, n.Payload); err != nil {
			log.Warn("gatewayd: cert remint failed", "surface", n.Payload, "err", err)
		}
	case db.NotifyCorsPresetChanged:
		// Issue #975 #4 PR-B / ADR-129 D4. The trigger at
		// migrations/00428 fires pg_notify
		// ('cors_preset_changed', NEW.account_id::text) on
		// every cors_presets INSERT / UPDATE / DELETE. The
		// compile path (cmd/gatewayd-internal/edge_rules.go
		// ::compileCORSRules) bakes the preset's
		// allow_origins / allow_methods / etc. into the
		// resolved EdgeRuleCORSResolved slice at compile
		// time, so a preset edit leaves stale resolved
		// shapes in the per-host LRU. Wholesale
		// ResetEdgeRules drops the LRU so the next request
		// recompiles and re-fetches the preset from PG.
		// The account_id payload is informational; the
		// LRU is per-host keyed, not per-account, so a
		// surgical per-account eviction would require a
		// richer key. Wholesale flush is the same posture
		// as NotifyEdgeRuleChanged and is bounded by the
		// compile cost (one SELECT per cache miss).
		//
		// Empty / missing payload is logged-and-dropped:
		// the production trigger always emits
		// NEW/OLD.account_id, so an empty payload signals a
		// bug or a future trigger revision. Better to
		// over-stale than to evict on a misrouted event.
		if n.Payload == "" {
			log.Warn("gatewayd: bad cors_preset_changed payload", "payload", n.Payload)
			return
		}
		inv.ResetCorsPresets(n.Payload)
	}
}
