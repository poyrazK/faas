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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
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
		case len(path) > len("/dashboard/apps/") && path[:len("/dashboard/apps/")] == "/dashboard/apps/":
			slug := path[len("/dashboard/apps/"):]
			s.renderAppDetail(w, r, log, acct, slug)
		case path == "/dashboard/usage":
			s.renderUsage(w, r, log, acct)
		case path == "/dashboard/billing":
			s.renderBilling(w, r, log, acct)
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
	return dashboard.AppListItem{
		Slug:            app.Slug,
		Status:          string(app.Status),
		URL:             "https://" + app.Slug + ".apps." + s.domain,
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
	// Finding 6 (issue #314): stamp the per-row quota-cell metadata.
	// account.Plan is the plan every app in this account sees
	// (RateLimitBurst / RateLimitRPS lives on api.LimitsFor(plan)).
	// QuotaLabel stays "—" until the apid→gatewayd loopback dial
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
	page := dashboard.Page{Title: "Apps", Body: "apps_list", Account: dashboardAccountView(view, len(apps)), Data: items}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
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
	// Bounded at the SQL layer (LIMIT 10 in ListLatestInstancesForApp)
	// so a long-lived app with hundreds of parked history rows doesn't
	// pull its full history on every dashboard render. Failure is
	// non-fatal — the section silently renders empty.
	recentInstances, err := s.store.ListLatestInstancesForApp(ctx, app.ID, 10)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list recent instances", "account_id", acct.ID, "app_id", app.ID, "err", err)
		recentInstances = nil
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
	appRow := s.appListItem(ctx, app, latest, time.Time{})
	appRow.Plan = string(acct.Plan)
	appRow.QuotaLabel = dashboardEmDash
	page := dashboard.Page{Title: app.Slug, Body: "app_detail", Account: dashboardAccountView(view, appCount), Data: dashboard.AppDetailData{
		App:             appRow,
		Manifest:        dashboardManifestView(app),
		Deployments:     deps,
		Crons:           cronItems,
		RecentInstances: recentItems,
		// Issue #273 / ADR-042 — best-effort metrics snapshot.
		// Failure is non-fatal: Prometheus being down renders the
		// "degraded" empty state rather than blocking the whole
		// page render. The 3s timeout matches the per-query timeout
		// in pkg/promql.
		Metrics: s.fetchDashboardMetrics(ctx, log, app.ID),
		// Issue #396 / ADR-045 PR 4 — best-effort alert-rule
		// snapshot. Failure is non-fatal: a Postgres blip on the
		// alert_rules read renders the panel's warning empty-state
		// instead of killing the whole page.
		Alerts: s.fetchDashboardAlerts(ctx, log, acct, app),
	}}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// fetchDashboardMetrics wraps the Prometheus fetch in a 3s budget
// so a slow Prometheus can't stall the dashboard render. nil return
// means "skip the section entirely" (Prometheus not configured, or
// the fetch timed out). A degraded result still returns a
// non-nil pointer so the template can render the "degraded" label
// rather than disappear silently — that's the same shape the
// public /status/slo.json uses.
func (s *server) fetchDashboardMetrics(ctx context.Context, log *slog.Logger, appID string) *dashboard.AppMetricsView {
	if s.promqlClient == nil {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
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
		deliveries, derr := s.store.ListAlertDeliveriesForRule(ctx, rule.ID, 5)
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
	var egressBytes int64
	for _, u := range rows {
		mbSec += u.MBSeconds
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
	}
	used := float64(mbSec) / 3_600_000.0
	// usedEgressGB carries the same framing caveat as the
	// docstring on api.UsageResponse.TotalEgressGB — see
	// pkg/api/dto.go for the wire-side semantics.
	usedEgressGB := float64(egressBytes) / (1024 * 1024 * 1024)
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
		UsedEgressGB:    usedEgressGB,
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
	used := float64(mbSec) / 3_600_000.0
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

	view, _ := AccountFrom(ctx)
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderBilling: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	page := dashboard.Page{Title: "Billing", Body: "billing", Account: dashboardAccountView(view, appCount), Data: dashboard.BillingData{
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
		PortalURL:                 s.billingPortalURLFor(acct),
	}}
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
