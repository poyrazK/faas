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
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

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
		case path == "/dashboard/account":
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

// renderAppsList renders /dashboard/apps — every deployed app + a
// "create new" link.
func (s *server) renderAppsList(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	view, _ := AccountFrom(r.Context())
	items := make([]dashboard.AppListItem, 0, len(apps))
	for _, a := range apps {
		var last time.Time
		if d, err := s.store.LatestDeployment(r.Context(), a.ID); err == nil {
			last = d.CreatedAt
		}
		items = append(items, dashboard.AppListItem{
			Slug:         a.Slug,
			Status:       string(a.Status),
			URL:          "https://" + a.Slug + ".apps." + s.domain,
			LastDeployed: last.UTC().Format("2006-01-02 15:04 MST"),
		})
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
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rows, err := s.store.ListDeploymentsForApp(r.Context(), app.ID, 25, 0)
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
	crons, err := s.store.ListCronsForApp(r.Context(), app.ID)
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
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{Title: app.Slug, Body: "app_detail", Account: dashboardAccountView(view, appCount), Data: dashboard.AppDetailData{
		App: dashboard.AppListItem{
			Slug:   app.Slug,
			Status: string(app.Status),
			URL:    "https://" + app.Slug + ".apps." + s.domain,
		},
		Manifest:    dashboardManifestView(app),
		Deployments: deps,
		Crons:       cronItems,
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
	page := dashboard.Page{Title: "Account", Body: "account", Account: dashboardAccountView(view, appCount), Data: dashboard.AccountData{
		Keys: keyItems,
	}}
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
