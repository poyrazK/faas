package main

// Dashboard surface for the production debugger (ADR-127). The API and CLI
// expose the complete machine-readable contract; this page is the customer
// investigation loop: regressions → request metadata → bounded span evidence
// → an explicit, CSRF-protected metadata-only replay.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	dashboardDebugReplayAction     = "debug_replay"
	dashboardDebugReplayCSRFCookie = "faas_csrf_debug_replay"
	dashboardDebugReplayPollLimit  = 20
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

// renderAppDebug renders the debugger page. Query parameters are
// intentionally small and stable so an incident link can be pasted into a
// ticket: ?since=24h&route=/api&request_id=<uuid>. A comparison link adds
// compare=1&compare_source=<uuid>&compare_mirror=<uuid>.
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
		AppSlug:       app.Slug,
		Plan:          string(acct.Plan),
		PlanAllowed:   limits.DebugTelemetryEnabled,
		Route:         strings.TrimSpace(r.URL.Query().Get("route")),
		ActionMessage: dashboardDebugReplayActionFlash(r),
	}
	data.ActionError = r.URL.Query().Get("action") == "replay_error"
	if s.sessions != nil {
		token, tokenErr := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardDebugReplayAction, acct.ID, dashboardDebugReplayCSRFCookie)
		if tokenErr != nil {
			log.Warn("dashboard debug: issue replay csrf", "account_id", acct.ID, "app_id", app.ID, "err", tokenErr)
		} else {
			data.ReplayCSRF = token
			http.SetCookie(w, &http.Cookie{Name: dashboardDebugReplayCSRFCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
		}
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

	// Seed the compare panel from the most recent deployments that have
	// actually shipped telemetry. The query is intentionally best-effort:
	// older databases and MemStore implementations may not expose the
	// optional compare index yet, but that must not hide request evidence.
	deployRows, err := s.store.ListDeploymentsForCompare(ctx, sqlc.ListDeploymentsForCompareParams{
		AppID:   stringToPgUUID(app.ID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
		Limit:   10,
	})
	if err != nil {
		log.Warn("dashboard renderAppDebug: list compare deployments", "account_id", acct.ID, "app_id", app.ID, "err", err)
	} else {
		data.Deployments = make([]dashboard.DebugDeploymentView, 0, len(deployRows))
		for _, row := range deployRows {
			id := row.DeploymentID.String()
			data.Deployments = append(data.Deployments, dashboard.DebugDeploymentView{
				ID:          id,
				Label:       dashboardDebugDeploymentLabel(id),
				FirstSeenAt: dashboardDebugTimeString(row.FirstSeen),
				LastSeenAt:  dashboardDebugTimeString(row.LastSeen),
				RowCount:    row.RowCount,
			})
		}
	}
	s.populateDashboardDebugCompare(ctx, log, app, data.Deployments, data.Since, data.WindowEnd, r.URL.Query(), &data)
	for i := range data.Regressions {
		data.Regressions[i].CompareURL = dashboardDebugRegressionCompareURL(slug, data.Since, data.Regressions[i].DeploymentID, data.Regressions[i].Route, data.Deployments)
	}

	if rawID := strings.TrimSpace(r.URL.Query().Get("request_id")); rawID != "" {
		if err := s.populateDashboardDebugDetail(ctx, log, app, acct, rawID, since, &data); err != nil {
			data.ErrorMessage = err.Error()
		}
		if data.Selected != nil && data.Selected.Regression != nil {
			data.Selected.Regression.CompareURL = dashboardDebugRegressionCompareURL(slug, data.Since, data.Selected.Regression.DeploymentID, data.Selected.Regression.Route, data.Deployments)
		}
	}
	if replayID := strings.TrimSpace(r.URL.Query().Get("replay_id")); replayID != "" && data.Selected != nil {
		data.ReplayPoll = parseDashboardDebugReplayPoll(r.URL.Query().Get("replay_poll"))
		if err := s.populateDashboardDebugReplay(ctx, app, acct, replayID, data.Selected.Request.ID, &data); err != nil {
			data.ActionMessage = err.Error()
			data.ActionError = true
		} else if data.Replay != nil && (data.Replay.State == "queued" || data.Replay.State == "running") {
			data.ReplayPollActive = data.ReplayPoll < dashboardDebugReplayPollLimit
			data.ReplayPollExhausted = !data.ReplayPollActive
		}
	}
	if err := renderAppDebugPage(w, r, log, acct, appCount, data); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardDebugReplayActionFlash(r *http.Request) string {
	switch r.URL.Query().Get("action") {
	case "replay_queued":
		return "Replay queued. The mirror status will update automatically."
	case "replay_error":
		switch r.URL.Query().Get("error") {
		case api.CodeDebugReplayUnsupported:
			return "Replay unavailable: enable a mirror rule for the deployment that served this request."
		case api.CodeNotFound:
			return "Replay unavailable: the request telemetry was not found or has aged out of retention."
		default:
			return "Replay could not be queued. Please try again shortly."
		}
	default:
		return ""
	}
}

func (s *server) dashboardDebugReplay(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	slug, reqID := r.PathValue("slug"), r.PathValue("req_id")
	if !validSlug(slug) || reqID == "" || strings.Contains(reqID, "/") {
		http.NotFound(w, r)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardDebugReplayAction, acct.ID, dashboardDebugReplayCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid CSRF token", "please reload the page and try again"))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	inv, problem := s.enqueueDebugReplay(r.Context(), app, acct, reqID)
	values := url.Values{
		"request_id": []string{reqID},
		"since":      []string{strings.TrimSpace(r.FormValue("since"))},
		"route":      []string{strings.TrimSpace(r.FormValue("route"))},
	}
	if problem != nil {
		values.Set("action", "replay_error")
		values.Set("error", problem.Code)
	} else {
		values.Set("action", "replay_queued")
		values.Set("replay_id", inv.ID)
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/debug?"+values.Encode(), http.StatusSeeOther)
}

func (s *server) populateDashboardDebugReplay(ctx context.Context, app state.App, acct state.Account, replayID, requestID string, data *dashboard.DebugPageData) error {
	inv, err := s.store.InvocationByID(ctx, replayID)
	if err != nil || inv.AccountID != acct.ID || inv.AppID != app.ID || inv.Source != state.InvocationReplay {
		return fmt.Errorf("replay invocation was not found")
	}
	var metadata map[string]string
	if err := json.Unmarshal(inv.Headers, &metadata); err != nil || metadata[api.DebugReplayRequestIDHeader] != requestID {
		return fmt.Errorf("replay invocation was not found")
	}
	view := &dashboard.DebugReplayView{
		ID:        inv.ID,
		State:     dashboardDebugReplayState(inv.State),
		CreatedAt: inv.CreatedAt.UTC().Format(time.RFC3339),
	}
	if inv.LastError != "" {
		view.LastError = inv.LastError
	}
	if inv.CompletedAt != nil {
		view.CompletedAt = inv.CompletedAt.UTC().Format(time.RFC3339)
	}
	if len(inv.Result) > 0 {
		var result struct {
			SourceStatusCode int  `json:"source_status_code"`
			MirrorStatusCode int  `json:"mirror_status_code"`
			SourceLatencyMS  int  `json:"source_latency_ms"`
			MirrorLatencyMS  int  `json:"mirror_latency_ms"`
			StatusDiff       bool `json:"status_diff"`
			Crashed          bool `json:"crashed"`
		}
		if err := json.Unmarshal(inv.Result, &result); err == nil {
			view.HasResult = true
			view.SourceStatusCode = result.SourceStatusCode
			view.MirrorStatusCode = result.MirrorStatusCode
			view.SourceLatencyMS = result.SourceLatencyMS
			view.MirrorLatencyMS = result.MirrorLatencyMS
			view.StatusDiff = result.StatusDiff
			view.Crashed = result.Crashed
		}
	}
	data.Replay = view
	return nil
}

func dashboardDebugReplayState(invState state.InvocationState) string {
	switch invState {
	case state.InvocationPending:
		return "queued"
	case state.InvocationDispatching:
		return "running"
	default:
		return string(invState)
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
		WakeID:       item.WakeID,
		InstanceID:   item.InstanceID,
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

func dashboardDebugDeploymentLabel(id string) string {
	if len(id) > 8 {
		return "deployment " + id[:8]
	}
	return "deployment " + id
}

func dashboardDebugTimeString(raw interface{}) string {
	switch value := raw.(type) {
	case time.Time:
		return value.UTC().Format(time.RFC3339)
	case *time.Time:
		if value != nil {
			return value.UTC().Format(time.RFC3339)
		}
	case pgtype.Timestamptz:
		if value.Valid {
			return value.Time.UTC().Format(time.RFC3339)
		}
	case string:
		return value
	}
	return ""
}

func dashboardDebugRegressionCompareURL(slug, since, source, route string, deployments []dashboard.DebugDeploymentView) string {
	found := false
	for _, deployment := range deployments {
		if deployment.ID == source {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	for _, deployment := range deployments {
		if deployment.ID == source {
			continue
		}
		values := url.Values{
			"since":          []string{since},
			"compare":        []string{"1"},
			"compare_source": []string{source},
			"compare_mirror": []string{deployment.ID},
			"compare_route":  []string{route},
		}
		return "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode()
	}
	return ""
}

func parseDashboardDebugReplayPoll(raw string) int {
	poll, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || poll < 0 {
		return 0
	}
	if poll > dashboardDebugReplayPollLimit {
		return dashboardDebugReplayPollLimit
	}
	return poll
}

func (s *server) populateDashboardDebugCompare(ctx context.Context, log *slog.Logger, app state.App, deployments []dashboard.DebugDeploymentView, sinceRaw, untilRaw string, query url.Values, data *dashboard.DebugPageData) {
	if len(deployments) < 2 {
		return
	}
	byID := make(map[string]dashboard.DebugDeploymentView, len(deployments))
	for _, deployment := range deployments {
		byID[deployment.ID] = deployment
	}
	sourceID := strings.TrimSpace(query.Get("compare_source"))
	mirrorID := strings.TrimSpace(query.Get("compare_mirror"))
	if sourceID == "" {
		sourceID = deployments[0].ID
	}
	if mirrorID == "" {
		for _, deployment := range deployments {
			if deployment.ID != sourceID {
				mirrorID = deployment.ID
				break
			}
		}
	}
	compare := &dashboard.DebugCompareView{
		SourceID: sourceID,
		MirrorID: mirrorID,
		Route:    strings.TrimSpace(query.Get("compare_route")),
		Compared: query.Get("compare") == "1",
	}
	data.Compare = compare
	if !compare.Compared {
		return
	}
	if _, ok := byID[sourceID]; !ok {
		compare.ErrorMessage = "The selected source deployment is not present in this telemetry window."
		return
	}
	if _, ok := byID[mirrorID]; !ok || mirrorID == sourceID {
		compare.ErrorMessage = "Choose two different deployments with retained telemetry."
		return
	}
	if len(compare.Route) > 256 {
		compare.ErrorMessage = "Compare route must be at most 256 characters."
		return
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		compare.ErrorMessage = "The selected source deployment id is invalid."
		return
	}
	if _, err := uuid.Parse(mirrorID); err != nil {
		compare.ErrorMessage = "The selected mirror deployment id is invalid."
		return
	}
	since := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	until, err := time.Parse(time.RFC3339, untilRaw)
	if err != nil {
		until = time.Now().UTC()
	}
	from := until.Add(-since)
	sourceStats, err := s.fetchRouteStats(ctx, app.ID, stringToPgUUID(sourceID), from, until, compare.Route)
	if err != nil {
		log.Warn("dashboard renderAppDebug: compare source", "app_id", app.ID, "deployment_id", sourceID, "err", err)
		compare.ErrorMessage = "Deployment comparison is temporarily unavailable."
		return
	}
	mirrorStats, err := s.fetchRouteStats(ctx, app.ID, stringToPgUUID(mirrorID), from, until, compare.Route)
	if err != nil {
		log.Warn("dashboard renderAppDebug: compare mirror", "app_id", app.ID, "deployment_id", mirrorID, "err", err)
		compare.ErrorMessage = "Deployment comparison is temporarily unavailable."
		return
	}
	rows := make(map[string]dashboard.DebugCompareRouteView, len(sourceStats)+len(mirrorStats))
	for route, stats := range sourceStats {
		rows[route] = dashboard.DebugCompareRouteView{
			Route:       route,
			SourceP50MS: stats.P50,
			SourceP95MS: stats.P95,
			SourceP99MS: stats.P99,
			SourceN:     stats.N,
		}
	}
	for route, stats := range mirrorStats {
		row := rows[route]
		row.Route = route
		row.MirrorP50MS = stats.P50
		row.MirrorP95MS = stats.P95
		row.MirrorP99MS = stats.P99
		row.MirrorN = stats.N
		rows[route] = row
	}
	compare.Rows = make([]dashboard.DebugCompareRouteView, 0, len(rows))
	for _, row := range rows {
		if row.SourceP95MS > 0 && row.MirrorP95MS > 0 {
			row.DeltaP95MS = row.SourceP95MS - row.MirrorP95MS
			row.Factor = fmt.Sprintf("%.2fx", float64(row.SourceP95MS)/float64(row.MirrorP95MS))
		}
		compare.Rows = append(compare.Rows, row)
	}
	sort.Slice(compare.Rows, func(i, j int) bool { return compare.Rows[i].Route < compare.Rows[j].Route })
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
	timeline, timelineErr := s.buildDebugRequestTimeline(ctx, app.ID, item, apiRegression)
	if timelineErr != nil {
		log.Warn("dashboard renderAppDebug: get timeline", "account_id", acct.ID, "app_id", app.ID, "request_id", rawID, "err", timelineErr)
		return fmt.Errorf("request timeline is temporarily unavailable")
	}
	timelineViews := make([]dashboard.DebugTimelineEventView, 0, len(timeline))
	for _, event := range timeline {
		timelineViews = append(timelineViews, dashboard.DebugTimelineEventView{
			At:          event.At,
			Phase:       event.Phase,
			Kind:        event.Kind,
			Actor:       event.Actor,
			Summary:     event.Summary,
			DurationMS:  event.DurationMS,
			Status:      event.Status,
			Approximate: event.Approximate,
		})
	}
	data.Selected = &dashboard.DebugRequestDetailView{
		Request:        request,
		Regression:     matching,
		Timeline:       timelineViews,
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
