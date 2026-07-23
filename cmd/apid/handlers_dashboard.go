// dashboard handlers (spec §14 M7.5, ADR-011).
//
// Slice 4 ships the full dashboard surface: account summary, apps
// list, app detail, usage, billing, account (keys + plan). All pages
// are server-rendered (pkg/dashboard.Render) and live behind
// sessionAuth so the single-public-listener invariant (spec §11)
// survives — gatewayd reverse-proxies /dashboard/* to apid's loopback
// listener.
//
// Each handler reads data via the v1 endpoints or directly via
// Store.Handlers stay <50 lines (spec §Conventions); anything bigger
// extracts into a helper or its own file.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardAccountPath is the route served by renderAccount below.
// Mirrors cliAuthDashboard in handlers_cli_auth.go but lives in this
// file because the two packages can share neither code nor constants
// across the cmd/apid boundary without churn, and goconst (see
// .golangci.yml) flags a third occurrence of the literal across
// non-test files.
const dashboardAccountPath = "/dashboard/account"

// dashboardHandler is a tiny per-path router for /dashboard/*. Each
// page is one method — keeping the HTTP layer thin so we don't grow
// a global switch statement that drifts from the route table.
//
// Path conventions:
//
//	GET /dashboard/                  → index
//	GET /dashboard/apps              → apps list
//	GET /dashboard/apps/{slug}       → app detail
//	GET /dashboard/usage             → usage meter
//	GET /dashboard/billing           → plan + portal placeholder
//	GET /dashboard/account           → account + keys + GitHub connect
//
// The sessionAuth middleware (server.go) runs first; the account is
// already on context when these fire.
func (s *server) dashboardHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := AccountFrom(r.Context())
		if !ok {
			// Shouldn't happen — sessionAuth would have redirected —
			// but be defensive.
			http.Redirect(w, r, loginPath, http.StatusFound)
			return
		}
		path := r.URL.Path
		switch {
		case path == "/dashboard/" || path == "/dashboard":
			s.renderIndex(w, r, log, acct)
		case path == "/dashboard/apps":
			s.renderAppsList(w, r, log, acct)
		case len(path) > len("/dashboard/apps/") && path[:len("/dashboard/apps/")] == "/dashboard/apps/":
			slug := path[len("/dashboard/apps/"):]
			s.renderAppDetail(w, r, log, acct, slug)
		case path == "/dashboard/usage":
			s.renderUsage(w, r, log, acct)
		case path == "/dashboard/billing":
			s.renderBilling(w, r, log, acct)
		case path == dashboardAccountPath:
			s.renderAccount(w, r, log, acct)
		default:
			http.NotFound(w, r)
		}
	}
}

// renderIndex renders /dashboard/ — account summary card + a "what's
// here" stub. Slice 4's index leans on the index.html template.
func (s *server) renderIndex(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderIndex: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	av := dashboardAccountView(view, appCount)
	page := dashboard.Page{Title: "Overview", Body: "index", Account: av, Data: dashboard.IndexData{
		DeployedAppCount: av.AppCount,
		Plan:             string(acct.Plan),
	}}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// appListItem is the single source of truth for "an app rendered as
// a dashboard row" (PR #48 follow-up). Both renderAppsList and
// renderAppDetail call it instead of duplicating the badge lookup.
//
// State rules (ux_spec §6.3):
//   - no row in `latest` (fresh deploy, never woken) → ◌ sleeping
//   - first row's state.State → BadgeFor
//
// `latest` is the batched map from ListLatestInstancePerApp; passing
// it in (rather than doing the lookup here) keeps the helper a pure
// builder so the per-render N+1 fix lives entirely in the callers.
func (s *server) appListItem(ctx context.Context, app state.App, latest map[string]state.Instance, lastDeployed time.Time) dashboard.AppListItem {
	cls, glyph, label := dashboard.BadgeForDefault()
	if ins, ok := latest[app.ID]; ok {
		cls, glyph, label = dashboard.BadgeFor(state.State(ins.State))
	}
	var lastStr string
	if !lastDeployed.IsZero() {
		lastStr = lastDeployed.UTC().Format("2006-01-02 15:04 MST")
	}
	return dashboard.AppListItem{
		Slug:            app.Slug,
		Status:          string(app.Status),
		URL:             "https://" + app.Slug + ".apps." + s.domain,
		LastDeployed:    lastStr,
		StateBadge:      cls,
		StateBadgeGlyph: glyph,
		StateBadgeLabel: label,
	}
}

// renderAppsList renders /dashboard/apps — every deployed app + a
// "create new" link.
func (s *server) renderAppsList(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	// ux_spec §6.3: one batched instance lookup instead of N
	// per-app ListInstancesForApp calls (PR #48 follow-up). With
	// 25 apps × 6/min meta-refresh, this drops the dashboard
	// query count from 300/min to ~12/min for an account.
	latest, err := s.store.ListLatestInstancePerApp(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppsList: latest instance per app", "account_id", acct.ID, "err", err)
		latest = nil
	}
	view, _ := AccountFrom(ctx)
	items := make([]dashboard.AppListItem, 0, len(apps))
	for _, a := range apps {
		var last time.Time
		if d, err := s.store.LatestDeployment(ctx, a.ID); err == nil {
			last = d.CreatedAt
		}
		items = append(items, s.appListItem(ctx, a, latest, last))
	}
	// Reuse the already-fetched apps list for the count (review
	// finding #5: avoid a second SQL round-trip when we already
	// have the data).
	page := dashboard.Page{Title: "Apps", Body: "apps_list", Account: dashboardAccountView(view, len(apps)), Data: items}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderAppDetail renders /dashboard/apps/{slug} — the app's plan
// settings, recent deployments (with rollback forms), and the
// deployment list view. Deployments tab is the primary one slice 4
// ships; logs tab is a placeholder until slice 5 lands.
func (s *server) renderAppDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rows, err := s.store.ListDeploymentsForApp(ctx, app.ID, 25, 0)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	deps := make([]dashboard.DeploymentItem, 0, len(rows))
	for _, d := range rows {
		deps = append(deps, dashboard.DeploymentItem{
			ID:        d.ID,
			Status:    string(d.Status),
			Kind:      string(d.Kind),
			CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
			Error:     d.Error,
		})
	}
	crons, err := s.store.ListCronsForApp(ctx, app.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	cronItems := make([]dashboard.CronItem, 0, len(crons))
	for _, c := range crons {
		item := dashboard.CronItem{
			ID: c.ID, Schedule: c.Schedule, Path: c.Path, Enabled: c.Enabled,
		}
		if !c.LastFiredAt.IsZero() {
			item.LastFiredAt = c.LastFiredAt.UTC().Format(time.RFC3339)
		}
		cronItems = append(cronItems, item)
	}
	// Single-app detail page reuses the batched instance map; for
	// a one-app render that's one extra row fetched but it keeps
	// the helper signatures symmetric with renderAppsList (PR #48
	// follow-up).
	latest, err := s.store.ListLatestInstancePerApp(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: latest instance per app", "account_id", acct.ID, "err", err)
		latest = nil
	}
	// Recent wakes for this app: capped at 10 newest rows so an
	// operator can paste the wake_id from a gateway response header
	// (x-faas-wake-id) and find which scheduled wake produced it.
	// Failure is non-fatal — the section silently renders empty.
	recentInstances, err := s.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		recentInstances = nil
	}
	sort.Slice(recentInstances, func(i, j int) bool {
		return recentInstances[i].StartedAt.After(recentInstances[j].StartedAt)
	})
	if len(recentInstances) > 10 {
		recentInstances = recentInstances[:10]
	}
	recentItems := make([]dashboard.RecentInstanceItem, 0, len(recentInstances))
	for _, ins := range recentInstances {
		item := dashboard.RecentInstanceItem{
			ID:     ins.ID,
			WakeID: ins.WakeID,
			State:  ins.State,
		}
		if !ins.StartedAt.IsZero() {
			item.StartedAt = ins.StartedAt.UTC().Format(time.RFC3339)
		}
		if !ins.LastRequestAt.IsZero() {
			item.LastRequestAt = ins.LastRequestAt.UTC().Format(time.RFC3339)
		}
		recentItems = append(recentItems, item)
	}
	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{Title: app.Slug, Body: "app_detail", Account: dashboardAccountView(view, appCount), Data: dashboard.AppDetailData{
		App:             s.appListItem(ctx, app, latest, time.Time{}),
		Manifest:        dashboardManifestView(app),
		Deployments:     deps,
		Crons:           cronItems,
		RecentInstances: recentItems,
	}}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderUsage renders /dashboard/usage — the GB-hours bar for the
// current month plus the roll-up numbers.
func (s *server) renderUsage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	month := time.Now().UTC()
	rows, err := s.store.UsageByMonth(r.Context(), acct.ID, month)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	var mbSec int64
	for _, u := range rows {
		mbSec += u.MBSeconds
	}
	used := float64(mbSec) / 3_600_000.0
	limits := api.MustLimitsFor(acct.Plan)
	included := int64(limits.IncludedGBHours)
	pct := 0.0
	if included > 0 {
		pct = used / float64(included) * 100
	}
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderUsage: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{Title: "Usage", Body: "usage", Account: dashboardAccountView(view, appCount), Data: dashboard.UsageData{
		Month:           month.Format("2006-01"),
		UsedGBHours:     used,
		IncludedGBHours: included,
		OverageGBHours:  max(0, used-float64(included)),
		UsedPct:         pct,
	}}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderBilling renders /dashboard/billing — the plan card + a
// placeholder for the Stripe customer portal (post-M7.5 add-on).
func (s *server) renderBilling(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderBilling: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{Title: "Billing", Body: "billing", Account: dashboardAccountView(view, appCount), Data: dashboard.BillingData{
		Plan:     string(acct.Plan),
		RAMMB:    limits.RAMMB,
		Included: int64(limits.IncludedGBHours),
		AppsCap:  limits.DeployedApps,
		AppLayer: limits.AppLayerMaxMB,
		IdleSec:  limits.IdleTimeoutS,
	}}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderAccount renders /dashboard/account — API keys (list + create
// + delete) and the plan-change form. The GitHub connect button
// arrives in slice 8 once githubd's bindings endpoint exists.
func (s *server) renderAccount(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	keys, err := s.store.ListAPIKeys(r.Context(), acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	keyItems := make([]dashboard.APIKeyItem, 0, len(keys))
	for _, k := range keys {
		item := dashboard.APIKeyItem{
			ID:        k.ID,
			Prefix:    api.APIKeyPrefix + hexPrefix(k.Hash),
			Label:     k.Label,
			CreatedAt: k.CreatedAt.UTC().Format("2006-01-02"),
		}
		if !k.LastUsedAt.IsZero() {
			item.LastUsedAt = k.LastUsedAt.UTC().Format("2006-01-02 15:04 MST")
		}
		keyItems = append(keyItems, item)
	}
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderAccount: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	data := dashboard.AccountData{
		Keys:        keyItems,
		ShowDelete:  view.Status != state.AccountDeletedPending,
		ShowRestore: view.Status == state.AccountDeletedPending,
	}
	// CSRF (review finding A3): mint sealed envelopes bound to
	// (action, account_id) and set the matching faas_csrf sidecar
	// cookie. The renderer always issues both the delete and the
	// restore tokens because the page conditionally shows one of the
	// forms — the unused cookie is harmless (10 min TTL) and avoids
	// the "user scrolled down, the form unrendered, the token went
	// stale" footgun.
	deleteTok, err := middleware.IssueForAuthenticated(s.sessions, "delete", view.ID)
	if err != nil {
		log.Error("dashboard renderAccount: csrf issue delete", "err", err, "account_id", view.ID)
		renderProblem(w, log, err)
		return
	}
	restoreTok, err := middleware.IssueForAuthenticated(s.sessions, "restore", view.ID)
	if err != nil {
		log.Error("dashboard renderAccount: csrf issue restore", "err", err, "account_id", view.ID)
		renderProblem(w, log, err)
		return
	}
	csrfCookie := &http.Cookie{
		Name:     middleware.CookieNameAuthenticated,
		Value:    deleteTok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	}
	http.SetCookie(w, csrfCookie)
	data.DeleteConfirmToken = deleteTok
	data.RestoreConfirmToken = restoreTok
	if view.DeletionRequestedAt != nil {
		restoreUntil := view.DeletionRequestedAt.Add(state.DeletionGraceDuration()).
			UTC().Format(time.RFC3339)
		data.RestoreUntil = restoreUntil
	}
	// Banner for ?deleted=1 / ?restored=1 (set after a dashboard form
	// POST redirects back here).
	switch r.URL.Query().Get("deleted") {
	case "1":
		data.FlashSurface = "Account scheduled for deletion in 30 days. Use the form below to restore before the deadline."
	}
	switch r.URL.Query().Get("restored") {
	case "1":
		data.FlashSurface = "Account restored. Welcome back."
	}
	page := dashboard.Page{Title: "Account", Body: "account", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderProblem turns a dashboard-render error into a 500 RFC 7807.
func renderProblem(w http.ResponseWriter, log *slog.Logger, err error) {
	log.Error("dashboard render", "err", err)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"type":"about:blank","title":"render","status":500,"detail":"dashboard render failed"}`))
}

// dashboardAccountView adapts state.Account into the dashboard's
// AccountView so we keep dashboard.go free of state-package imports.
// appCount is supplied by the caller (review finding #5: the previous
// implementation hardcoded 0, so the dashboard always rendered "0
// apps"). When appCount < 0 the caller has no count available
// (the page already had to query the apps list separately, so we
// reuse that count instead of issuing a second SQL round-trip).
func dashboardAccountView(acct state.Account, appCount int) *dashboard.AccountView {
	n := appCount
	if n < 0 {
		n = 0
	}
	return &dashboard.AccountView{
		ID:       acct.ID,
		Email:    acct.Email,
		Plan:     string(acct.Plan),
		AppCount: n,
	}
}

// dashboardManifestView adapts state.AppManifest into the page-friendly
// shape. Encoded as JSON-ish entrypoint for the template's sake.
func dashboardManifestView(a state.App) dashboard.ManifestView {
	return dashboard.ManifestView{
		Entrypoint: a.Manifest.Entrypoint,
		Env:        a.Manifest.Env,
		WorkingDir: a.Manifest.WorkingDir,
		Port:       a.Manifest.Port,
		Healthz:    a.Manifest.Healthz,
		User:       a.Manifest.User,
	}
}

// hexPrefix renders the first 12 hex chars of a SHA-256 hash for the
// API key display ("fp_live_abc123…"). Matches the legacy prefix
// (keyPrefixFromHash) so the UI doesn't drift.
func hexPrefix(hash []byte) string {
	if len(hash) < 6 {
		return "000000000000"
	}
	return strconv.FormatUint(uint64(hash[0])<<40|uint64(hash[1])<<32|uint64(hash[2])<<24|uint64(hash[3])<<16|uint64(hash[4])<<8|uint64(hash[5]), 16)
}

// max returns the larger of two non-negative floats. Inline because
// math.Max forces a non-int return type we don't use anywhere else.
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
