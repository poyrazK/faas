package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	dashboardRouteInsightFixAction = "route_insight_fix"
	dashboardRouteInsightFixCookie = "faas_csrf_route_insight_fix"
)

// dashboardRouteInsightKindLabels names insight kinds on the app page.
var dashboardRouteInsightKindLabels = map[string]string{
	api.RouteInsightKeptAwake:     "Keeps the app awake",
	api.RouteInsightCacheable:     "Safe to cache",
	api.RouteInsightCacheStale:    "Cache serves stale responses",
	api.RouteInsightCacheTooShort: "Cache max-age too short",
}

// dashboardRouteInsightFlashes are the only flash details the panel shows,
// so a crafted redirect cannot put text on the page.
var dashboardRouteInsightFlashes = map[string]string{
	"fixed":   "The rule change is on the edge-rules page; this insight moved to resolved.",
	"changed": "",
	"error":   "Reload the page and try again. If it keeps failing, apply the fix with `gregale routes insights fix`, which shows the reason.",
}

// fetchDashboardRouteInsights builds the app page's route insights panel
// (ADR-20261020161310). It is nil on plans without request telemetry; a
// failed read keeps the rest of the page available.
func (s *server) fetchDashboardRouteInsights(ctx context.Context, log *slog.Logger, w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) *dashboard.RouteInsightsView {
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		return nil
	}
	view := &dashboard.RouteInsightsView{}
	if flash := r.URL.Query().Get("insight_fix"); flash != "" {
		if detail, ok := dashboardRouteInsightFlashes[flash]; ok {
			view.Flash, view.FlashDetail = flash, detail
		}
	}
	store, ok := s.store.(routeinsights.ListStore)
	if !ok {
		return view
	}
	insights, err := routeinsights.List(ctx, store, acct.ID, app.ID, time.Now().UTC())
	if err != nil {
		log.Warn("dashboard renderAppDetail: list route insights", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return view
	}
	view.Available = true
	for _, insight := range insights {
		item := dashboard.RouteInsightItem{
			ID: insight.ID, KindLabel: dashboardRouteInsightKindLabels[insight.Kind], Method: insight.Method, Route: insight.Route,
			Message: insight.Message, FirstDetectedAt: insight.FirstDetectedAt, ResolvedAt: insight.ResolvedAt, FixedAt: insight.FixedAt,
		}
		if insight.Fix != nil {
			item.FixID, item.FixSummary = insight.Fix.ID, insight.Fix.Summary
		}
		if insight.State == api.RouteInsightActive {
			view.Active = append(view.Active, item)
		} else {
			view.Resolved = append(view.Resolved, item)
		}
	}
	if len(view.Active) > 0 {
		view.CSRF = s.issueDashboardRouteInsightCSRF(w, log, acct)
	}
	return view
}

func (s *server) issueDashboardRouteInsightCSRF(w http.ResponseWriter, log *slog.Logger, acct state.Account) string {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardRouteInsightFixAction, acct.ID, dashboardRouteInsightFixCookie)
	if err != nil {
		log.Error("dashboard renderAppDetail: csrf issue route insight fix", "account_id", acct.ID, "err", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: dashboardRouteInsightFixCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds()),
	})
	return token
}

// dashboardRouteInsightFix handles the app page's "Apply fix" form. It
// verifies a dedicated CSRF envelope, re-checks ownership and applies the
// fix through the same core as POST /v1/apps/{slug}/routes/insights/{id}/fix.
func (s *server) dashboardRouteInsightFix(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardRouteInsightFixAction, acct.ID, dashboardRouteInsightFixCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad form", err.Error()))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	outcome := "error"
	if fixer, ok := s.routeInsightFixer(); ok && api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		_, prob := fixer.Apply(r.Context(), acct, app, r.PathValue("id"), strings.TrimSpace(r.FormValue("fix_id")))
		switch {
		case prob == nil:
			outcome = "fixed"
		case prob.Code == api.CodeRouteInsightFixChanged:
			outcome = "changed"
		default:
			s.log.Info("dashboard route insight fix refused", "app_id", app.ID, "code", prob.Code, "detail", prob.Detail)
		}
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"?insight_fix="+outcome+"#route-insights", http.StatusSeeOther)
}
