package main

// Dashboard surface for the production debugger (ADR-127). The API and CLI
// expose the complete machine-readable contract; this page is the customer
// investigation loop: regressions → request metadata → bounded span evidence.
// It deliberately remains read-only in the first dashboard slice so a stale
// browser cannot accidentally enqueue a replay.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// parseAppDebugPath recognizes /dashboard/apps/{slug}/debug (with an
// optional trailing slash) and leaves deeper drill-down paths for a future
// sibling route instead of accidentally treating them as an app slug.
func parseAppDebugPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/debug"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") || !validSlug(slug) {
		return "", false
	}
	return slug, true
}

// renderAppDebug renders the read-only debugger page. Query parameters are
// intentionally small and stable so an incident link can be pasted into a
// ticket: ?since=24h&route=/api&request_id=<uuid>.
func (s *server) renderAppDebug(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppDebug: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}

	limits := api.MustLimitsFor(acct.Plan)
	data := dashboard.DebugPageData{
		AppSlug:     app.Slug,
		Plan:        string(acct.Plan),
		PlanAllowed: limits.DebugTelemetryEnabled,
		Route:       strings.TrimSpace(r.URL.Query().Get("route")),
	}
	if !data.PlanAllowed {
		data.ErrorMessage = "Production debugging is available on Hobby and higher plans."
		if err := renderAppDebugPage(w, r, log, acct, appCount, data); err != nil {
			renderProblem(w, log, err)
		}
		return
	}
	if len(data.Route) > 256 {
		api.WriteProblem(w, api.ErrValidation("route must be at most 256 characters"))
		return
	}

	now := time.Now().UTC()
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if retention > 0 && since > retention {
		since = retention
		data.WindowClamped = true
	}
	data.Since = echoDebugSince(sinceRaw, since)
	data.WindowStart = now.Add(-since).Format(time.RFC3339)
	data.WindowEnd = now.Format(time.RFC3339)

	rows, err := s.store.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
		AppID:        stringToPgUUID(app.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-since), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:        50,
		Route:        data.Route,
	})
	if err != nil {
		log.Warn("dashboard renderAppDebug: list requests", "account_id", acct.ID, "app_id", app.ID, "err", err)
		data.ErrorMessage = "Debugger telemetry is temporarily unavailable. Please try again shortly."
	} else {
		data.Requests = make([]dashboard.DebugRequestView, 0, len(rows))
		for _, row := range rows {
			data.Requests = append(data.Requests, dashboardDebugRequestView(debugTelemetryRowToItem(row), app.Slug, data.Since, data.Route))
		}
	}

	regRows, err := s.store.ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID:   stringToPgUUID(app.ID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
	})
	if err != nil {
		log.Warn("dashboard renderAppDebug: list regressions", "account_id", acct.ID, "app_id", app.ID, "err", err)
	} else {
		data.Regressions = make([]dashboard.DebugRegressionView, 0, len(regRows))
		for _, row := range regRows {
			reg := debugRegressionRowToItem(row)
			data.Regressions = append(data.Regressions, dashboardDebugRegressionView(reg, app.Slug, data.Since))
		}
	}

	if rawID := strings.TrimSpace(r.URL.Query().Get("request_id")); rawID != "" {
		if err := s.populateDashboardDebugDetail(ctx, log, app, acct, rawID, since, &data); err != nil {
			data.ErrorMessage = err.Error()
		}
	}
	if err := renderAppDebugPage(w, r, log, acct, appCount, data); err != nil {
		renderProblem(w, log, err)
	}
}

func renderAppDebugPage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, appCount int, data dashboard.DebugPageData) error {
	view, _ := AccountFrom(r.Context())
	return dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), dashboard.Page{
		Title:   data.AppSlug + " debugger",
		Body:    "app_debug",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	})
}

func dashboardDebugRequestView(item api.DebugTelemetryRequestItem, slug, since, route string) dashboard.DebugRequestView {
	values := url.Values{"since": []string{since}}
	if route != "" {
		values.Set("route", route)
	}
	values.Set("request_id", item.ID)
	return dashboard.DebugRequestView{
		ID:           item.ID,
		DeploymentID: item.DeploymentID,
		Route:        item.Route,
		Method:       item.Method,
		Status:       item.Status,
		LatencyMS:    item.LatencyMS,
		Count:        item.Count,
		ColdBoot:     item.ColdBoot,
		TraceID:      valueOrEmpty(item.TraceID),
		ReceivedAt:   item.ReceivedAt,
		DetailURL:    "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode(),
	}
}

func dashboardDebugRegressionView(item api.DebugRegressionItem, slug, since string) dashboard.DebugRegressionView {
	values := url.Values{"since": []string{since}, "route": []string{item.Route}}
	return dashboard.DebugRegressionView{
		DeploymentID:    item.DeploymentID,
		Route:           item.Route,
		P95MS:           item.P95MS,
		P95BaseMS:       item.P95BaseMS,
		AffectedCount:   item.AffectedCount,
		Factor:          item.Factor,
		FirstDetectedAt: item.FirstDetectedAt,
		LastDetectedAt:  item.LastDetectedAt,
		RequestsURL:     "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode(),
	}
}

func (s *server) populateDashboardDebugDetail(ctx context.Context, log *slog.Logger, app state.App, acct state.Account, rawID string, since time.Duration, data *dashboard.DebugPageData) error {
	parsedID, err := uuid.Parse(rawID)
	if err != nil {
		return fmt.Errorf("request id must be a UUID")
	}
	now := time.Now().UTC()
	row, err := s.store.GetRequestTelemetryByAppAndID(ctx, sqlc.GetRequestTelemetryByAppAndIDParams{
		AppID:        stringToPgUUID(app.ID),
		ID:           pgtype.UUID{Bytes: parsedID, Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: now.Add(-since), Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("request telemetry was not found or has aged out of retention")
	}
	if err != nil {
		log.Warn("dashboard renderAppDebug: get request", "account_id", acct.ID, "app_id", app.ID, "request_id", rawID, "err", err)
		return fmt.Errorf("request evidence is temporarily unavailable")
	}

	item := debugTelemetryGetRowToItem(row)
	request := dashboardDebugRequestView(item, app.Slug, data.Since, data.Route)
	spans, truncated := parseDebugEvidenceSpans(row.SpansSummary)
	spanViews := make([]dashboard.DebugSpanView, 0, len(spans))
	for _, span := range spans {
		spanViews = append(spanViews, dashboard.DebugSpanView{
			Name:        span.Name,
			Kind:        span.Kind,
			DurationMS:  int64(span.DurationNanos / 1_000_000),
			Status:      span.Status,
			DBStatement: span.DBStatement,
			TraceID:     span.TraceID,
			SpanID:      span.SpanID,
		})
	}

	var matching *dashboard.DebugRegressionView
	regRows, err := s.store.ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID:   stringToPgUUID(app.ID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
	})
	if err == nil {
		for _, regRow := range regRows {
			reg := debugRegressionRowToItem(regRow)
			if reg.DeploymentID == item.DeploymentID && reg.Route == item.Route {
				view := dashboardDebugRegressionView(reg, app.Slug, data.Since)
				matching = &view
				break
			}
		}
	}
	var apiRegression *api.DebugRegressionItem
	if matching != nil {
		apiRegression = &api.DebugRegressionItem{DeploymentID: matching.DeploymentID, Route: matching.Route, P95MS: matching.P95MS, P95BaseMS: matching.P95BaseMS, AffectedCount: matching.AffectedCount, Factor: matching.Factor, FirstDetectedAt: matching.FirstDetectedAt, LastDetectedAt: matching.LastDetectedAt}
	}
	explanation := buildDebugEvidenceExplanation(item, apiRegression, spans)
	data.Selected = &dashboard.DebugRequestDetailView{
		Request:        request,
		Regression:     matching,
		Spans:          spanViews,
		SpansTruncated: truncated,
		Explanation:    explanation.Headline,
		EvidenceStatus: explanation.Status,
		GeneratedAt:    now.Format(time.RFC3339),
	}
	return nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
