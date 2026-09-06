// dashboard handlers (spec §14 M7.5, ADR-011).
//
// Slice 4 ships the full dashboard surface: account summary, apps
// list, app detail, usage, billing, account (keys + plan). All pages
// are server-rendered (pkg/dashboard.Render) and live behind
// sessionAuth so the single-public-listener invariant (spec §11)
// survives — gatewayd-public reverse-proxies /dashboard/* to apid's loopback
// listener.
//
// Each handler reads data via the v1 endpoints or directly via
// Store.Handlers stay <50 lines (spec §Conventions); anything bigger
// extracts into a helper or its own file.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/stages"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/presetwhy"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/whycopy"
	"github.com/onebox-faas/faas/pkg/wire"
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
//	GET /dashboard/billing           → plan + usage + last invoice + portal link (issue #253)
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
		case path == "/dashboard/apps/new":
			// Issue #961 / Mega-B PR-3 — thin dashboard deploy
			// wizard. The CLI's `gregale connect repo <owner>/<name>`
			// (PR-1) deep-links here with ?repo=…; the OAuth
			// callback redirects here with ?install=…&branch=….
			// ADR-116 documents the trust-root split.
			s.renderAppNew(w, r, log, acct)
		case path == "/dashboard/previews":
			// Mega-C PR-1 / issue #961 leaf 3: global previews
			// page. Every preview across the account, with a
			// "Tear down" link that POSTs to the new
			// /v1/preview/{slug}/destroy endpoint (via the
			// dashboard's CSRF envelope — wired in a follow-up
			// commit if/when the form gets JS).
			s.renderPreviewsList(w, r, log, acct)
		case len(path) > len("/dashboard/apps/") && path[:len("/dashboard/apps/")] == "/dashboard/apps/":
			slug := path[len("/dashboard/apps/"):]
			// Per-deploy drill-down (issue #464 / ADR-075):
			// /dashboard/apps/{slug}/deployments/{id} renders the
			// full grype CVE list for one deployment. Falls through
			// to renderAppDetail if the suffix doesn't match.
			if dslug, did, ok := parseDeployDetailPath(slug); ok {
				s.renderDeploymentDetail(w, r, log, acct, dslug, did)
				return
			}
			// Per-domain doctor drill-down (ADR-120 Tier A2):
			// /dashboard/apps/{slug}/domains/{domain}/doctor
			// renders the 5-check Render-style report via
			// pkg/dashboard/templates/domain_doctor.html. Falls
			// through to renderAppDetail if the suffix doesn't
			// match the {domain}/doctor shape — same posture as
			// parseDeployDetailPath above. The IDOR check is the
			// AppBySlug + AccountID rejection in
			// renderDomainDoctor.
			if dslug, domain, ok := parseDomainDoctorPath(slug); ok {
				s.renderDomainDoctor(w, r, log, acct, dslug, domain)
				return
			}
			// PR-A (ADR-123 follow-on) — per-app wake timeline
			// drill-down. /dashboard/apps/{slug}/wake-timeline
			// renders a 24h trigger-histogram + at-cap count
			// summary card and a 50-row recent-wakes table
			// mirroring the recent-wakes columns on app_detail.
			// Falls through to renderAppDetail when the suffix
			// doesn't match.
			if tslug, ok := parseWakeTimelinePath(slug); ok {
				s.renderAppWakeTimeline(w, r, log, acct, tslug)
				return
			}
			s.renderAppDetail(w, r, log, acct, slug)
		case path == "/dashboard/usage":
			s.renderUsage(w, r, log, acct)
		case path == "/dashboard/billing":
			s.renderBilling(w, r, log, acct)
		case path == "/dashboard/upgrade":
			// Hosted-checkout confirmation page (dashboard_upgrade.go).
			s.renderUpgrade(w, r, log, acct)
		case path == "/dashboard/pricing":
			s.renderPricing(w, r, log, acct)
		case path == "/dashboard/invoices":
			s.renderInvoices(w, r, log, acct)
		case path == "/dashboard/audit-events":
			// Wave 0 PR-C / ADR-047: the operator/customer surface
			// for stateless-advisory audit rows. Mirrors
			// GET /v1/audit-events?kind_prefix=stateless.advisory
			// with optional ?app_id= for the per-app drill-down.
			s.renderAuditEvents(w, r, log, acct)
		case path == "/dashboard/safe-releases":
			// SAFE-RELEASES-OBS PR-C (issue #976 / ADR-122):
			// operator's "everything in-flight" surface for the
			// canary + safedeploy lifecycle. Three sections
			// (in-flight rollouts / recent audit / active alerts);
			// bounded by safedeploy_in_flight_rollouts gauge.
			s.renderSafeReleasesDashboard(w, r, log, acct)
		case path == "/dashboard/admin":
			s.renderAdminDashboard(w, r, log, acct)
		case strings.HasPrefix(path, "/dashboard/alerts/"):
			// SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122):
			// per-alert-rule drill-down. URL shape:
			//   /dashboard/alerts/{rule_id}
			// where rule_id is a UUID. Renders (a) the rule
			// row (name, expr, severity, action), (b) every
			// deployment_audit row the rule triggered (joined
			// via deployment_audit.alert_rule_id, partial index
			// from migrations/20260905000000002), and (c) the rule's
			// recent deliveries (alert_deliveries).
			rid := strings.TrimPrefix(path, "/dashboard/alerts/")
			if rid == "" || strings.ContainsRune(rid, '/') {
				s.notFound(w, "alert rule id required")
				return
			}
			s.renderAlertRuleDetail(w, r, log, acct, rid)
		case path == "/dashboard/stateless":
			// Move 1 PR-A: customer-facing landing page for the
			// stateless contract. Renders (a) the contract in
			// human copy, (b) the 8 base denylist + 10 closed paths,
			// (c) the account's 50 most recent advisory rows.
			// Pin: always scoped to the signed-in account, never
			// cross-account. The data slice sources (pkg/dashboard's
			// StatelessDenylist + StatelessClosedPaths) are
			// documentation copy mirrored from pkg/imaged and
			// guest-init; see dashboard.go for the rationale.
			s.renderStateless(w, r, log, acct)
		case path == "/dashboard/orgs" || path == "/dashboard/orgs/":
			// PR-8 §3: org landing — every org the signed-in
			// account is a member of. Read-only; the
			// create-org/promote-personal forms land with PR-9's
			// per-seat cut-over. Both "/dashboard/orgs" and
			// "/dashboard/orgs/" serve the same page (same
			// posture as /dashboard/ handling; PR-8 review
			// found trailing-slash 404 otherwise).
			s.renderOrgsList(w, r, log, acct)
		case len(path) > len("/dashboard/orgs/") &&
			path[:len("/dashboard/orgs/")] == "/dashboard/orgs/":
			slug := path[len("/dashboard/orgs/"):]
			// Same per-page router pattern as /dashboard/apps/{slug}:
			// one level of sub-routing lives in the slug suffix so a
			// follow-up PR can add /dashboard/orgs/{slug}/billing or
			// /dashboard/orgs/{slug}/audit. Note for follow-up
			// owners: when those subpages land, mirror the
			// parseDeployDetailPath helper in the apps dispatcher
			// (cmd/apid/handlers_dashboard.go:1213) — add
			// parseOrgDetailSubpath(slug) for "/audit", "/billing",
			// etc., BEFORE passing the slug to OrgBySlug. PR-9
			// / PR-10 / PR-13 expect this seam.
			s.renderOrgDetail(w, r, log, acct, slug)
		case path == dashboardAccountPath:
			s.renderAccount(w, r, log, acct)
		case len(path) > len("/dashboard/projects/") &&
			path[:len("/dashboard/projects/")] == "/dashboard/projects/":
			// ADR-124 affected-workloads preview. The dispatcher
			// peeks the suffix to extract the slug; the preview
			// template lives behind GET (form) only — the POST
			// routes for this surface are registered directly in
			// server.go (POST /preview, POST /preview/apply) so the
			// multipart body is parsed by Go's stdlib rather than
			// by a method-switch here.
			slug := path[len("/dashboard/projects/"):]
			// Only the bare slug + "/preview" subpath is GET-routed
			// through this dispatcher; deeper paths fall through to
			// 404 so a future /dashboard/projects/{slug}/audit
			// landing gets a clean seam without colliding with
			// /dashboard/projects/{slug}/preview/apply.
			const previewSuffix = "/preview"
			if slug == "" || !strings.HasSuffix(slug, previewSuffix) {
				http.NotFound(w, r)
				return
			}
			pslug := strings.TrimSuffix(slug, previewSuffix)
			if pslug == "" || !previewSlugOK(pslug) {
				http.NotFound(w, r)
				return
			}
			s.renderProjectPreview(w, r, log, acct, pslug)
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
	developerAppCount, err := s.store.CountDeveloperApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderIndex: count developer apps", "account_id", acct.ID, "err", err)
		developerAppCount = 0
	}
	limits := api.MustLimitsFor(acct.Plan)
	av := dashboardAccountView(view, appCount)
	page := dashboard.Page{Title: "Overview", Body: "index", Account: av, Data: dashboard.IndexData{
		DeployedAppCount:   av.AppCount,
		DeveloperAppCount:  developerAppCount,
		DeveloperAppsLimit: limits.DeveloperApps,
		Plan:               string(acct.Plan),
	}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
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
	item := dashboard.AppListItem{
		Slug:            app.Slug,
		Status:          string(app.Status),
		URL:             appURLForDomain(app.Slug, s.domain),
		LastDeployed:    lastStr,
		StateBadge:      cls,
		StateBadgeGlyph: glyph,
		StateBadgeLabel: label,
		// Finding 6 (issue #314): the cell's data attributes are
		// stamped at the caller level (renderAppsList knows the
		// account-wide plan; this helper has neither). Supplied
		// empty here so a partial migration doesn't render a
		// confusing cell.
		AppID: app.ID,
		// ADR-095 PR-C / issue #272: stamp preview metadata at
		// the same edge as the production URL so the apps list
		// can render the "preview" chip without re-querying.
		// Scope is the canonical preview subdomain label
		// ("pr-{N}.{parent}"); the apps list template renders it
		// as the URL on a preview row so a customer can copy /
		// click straight to the preview host without re-deriving
		// the shape.
		IsPreview: app.PreviewOfSlug != "",
	}
	if app.PreviewOfSlug != "" && app.PreviewPrNumber > 0 {
		item.Scope = fmt.Sprintf("pr-%d-%s", app.PreviewPrNumber, app.PreviewOfSlug)
		item.URL = appURLForDomain(item.Scope, s.domain)
	}
	return item
}

// appURLForDomain joins an app or preview slug to the configured public
// suffix. The suffix is intentionally opaque: `apps.gregale.dev` remains a
// valid backwards-compatible value, while `gregale.dev` yields the current
// wildcard contract (`<slug>.gregale.dev`) without hard-coding an extra
// `.apps` label into the application.
func appURLForDomain(slug, domain string) string {
	domain = strings.Trim(strings.TrimSpace(domain), ".")
	if domain == "" {
		return "https://" + slug
	}
	return "https://" + slug + "." + domain
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
	// Finding 6 (issue #314): stamp the per-row quota-cell metadata.
	// account.Plan is the plan every app in this account sees
	// (RateLimitBurst / RateLimitRPS lives on api.LimitsFor(plan)).
	// QuotaLabel stays "—" until the apid→gatewayd-internal loopback dial
	// lands — distinguishable from the live "N/M" reading on the
	// next PR. The static cells don't currently render the plan in
	// the visible text, so this is essentially dead-handed metadata
	// for today; the data attributes carry the keys the follow-up
	// HTMX wire-in will read.
	for i := range items {
		items[i].Plan = string(acct.Plan)
		items[i].QuotaLabel = dashboardEmDash
	}
	// Reuse the already-fetched apps list for the count (review
	// finding #5: avoid a second SQL round-trip when we already
	// have the data).
	// Issue #696 / ADR-082 dashboard follow-up PR — per-row SLO
	// badge. The badge shows the worst-field value (p95 latency
	// if elevated, else error rate) and the colour reflects the
	// threshold. Cost tripwire: capped at 25 apps (matches
	// dashboardSLOAppsListBadgeCap) so a 100-app account doesn't
	// issue 100 PromQL calls per render. nil = no badge.
	badges := s.fetchDashboardSLOBadges(ctx, log, items, acct)
	attachSLOBadges(items, badges)
	page := dashboard.Page{Title: "Apps", Body: "apps_list", Account: dashboardAccountView(view, len(apps)), Data: items}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// attachSLOBadges wires the per-row SLO badge map produced
// by fetchDashboardSLOBadges into the items slice. The
// helper is extracted so the loop's pointer-aliasing
// correctness is testable directly. The defensive `b := badge`
// copy below is correct on every supported Go version, even
// if a future refactor flips `for i := range items` into
// `for i, badge := range items` with &badge on the for-clause
// binding (where Go ≤ 1.21 would alias the &badge address
// across all rows). PR #724 /code-review finding.
func attachSLOBadges(items []dashboard.AppListItem, badges map[string]views.SLOBadge) {
	for i := range items {
		if badge, ok := badges[items[i].Slug]; ok {
			b := badge
			items[i].SLO = &b
		}
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
		item := dashboard.DeploymentItem{
			ID:        d.ID,
			Status:    string(d.Status),
			Kind:      string(d.Kind),
			CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
			Error:     d.Error,
			// Issue #606 / SAFE-RELEASES-E.1: deploy list rows
			// get a compact via-only chip rendered via the
			// existing app_detail.html badge palette. The full
			// triple (user / pusher / IP) is reserved for the
			// drill-down page (deployment_detail.html).
			DeployedByUserID: d.DeployedByUserID,
			DeployedVia:      d.DeployedVia,
			DeployedFromIP:   d.DeployedFromIP,
			PusherLogin:      d.PusherLogin,
		}
		// Per-deploy grype scan summary (issue #464 / ADR-075).
		// Populate ScanSummary only when the row carries a
		// scan_status; nil means "scan pending" on the template.
		// The SeverityCounts read comes from the jsonb column's
		// typed payload — the same ScanResult the /scan route
		// returns — so the list-view chip + the detail page
		// agree on counts.
		if d.ScanStatus != "" {
			sum := &dashboard.ScanSummary{
				Status: d.ScanStatus,
			}
			if !d.ScannedAt.IsZero() {
				sum.ScannedAt = d.ScannedAt.UTC().Format(time.RFC3339)
			}
			if len(d.ScanResult) > 0 {
				var sr api.ScanResult
				if err := json.Unmarshal(d.ScanResult, &sr); err == nil {
					sum.Critical = sr.SeverityCounts.Critical
					sum.High = sr.SeverityCounts.High
					sum.Medium = sr.SeverityCounts.Medium
					sum.Low = sr.SeverityCounts.Low
					sum.Unknown = sr.SeverityCounts.Unknown
				}
			}
			item.ScanSummary = sum
		}
		deps = append(deps, item)
	}
	crons, err := s.store.ListCronsForApp(ctx, app.ID)
	if err != nil {
		renderProblem(w, log, err)
		return
	}
	// Per-row CSRF envelope (issue #791 PR-E / ADR-090 §"Sub-decision
	// 7") shared by every cron on the page. Minted once, reused —
	// the per-row <input name="csrf_token" value="{{$.Foo}}"> is the
	// form-hidden sibling that pairs with the faas_csrf cookie.
	// The bound action is page-scoped ("fire_cron_{id}") so a token
	// for cron A cannot be replayed against cron B; the same value
	// is reused across renders as long as the cookie's MaxAge holds.
	fireCSRFToken, err := middleware.IssueForAuthenticated(s.sessions, "fire_cron", acct.ID)
	if err != nil {
		log.Error("dashboard renderAppDetail: csrf issue fire_cron", "app_id", app.ID, "err", err)
		fireCSRFToken = ""
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieNameAuthenticated,
			Value:    fireCSRFToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.domain != "",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
		})
	}
	rollbackCSRFToken, err := middleware.IssueForAuthenticatedNamed(
		s.sessions, dashboardRollbackAction, acct.ID, dashboardRollbackCSRFCookie)
	if err != nil {
		log.Error("dashboard renderAppDetail: csrf issue rollback", "app_id", app.ID, "err", err)
		rollbackCSRFToken = ""
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:     dashboardRollbackCSRFCookie,
			Value:    rollbackCSRFToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.domain != "",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
		})
	}
	cronItems := make([]dashboard.CronItem, 0, len(crons))
	for _, c := range crons {
		item := dashboard.CronItem{
			ID:                  c.ID,
			Schedule:            c.Schedule,
			Path:                c.Path,
			Enabled:             c.Enabled,
			FireNowConfirmToken: fireCSRFToken,
		}
		if !c.LastFiredAt.IsZero() {
			item.LastFiredAt = c.LastFiredAt.UTC().Format(time.RFC3339)
		}
		// Last 10 runs (issue #791 PR-E / ADR-090). Failure
		// non-fatal — a transient Postgres blip on the runs read
		// shouldn't kill the dashboard. handler pre-formats so
		// the template stays a pure renderer.
		if rows, rerr := s.store.ListCronRunsForCron(ctx, c.ID, 10, ""); rerr == nil {
			proj := make([]dashboard.CronRunRow, 0, len(rows))
			for _, inv := range rows {
				proj = append(proj, projectCronRunRow(inv))
			}
			item.Runs = proj
			item.RunsCount = len(proj)
		} else {
			log.Warn("dashboard renderAppDetail: list cron runs", "account_id", acct.ID, "app_id", app.ID, "cron_id", c.ID, "err", rerr)
		}
		cronItems = append(cronItems, item)
	}
	workflowItems := s.fetchDashboardWorkflows(ctx, log, app.ID)
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
	// Bounded at the SQL layer (LIMIT 10 in ListLatestInstancesForApp)
	// so a long-lived app with hundreds of parked history rows doesn't
	// pull its full history on every dashboard render. Failure is
	// non-fatal — the section silently renders empty.
	recentInstances, err := s.store.ListLatestInstancesForApp(ctx, app.ID, 10)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list recent instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		recentInstances = nil
	}
	// ADR-123: batched lookup of the wake-boot telemetry for every
	// recent wake_id in one SQL round-trip (uses events_wake_id_idx).
	// Failure is non-fatal — pre-ADR-123 fleet or a transient Postgres
	// blip on the events table just leaves the new columns blank.
	wakeIDs := make([]string, 0, len(recentInstances))
	for _, ins := range recentInstances {
		if ins.WakeID != "" {
			wakeIDs = append(wakeIDs, ins.WakeID)
		}
	}
	bootMetas := make(map[string]state.WakeBootMeta)
	if len(wakeIDs) > 0 {
		if m, err := s.store.LookupBootStartedForWakes(ctx, wakeIDs); err == nil {
			bootMetas = m
		} else {
			log.Warn("dashboard renderAppDetail: lookup boot started", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
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
		// ADR-123 + PR-A: stamp trigger / queued_count /
		// concurrency_at_admit / at_capacity / ready_in_ms from the
		// wake.boot_started (+ boot_completed, for ready_in_ms) event
		// rows. Pre-ADR-123 rows have no event row, so the fields stay
		// zero/empty — the template renders an em-dash in that case
		// (existing convention). For at_capacity and ready_in_ms
		// specifically: pre-PR-A rows still lack the at_capacity field
		// on the boot_started jsonb; pgstore.LookupBootStartedForWakes
		// defaults to (false, 0) via COALESCE.
		if meta, ok := bootMetas[ins.WakeID]; ok {
			item.Trigger = meta.Trigger
			item.QueuedCount = meta.QueuedCount
			item.ConcurrencyAtAdmit = meta.ConcurrencyAtAdmit
			item.AtCapacity = meta.AtCapacity
			item.AtCapacityPresent = meta.AtCapacityPresent
			item.ReadyInMS = meta.ReadyInMS
		}
		recentItems = append(recentItems, item)
	}
	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	appRow := s.appListItem(ctx, app, latest, time.Time{})
	appRow.Plan = string(acct.Plan)
	appRow.QuotaLabel = dashboardEmDash
	// ADR-095 PR-C / issue #272 — preview environments panel. Fetched
	// only when the page is a production app (a preview row never has
	// its own previews) and only when the store is in a state where
	// a failure is non-fatal. PreviewAppsByParent filters tombed rows
	// so the live pane never surfaces a deleted preview.
	var previews []dashboard.PreviewItem
	if appRow.IsPreview {
		previews = nil
	} else {
		previewRows, perr := s.store.PreviewAppsByParent(ctx, acct.ID, app.Slug)
		if perr != nil {
			log.Warn("dashboard renderAppDetail: list previews",
				"account_id", acct.ID, "app_slug", app.Slug, "err", perr)
			previews = nil
		} else {
			previews = projectPreviewItems(previewRows, app.Slug, s.domain)
		}
	}
	// F1 / issue #1397: durable custom-domain TLS state. This is a
	// best-effort read like the other detail panels; a transient domains
	// query failure must not take down the app page.
	var domainItems []dashboard.DomainItem
	if domains, derr := s.store.ListDomainsForApp(ctx, app.ID); derr != nil {
		log.Warn("dashboard renderAppDetail: list domains", "account_id", acct.ID, "app_id", app.ID, "err", derr)
	} else {
		domainItems = make([]dashboard.DomainItem, 0, len(domains))
		for _, d := range domains {
			item := dashboard.DomainItem{
				Domain: d.Domain, Verified: d.Verified(), CertStatus: string(d.CertStatus),
				CertLastError: d.CertLastError,
			}
			if !d.CertExpiresAt.IsZero() {
				item.CertExpiresAt = d.CertExpiresAt.UTC().Format(time.RFC3339)
			}
			if !d.DNSLastCheckedAt.IsZero() {
				item.DNSLastCheckedAt = d.DNSLastCheckedAt.UTC().Format(time.RFC3339)
			}
			domainItems = append(domainItems, item)
		}
	}
	analyticsRoute, analyticsMethod, _ := parseRequestAnalyticsRouteFilter(r.URL.Query().Get("analytics_route"), r.URL.Query().Get("analytics_method"))
	page := dashboard.Page{Title: app.Slug, Body: "app_detail", Account: dashboardAccountView(view, appCount), Data: dashboard.AppDetailData{
		App:             appRow,
		Manifest:        dashboardManifestView(app),
		EffectiveLimits: appEffectiveLimits(app, acct.Plan),
		ConfiguredResources: api.AppConfiguredResources{
			MemoryMB: app.RAMMB, CPUMillicores: effectiveAppCPUMillicores(app, acct.Plan),
		},
		Deployments:     deps,
		Crons:           cronItems,
		Workflows:       workflowItems,
		Previews:        previews,
		Domains:         domainItems,
		RecentInstances: recentItems,
		// Issue #791 PR-E / ADR-090 closure — cron fire-now
		// post-redirect banner. Reads ?fired=1 / ?fired=error and
		// forwards through to the template's flash block.
		// Anything other than the canonical values collapses to
		// empty so a stale "?fired=" doesn't render an empty banner.
		FiredFlash:           firedFlash(r),
		RollbackConfirmToken: rollbackCSRFToken,
		RollbackFlash:        rollbackFlash(r),
		// Issue #273 / ADR-042 — best-effort metrics snapshot.
		// Failure is non-fatal: Prometheus being down renders the
		// "degraded" empty state rather than blocking the whole
		// page render. The 3s timeout matches the per-query timeout
		// in pkg/promql.
		Metrics: s.fetchDashboardMetrics(ctx, log, app.ID),
		// Issue #696 / ADR-082 dashboard follow-up PR — best-effort
		// SLO panel. Same 3s budget envelope as Metrics. Window is
		// resolved from ?window= on the URL (default 24h, "1h" / "24h"
		// / "7d" closed set; invalid → default). The stamp echoes the
		// active window so the template's window-selector tab strip
		// can mark the current tab.
		SLOApp: s.fetchDashboardSLO(ctx, log, app, acct, resolveSLOWindow(r)),
		SLODuration: views.SLOStamp{
			Window: resolveSLOWindow(r),
			AsOf:   time.Now().UTC().Format(time.RFC3339Nano),
		},
		// Customer request analytics is a best-effort durable rollup. It is
		// separate from the live Prometheus snapshot above and is omitted for
		// plans without request-telemetry retention.
		RequestAnalytics: s.fetchDashboardRequestAnalytics(ctx, log, app, acct, analyticsRoute, analyticsMethod),
		// Issue #396 / ADR-045 PR 4 — best-effort alert-rule
		// snapshot. Failure is non-fatal: a Postgres blip on the
		// alert_rules read renders the panel's warning empty-state
		// instead of killing the whole page.
		Alerts: s.fetchDashboardAlerts(ctx, log, acct, app),
		// Issue #1233 / ADR-123 — best-effort alert-preset catalog
		// snapshot for the "Alert presets" grid below the Alerts
		// panel. Same non-fatal posture: a Postgres blip on the
		// alert_presets read renders an empty grid instead of
		// killing the whole page.
		Presets: s.fetchDashboardPresets(ctx, log, acct, app),
	}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// fetchDashboardWorkflows returns a bounded, safe projection of the most
// recent workflow runs for an app. A workflow read failure is non-fatal to the
// rest of the app page, matching the dashboard's other best-effort panels.
func (s *server) fetchDashboardWorkflows(ctx context.Context, log *slog.Logger, appID string) []dashboard.WorkflowRunItem {
	runs, _, err := s.store.ListWorkflowRuns(ctx, appID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil {
		log.Warn("dashboard renderAppDetail: list workflow runs", "app_id", appID, "err", err)
		return nil
	}
	items := make([]dashboard.WorkflowRunItem, 0, len(runs))
	for _, run := range runs {
		if run == nil {
			continue
		}
		item := dashboard.WorkflowRunItem{
			ID:           run.ID,
			WorkflowName: run.WorkflowName,
			Status:       run.Status,
			CreatedAt:    dashboardWorkflowTime(run.CreatedAt),
			StartedAt:    dashboardWorkflowPtrTime(run.StartedAt),
			FinishedAt:   dashboardWorkflowPtrTime(run.FinishedAt),
			LastError:    dashboardWorkflowError(run.LastError),
		}
		if run.CurrentStep != nil {
			item.CurrentStep = *run.CurrentStep
		}
		steps, stepErr := s.store.GetWorkflowSteps(ctx, run.ID)
		if stepErr != nil {
			log.Warn("dashboard renderAppDetail: list workflow steps", "app_id", appID, "run_id", run.ID, "err", stepErr)
		} else {
			item.Steps = make([]dashboard.WorkflowStepItem, 0, len(steps))
			for _, step := range steps {
				if step == nil {
					continue
				}
				item.Steps = append(item.Steps, dashboard.WorkflowStepItem{
					Name:    step.StepName,
					Status:  step.Status,
					Attempt: step.Attempt,
					Error:   dashboardWorkflowError(step.Error),
				})
			}
		}
		items = append(items, item)
	}
	return items
}

func dashboardWorkflowTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func dashboardWorkflowPtrTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return dashboardWorkflowTime(*t)
}

func dashboardWorkflowError(err *string) string {
	if err == nil {
		return ""
	}
	return *err
}

// firedFlash translates the dashboard cron fire-now POST-redirect
// `?fired=…` query flag into the closed vocab the template
// understands. Values:
//
//	?fired=1       → "ok"     (green banner: "Fire-now enqueued…")
//	?fired=error   → "error"  (red banner: "Fire-now failed…")
//	?fired= (any other) → ""  (no banner — future variants can grow here)
//
// Empty output is the conservative default: a stale "?fired="
// rendering no banner is preferable to one rendering an empty one.
// Issue #791 PR-E / ADR-090.
func firedFlash(r *http.Request) string {
	switch r.URL.Query().Get("fired") {
	case "1":
		return "ok"
	case "error":
		return "error"
	default:
		return ""
	}
}

func rollbackFlash(r *http.Request) string {
	switch r.URL.Query().Get("rollback") {
	case "1":
		return "ok"
	case "error":
		return "error"
	default:
		return ""
	}
}

// fetchDashboardMetrics wraps the Prometheus fetch in a 3s budget
// so a slow Prometheus can't stall the dashboard render. nil return
// means "skip the section entirely" (Prometheus not configured, or
// the fetch timed out). A degraded result still returns a
// non-nil pointer so the template can render the "degraded" label
// rather than disappear silently — that's the same shape the
// public /status/slo.json uses.
// budgetCtx is the ADR-093 / PR-D helper for the apid-side per-call
// ceilings (PromQL 3s, billingOps 30s, sync-invoke 5s / 30s). When
// the inbound ctx carries a Budget (the gatewayd-public → apid
// round-trip), the per-call ceiling becomes a child of the budget:
// childDeadline = min(parentRemaining, ceiling). The absolute
// ceiling is unchanged — the budget can only tighten the cap. When
// no Budget is attached (a direct apid call from the dashboard SPA
// without going through gatewayd-public), the per-call
// context.WithTimeout ceiling is preserved so the per-handler
// safety cap cannot regress on the direct-call path.
//
// Returns the ctx + cancel. Callers must `defer cancel()` so the
// context isn't leaked.
func budgetCtx(parent context.Context, ceiling time.Duration) (context.Context, context.CancelFunc) {
	if b, ok := reqbudget.FromContext(parent); ok {
		newCtx, cancel, _ := b.WithCeiling(parent, ceiling)
		return newCtx, cancel
	}
	return context.WithTimeout(parent, ceiling)
}

func (s *server) fetchDashboardMetrics(ctx context.Context, log *slog.Logger, appID string) *dashboard.AppMetricsView {
	if s.promqlClient == nil {
		return nil
	}
	// ADR-093 / PR-D: the 3s Prometheus ceiling becomes a child of
	// the inbound budget when one is attached (gatewayd-public's
	// BudgetMiddleware stamped it on r.Context()). childDeadline
	// = min(parentRemaining, 3s). When no budget is on ctx the
	// legacy 3s WithTimeout ceiling is preserved.
	dctx, cancel := budgetCtx(ctx, 3*time.Second)
	defer cancel()
	resp, src := appmetrics.Fetch(dctx, s.promqlClient, log, appID, appmetrics.DefaultRange)
	view := &dashboard.AppMetricsView{
		Range:        appmetrics.DefaultRange,
		Source:       src,
		RequestCount: resp.RequestCount,
		LatencyP50MS: resp.LatencyP50MS,
		LatencyP95MS: resp.LatencyP95MS,
		LatencyP99MS: resp.LatencyP99MS,
		ErrorRatePct: resp.ErrorRatePct,
		ColdStartPct: resp.ColdStartPct,
		WakeP95MS:    resp.WakeP95MS,
	}
	if src != appmetrics.SourcePrometheus && log != nil {
		log.Warn("dashboard renderAppDetail: metrics fetch degraded", "app_id", appID, "source", src)
	}
	return view
}

// fetchDashboardAlerts returns the per-app alert-rule snapshot for the
// Alerts panel (issue #396 / ADR-045, PR 4). Returns a non-nil
// AlertDetailData on success; on Postgres blip it logs + returns an
// empty struct so the template renders the panel header and a single
// "alert rules unavailable" row instead of falling off the cliff.
//
// Scope rule mirrors handlers_alerts.go:106-110 — visible rules are
// rule.AppID == app.ID (per-app) OR rule.AppID == "" (account-wide).
// PR 1's plan-tier gate stops a Free-tier account from creating an
// alert rule, so today this read never sees Free-tier account-wide
// rules; the visibility filter is forward-compatible for a future
// "account-wide" loosening.
//
// The 5-row deliveries cap matches the public /v1/alert-rules/{id}/deliveries
// default (handlers_alerts.go); PR 3 set the contract, PR 4 mirrors
// it on the dashboard so the operator sees the same per-rule recent
// surface on both surfaces.
func (s *server) fetchDashboardAlerts(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) *dashboard.AlertDetailData {
	rules, err := s.store.ListAlertRulesForAccount(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list alert rules", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return &dashboard.AlertDetailData{Rules: nil}
	}
	items := make([]dashboard.AlertItem, 0, len(rules))
	now := time.Now().UTC()
	for i := range rules {
		rule := rules[i]
		if rule.AppID != "" && rule.AppID != app.ID {
			continue
		}
		item := dashboard.AlertItem{Rule: rule}
		if !rule.LastFiredAt.IsZero() {
			item.LastFiredAtLabel = dashboard.RelativeTime(rule.LastFiredAt, now)
		} else {
			item.LastFiredAtLabel = dashboardEmDash
		}
		deliveries, derr := s.store.ListAlertDeliveriesForRule(ctx, rule.ID, 5, false)
		if derr != nil {
			// Per-rule read failure is non-fatal — log once and
			// render an empty recent-deliveries row for that rule.
			log.Warn("dashboard renderAppDetail: list alert deliveries", "account_id", acct.ID, "rule_id", rule.ID, "err", derr)
			deliveries = nil
		} else {
			// Truncate LastError at the store boundary so the
			// template is a pure renderer. The helper lives in
			// pkg/dashboard and is also exercised by
			// pkg/dashboard/dashboard_test.go.
			for i := range deliveries {
				if deliveries[i].LastError != "" {
					deliveries[i].LastError = dashboard.FormatAlertError(deliveries[i].LastError)
				}
			}
		}
		item.RecentDeliveries = deliveries
		items = append(items, item)
	}
	return &dashboard.AlertDetailData{Rules: items}
}

// fetchDashboardPresets returns the per-app alert-preset catalog
// snapshot for the "Alert presets" grid (issue #1233 / ADR-123).
// On Postgres blip, logs + returns an empty slice so the template
// renders the grid header without falling off the cliff (same
// non-fatal posture as fetchDashboardAlerts above).
//
// Plan-tier gate is computed on the dashboard side via
// api.PlanMeetsMinimumPlan(acct.Plan, preset.MinimumPlan) — the
// same helper the apid enable handler uses
// (handlers_alert_presets.go:118-121). When the row's plan is
// above the customer's plan, MeetsPlan=false and the dashboard
// renders an "upgrade to <plan>" hint instead of the Enable
// button. When EnabledInCatalog=false the card renders as
// "coming soon" (greyed). When both gates pass, the form posts
// to /apps/{slug}/alert-presets/{name}/enable with the customer's
// webhook_url + webhook_secret as application/x-www-form-urlencoded
// fields (NOT JSON — see enableAlertPresetFromForm below).
func (s *server) fetchDashboardPresets(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) []dashboard.AlertPresetItem {
	rows, err := s.store.ListAlertPresets(ctx)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list alert presets", "account_id", acct.ID, "err", err)
		return nil
	}
	// Mint ONE CSRF token for the action — the verifier at
	// cmd/apid/dashboard_preset_enable.go:72 seals
	// (action="enable_alert_preset", acct.ID) regardless of which
	// preset card posted. Reusing a single token across all
	// enabled cards is safe (the verifier doesn't bind the rule
	// ID or slug — the underlying enableAlertPresetFromForm does
	// its own per-preset validation) AND avoids burning a fresh
	// session-key write per card. On a session-store failure we
	// fall back to empty tokens, which the verifier rejects —
	// the customer sees the error banner rather than a silently-
	// broken form.
	var enableCSRF string
	if s.sessions != nil {
		if t, err := middleware.IssueForAuthenticated(s.sessions, dashboardEnablePresetAction, acct.ID); err == nil {
			enableCSRF = t
		} else {
			log.Warn("dashboard fetchDashboardPresets: mint enable CSRF", "account_id", acct.ID, "err", err)
		}
	}
	// Issue #1233 / ADR-123 PR-C commit 2: mint a SEPARATE CSRF
	// envelope for the "Send test alert" form so the verifier at
	// dashboard_preset_enable.go:165 (action=
	// dashboardSendTestAlertPresetAction) accepts the POST. Sharing
	// the enable envelope across both forms would let a replayed
	// enable click fire a test — separate envelopes enforce the
	// "every POST carries a fresh, action-bound token" rule.
	var testAlertCSRF string
	if s.sessions != nil {
		if t, err := middleware.IssueForAuthenticated(s.sessions, dashboardSendTestAlertPresetAction, acct.ID); err == nil {
			testAlertCSRF = t
		} else {
			log.Warn("dashboard fetchDashboardPresets: mint test-alert CSRF", "account_id", acct.ID, "err", err)
		}
	}
	// Issue #1233 / ADR-123 PR-C commit 2: determine which presets
	// the customer has already instantiated for this app so the
	// card can render a "Send test alert" button instead of (or
	// alongside) the enable form. We pull the full per-account
	// rule set in one shot and match each catalog row against the
	// display-name prefix "<DisplayName> (<app_slug>)" — bounded by
	// AlertRuleLimitPerAccount = 100 on Scale, so the loop is O(rows
	// × rules) ≈ 8 × 100 = 800 string-prefix checks per render.
	// That's well under a millisecond; no need for an indexed lookup
	// per card.
	allRules, ruleErr := s.store.ListAlertRulesForAccount(ctx, acct.ID)
	if ruleErr != nil {
		log.Warn("dashboard fetchDashboardPresets: list alert rules", "account_id", acct.ID, "err", ruleErr)
		// Non-fatal — fall through with an empty rule set, which
		// makes every card render the enable form as before.
		allRules = nil
	}
	// Pre-index rules by app_id so the inner loop is O(rules_for_app)
	// rather than O(all_rules). Most accounts have ≤3 apps; this is
	// marginal but keeps the hot path tight.
	rulesByApp := make(map[string][]state.AlertRule, 3)
	for _, r := range allRules {
		rulesByApp[r.AppID] = append(rulesByApp[r.AppID], r)
	}
	out := make([]dashboard.AlertPresetItem, 0, len(rows))
	for _, p := range rows {
		meetsPlan := api.PlanMeetsMinimumPlan(acct.Plan, api.Plan(p.MinimumPlan))
		enabled := p.EnabledInCatalog && meetsPlan
		item := dashboard.AlertPresetItem{
			Name:                   p.Name,
			DisplayName:            p.DisplayName,
			Description:            p.Description,
			Category:               p.Category,
			Metric:                 p.Metric,
			Comparison:             p.Comparison,
			Threshold:              p.Threshold,
			WindowSpec:             p.WindowSpec,
			DefaultCooldownMinutes: p.DefaultCooldownMinutes,
			MinimumPlan:            p.MinimumPlan,
			EnabledInCatalog:       p.EnabledInCatalog,
			Enabled:                enabled,
			MeetsPlan:              meetsPlan,
			AppSlug:                app.Slug,
		}
		// Only stamp the token on cards that will render a form.
		// Coming-soon / upgrade cards have no form so an empty
		// token is correct (the template's {{if .Enabled}} branch
		// never reads EnableConfirmToken for those).
		if enabled {
			item.EnableConfirmToken = enableCSRF
		}
		// Stamp the test-alert envelope ONLY on instantiated cards
		// — the test button only renders inside the
		// {{if and .Enabled .Instantiated}} branch, so an empty
		// token on a non-instantiated card is correct (the template
		// never reads it there).
		if enabled && item.Instantiated {
			item.TestAlertConfirmToken = testAlertCSRF
		}
		// Match against the customer's rules for THIS app by the
		// canonical display-name prefix. We tolerate multiple
		// matches but only emit the first (the catalog doesn't
		// allow duplicates per (account, app, preset) anyway —
		// the create path surfaces ErrConflict on the second
		// instantiation).
		prefix := p.DisplayName + " (" + app.Slug + ")"
		for _, r := range rulesByApp[app.ID] {
			if r.Name == prefix || (len(r.Name) >= len(prefix) && r.Name[:len(prefix)] == prefix) {
				item.Instantiated = true
				item.RuleID = r.ID
				break
			}
		}
		// Stamp the "What does this alert mean?" panel for every card
		// (issue #1233 / ADR-123 PR-C commit 3). observed=0 keeps the
		// static prose — the Observed renderer takes over in the
		// alert-detail panel when an actual alert fires. Decorate
		// returns nil for an undocumented preset, and the template
		// uses `with` to skip the panel cleanly (no broken <details>
		// block for a future catalog row).
		item.Explanation = presetwhy.Decorate(p.Name, 0)
		out = append(out, item)
	}
	return out
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
	var requests int64
	var cpuUsec int64
	var egressBytes int64
	var ingressBytes int64
	var coldBoots int64
	for _, u := range rows {
		mbSec += u.MBSeconds
		requests += u.Requests
		cpuUsec += u.CPUUsec
		// ADR-046 (step 10): sum both egress columns so
		// the dashboard's "egress this month" panel
		// surfaces a single GB number. Informational;
		// not billed.
		//
		// NOTE (PR-414 I5): the resulting GB number
		// INCLUDES Ethernet framing because net_tx_bytes
		// is the root-side kernel interface byte counter
		// (vethHost.rx_bytes). A 1 GB HTTP workload can
		// show as ~1.2-1.5 GB on this counter. The
		// dashboard template renders this with a footer
		// note; the future billing PR will pick the unit.
		egressBytes += u.TXBytes + u.NetTxBytes
		ingressBytes += u.NetRxBytes
		coldBoots += u.ColdBootCount
	}
	used := meter.GBHours(mbSec)
	// usedEgressGB carries the same framing caveat as the
	// docstring on api.UsageResponse.TotalEgressGB — see
	// pkg/api/dto.go for the wire-side semantics.
	usedEgressGB := float64(egressBytes) / (1024 * 1024 * 1024)
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderUsage: list apps", "account_id", acct.ID, "err", err)
		apps = nil
	}
	dailyRows, err := s.store.UsageDailyForAccount(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderUsage: load daily usage", "account_id", acct.ID, "err", err)
		dailyRows = nil
	}
	perApp := usageAppData(rows, apps, used)
	daily, dailySparkline := usageDailyView(dailyRows, apps)
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
		Month:              month.Format("2006-01"),
		UsedGBHours:        used,
		IncludedGBHours:    included,
		OverageGBHours:     max(0, used-float64(included)),
		UsedPct:            pct,
		Requests:           requests,
		UsedEgressGB:       usedEgressGB,
		UsedIngressGB:      float64(ingressBytes) / (1024 * 1024 * 1024),
		UsedCPUHours:       meter.CPUHours(cpuUsec),
		ColdBoots:          coldBoots,
		PerApp:             perApp,
		Daily:              daily,
		DailySparklineHTML: dailySparkline,
	}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderBilling renders /dashboard/billing — the plan card + current-month
// usage + last invoice + Stripe billing portal link (issue #253).
//
// The page is intentionally lenient about failures: every read failures
// (UsageByMonth, ListInvoicesForAccount, CountDeployedApps) only logs +
// falls back to an empty value. The customer must always see something
// useful on /dashboard/billing — a Postgres blip on the read path must
// not collapse the page to a 500. The dashboard render itself is the
// only fatal path (renderProblem 500 RFC 7807).
//
// HasPaidPlan is sourced from acct.StripeSubscriptionItem (the durable
// "this account has a paid subscription" signal, written by the
// invoice.payment_succeeded webhook) OR acct.Plan != free (catches
// admin-elevated accounts that have not yet completed a Stripe
// checkout; the portal link still works because the operator can
// attach the customer to a subscription through the portal itself).
// Both legs are gated so a Free account never sees a portal link.
func (s *server) renderBilling(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	limits := api.MustLimitsFor(acct.Plan)

	// Current-month usage (reuses UsageByMonth + renderUsage's shape).
	// Failure is non-fatal: log + fall through with 0 mbSec so the page
	// still renders.
	month := time.Now().UTC()
	rows, err := s.store.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		log.Warn("dashboard renderBilling: usage by month", "account_id", acct.ID, "err", err)
		rows = nil
	}
	var mbSec int64
	var egressBytes int64
	for _, u := range rows {
		mbSec += u.MBSeconds
		// Same framing caveat as renderUsage:counts both egress columns
		// so the page can surface a single GB number. Informational only.
		egressBytes += u.TXBytes + u.NetTxBytes
	}
	used := meter.GBHours(mbSec)
	usedEgressGB := float64(egressBytes) / (1024 * 1024 * 1024)
	pct := 0.0
	if limits.IncludedGBHours > 0 {
		pct = used / float64(limits.IncludedGBHours) * 100
	}

	// Last invoice (LIMIT 1). Failure is non-fatal: log + render the
	// "No invoices yet" empty state from the template.
	lastInvoice, err := s.store.ListInvoicesForAccount(ctx, acct.ID, nil, time.Time{}, 1)
	if err != nil {
		log.Warn("dashboard renderBilling: list invoices", "account_id", acct.ID, "err", err)
		lastInvoice = nil
	}
	var lastInvDate, lastInvStatus, lastInvTotal, lastInvCcy string
	if len(lastInvoice) > 0 {
		li := lastInvoice[0]
		// Go's time layout uses "02" for day-of-month, NOT a literal
		// day value — "2006-01-31" renders 2026-07-127 because "31" is
		// the day count for the reference time (Jan 31). The first
		// row's PeriodEnd is already a month-end, so the day is
		// always ≤ 31 — using "2006-01-02" fixes the off-by-one and
		// also correctly handles shorter months.
		lastInvDate = li.PeriodEnd.UTC().Format("2006-01-02")
		lastInvStatus = li.Status
		lastInvTotal = formatCentsEuros(li.TotalCents)
		// Stripe stores currency as lowercase ISO 4217 ("eur");
		// display it as "EUR" to match the receipt the customer
		// sees in their portal.
		lastInvCcy = strings.ToUpper(li.Currency)
	}

	hasPaidPlan := acct.StripeSubscriptionItem != "" || acct.Plan != api.PlanFree
	portalURL := ""
	if hasPaidPlan {
		portalURL = s.billingPortalURLForProvider(ctx, acct)
	}

	// Issue #561 — spend cap (issue #279 storage, #561 enforcement).
	// Read the cap (nullable) and the current-month overage cents so
	// the billing page can render the % bar and the inline Raise-cap
	// form. Failure is non-fatal: render with no cap (NULL) so the
	// page still loads on a transient PG blip.
	capCents, capOK, capErr := s.store.GetAccountOverageCapCents(ctx, acct.ID)
	if capErr != nil {
		log.Warn("dashboard renderBilling: get overage cap", "account_id", acct.ID, "err", capErr)
	}
	var capPtr *int64
	if capOK {
		v := capCents
		capPtr = &v
	}
	overageCents, obsErr := s.store.CurrentMonthOverageCents(ctx, acct.ID)
	if obsErr != nil {
		log.Warn("dashboard renderBilling: current month overage", "account_id", acct.ID, "err", obsErr)
		overageCents = 0
	}
	var overageRatio float64
	if capOK && capCents > 0 && overageCents > 0 {
		overageRatio = float64(overageCents) / float64(capCents) * 100
	}

	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderBilling: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	data := dashboard.BillingData{
		Plan:                      string(acct.Plan),
		RAMMB:                     limits.RAMMB,
		Included:                  int64(limits.IncludedGBHours),
		AppsCap:                   limits.DeployedApps,
		AppLayer:                  limits.AppLayerMaxMB,
		IdleSec:                   limits.IdleTimeoutS,
		MaxConcurrency:            limits.MaxConcurrency,
		UsedGBHours:               used,
		UsedPct:                   pct,
		UsedEgressGB:              usedEgressGB,
		LastInvoiceDate:           lastInvDate,
		LastInvoiceStatus:         lastInvStatus,
		LastInvoiceTotalFormatted: lastInvTotal,
		LastInvoiceCurrency:       lastInvCcy,
		HasPaidPlan:               hasPaidPlan,
		Provider:                  providerName(s.billingProvider),
		PortalURL:                 portalURL,
		OverageCapCents:           capPtr,
		OverageUsedCents:          overageCents,
		OverageUsedThisMBCap:      overageRatio,
	}
	// Free → paid hand-off (dashboard_upgrade.go): per-plan links to the
	// /dashboard/upgrade confirmation page when the provider has hosted
	// checkout and the account has no subscription yet.
	data.CanCheckout = s.canStartCheckout(acct)
	data.UpgradeOptions = upgradeOptionsFor(acct)
	data.UpgradeNotice = upgradeNoticeFor(r.URL.Query().Get("upgrade"))

	// Issue #561 CSRF envelope for the raise-cap form. Mirrors the
	// renderAccount pattern at line 792 (delete + restore tokens). The
	// unused-on-this-page cookie is harmless (10 min TTL) and the
	// uniform shape ("dashboard always issues the action's CSRF
	// token at GET time") lets the dashboard helper in pkg/middleware
	// stay simple. Failure is non-fatal at the log line; the page
	// still renders without the form so the customer sees the existing
	// cap value.
	//
	// STAMPED ON data BEFORE the page is built so the embedded
	// copy inside page.Data observes the token. Setting the token
	// after the page construction would mutate the local `data`
	// only and leave the form's hidden input empty (the test
	// TestDashboardRaiseOverageCap_PostsForm catches that exact
	// regression).
	if raiseTok, err := middleware.IssueForAuthenticated(s.sessions, "raise_overage_cap", acct.ID); err == nil {
		data.RaiseCapConfirmToken = raiseTok
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieNameAuthenticated,
			Value:    raiseTok,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.domain != "",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
		})
	} else {
		log.Warn("dashboard renderBilling: csrf issue raise_overage_cap", "account_id", acct.ID, "err", err)
	}
	page := dashboard.Page{Title: "Billing", Body: "billing", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderPricing renders /dashboard/pricing — a four-column plan
// comparison driven entirely by pkg/api/limits.go. Money is integer
// millicents upstream; we divide by 1000 at render time only and
// format with %d.%02d (CLAUDE.md Money: integer cents/millicents —
// no float math on money).
func (s *server) renderPricing(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderPricing: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	plans := make([]dashboard.PricingPlanView, 0, len(api.Plans))
	for _, p := range api.Plans {
		l := api.MustLimitsFor(p)
		plans = append(plans, dashboard.PricingPlanView{
			Plan:                    string(p),
			PriceFormatted:          formatPriceEuros(l.PriceMillicents),
			Highlighted:             p == acct.Plan,
			DeployedApps:            l.DeployedApps,
			MaxConcurrency:          l.MaxConcurrency,
			RAMMB:                   l.RAMMB,
			AppLayerMaxMB:           l.AppLayerMaxMB,
			SourceTarballMaxMB:      l.SourceTarballMaxMB,
			IdleTimeoutS:            l.IdleTimeoutS,
			IncludedGBHours:         int64(l.IncludedGBHours),
			RateLimitRPS:            l.RateLimitRPS,
			RateLimitBurst:          l.RateLimitBurst,
			EgressMbit:              l.EgressMbit,
			SecretCountMax:          l.SecretCountMax,
			AsyncInvokeAllowed:      l.AsyncInvokeAllowed,
			MinInstancesAllowed:     l.MinInstancesAllowed,
			ScaleUpTargetRPSAllowed: l.ScaleUpTargetRPSAllowed,
			ScaleUpTargetCPUAllowed: l.ScaleUpTargetCPUAllowed,
			EgressAllowlistAllowed:  l.EgressAllowlistAllowed,
			EgressAllowlistMaxSize:  l.EgressAllowlistMaxSize,
		})
	}
	page := dashboard.Page{Title: "Pricing", Body: "pricing", Account: dashboardAccountView(view, appCount), Data: dashboard.PricingData{Plans: plans}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderInvoices renders /dashboard/invoices — the account's billing
// history (issue #259). Optional ?month=YYYY-MM filter and ?before
// cursor pagination, mirroring the API handler's parsing. Bad input
// is silently dropped (the dashboard is forgiving; the API enforces
// RFC 7807). The handlers are intentionally kept small enough to fit
// the 50-line guideline via parseInvoiceMonth / parseInvoiceBefore.
func (s *server) renderInvoices(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderInvoices: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	monthStr, monthPtr := parseInvoiceMonth(r.URL.Query().Get("month"))
	before := parseInvoiceBefore(r.URL.Query().Get("before"))
	rows, err := s.store.ListInvoicesForAccount(r.Context(), acct.ID, monthPtr, before, 25)
	if err != nil {
		log.Warn("dashboard renderInvoices: list invoices", "account_id", acct.ID, "err", err)
		renderProblem(w, log, err)
		return
	}
	items := make([]dashboard.InvoiceRow, 0, len(rows))
	for _, inv := range rows {
		items = append(items, dashboard.InvoiceRow{
			ID:             inv.ID,
			Number:         inv.Number,
			Provider:       inv.Provider,
			Status:         inv.Status,
			Period:         inv.PeriodEnd.Format("2006-01"),
			TotalFormatted: formatCentsEuros(inv.TotalCents),
			Currency:       inv.Currency,
			PDFAvailable:   inv.PDFAvailable,
		})
	}
	nextBefore := ""
	if len(rows) == 25 && len(items) > 0 {
		nextBefore = rows[len(rows)-1].PeriodEnd.UTC().Format(time.RFC3339Nano)
	}
	page := dashboard.Page{Title: "Invoices", Body: "invoices", Account: dashboardAccountView(view, appCount), Data: dashboard.InvoicesData{
		Month: monthStr, Items: items, NextBefore: nextBefore,
	}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// parseInvoiceMonth parses the dashboard's optional month query.
// Returns (echoed-string, parsed-pointer). Bad input echoes empty so
// the form re-renders cleanly without surfacing the validation error
// (the API surfaces the RFC 7807 problem; the dashboard is forgiving).
func parseInvoiceMonth(raw string) (string, *time.Time) {
	if raw == "" {
		return "", nil
	}
	m, err := time.Parse("2006-01", raw)
	if err != nil {
		return "", nil
	}
	return raw, &m
}

// parseInvoiceBefore parses the dashboard's optional before cursor.
// Empty → zero time. Bad input → zero time (forgiving; matches the
// dashboard's behaviour for month). The API is strict; the dashboard
// is a thin read surface, not a validator.
func parseInvoiceBefore(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

// formatPriceEuros converts integer millicents (1 cent = 1000
// millicents) into the dashboard's "€X.YY" string, or "Free" when
// the price is zero. This is the only place float math on money
// happens — at the human display edge (CLAUDE.md Money).
func formatPriceEuros(millicents int64) string {
	if millicents == 0 {
		return "Free"
	}
	cents := millicents / 1000
	euros := cents / 100
	rem := cents % 100
	if rem < 0 {
		rem = -rem
	}
	return fmt.Sprintf("€%d.%02d", euros, rem)
}

// formatCentsEuros converts integer cents (the invoice total unit)
// into the dashboard's "€X.YY" string. Distinct from
// formatPriceEuros (which takes millicents and collapses €0 to
// "Free" for the pricing page). formatCentsEuros is for invoice
// rows where €0 means "a real €0 invoice" (refund / void / draft)
// and must display as "€0.00", not as "Free".
func formatCentsEuros(cents int64) string {
	euros := cents / 100
	rem := cents % 100
	if rem < 0 {
		rem = -rem
	}
	return fmt.Sprintf("€%d.%02d", euros, rem)
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
			Scopes:    k.Scopes,
			CreatedAt: k.CreatedAt.UTC().Format("2006-01-02"),
			CanRevoke: k.Status != string(state.APIKeyStatusRevoked),
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
	// Issue #961 / Mega-B PR-3 — the account page now ships a
	// working "Connect GitHub" button (replaces the slice 8 stub).
	// Mint the connect_github CSRF envelope here so the form's
	// hidden input matches the cookie the POST handler reads.
	connectGithubTok, err := middleware.IssueForAuthenticated(s.sessions, "connect_github", view.ID)
	if err != nil {
		log.Error("dashboard renderAccount: csrf issue connect_github", "err", err, "account_id", view.ID)
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
	keyDeleteTok, err := middleware.IssueForAuthenticatedNamed(
		s.sessions, dashboardKeyDeleteAction, view.ID, dashboardKeyDeleteCSRFCookie)
	if err != nil {
		log.Error("dashboard renderAccount: csrf issue key delete", "err", err, "account_id", view.ID)
		renderProblem(w, log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardKeyDeleteCSRFCookie,
		Value:    keyDeleteTok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	data.KeyDeleteConfirmToken = keyDeleteTok
	planTok, err := middleware.IssueForAuthenticatedNamed(
		s.sessions, dashboardAccountPlanAction, view.ID, dashboardAccountPlanCSRFCookie)
	if err != nil {
		log.Error("dashboard renderAccount: csrf issue plan", "err", err, "account_id", view.ID)
		renderProblem(w, log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardAccountPlanCSRFCookie,
		Value:    planTok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	data.PlanConfirmToken = planTok
	// Wire to .Data.ConnectGithubConfirmToken in account.html; the
	// form action is /dashboard/install/connect (handlers_oauth_code_callback.go).
	data.ConnectGithubConfirmToken = connectGithubTok
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
	switch r.URL.Query().Get("key_revoked") {
	case "1":
		data.FlashSurface = "API key revoked."
	}
	switch r.URL.Query().Get("plan") {
	case "unchanged":
		data.FlashSurface = "Your account is already on that plan."
	case "unavailable":
		data.FlashSurface = "Plan changes are unavailable until billing is configured."
	}
	// Issue #695 / ADR-080: per-account apps-auth-default
	// grand-father banner. Renders when the account has at
	// least one pre-flip app (auth_default_flipped_at IS NOT
	// NULL on a live row). The banner copy points at the
	// universal opt-out CLI; the count-zero path is the
	// natural off-switch — no dismissal cookie required.
	//
	// The cut-over date is read from the events table (the
	// apps.auth_default_global_flipped audit row emitted by
	// the migration). A transient store error falls back to
	// the empty string copy ("Recently") so the banner still
	// renders with a reasonable message instead of 500ing
	// the dashboard load. A never-applied environment (zero
	// events) also falls back to "Recently" — the banner is
	// unreachable in that state because CountAuthDefaultFlippedApps
	// returns 0 too, so the empty-date path is a no-op.
	if n, err := s.store.CountAuthDefaultFlippedApps(r.Context(), acct.ID); err == nil && n > 0 {
		cutover := "Recently"
		if t, terr := s.store.AuthDefaultFlippedAt(r.Context()); terr == nil && !t.IsZero() {
			cutover = t.UTC().Format("2006-01-02")
		}
		data.ActionRequiredSurface = fmt.Sprintf(
			"On %s the default for newly-created apps changed to require authentication. "+
				"Your existing %d app(s) were not affected and continue to serve anonymous traffic. "+
				"New apps now require \"Authorization: Bearer <token>\" by default; "+
				"run \"gregale app <slug> --no-require-authn --public-auth=open\" to opt out any pre-flip app.",
			cutover, n)
	}
	// Issue #696 / ADR-082 dashboard follow-up PR — best-effort
	// per-account SLO panel. Mirrors the per-app fetch in
	// renderAppDetail. Same 3s budget envelope; nil = skip the
	// section entirely (Prometheus not configured / fetch failed).
	data.SLOAccount = s.fetchDashboardAccountSLO(r.Context(), log, acct, resolveSLOWindow(r))
	data.SLODuration = views.SLOStamp{
		Window: resolveSLOWindow(r),
		AsOf:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	page := dashboard.Page{Title: "Account", Body: "account", Account: dashboardAccountView(view, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
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
//
// dashboardEmDash is the "not yet known" placeholder the dashboard
// renders for cells whose values come from a missing dial/wire.
// Lifted to a constant so goconst stops nagging about the three
// em-dash occurrences (relative-time "—", quota-cell "—").
const dashboardEmDash = "—"

func dashboardAccountView(acct state.Account, appCount int) *dashboard.AccountView {
	n := appCount
	if n < 0 {
		n = 0
	}
	return &dashboard.AccountView{
		ID:                         acct.ID,
		Email:                      acct.Email,
		Plan:                       string(acct.Plan),
		AppCount:                   n,
		EmailVerified:              acct.EmailVerified(),
		EmailVerificationGraceEnds: acct.CreatedAt.Add(emailVerificationGrace).UTC().Format("2006-01-02"),
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

// renderAuditEvents renders /dashboard/audit-events — the
// customer-facing drill-down on the audit log. Wave 0 PR-C /
// ADR-047. The handler reuses the same store path as the public
// GET /v1/audit-events handler (handlers_audit.go::listAuditEvents)
// by calling ListEvents directly with the same constraints:
//
//   - The kind_prefix filter is the SQL-anchored one (cheap).
//   - The app_id filter is the Go-side post-SQL filter added in
//     handlers_audit.go (no events-table migration).
//   - include_anonymous surfaces subject=NULL rows (the rare
//     defensive case where the app row was deleted between wake
//     and advisory).
//
// Resolving the app_id → slug for the header chip is a single
// indexed GetApp call; on AppNotFound the chip is empty (the row
// may legitimately be from an app that was deleted before the
// dashboard rendered).
//
// Pagination limit is the same as the public handler (50 default,
// 100 max) — the dashboard renders the same rows the API would
// return, so a customer toggling between "view source" and the
// hosted page sees the same shape.
func (s *server) renderAuditEvents(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	q := r.URL.Query()
	prefix := q.Get("kind_prefix")
	appIDFilter := q.Get("app_id")
	includeAnonymous, _ := strconv.ParseBool(q.Get("include_anonymous"))

	limit := listAuditEventsLimitDefault
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			renderProblem(w, log, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return
		}
		if n > listAuditEventsLimitMax {
			n = listAuditEventsLimitMax
		}
		limit = n
	}

	rows, err := s.store.ListEvents(r.Context(), acct.ID, listAuditEventsOverRead)
	if err != nil {
		log.Warn("dashboard renderAuditEvents: list account events", "account_id", acct.ID, "err", err)
		renderProblem(w, log, api.ErrCapacity("could not list audit events"))
		return
	}
	var anonRows []state.Event
	if includeAnonymous {
		anonRows, err = s.store.ListEvents(r.Context(), "", listAuditEventsOverRead)
		if err != nil {
			log.Warn("dashboard renderAuditEvents: list anonymous events", "account_id", acct.ID, "err", err)
			renderProblem(w, log, api.ErrCapacity("could not list anonymous audit events"))
			return
		}
	}
	merged := append([]state.Event{}, rows...)
	merged = append(merged, anonRows...)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].At.After(merged[j].At)
	})

	now := time.Now()
	// Cap the backing array at listAuditEventsLimitMax (the same
	// bound the request handler applies to ?limit=…) regardless of
	// the caller-supplied limit value. CodeQL go/allocation-rule
	// flags `make([]T, 0, limit)` because `limit` is a parsed
	// query-string value the taint analysis can't bound. The render
	// loop's `if len(items) >= limit { break }` truncates the actual
	// number of rows. Mirrors handlers_audit.go:169's shape.
	items := make([]dashboard.AuditEventRow, 0, listAuditEventsLimitMax)
	for _, e := range merged {
		if prefix != "" && !strings.HasPrefix(e.Kind, prefix) {
			continue
		}
		if appIDFilter != "" && !eventDataHasAppID(e.Data, appIDFilter) {
			continue
		}
		row := dashboard.AuditEventRow{
			ID:        strconv.FormatInt(e.ID, 10),
			TimeLabel: dashboard.RelativeTime(e.At, now),
			Actor:     e.Actor,
			Kind:      e.Kind,
		}
		// Severity is hoisted from the audit row's data map. Only
		// stateless.advisory rows carry the field today; the apid
		// receiver (cmd/apid/advisory_receiver.go) populates it at
		// emit time. A future audit kind with its own severity
		// classification can write a similar key without touching
		// the dashboard.
		if severity, ok := dataSeverity(e.Data); ok {
			row.Severity = severity
		}
		if e.Subject != nil {
			row.Subject = e.Subject.String()
		}
		// Pretty-print the JSON for the dashboard table; raw JSON
		// would be hostile to operators. The detail surface is
		// GET /v1/audit-events/{id} for the structured view.
		if len(e.Data) > 0 {
			var pretty any
			if err := json.Unmarshal(e.Data, &pretty); err == nil {
				if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
					row.DataPretty = string(b)
				}
			}
		}
		items = append(items, row)
		if len(items) >= limit {
			break
		}
	}

	appSlug := ""
	if appIDFilter != "" {
		if a, err := s.store.AppByID(r.Context(), appIDFilter); err == nil && a.AccountID == acct.ID {
			appSlug = a.Slug
		}
	}

	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderAuditEvents: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{
		Title:   "Audit events",
		Body:    "audit_events",
		Account: dashboardAccountView(view, appCount),
		Data: dashboard.AuditEventsData{
			KindPrefix:       prefix,
			AppID:            appIDFilter,
			AppSlug:          appSlug,
			Events:           items,
			IncludeAnonymous: includeAnonymous,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderSafeReleasesDashboard (SAFE-RELEASES-OBS PR-C, issue
// #976 / ADR-122) renders /dashboard/safe-releases — the operator's
// "everything in-flight" surface for the canary + safedeploy
// lifecycle. Three sections, bounded by the
// safedeploy_in_flight_rollouts gauge that PR-B's
// canary_fleet_in_flight_high alert tripwires at 50. Operator-only
// (gated upstream by role.IsOperator — see cmd/apid/handlers.go).
func (s *server) renderSafeReleasesDashboard(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	ctx := r.Context()
	now := time.Now().UTC()

	// (1) In-flight rollouts.
	rows, err := s.store.SafedeployListPendingRollouts(ctx)
	if err != nil {
		log.Warn("dashboard renderSafeReleasesDashboard: list pending rollouts",
			"account_id", acct.ID, "err", err)
		renderProblem(w, log, err)
		return
	}
	inFlight := make([]dashboard.DeploymentItem, 0, len(rows))
	slugByID := make(map[string]string, len(rows))
	for _, d := range rows {
		inFlight = append(inFlight, dashboardDeploymentItem(d))
		// Resolve AppID → slug once per deployment so the
		// audit-row table can deep-link back to the per-app
		// drill-down. Best-effort: an AppByID miss just leaves
		// the row without a deep link.
		if _, ok := slugByID[d.AppID]; !ok {
			if app, err := s.store.AppByID(ctx, d.AppID); err == nil && app.AccountID == acct.ID {
				slugByID[d.AppID] = app.Slug
			}
		}
	}

	// (2) Recent audit per in-flight deployment, filtered to the 5
	// PR-A widened kinds. N+1 query — bounded by in-flight count.
	recent := make([]dashboard.SafeReleasesAuditRow, 0)
	for _, d := range rows {
		audits, err := s.store.ListDeploymentAudit(ctx, d.ID, 20)
		if err != nil {
			log.Warn("dashboard renderSafeReleasesDashboard: list deployment audit",
				"account_id", acct.ID, "deployment_id", d.ID, "err", err)
			continue
		}
		for _, a := range audits {
			if !safeReleasesAuditKind(a.Kind) {
				continue
			}
			recent = append(recent, dashboard.SafeReleasesAuditRow{
				DeploymentID: d.ID,
				AppSlug:      slugByID[d.AppID],
				Kind:         string(a.Kind),
				TimeLabel:    dashboard.RelativeTime(a.At, now),
				Actor:        a.Actor,
			})
		}
	}

	// (3) Active alerts filtered to the 4 PR-B kinds.
	allAlerts, err := s.store.ListEnabledAlertRules(ctx)
	if err != nil {
		log.Warn("dashboard renderSafeReleasesDashboard: list enabled alerts",
			"account_id", acct.ID, "err", err)
		allAlerts = nil
	}
	active := make([]dashboard.SafeReleasesAlertRow, 0)
	for _, rule := range allAlerts {
		if !safeReleasesAlertMetric(string(rule.Metric)) {
			continue
		}
		active = append(active, dashboard.SafeReleasesAlertRow{
			Name:    rule.Name,
			Metric:  string(rule.Metric),
			Enabled: rule.Enabled,
		})
	}

	// Render. The template lives at pkg/dashboard/templates/safe_releases.html.
	page := dashboard.Page{
		Title:   "Safe-releases",
		Body:    "safe_releases",
		Account: dashboardAccountView(acct, 0),
		Data: dashboard.SafeReleasesData{
			InFlight:     inFlight,
			RecentAudit:  recent,
			ActiveAlerts: active,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// safeReleasesAuditKind returns true iff k is one of the 5 audit
// kinds PR-A widened into deployment_audit_kind_chk
// (migrations/20260905000000000). Closed-set; any future kind added to
// that migration must also be appended here.
func safeReleasesAuditKind(k state.DeploymentAuditKind) bool {
	switch k {
	case state.DeployRolloutStarted,
		state.DeployRolloutCompleted,
		state.DeployRolloutAborted,
		state.DeployCanaryStepAdvanced,
		state.DeployAlertRuleFired:
		return true
	}
	return false
}

// safeReleasesAlertMetric returns true iff m is one of the 4 PR-B
// alert metric kinds. Closed-set; the catalog seed in
// migrations/20260905000000001 inserts these as the only safe-releases metric
// values. A future PR adding a new safe-releases metric must also
// append the metric here AND add an alert_presets row AND extend
// pkg/state.AlertMetric* AND pkg/api.AllowedAlertRuleMetrics.
func safeReleasesAlertMetric(m string) bool {
	switch state.AlertMetric(m) {
	case state.AlertMetricCanaryStuckStep,
		state.AlertMetricSafedeployAuditEmitFailing,
		state.AlertMetricDeploymentAuditGCFailing,
		state.AlertMetricCanaryFleetInFlightHigh:
		return true
	}
	return false
}

// renderAlertRuleDetail (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122)
// renders /dashboard/alerts/{rule_id} — the per-rule drill-down that
// closes the operator's cross-correlation gap. The handler pulls:
//  1. state.AlertRule via AlertRuleByID (single-row read).
//  2. state.ListDeploymentAuditByAlertRule — every audit row the
//     rule triggered (joins on deployment_audit.alert_rule_id, the
//     partial index from migrations/20260905000000002 keeps this cheap even at
//     90-day retention). The store accepts a UUID string; pass
//     rule.ID directly.
//  3. state.ListAlertDeliveriesForRule — the rule's recent webhook
//     deliveries, so the operator can see "rule fired, here are the
//     Slack webhooks that went out".
//
// Operator-gated: the URL is registered only when role.IsOperator is
// true (the dashboard router at line 138 onward gates that). Missing
// rule renders a "rule no longer exists" chip (forward-compat with
// the migration comment — a rule can be deleted while its audit
// trail outlives it).
//
// IDOR posture: AlertRuleByID is keyed on the alert_rules.id UUID,
// which is opaque to the caller. No account scoping — alert rules
// are operator-side state, not customer data.
func (s *server) renderAlertRuleDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, ruleID string) {
	if _, err := uuid.Parse(ruleID); err != nil {
		s.notFound(w, "invalid rule id")
		return
	}
	ctx := r.Context()
	rule, err := s.store.AlertRuleByID(ctx, ruleID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "alert rule not found")
			return
		}
		log.Warn("dashboard renderAlertRuleDetail: lookup rule",
			"rule", ruleID, "err", err.Error())
		renderProblem(w, log, fmt.Errorf("alert rule lookup failed: %w", err))
		return
	}
	auditRows, err := s.store.ListDeploymentAuditByAlertRule(ctx, ruleID, 100)
	if err != nil {
		log.Warn("dashboard renderAlertRuleDetail: list deployment audit by rule",
			"rule", ruleID, "err", err.Error())
		auditRows = nil
	}
	deliveries, err := s.store.ListAlertDeliveriesForRule(ctx, ruleID, 50, false)
	if err != nil {
		log.Warn("dashboard renderAlertRuleDetail: list alert deliveries",
			"rule", ruleID, "err", err.Error())
		deliveries = nil
	}
	page := dashboard.Page{
		Title:   "Alert rule",
		Body:    "alert_rule_detail",
		Account: dashboardAccountView(acct, 0),
		Data: dashboard.AlertRuleDetailData{
			Rule:       rule,
			AuditRows:  auditRows,
			Deliveries: deliveries,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderStateless renders /dashboard/stateless — Move 1 PR-A landing
// page for the stateless contract. Pulls the account's last 50
// stateless.advisory audit rows via the same store path as
// renderAuditEvents, scoped to the signed-in account. The 8-base
// denylist + 10 closed paths come from pkg/dashboard's package-level
// slices (mirrored from pkg/imaged and guest-init — see dashboard.go's
// source-of-truth comment on each slice).
//
// Pagination cap is 50 (vs listAuditEventsLimitMax=100 on the public
// audit-events page) to keep the table scannable. The full set is
// reachable via /dashboard/audit-events?kind_prefix=stateless.advisory.
func (s *server) renderStateless(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	const pageSize = 50
	rows, err := s.store.ListEvents(r.Context(), acct.ID, listAuditEventsOverRead)
	if err != nil {
		log.Warn("dashboard renderStateless: list account events", "account_id", acct.ID, "err", err)
		renderProblem(w, log, api.ErrCapacity("could not list audit events"))
		return
	}
	now := time.Now()
	items := make([]dashboard.AuditEventRow, 0, pageSize)
	for _, e := range rows {
		if !strings.HasPrefix(e.Kind, "stateless.advisory") {
			continue
		}
		row := dashboard.AuditEventRow{
			ID:        strconv.FormatInt(e.ID, 10),
			TimeLabel: dashboard.RelativeTime(e.At, now),
			Actor:     e.Actor,
			Kind:      e.Kind,
		}
		// Severity hoist mirrors renderAuditEvents — see the comment
		// there. Stateless.advisory rows always carry a "severity"
		// key (advisory_receiver.go:116) so the badge column renders
		// for every row on this landing page.
		if severity, ok := dataSeverity(e.Data); ok {
			row.Severity = severity
		}
		if e.Subject != nil {
			row.Subject = e.Subject.String()
		}
		if len(e.Data) > 0 {
			var pretty any
			if err := json.Unmarshal(e.Data, &pretty); err == nil {
				if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
					row.DataPretty = string(b)
				}
			}
		}
		items = append(items, row)
		if len(items) >= pageSize {
			break
		}
	}
	view, _ := AccountFrom(r.Context())
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderStateless: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{
		Title:   "Stateless advisories",
		Body:    "stateless",
		Account: dashboardAccountView(view, appCount),
		Data: dashboard.StatelessData{
			RecentAdvisories:      items,
			RecentAdvisoriesEmpty: len(items) == 0,
			RecentAdvisoriesTotal: len(items),
			StatelessDenylist:     dashboard.StatelessDenylist,
			ClosedPaths:           dashboard.StatelessClosedPaths,
		},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// dashboardOrgItem is the shared projection used by
// renderOrgsList + renderOrgDetail. Both build the same per-org
// row from a state.Org + the caller's role on it; one helper
// keeps the projection in lockstep with the templates. SeatUsed
// is sourced from CountActiveOrgMembers; SeatLimit is sourced
// from the org plan (Plan.OrgMembersMax) and is 0 for personal
// orgs (the dashboard renders that as "personal org").
//
// Best-effort: a non-fatal failure to read seat counts falls
// back to a (0, 0) row so the listing still renders; the
// dashboard log line carries the err.
func dashboardOrgItem(ctx context.Context, log *slog.Logger, store state.Store, org state.Org, callerRole state.OrgRole) dashboard.OrgListItem {
	item := dashboard.OrgListItem{
		Slug:     org.Slug,
		Name:     org.Name,
		Plan:     string(org.Plan),
		Role:     string(callerRole),
		Personal: org.Personal,
	}
	if org.Personal {
		return item // SeatUsed/SeatLimit stay zero on personal orgs
	}
	used, err := store.CountActiveOrgMembers(ctx, org.ID)
	if err != nil {
		log.Warn("dashboardOrgItem: CountActiveOrgMembers",
			"org_id", org.ID, "slug", org.Slug, "err", err)
	} else {
		item.SeatUsed = used
	}
	item.SeatLimit = org.Plan.OrgMembersMax()
	return item
}

// resolveCallerRole returns the signed-in caller's role on the
// given org. Returns ErrNotFound when the caller's account has
// no active membership — handlers translate that to a 404 after
// the row check so the dashboard exposes the same IDOR posture
// as the public GET /v1/orgs/{slug} surfaces. Removed-membership
// rows are treated the same as "no membership" so a removed
// caller cannot peer at the org via a stale row.
func resolveCallerRole(ctx context.Context, store state.Store, orgID, accountID string) (state.OrgRole, error) {
	mem, err := store.OrgMemberByAccount(ctx, orgID, accountID)
	if err != nil {
		return "", err
	}
	if mem.RemovedAt != nil {
		return "", state.ErrNotFound
	}
	return mem.Role, nil
}

// dashboardMembershipProjection converts state.OrgMembership →
// dashboard.OrgMemberItem, joining a pre-fetched account map (one
// batch fetch per render — see AccountsByIDs at the renderOrgDetail
// call site) to surface the email (never the bare account ID).
// Map-absence is best-effort: a deleted-account race surfaces as
// "(deleted account)" + the original Role, preserving the "who
// was in the org historically" view that the audit table still
// requires. The deleted-account contract is unchanged from the
// previous per-row AccountByID path.
func dashboardMembershipProjection(mem state.OrgMembership, accts map[string]state.Account) dashboard.OrgMemberItem {
	item := dashboard.OrgMemberItem{
		AccountID: mem.AccountID,
		Role:      string(mem.Role),
		JoinedAt:  mem.JoinedAt.UTC().Format("2006-01-02"),
	}
	if acct, ok := accts[mem.AccountID]; ok {
		item.Email = acct.Email
	} else {
		item.Email = "(deleted account)"
	}
	return item
}

// renderOrgsList renders /dashboard/orgs — every org the
// signed-in account is an active member of. Source data is
// Store.ListOrgsForAccount (PR-5 store method) joined with
// Store.OrgMemberByAccount per row to surface the caller's
// role on each org. Personal orgs surface with the muted
// "(personal)" tag and zero seat count; the rest show
// <used>/<limit> so the customer can spot cap pressure without
// leaving the dashboard.
func (s *server) renderOrgsList(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	orgs, err := s.store.ListOrgsForAccount(r.Context(), acct.ID)
	if err != nil {
		log.Error("dashboard renderOrgsList: ListOrgsForAccount",
			"account_id", acct.ID, "err", err)
		renderProblem(w, log, err)
		return
	}
	items := make([]dashboard.OrgListItem, 0, len(orgs))
	for _, org := range orgs {
		role, err := resolveCallerRole(r.Context(), s.store, org.ID, acct.ID)
		if err != nil {
			// Skip rows where the caller's membership is
			// removed (race vs RemoveOrgMember); a stale row
			// shouldn't render in the dashboard listing.
			continue
		}
		items = append(items, dashboardOrgItem(r.Context(), log, s.store, org, role))
	}
	// Sort: personal first (so the "your personal org" affordance
	// is at the top), then alphabetical by slug. Stable so
	// personal orgs without a slug sort consistently with the
	// deterministic PersonalOrgSlug derivation.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Personal != items[j].Personal {
			return items[i].Personal // personal first
		}
		return items[i].Slug < items[j].Slug
	})
	page := dashboard.Page{
		Title:   "Organizations",
		Body:    "orgs",
		Account: dashboardAccountView(acct, 0),
		Data:    dashboard.OrgListData{Orgs: items},
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// renderOrgDetail renders /dashboard/orgs/{slug} — the org's
// seat count + members + invitations. The slug is resolved via
// Store.OrgBySlug (case-insensitive per the orgs_slug_lower_uniq
// CHECK). A non-member renders the standard 404, mirroring the
// IDOR posture of every other /v1/orgs/{slug} surface. The
// page is read-only in PR-8: invite/revoke forms land with the
// reverse-call infrastructure in a follow-up PR (per the
// "Revoke affordance" deferred comment in dashboard.go).
func (s *server) renderOrgDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	org, err := s.store.OrgBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Error("dashboard renderOrgDetail: OrgBySlug",
			"slug", slug, "err", err)
		renderProblem(w, log, err)
		return
	}
	callerRole, err := resolveCallerRole(r.Context(), s.store, org.ID, acct.ID)
	if err != nil {
		// ErrNotFound from resolveCallerRole means the caller is
		// not an active member — surface 404, not 403, so the
		// dashboard preserves the org-scoped IDOR posture (don't
		// reveal that a slug exists unless the caller belongs to it).
		if errors.Is(err, state.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Error("dashboard renderOrgDetail: resolveCallerRole",
			"org_id", org.ID, "account_id", acct.ID, "err", err)
		renderProblem(w, log, err)
		return
	}
	data := dashboard.OrgDetailData{
		Org:         dashboardOrgItem(r.Context(), log, s.store, org, callerRole),
		CallersRole: string(callerRole),
	}

	// Members (best-effort — log + continue on failure).
	members, err := s.store.ListOrgMembers(r.Context(), org.ID)
	if err != nil {
		log.Warn("dashboard renderOrgDetail: ListOrgMembers",
			"org_id", org.ID, "err", err)
		data.Error = "members: " + err.Error()
	} else {
		// PR-9 §1: pre-collect every active member's account ID
		// into a unique slice; one batch fetch replaces N
		// per-row AccountByID calls. The deleted-account race is
		// preserved by map-absence in the batch helper (same
		// contract as the per-row AccountByID path).
		active := make([]string, 0, len(members))
		for _, m := range members {
			if m.RemovedAt != nil {
				continue
			}
			active = append(active, m.AccountID)
		}
		accts := map[string]state.Account{}
		if len(active) > 0 {
			fetched, err := s.store.AccountsByIDs(r.Context(), active)
			if err != nil {
				log.Warn("dashboard renderOrgDetail: AccountsByIDs",
					"org_id", org.ID, "err", err)
				// Best-effort: fall through with the empty map
				// we already initialised. The projection treats
				// missing accounts as "(deleted account)".
			} else {
				accts = fetched
			}
		}
		items := make([]dashboard.OrgMemberItem, 0, len(members))
		for _, m := range members {
			if m.RemovedAt != nil {
				continue
			}
			items = append(items, dashboardMembershipProjection(m, accts))
		}
		data.Members = items
	}

	// Invitations: cap at the first page (25 rows) — the full
	// page-walk is a follow-up after the dashboard reverse-call
	// exists. PR-8 ships the table surface so the dashboard
	// can render "you have N pending invites" via the
	// Store.CountPendingOrgInvitations side-channel.
	invitations, err := s.store.ListOrgInvitationsForOrgPage(r.Context(), org.ID, 25, "")
	if err != nil {
		log.Warn("dashboard renderOrgDetail: ListOrgInvitationsForOrgPage",
			"org_id", org.ID, "err", err)
		if data.Error == "" {
			data.Error = "invitations: " + err.Error()
		} else {
			data.Error += "; invitations: " + err.Error()
		}
	} else {
		now := time.Now()
		items := make([]dashboard.OrgInvitationItem, 0, len(invitations))
		for _, inv := range invitations {
			status := api.DeriveOrgInvitationStatus(inv.ConsumedAt, inv.RevokedAt, inv.ExpiresAt, now)
			prefix := ""
			if len(inv.TokenHash) >= 4 {
				prefix = base64.RawURLEncoding.EncodeToString(inv.TokenHash)[:8]
			}
			items = append(items, dashboard.OrgInvitationItem{
				ID:          inv.ID,
				Email:       inv.Email,
				Role:        string(inv.Role),
				Status:      status,
				CreatedAt:   inv.CreatedAt.UTC().Format("2006-01-02 15:04 MST"),
				ExpiresAt:   inv.ExpiresAt.UTC().Format("2006-01-02 15:04 MST"),
				TokenPrefix: prefix,
			})
		}
		data.Invitations = items
	}

	page := dashboard.Page{
		Title:   org.Name,
		Body:    "org_detail",
		Account: dashboardAccountView(acct, 0),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// parseDeployDetailPath splits a /dashboard/apps/{slug}/... suffix
// into (slug, deployment_id, ok). Returns ok=false when the
// suffix doesn't match /deployments/{id} — caller falls through
// to renderAppDetail. The match is prefix-based so the function
// stays robust to future suffix additions (e.g.
// /deployments/{id}/logs); today only /deployments/{id} is
// dispatched.
func parseDeployDetailPath(rest string) (string, string, bool) {
	const deploys = "/deployments/"
	i := strings.Index(rest, deploys)
	if i < 0 {
		return "", "", false
	}
	slug := rest[:i]
	id := rest[i+len(deploys):]
	if slug == "" || id == "" || strings.Contains(id, "/") {
		return "", "", false
	}
	return slug, id, true
}

// renderDeploymentDetail renders /dashboard/apps/{slug}/deployments/{id}
// — the per-deploy grype scan drill-down page (issue #464 /
// ADR-055 / PR-A). Pulls the typed ScanResult directly from the
// store rather than calling GET /v1/deployments/{id}/scan over
// loopback — same data, one fewer indirection. IDOR posture is
// the same AppByID + AccountID check as renderAppDetail (above).
//
// On the nil Scan case (deploy is mid-pipeline or predates the
// feature), the template renders "scan pending". On a non-empty
// scan_status we project the wire api.ScanResult into the
// dashboard-local ScanPayload so pkg/dashboard stays free of
// pkg/api imports (the same isolation rule that drove the
// AppListItem vs pkg/api.App split).
func (s *server) renderDeploymentDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug, deploymentID string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	dep, err := s.store.DeploymentByID(ctx, deploymentID)
	if err != nil || dep.AppID != app.ID {
		http.NotFound(w, r)
		return
	}

	data := dashboard.DeploymentDetailData{
		App:        dashboard.AppListItem{Slug: app.Slug},
		Deployment: dashboardDeploymentItem(dep),
	}
	if dep.ScanStatus != "" {
		payload := dashboardScanPayload(s.scanResponse(dep))
		data.Scan = &payload
	}
	// A2 (ADR-117 v2 follow-on): render the closed-6-stage
	// post-stream summary into the page. The projection is
	// handler-edge so pkg/dashboard stays free of html/template
	// FuncMap wiring; the template only inlines the pre-rendered
	// HTML. nil Stages means the jsonb was empty (pre-00302 OR
	// in-flight pre-first-frame) — the template omits the section.
	stagePayload, err := dashboardStagePayload(dep)
	if err != nil {
		// Bad jsonb shape (CLI/server drift) — fall through with
		// nil Stages so the page renders, and log to stdout for
		// the operator. The same fallback posture as
		// dashboardScanPayload on a nil scan (see above).
		_ = err
	} else if stagePayload.BodyHTML != "" {
		data.Stages = &stagePayload
	}
	// Issue #976 / ADR-122 / SAFE-RELEASES-C.3 — populate the
	// per-deployment preview URL for the dashboard. Mirrors
	// getDeploymentURL (cmd/apid/handlers_url.go::getDeploymentURL)
	// so the dashboard and the apid wire surface agree. The
	// resolved hostname goes through state.DeploymentOrdinal
	// (per-app 1-based rank ordered by (created_at, id)) —
	// the same code path the cert allowlist takes on TLS
	// handshake, so the dashboard copy button's URL is the
	// exact same URL the edge will mint under.
	//
	// Always populates the field (pointer, never nil), even
	// when Alive=false on a failed/superseded row: the
	// template distinguishes "preview active" vs "preview
	// closed" rather than absent-vs-present.
	alive := dep.DeploymentPreviewActive()
	preview := dashboard.DeploymentPreviewURL{Alive: alive}
	if alive {
		suffix := wire.DeployWildcardSuffix
		if suffix != "" {
			ord, ordErr := s.store.DeploymentOrdinal(ctx, app.ID, dep.ID)
			if ordErr == nil {
				if host := gateway.BuildDeploymentPreviewURL(suffix, ord, app.Slug); host != "" {
					preview.Host = host
					preview.URL = wire.DeployPreviewURIScheme + "://" + host
				}
			}
			// On ordinal lookup error we leave preview at
			// Alive=true with empty Host/URL — the
			// template renders the "preview pending" chip
			// (transient failure). Surfacing a hard error
			// here would 500 the dashboard's read-only
			// page on a benign transient.
		}
	}
	data.PreviewURL = &preview

	// ADR-117 §Production-ready follow-on, C4: per-stage retry
	// form (visible only when the deployment failed AND a
	// recoverable failed-stage name is recoverable from the
	// jsonb). The form posts to
	// /dashboard/apps/{slug}/deployments/{id}/retry?from=<stage>
	// with the sealed (action="retry_deployment", account_id)
	// envelope minted below. failedStageFromJSON lives in
	// dashboard_retry_deployment.go alongside the POST handler
	// so the two stay in lock-step.
	if dep.Status == state.DeployFailed {
		if from := failedStageFromJSON(dep.StageState); from != "" {
			data.CanRetry = true
			data.RetryFromStage = from
			if tok, err := middleware.IssueForAuthenticated(s.sessions, dashboardRetryDeploymentAction, acct.ID); err == nil {
				data.DeploymentRetryCSRF = tok
				http.SetCookie(w, &http.Cookie{
					Name:     middleware.CookieNameAuthenticated,
					Value:    tok,
					Path:     "/",
					HttpOnly: true,
					Secure:   s.domain != "",
					SameSite: http.SameSiteLaxMode,
					MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
				})
			}
		}
	}

	// Production-leveling Stream A: per-deployment audit timeline
	// (issue #976 / ADR-122 / SAFE-RELEASES-E.2). The handler
	// already verified dep.AppID == app.ID above (IDOR posture
	// line 1931) so a plain ListDeploymentAudit(dep.ID) is safe.
	// On read error we leave DeploymentAudit empty and log —
	// the page is read-only so a missing timeline shouldn't 500
	// the operator's day. Capped at listDeploymentAuditLimitDefault
	// (50) rows; the same wire shape /v1/deployments/{id}/audit
	// returns, just projected through dashboard.DeploymentAuditRow
	// instead of api.DeploymentAuditResponse.
	if auditRows, err := s.store.ListDeploymentAudit(ctx, dep.ID, listDeploymentAuditLimitDefault); err == nil {
		rows := make([]dashboard.DeploymentAuditRow, 0, len(auditRows))
		for _, row := range auditRows {
			rows = append(rows, dashboard.DeploymentAuditRow{
				At:            row.At,
				Kind:          string(row.Kind),
				Actor:         row.Actor,
				SeverityClass: dashboard.DeploymentAuditSeverityClass(string(row.Kind)),
				DataPretty:    dashboard.PrettyAuditData(row.Data),
			})
		}
		data.DeploymentAudit = rows
	} else {
		log.Warn("dashboard renderDeploymentDetail: list deployment audit",
			"deployment_id", dep.ID, "err", err)
	}

	nonce := httpsec.NonceFromContext(r.Context())
	page := dashboard.Page{
		Title:   "Deployment " + dep.ID,
		Nonce:   nonce,
		Account: dashboardAccountView(acct, 0),
		Body:    "deployment_detail",
		Data:    data,
	}
	if err := dashboard.Render(w, log, nonce, page); err != nil {
		renderProblem(w, log, err)
	}
}

// dashboardStagePayload projects the typed state.Deployment row
// into the dashboard-local StagePayload (ADR-117 v2 follow-on
// A2). The HTML is rendered at the handler edge so the dashboard
// template only inlines the pre-rendered block via
// {{ .Data.Stages.BodyHTML }} — no FuncMap wiring, no template
// divergence. Mirrors cmd/gregale/deploys_show.go::deriveTerminalAt
// for the footer-timestamp branch: "live" → first history row's
// StartedAt; "failed" → first failed row's EndedAt; zero when
// status is unknown / superseded / future.
//
// Empty stage_state (pre-00302 OR in-flight pre-first-frame)
// returns StagePayload{}, nil — the template omits the section
// entirely on the nil pointer. Non-empty + bad shape (schema
// drift) returns the unmarshal error so the caller can fall
// through to renderProblem OR silently no-op (current behaviour:
// fall through with nil Stages + the warning is logged at WARN
// by the dashboard scanner).
//
// IDOR posture: unchanged. We read d.StageState from the same
// authorized row returned by DeploymentByID (AccountID check
// bounds the read at the handler edge). No additional fetch.
func dashboardStagePayload(d state.Deployment) (dashboard.StagePayload, error) {
	if len(d.StageState) == 0 {
		return dashboard.StagePayload{}, nil
	}
	var ss state.StageState
	if err := json.Unmarshal(d.StageState, &ss); err != nil {
		return dashboard.StagePayload{}, err
	}
	status := string(d.Status)
	terminalAt := dashboardStageTerminalAt(ss, status, d.CreatedAt)
	html := stages.RenderSummaryHTML(ss, status, terminalAt)
	if html == "" {
		// Empty history + empty current — caller sees a nil
		// pointer at the template level and omits the section.
		return dashboard.StagePayload{}, nil
	}
	payload := dashboard.StagePayload{
		BodyHTML:   html,
		Status:     status,
		TerminalAt: terminalAt,
	}
	// Failure decoration (ADR-117 §Production-ready follow-on, C4):
	// when the row's status is "failed" AND ErrorCode is in the
	// CodeStage* set, lift the whycopy prose via Decorate so the
	// template can inline the cluster-A `.error-explanation` block.
	// The code → title/hint/why/fix mapping lives in pkg/whycopy
	// (decorated against an *api.Problem stub; the customer-facing
	// copy comes from the catalog row, not the row's raw reason).
	if status == string(state.DeployFailed) && d.ErrorCode != "" {
		problem := &api.Problem{}
		if decorated := whycopy.Decorate(problem, d.ErrorCode, nil); decorated != nil {
			if decorated.Title != "" || decorated.Hint != "" ||
				decorated.Why != "" || decorated.Fix != "" {
				payload.FailureExplanation = &dashboard.StageFailureExplanation{
					Title: decorated.Title,
					Hint:  decorated.Hint,
					Why:   decorated.Why,
					Fix:   decorated.Fix,
				}
			}
		}
	}
	return payload, nil
}

// dashboardStageTerminalAt picks the footer timestamp for the
// dashboard stage widget. Mirrors cmd/gregale/deploys_show.go::
// deriveTerminalAt so the dashboard and the CLI agree on the
// row whose StartedAt / EndedAt supplies the "live since" /
// "failed at" copy. The two paths are physically separate so
// a future refactor must update both.
//
// Review finding C1 (closed by this signature widening): the
// pre-fix version only handled live/failed and silently returned
// time.Time{} for superseded, leaving the customer looking at a
// stage table with no terminal anchor even though deployments.status
// said "superseded". The CLI's deriveTerminalAt has the same fix
// applied (same depCreatedAt argument) so the two surfaces stay
// in lock-step.
func dashboardStageTerminalAt(ss state.StageState, status string, depCreatedAt time.Time) time.Time {
	switch status {
	case "live":
		if len(ss.History) > 0 && ss.History[0].StartedAt != nil {
			return *ss.History[0].StartedAt
		}
	case "failed":
		for _, item := range ss.History {
			if item.Status == "failed" && item.EndedAt != nil {
				return *item.EndedAt
			}
		}
	case "superseded":
		// depCreatedAt is the deployment row's insert
		// timestamp; for a superseded row this is when the new
		// deployment that replaced it was created. The zero
		// value is a safe fallback (caller's terminalAt.IsZero()
		// gate skips the footer).
		return depCreatedAt
	}
	return time.Time{}
}

// dashboardScanPayload projects the wire api.ScanResult into the
// dashboard-local ScanPayload / VulnerabilityRow shapes. The
// handler is the only thing that crosses the api → dashboard
// boundary; pkg/dashboard itself never imports pkg/api.
//
// Accepts a pointer so the caller can pass s.scanResponse(d)
// directly; the helper is only reached on a non-empty
// scanStatus (the dashboard's "scan pending" pill renders
// on the absence, not on a 200/empty payload — the same
// convention as getDeploymentScan).
//
// Sort+cap (issue #464 / AC #3): the dashboard renders the
// top-N CVEs by severity (CRITICAL first, then HIGH, MEDIUM,
// LOW, UNKNOWN; stable on ID for ties). The cap is at the
// handler edge — the wire DTO + /scan route + SDK + CLI keep
// the full list so customers reaching the API directly don't
// have to reimplement the cap. TotalCount carries the
// pre-truncation count so the template can render the
// "Showing N of M" copy + a "View full scan (JSON)" link to
// GET /v1/deployments/{id}/scan when the scan had more
// findings than the dashboard width allows.
func dashboardScanPayload(s *api.ScanResult) dashboard.ScanPayload {
	if s == nil {
		return dashboard.ScanPayload{}
	}
	rows := make([]dashboard.VulnerabilityRow, 0, len(s.Vulnerabilities))
	for _, v := range s.Vulnerabilities {
		rows = append(rows, dashboard.VulnerabilityRow{
			ID:       v.ID,
			Severity: v.Severity,
			Package:  v.Package,
			Version:  v.Version,
			FixedIn:  v.FixedIn,
			Paths:    v.Paths,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return severityOrdinal(rows[i].Severity) < severityOrdinal(rows[j].Severity)
	})
	total := len(rows)
	limit := total
	if limit > dashboardScanTopN {
		limit = dashboardScanTopN
	}
	vulns := rows[:limit]
	sc := s.SeverityCounts
	return dashboard.ScanPayload{
		Status:         s.Status,
		ScannedAt:      s.ScannedAt,
		ScannerVersion: s.ScannerVersion,
		ImageDigest:    s.ImageDigest,
		SeverityCounts: dashboard.SeverityBucket{
			Critical: sc.Critical,
			High:     sc.High,
			Medium:   sc.Medium,
			Low:      sc.Low,
			Unknown:  sc.Unknown,
		},
		Vulnerabilities: vulns,
		TotalCount:      total,
		Error:           s.Error,
	}
}

// dashboardScanTopN is the AC #3 cap (issue #464): the
// dashboard's deployment detail page renders the top-N CVEs by
// severity, with a link to the full list at the wire endpoint.
// 10 is the spec number; the value is exposed at handler edge
// so the dashboard_test unit pin can iterate without copy-paste.
const dashboardScanTopN = 10

// severityOrdinalTable maps the closed-enum severity strings
// the dashboard renders to their render-first ordinal. Lower
// ordinal = more severe. Strings outside the closed enum
// (the default branch) sort after UNKNOWN so an upstream
// change doesn't disturb the ordering of known-severity rows.
//
// A single package-level map declaration keeps the goconst
// rule quiet (each severity string appears only inside the
// literal here; the return path reads off the ordinal).
var severityOrdinalTable = map[string]int{
	"CRITICAL": 0,
	"HIGH":     1,
	"MEDIUM":   2,
	"LOW":      3,
	"UNKNOWN":  4,
}

// severityOrdinal returns the render-first ordinal of a
// severity string. Unknown / malformed values return one past
// UNKNOWN so they sort at the end of the cap.
func severityOrdinal(s string) int {
	if n, ok := severityOrdinalTable[s]; ok {
		return n
	}
	return len(severityOrdinalTable) // any unknown sorts after UNKNOWN
}

// dashboardDeploymentItem projects a state.Deployment into the
// minimal DeploymentItem shape the deployment_detail template
// needs (ID, Kind, Status, CreatedAt). The full Deployments list
// uses a richer shape; the detail page only renders a header.
//
// Error-explanations cluster (spec §6.4 amendment 1): the 5 new
// prose columns stamped on the deployments row by SetDeploymentFailedEx
// flow through verbatim. The deployment_detail template gates the
// error-explanation section on ErrorCode != "" so pre-cluster rows
// (and non-failure rows) render unchanged.
//
// Issue #977 / ADR-116: stamps the four deploy-annotation fields
// (Reason / Tag / DeployedBy / PRNumber) so the dashboard list and
// detail pages render them uniformly from the same projection. The
// handler edge is the single seam — every code path that produces a
// DeploymentItem (list view, detail view, JSON drill-down) flows
// through here, so the annotation fields stay consistent without
// per-page duplication.
//
// Review fix CRIT-1 (issue #977 / ADR-116): also stamps RepoFullName
// parsed off the deployment's SourceURL (the github://<owner>/<repo>@<sha>
// scheme stamped by handlers_source_ref.go:148). The list-view
// template uses it to build the PR link target so a clickable #N
// chip lands on GitHub rather than on
// https://github.com/<app-slug>/pull/N (which 404s — App.Slug is the
// app slug, not the GitHub owner/name).
func dashboardDeploymentItem(d state.Deployment) dashboard.DeploymentItem {
	return dashboard.DeploymentItem{
		ID:                d.ID,
		Status:            string(d.Status),
		Kind:              string(d.Kind),
		CreatedAt:         d.CreatedAt.UTC().Format(time.RFC3339),
		Error:             d.Error,
		ErrorCode:         d.ErrorCode,
		ErrorHint:         d.ErrorHint,
		ErrorWhy:          d.ErrorWhy,
		ErrorFix:          d.ErrorFix,
		ErrorRelevantLogs: d.ErrorRelevantLogs,
		// Issue #606 / SAFE-RELEASES-E.1: structured deployer
		// attribution surfaced on the dashboard deploy detail
		// page. Server-stamped from the HTTP request context
		// (cmd/apid/handlers.go::createDeployment / handlers_source_*.go
		// / githubd_bridge.go) — never client-supplied. Pre-#606
		// rows carry empty strings; the via-chip conditional
		// render in deployment_detail.html keeps the wire + UI
		// byte-identical for those rows.
		DeployedByUserID: d.DeployedByUserID,
		DeployedVia:      d.DeployedVia,
		DeployedFromIP:   d.DeployedFromIP,
		PusherLogin:      d.PusherLogin,
		// Issue #977 / ADR-116: deployment annotations rendered on
		// the dashboard deploy detail page. nil/zero values drop
		// out at the template layer (annotation-chip conditional)
		// so pre-feature rows stay visually identical.
		Reason:       d.Reason,
		Tag:          d.Tag,
		DeployedBy:   d.DeployedBy,
		PRNumber:     d.PRNumber,
		RepoFullName: repoFullNameFromSourceURL(d.SourceURL),
	}
}

// repoFullNameFromSourceURL extracts the "owner/name" string from a
// github://<owner>/<repo>@<sha> SourceURL. Returns "" for the
// non-github-deployed rows (image-deploy, local tarball) so the
// template quietly drops the PR link instead of rendering a 404.
//
// The parsing is intentionally tight: the githubd bridge always
// formats the URL as exactly `github://<owner>/<repo>@<sha>` (no
// trailing slash, no port, no scheme variant). A regression that
// introduced a different shape would surface here as a missing
// PR link rather than a panic — the template treats the empty
// string as "no PR link".
//
// The strings.Cut at "@" splits the owner/repo prefix from the SHA
// suffix; the strings.Cut at "/" splits owner from repo. Both
// return ok=false on a malformed URL, which we map to "".
func repoFullNameFromSourceURL(sourceURL string) string {
	const prefix = "github://"
	if !strings.HasPrefix(sourceURL, prefix) {
		return ""
	}
	body := sourceURL[len(prefix):]
	at := strings.IndexByte(body, '@')
	if at < 0 {
		return ""
	}
	ownerRepo := body[:at]
	slash := strings.IndexByte(ownerRepo, '/')
	if slash < 0 {
		return ""
	}
	owner := ownerRepo[:slash]
	repo := ownerRepo[slash+1:]
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

// projectPreviewItems (ADR-095 PR-C / issue #272) materialises a
// dashboard.PreviewItem slice from the raw state.App preview rows.
// The preview-host label ("pr-{N}.{parent-slug}") is derived from
// PreviewOfSlug + PreviewPrNumber rather than parsed from any
// stored field because the column is the canonical input — the
// dashboard never round-trips a host header to mint URLs.
//
// URL is the FULL https form (e.g.
// "https://pr-42-myapp.gregale.dev") matching the apps-list
// convention in appListItem above — both the visible link and the
// Copy-URL button read .URL, and a relative host label would hand
// the user a non-clickable string.
//
// parentSlug is the slug of the page being rendered (used to
// resolve the preview-app slug back to its host label); domain
// is the configured apps suffix (for example "gregale.dev"). When domain is empty
// (local dev), the URL falls back to the bare host label so
// dev-environment renders don't render a broken link to
// a broken link to an incomplete hostname.
//
// The result is sorted newest-first because PreviewAppsByParent
// already orders that way (preview apps are not frequently re-
// ordered), and the dashboard template iterates in slice order.
func projectPreviewItems(rows []state.App, parentSlug, domain string) []dashboard.PreviewItem {
	out := make([]dashboard.PreviewItem, 0, len(rows))
	for _, a := range rows {
		isDev := a.PreviewPrNumber == 0
		scope := a.Slug
		if !isDev {
			scope = fmt.Sprintf("pr-%d-%s", a.PreviewPrNumber, parentSlug)
		}
		url := appURLForDomain(scope, domain)
		if domain == "" {
			url = scope
		}
		item := dashboard.PreviewItem{
			Slug:       a.Slug,
			URL:        url,
			PrNumber:   a.PreviewPrNumber,
			IsDev:      isDev,
			PrState:    a.PreviewPrState,
			StateLabel: a.PreviewPrState,
			StateClass: "preview-state-" + a.PreviewPrState,
		}
		if !a.CreatedAt.IsZero() {
			item.CreatedAt = a.CreatedAt.UTC().Format(time.RFC3339)
		}
		if a.PreviewExpiresAt != nil && !a.PreviewExpiresAt.IsZero() {
			item.ExpiresAt = a.PreviewExpiresAt.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	return out
}

// parseDomainDoctorPath (ADR-120 Tier A2) splits a
// /dashboard/apps/{slug}/... suffix into (slug, domain, ok)
// when the suffix matches /domains/{domain}/doctor. Returns
// ok=false when the suffix doesn't match — caller falls
// through to renderAppDetail. The match is prefix-based so a
// future PR can add /domains/{domain}/doctor/{tab} (e.g. a
// "logs" tab) without breaking the dispatcher. Mirrors
// parseDeployDetailPath at :1697.
func parseDomainDoctorPath(rest string) (string, string, bool) {
	const doctor = "/domains/"
	i := strings.Index(rest, doctor)
	if i < 0 {
		return "", "", false
	}
	slug := rest[:i]
	after := rest[i+len(doctor):]
	if slug == "" || after == "" {
		return "", "", false
	}
	// Match the /doctor suffix. Three cases land here:
	//   1. /doctor (exact)                — this parser owns it
	//   2. /doctor/{tab} (future sub-tab) — fall through to a
	//      sibling dispatcher (this function stays untouched when
	//      that lands)
	//   3. anything else                   — fall through to
	//      renderAppDetail
	// We split on /doctor so a sub-tab can claim the part after
	// /doctor without touching this function. Review-fix #4 caught
	// the prior HasSuffix-only implementation rejecting sub-tabs
	// despite the comment promising the opposite.
	const doctorTail = "/doctor"
	idx := strings.Index(after, doctorTail)
	if idx < 0 {
		return "", "", false
	}
	tail := after[idx+len(doctorTail):]
	if tail != "" {
		// Sub-tab shape (/doctor/logs, /doctor/...) — leave for
		// a sibling dispatcher case to claim.
		return "", "", false
	}
	domain := after[:idx]
	if domain == "" || strings.Contains(domain, "/") {
		return "", "", false
	}
	return slug, domain, true
}

// renderDomainDoctor (ADR-120 Tier A2) renders
// /dashboard/apps/{slug}/domains/{domain}/doctor — the
// per-domain Render-style drill-down page mirroring the CLI's
// `gregale domains doctor <domain>` output. The handler reads
// the same DomainDoctorReport the JSON endpoint returns
// (cmd/apid/handlers_ext.go::getDomainDoctor) by calling the
// shared buildDoctorReport helper so the JSON wire shape and
// the dashboard HTML stay in lockstep — single source of truth
// for the per-check row + Remediation text.
//
// IDOR posture mirrors renderDeploymentDetail: AppBySlug +
// AccountID rejection (line 1727), then loadDomain (the
// same ownership check the JSON endpoint uses) to confirm the
// domain belongs to the same app. Falls through to
// http.NotFound on either failure so a cross-tenant probe
// returns 404, not 403 (no signal that the row exists).
func (s *server) renderDomainDoctor(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug, domain string) {
	// Same dark-launch guard as the JSON endpoint at
	// handlers_ext.go:1892 — without this, the dashboard renders
	// a stale 5-check report while the CLI returns 503
	// doctor_disabled, and the two surfaces disagree on the same
	// operator choice. The dark-launch was a soak-only construct
	// per ADR-120's Tier-A3 section; the operator's escape hatch
	// MUST be visible in BOTH surfaces, not just the CLI.
	if !s.runtimeBool(runtimeConfigDomainDoctor, api.DomainDoctorEnabled()) {
		api.WriteProblem(w, api.ErrDoctorDisabled())
		return
	}
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	d, ok := s.loadDomain(w, r, acct, domain)
	if !ok {
		// loadDomain already wrote the 404 problem JSON; bail
		// without touching the ResponseWriter again — calling
		// http.NotFound here would clobber the RFC 7807 body
		// with text/plain '404 page not found' and change the
		// Content-Type under the customer's browser.
		return
	}
	if d.AppID != app.ID {
		// loadDomain passed (domain belongs to acct) but the
		// {slug} in the URL doesn't match the owning app. Same
		// 404 posture as cross-tenant: no signal that the row
		// exists, single write.
		s.notFound(w, "no such domain")
		return
	}
	report, err := s.buildDoctorReport(ctx, d)
	if err != nil {
		log.Warn("dashboard renderDomainDoctor: buildDoctorReport failed", "domain", domain, "err", err)
		http.Error(w, "doctor unavailable", http.StatusServiceUnavailable)
		return
	}
	view := dashboard.DomainDoctorView{
		App:        dashboard.AppListItem{Slug: app.Slug},
		Domain:     report.Domain,
		AppID:      report.AppID,
		Healthy:    report.Healthy,
		Stale:      report.Stale,
		ObservedAt: report.ObservedAt,
	}
	for _, c := range report.Checks {
		view.Checks = append(view.Checks, dashboard.DashboardDoctorCheck{
			Name:        c.Name,
			Status:      c.Status,
			Detail:      c.Detail,
			Observed:    c.Observed,
			Remediation: c.Remediation,
			CheckedAt:   c.CheckedAt,
		})
	}
	page := dashboard.Page{
		Title:   "Doctor — " + domain,
		Body:    "domain_doctor",
		Account: dashboardAccountView(acct, 0),
		Data:    view,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// parseWakeTimelinePath (PR-A / ADR-123 follow-on) splits a
// /dashboard/apps/{slug}/... suffix into (slug, ok) when the suffix
// matches /wake-timeline exactly. Returns ok=false when the suffix
// doesn't match — caller falls through to renderAppDetail. Mirrors
// parseDomainDoctorPath at :2108 (prefix match + exact-tail match).
// The exact-tail match posture is intentional — a future sub-tab
// (/wake-timeline/{day|trigger|state}) would land with its own
// parser without disturbing this one.
func parseWakeTimelinePath(rest string) (string, bool) {
	const tail = "/wake-timeline"
	idx := strings.Index(rest, tail)
	if idx < 0 {
		return "", false
	}
	after := rest[idx+len(tail):]
	if after != "" {
		// Sub-tab shape (/wake-timeline/{tab}) — leave for a
		// sibling dispatcher to claim when one lands.
		return "", false
	}
	slug := rest[:idx]
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// renderAppWakeTimeline (PR-A / ADR-123 follow-on) renders
// /dashboard/apps/{slug}/wake-timeline — the per-app wake-narrative
// drill-down. Composed of three blocks:
//
//  1. Page header (app name + slug + back-link to app detail).
//  2. 24h summary card: total wakes + trigger histogram +
//     at-cap count + at-cap pct. All aggregation happens at the
//     handler edge so the template stays FuncMap-free (the
//     stages pattern).
//  3. Recent wakes table (up to 50 rows) — the same column
//     set the app-detail recent-wakes table exposes, with
//     AtCapacity + ReadyInMS added by PR-A. Rows with no
//     matching wake.boot_started event (pre-ADR-123 fleet)
//     still appear; the view renders em-dash for missing
//     fields per the existing convention.
//
// IDOR posture mirrors renderDomainDoctor / renderDeploymentDetail:
// AppBySlug + AccountID rejection first, then render. Falls through
// to http.NotFound on a cross-tenant probe.
func (s *server) renderAppWakeTimeline(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	// Recent wakes for this app: capped at 50 (vs 10 on
	// app_detail) since the per-app page is the dedicated
	// wake-narrative surface. Bounded at the SQL layer
	// (LIMIT 50 in ListLatestInstancesForApp) so a long-lived
	// app never pulls its full history on every dashboard
	// render. Failure is non-fatal — the section silently
	// renders empty.
	instances, err := s.store.ListLatestInstancesForApp(ctx, app.ID, 50)
	if err != nil {
		log.Warn("dashboard renderAppWakeTimeline: list recent instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		instances = nil
	}
	// Batched lookup of the wake-boot telemetry for every wake_id
	// in one SQL round-trip (uses events_wake_id_idx). Failure
	// is non-fatal — pre-ADR-123 fleet or a transient Postgres
	// blip on the events table leaves the new columns blank
	// (em-dash at the view layer).
	wakeIDs := make([]string, 0, len(instances))
	for _, ins := range instances {
		if ins.WakeID != "" {
			wakeIDs = append(wakeIDs, ins.WakeID)
		}
	}
	bootMetas := make(map[string]state.WakeBootMeta)
	if len(wakeIDs) > 0 {
		if m, err := s.store.LookupBootStartedForWakes(ctx, wakeIDs); err == nil {
			bootMetas = m
		} else {
			log.Warn("dashboard renderAppWakeTimeline: lookup boot started", "account_id", acct.ID, "app_id", app.ID, "err", err)
		}
	}
	// 24h summary card. TriggerHistogram is built from the
	// wake.boot_started metas of the rows returned (which are
	// the 50 most-recent instance rows — a 24h upper bound
	// for any sane customer workload; the dashboard never
	// claims "true" 24h since it would need a separate SQL
	// scan). AtCapacityCount mirrors that scope. Documented
	// on the template: "the 50 most recent wakes (≤24h for
	// any sane workload)".
	//
	// PR-A review cluster (PR #1031 finding #5): the histogram
	// and at-cap% previously diverged from wakeCount24h because
	// they only counted rows where hasMeta was true AND the
	// field was non-zero. A customer reading "24 wakes" and a
	// histogram totaling 20 was left wondering where the other
	// 4 went. The fix: track wakeCountWithMeta as the
	// denominator for the at-cap% and label the histogram
	// header so the customer understands "of N known wakes".
	// The body table still renders all 24h rows so the per-row
	// audit trail isn't lossy.
	//
	// Counter math is shared with the JSON mirror at
	// cmd/apid/handlers_app_wake_timeline_json.go via
	// aggregateWakeTimeline (review-fix cluster, PR #1097). The
	// row-shape loop still lives here because the HTML page
	// emits a different row type (views.WakeTimelineRow, no
	// JSON sentinels).
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	agg := aggregateWakeTimeline(instances, bootMetas, cutoff)

	rows := make([]views.WakeTimelineRow, 0, len(instances))
	for _, ins := range instances {
		if !ins.StartedAt.IsZero() && ins.StartedAt.UTC().Before(cutoff) {
			break
		}
		meta, hasMeta := bootMetas[ins.WakeID]
		row := views.WakeTimelineRow{
			Kind:  "wake.boot_started",
			State: ins.State,
		}
		if !ins.StartedAt.IsZero() {
			row.At = ins.StartedAt.UTC().Format(time.RFC3339)
		}
		if hasMeta {
			row.Trigger = meta.Trigger
			row.QueuedCount = meta.QueuedCount
			row.ConcurrencyAtAdmit = meta.ConcurrencyAtAdmit
			row.AtCapacity = meta.AtCapacity
			row.AtCapacityPresent = meta.AtCapacityPresent
			row.ReadyInMS = meta.ReadyInMS
		}
		rows = append(rows, row)
	}
	view := dashboard.WakeTimelinePageData{
		App: dashboard.AppListItem{
			Slug: app.Slug,
		},
		WakeCount24h:         agg.WakeCount24h,
		WakeCountWithMeta:    agg.WakeCountWithMeta,
		AtCapacityCount:      agg.AtCapacityCount,
		AtCapacityPct:        agg.AtCapacityPct,
		TriggerHistogramHTML: views.RenderTriggerHistogram(agg.TriggerHistogram),
		RenderTable:          views.RenderWakeTimelineTable(rows),
	}
	page := dashboard.Page{
		Title:   "Wake timeline — " + app.Slug,
		Body:    "app_wake_timeline",
		Account: dashboardAccountView(acct, 0),
		Data:    view,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
