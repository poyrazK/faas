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
	"github.com/onebox-faas/faas/pkg/debugger"
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
// ticket: ?since=24h&route=/api&status=500&request_id=<uuid>. A comparison
// link adds compare=1&compare_source=<uuid>&compare_mirror=<uuid>.
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
	filters, filterErr := parseDebugTelemetryFilters(r.URL.Query())
	if filterErr != nil {
		api.WriteProblem(w, api.ErrValidation(filterErr.Error()))
		return
	}
	data.DeploymentID = filters.DeploymentID
	data.Status = filters.Status
	data.ColdBoot = filters.ColdBoot
	if filters.ColdBoot != nil {
		data.ColdBootFilter = strconv.FormatBool(*filters.ColdBoot)
	}
	data.ConsumerID = filters.ConsumerID
	data.MinLatencyMS = filters.MinLatencyMS

	cursorRaw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if len(cursorRaw) > 8192 {
		api.WriteProblem(w, api.ErrValidation("cursor must be at most 8192 characters"))
		return
	}
	decodedCursor, cursorErr := decodeDebugTelemetryCursor(cursorRaw)
	if cursorErr != nil {
		api.WriteProblem(w, api.ErrValidation("cursor is invalid; restart the request list"))
		return
	}
	if decodedCursor.Version != 0 && (decodedCursor.AppID != app.ID || decodedCursor.Route != data.Route || !filters.same(decodedCursor.filters())) {
		api.WriteProblem(w, api.ErrValidation("cursor does not match this app or filters"))
		return
	}
	data.Cursor = cursorRaw

	now := time.Now().UTC()
	sinceRaw := strings.TrimSpace(r.URL.Query().Get("since"))
	since := parseDebugSinceFromString(sinceRaw, 24*time.Hour)
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	if retention > 0 && since > retention {
		since = retention
		data.WindowClamped = true
	}
	windowStart := now.Add(-since)
	windowEnd := now
	if decodedCursor.Version != 0 {
		if decodedCursor.WindowEnd.After(now.Add(time.Minute)) || decodedCursor.WindowStart.Before(now.Add(-retention)) {
			api.WriteProblem(w, api.ErrValidation("cursor has expired; restart the request list"))
			return
		}
		if sinceRaw != "" && since != decodedCursor.WindowEnd.Sub(decodedCursor.WindowStart) {
			api.WriteProblem(w, api.ErrValidation("cursor does not match since; restart the request list"))
			return
		}
		windowStart = decodedCursor.WindowStart.UTC()
		windowEnd = decodedCursor.WindowEnd.UTC()
		since = windowEnd.Sub(windowStart)
		data.WindowClamped = decodedCursor.RetentionClamped || data.WindowClamped
	}
	data.Since = echoDebugSince(sinceRaw, since)
	data.WindowStart = windowStart.Format(time.RFC3339Nano)
	data.WindowEnd = windowEnd.Format(time.RFC3339Nano)
	running, runningErr := s.readDebugRunning(ctx, app, windowStart, windowEnd, 20, limits.IdleTimeoutS)
	if runningErr != nil {
		log.Warn("dashboard renderAppDebug: running explanation", "account_id", acct.ID, "app_id", app.ID, "err", runningErr)
		data.RunningError = "Running-state observations are temporarily unavailable. Please try again shortly."
	} else {
		running.Since = data.Since
		running.RetentionClamped = data.WindowClamped
		data.Running = dashboardDebugRunningView(running, app.Slug)
	}

	coverage, coverageErr := s.store.RequestTelemetryCoverage(ctx, sqlc.RequestTelemetryCoverageParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: windowEnd, Valid: true},
	})
	if coverageErr != nil {
		// Coverage is an enrichment card. Keep the request investigation
		// usable when an older database has not applied the aggregate query.
		log.Warn("dashboard renderAppDebug: coverage", "account_id", acct.ID, "app_id", app.ID, "err", coverageErr)
	} else {
		data.Coverage = dashboardDebugCoverageView(coverage, data.Since, data.WindowStart, data.WindowEnd, limits.DebugTelemetryRetentionDays)
	}

	dependencyRows, dependencyErr := s.store.ListRequestTelemetryDependencySpans(ctx, sqlc.ListRequestTelemetryDependencySpansParams{
		AppID:        stringToPgUUID(app.ID),
		AccountID:    stringToPgUUID(acct.ID),
		ReceivedAt:   pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: windowEnd, Valid: true},
		Limit:        debugDependencyHistoryMaxRows + 1,
	})
	if dependencyErr != nil {
		log.Warn("dashboard renderAppDebug: dependency latency", "account_id", acct.ID, "app_id", app.ID, "err", dependencyErr)
	} else {
		dependencyRowsTruncated := len(dependencyRows) > debugDependencyHistoryMaxRows
		if dependencyRowsTruncated {
			dependencyRows = dependencyRows[:debugDependencyHistoryMaxRows]
		}
		dependencies, aggregationTruncated, representedRequests, spanSamples := buildDebugDependencyLatencyHistory(dependencyRows, windowStart, windowEnd)
		dependencyTruncated := dependencyRowsTruncated || aggregationTruncated
		data.DependencyHistory = dashboardDebugDependencyLatencyHistoryView(api.DebugDependencyLatencyResponse{
			Since:               data.Since,
			WindowStart:         data.WindowStart,
			WindowEnd:           data.WindowEnd,
			RetentionClamped:    data.WindowClamped,
			Complete:            !dependencyTruncated,
			Truncated:           dependencyTruncated,
			TelemetryRows:       int64(len(dependencyRows)),
			RepresentedRequests: representedRequests,
			SpanSamples:         spanSamples,
			Dependencies:        dependencies,
		})
		criticalPaths, pathTruncated, pathComplete, pathRepresentedRequests, pathSamples := buildDebugCriticalPathHistory(dependencyRows, windowStart, windowEnd)
		pathTruncated = pathTruncated || dependencyRowsTruncated
		data.CriticalPathHistory = dashboardDebugCriticalPathHistoryView(api.DebugCriticalPathHistoryResponse{
			Since:               data.Since,
			WindowStart:         data.WindowStart,
			WindowEnd:           data.WindowEnd,
			Complete:            pathComplete && !pathTruncated,
			Truncated:           pathTruncated,
			TelemetryRows:       int64(len(dependencyRows)),
			RepresentedRequests: pathRepresentedRequests,
			PathSamples:         pathSamples,
			CriticalPaths:       criticalPaths,
		}, app.Slug)
	}

	cursorReceivedAt, cursorID := debugTelemetryCursorParams(decodedCursor)
	rows, err := s.store.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
		AppID:             stringToPgUUID(app.ID),
		ReceivedAt:        pgtype.Timestamptz{Time: windowStart, Valid: true},
		ReceivedAt_2:      pgtype.Timestamptz{Time: windowEnd, Valid: true},
		CursorReceivedAt:  cursorReceivedAt,
		CursorID:          cursorID,
		Limit:             51,
		Route:             data.Route,
		DeploymentID:      filters.DeploymentID,
		StatusFilter:      int32(filters.Status),
		ColdBootFilter:    filters.sqlColdBootFilter(),
		ConsumerID:        filters.sqlConsumerID(),
		ConsumerAnonymous: filters.sqlConsumerAnonymous(),
		MinLatencyMs:      int32(filters.MinLatencyMS),
	})
	if err != nil {
		log.Warn("dashboard renderAppDebug: list requests", "account_id", acct.ID, "app_id", app.ID, "err", err)
		data.ErrorMessage = "Debugger telemetry is temporarily unavailable. Please try again shortly."
	} else {
		hasMore := len(rows) > 50
		if hasMore {
			rows = rows[:50]
		}
		data.Requests = make([]dashboard.DebugRequestView, 0, len(rows))
		for _, row := range rows {
			data.Requests = append(data.Requests, dashboardDebugRequestView(debugTelemetryRowToItem(row), app.Slug, data.Since, data.Route, data.Cursor, filters))
		}
		data.NextCursor = ""
		if hasMore && len(rows) > 0 {
			data.NextCursor = encodeDebugTelemetryCursorWithFilters(app.ID, data.Route, filters.cursor(), windowStart, windowEnd, data.WindowClamped, rows[len(rows)-1])
		}
		data.Complete = !hasMore || data.NextCursor == ""
		if data.NextCursor != "" {
			values := debugTelemetryFilterValues(data.Since, data.Route, data.Cursor, filters)
			values.Set("cursor", data.NextCursor)
			data.NextPageURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/debug?" + values.Encode() + "#requests"
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
			data.Regressions = append(data.Regressions, dashboardDebugRegressionView(reg, app.Slug, data.Since, filters))
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
		data.Regressions[i].CompareURL = dashboardDebugRegressionCompareURL(slug, data.Since, data.Regressions[i].DeploymentID, data.Regressions[i].Route, data.Deployments, filters)
	}

	if rawID := strings.TrimSpace(r.URL.Query().Get("request_id")); rawID != "" {
		if err := s.populateDashboardDebugDetail(ctx, log, app, acct, rawID, since, &data); err != nil {
			data.ErrorMessage = err.Error()
		}
		if data.Selected != nil && data.Selected.Regression != nil {
			data.Selected.Regression.CompareURL = dashboardDebugRegressionCompareURL(slug, data.Since, data.Selected.Regression.DeploymentID, data.Selected.Regression.Route, data.Deployments, filters)
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
	result, problem := s.enqueueDebugReplay(r.Context(), app, acct, reqID, "")
	values := url.Values{
		"request_id":     []string{reqID},
		"since":          []string{strings.TrimSpace(r.FormValue("since"))},
		"route":          []string{strings.TrimSpace(r.FormValue("route"))},
		"cursor":         []string{strings.TrimSpace(r.FormValue("cursor"))},
		"deployment_id":  []string{strings.TrimSpace(r.FormValue("deployment_id"))},
		"status":         []string{strings.TrimSpace(r.FormValue("status"))},
		"cold_boot":      []string{strings.TrimSpace(r.FormValue("cold_boot"))},
		"consumer_id":    []string{strings.TrimSpace(r.FormValue("consumer_id"))},
		"min_latency_ms": []string{strings.TrimSpace(r.FormValue("min_latency_ms"))},
	}
	if problem != nil {
		values.Set("action", "replay_error")
		values.Set("error", problem.Code)
	} else {
		values.Set("action", "replay_queued")
		values.Set("replay_id", result.Invocation.ID)
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

func dashboardDebugCoverageView(row sqlc.RequestTelemetryCoverageRow, since, windowStart, windowEnd string, retentionDays int) *dashboard.DebugCoverageView {
	return &dashboard.DebugCoverageView{
		Since:               since,
		WindowStart:         windowStart,
		WindowEnd:           windowEnd,
		PlanRetentionDays:   retentionDays,
		TelemetryRows:       row.TelemetryRows,
		RepresentedRequests: row.RepresentedRequests,
		ErrorRequests:       row.ErrorRequests,
		TraceLinked:         dashboardDebugCoverageSignalView(row.TraceLinkedRows, row.TraceLinkedRequests, row.RepresentedRequests),
		SpanEvidence:        dashboardDebugCoverageSignalView(row.SpanEvidenceRows, row.SpanEvidenceRequests, row.RepresentedRequests),
		WakeEvidence:        dashboardDebugCoverageSignalView(row.WakeEvidenceRows, row.WakeEvidenceRequests, row.RepresentedRequests),
		GuestEvidence:       dashboardDebugCoverageSignalView(row.GuestEvidenceRows, row.GuestEvidenceRequests, row.RepresentedRequests),
		OldestTelemetryAt:   debugCoverageTimestamp(row.OldestTelemetryAt),
		LatestTelemetryAt:   debugCoverageTimestamp(row.LatestTelemetryAt),
	}
}

func dashboardDebugCoverageSignalView(rows, requests, total int64) dashboard.DebugCoverageSignalView {
	rate := float64(0)
	if total > 0 {
		rate = float64(requests) * 100 / float64(total)
	}
	return dashboard.DebugCoverageSignalView{Rows: rows, Requests: requests, RatePct: rate}
}

func dashboardDebugDependencyLatencyHistoryView(response api.DebugDependencyLatencyResponse) *dashboard.DebugDependencyLatencyHistoryView {
	view := &dashboard.DebugDependencyLatencyHistoryView{
		Since:               response.Since,
		WindowStart:         response.WindowStart,
		WindowEnd:           response.WindowEnd,
		Complete:            response.Complete,
		Truncated:           response.Truncated,
		TelemetryRows:       response.TelemetryRows,
		RepresentedRequests: response.RepresentedRequests,
		SpanSamples:         response.SpanSamples,
		Dependencies:        make([]dashboard.DebugDependencyLatencyHistoryItemView, 0, len(response.Dependencies)),
	}
	for _, dependency := range response.Dependencies {
		view.Dependencies = append(view.Dependencies, dashboard.DebugDependencyLatencyHistoryItemView{
			Type:                 dependency.Type,
			Kind:                 dependency.Kind,
			Name:                 dependency.Name,
			Calls:                dependency.Calls,
			ErrorCalls:           dependency.ErrorCalls,
			ErrorRatePct:         dependency.ErrorRatePct,
			P50MS:                dependency.P50MS,
			P95MS:                dependency.P95MS,
			P99MS:                dependency.P99MS,
			BaselineP95MS:        dependency.BaselineP95MS,
			CurrentP95MS:         dependency.CurrentP95MS,
			P95DeltaMS:           dependency.P95DeltaMS,
			RegressionFactor:     dependency.RegressionFactor,
			Regression:           dependency.Regression,
			BaselineErrorRatePct: dependency.BaselineErrorRatePct,
			CurrentErrorRatePct:  dependency.CurrentErrorRatePct,
			ErrorRateDeltaPct:    dependency.ErrorRateDeltaPct,
		})
	}
	return view
}

func dashboardDebugCriticalPathHistoryView(response api.DebugCriticalPathHistoryResponse, slug string) *dashboard.DebugCriticalPathHistoryView {
	view := &dashboard.DebugCriticalPathHistoryView{
		Since:               response.Since,
		WindowStart:         response.WindowStart,
		WindowEnd:           response.WindowEnd,
		Complete:            response.Complete,
		Truncated:           response.Truncated,
		TelemetryRows:       response.TelemetryRows,
		RepresentedRequests: response.RepresentedRequests,
		PathSamples:         response.PathSamples,
		CriticalPaths:       make([]dashboard.DebugCriticalPathHistoryItemView, 0, len(response.CriticalPaths)),
	}
	for _, path := range response.CriticalPaths {
		pathView := dashboard.DebugCriticalPathHistoryItemView{
			Signature:                  path.Signature,
			Segments:                   make([]dashboard.DebugCriticalPathSegmentView, 0, len(path.Segments)),
			DominantSegmentExclusiveMS: path.DominantSegmentExclusiveMS,
			Calls:                      path.Calls,
			ErrorCalls:                 path.ErrorCalls,
			ErrorRatePct:               path.ErrorRatePct,
			P50MS:                      path.P50MS,
			P95MS:                      path.P95MS,
			P99MS:                      path.P99MS,
			BaselineCalls:              path.BaselineCalls,
			CurrentCalls:               path.CurrentCalls,
			BaselineP95MS:              path.BaselineP95MS,
			CurrentP95MS:               path.CurrentP95MS,
			P95DeltaMS:                 path.P95DeltaMS,
			RegressionFactor:           path.RegressionFactor,
			Regression:                 path.Regression,
			BaselineErrorRatePct:       path.BaselineErrorRatePct,
			CurrentErrorRatePct:        path.CurrentErrorRatePct,
			ErrorRateDeltaPct:          path.ErrorRateDeltaPct,
		}
		for _, segment := range path.Segments {
			pathView.Segments = append(pathView.Segments, dashboard.DebugCriticalPathSegmentView{
				Type: segment.Type,
				Kind: segment.Kind,
				Name: segment.Name,
			})
		}
		if path.DominantSegment != nil {
			pathView.DominantSegment = &dashboard.DebugCriticalPathSegmentView{
				Type: path.DominantSegment.Type,
				Kind: path.DominantSegment.Kind,
				Name: path.DominantSegment.Name,
			}
		}
		pathView.Exemplars = make([]dashboard.DebugCriticalPathExemplarView, 0, len(path.Exemplars))
		for _, exemplar := range path.Exemplars {
			exemplarView := dashboard.DebugCriticalPathExemplarView{
				RequestID:                  exemplar.RequestID,
				TraceID:                    exemplar.TraceID,
				Window:                     exemplar.Window,
				ReceivedAt:                 exemplar.ReceivedAt,
				DurationMS:                 exemplar.DurationMS,
				HTTPStatus:                 exemplar.HTTPStatus,
				Error:                      exemplar.Error,
				Count:                      exemplar.Count,
				DominantSegmentExclusiveMS: exemplar.DominantSegmentExclusiveMS,
			}
			if exemplar.DominantSegment != nil {
				exemplarView.DominantSegment = &dashboard.DebugCriticalPathSegmentView{
					Type: exemplar.DominantSegment.Type,
					Kind: exemplar.DominantSegment.Kind,
					Name: exemplar.DominantSegment.Name,
				}
			}
			values := url.Values{"since": []string{response.Since}, "request_id": []string{exemplar.RequestID}}
			exemplarView.RequestURL = "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode() + "#request-detail"
			pathView.Exemplars = append(pathView.Exemplars, exemplarView)
		}
		values := url.Values{"since": []string{response.Since}, "min_latency_ms": []string{strconv.FormatInt(path.CurrentP95MS, 10)}}
		pathView.RequestsURL = "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode() + "#requests"
		view.CriticalPaths = append(view.CriticalPaths, pathView)
	}
	return view
}

func dashboardDebugRunningView(response api.DebugRunningResponse, slug string) *dashboard.DebugRunningView {
	view := &dashboard.DebugRunningView{
		Since:             response.Since,
		WindowStart:       response.WindowStart,
		WindowEnd:         response.WindowEnd,
		RetentionClamped:  response.RetentionClamped,
		CurrentObservedAt: response.CurrentObservedAt,
		Config: dashboard.DebugRunningConfigView{
			ConfiguredMinInstances: response.Config.ConfiguredMinInstances,
			EffectiveMinInstances:  response.Config.EffectiveMinInstances,
			PrewarmMinInstances:    response.Config.PrewarmMinInstances,
			IdleTimeoutSeconds:     response.Config.IdleTimeoutSeconds,
		},
		HistoryTruncated: response.HistoryTruncated,
		HasObservation:   len(response.History) > 0,
		CLICommand:       fmt.Sprintf("gregale debug running --since %s %s", response.Since, slug),
		Current:          dashboardDebugRunningCauseViews(response.Current, slug, response.Since),
		History:          make([]dashboard.DebugRunningObservationView, 0, len(response.History)),
	}
	for _, observation := range response.History {
		view.History = append(view.History, dashboard.DebugRunningObservationView{
			ObservedAt:             observation.ObservedAt,
			RunningInstances:       observation.RunningInstances,
			ConfiguredMinInstances: observation.ConfiguredMinInstances,
			EffectiveMinInstances:  observation.EffectiveMinInstances,
			PrewarmMinInstances:    observation.PrewarmMinInstances,
			IdleTimeoutSeconds:     observation.IdleTimeoutSeconds,
			Degraded:               observation.Degraded,
			Causes:                 dashboardDebugRunningCauseViews(observation.Causes, slug, response.Since),
		})
	}
	return view
}

func dashboardDebugRunningCauseViews(causes []api.DebugRunningCause, slug, since string) []dashboard.DebugRunningCauseView {
	views := make([]dashboard.DebugRunningCauseView, 0, len(causes))
	for _, cause := range causes {
		view := dashboard.DebugRunningCauseView{
			Code:                 cause.Code,
			Label:                dashboardDebugRunningCauseLabel(cause.Code),
			Summary:              cause.Summary,
			InstanceCount:        cause.InstanceCount,
			OpenConnections:      cause.OpenConnections,
			TailTasks:            cause.TailTasks,
			Mode:                 cause.Mode,
			WorkloadClass:        cause.WorkloadClass,
			LastActivityAt:       cause.LastActivityAt,
			IdleDeadline:         cause.IdleDeadline,
			FlowTopologyDegraded: cause.FlowTopologyDegraded,
		}
		if len(cause.FlowTopology) > 0 {
			view.FlowTopology = make([]dashboard.DebugRunningFlowView, 0, len(cause.FlowTopology))
			for _, flow := range cause.FlowTopology {
				view.FlowTopology = append(view.FlowTopology, dashboard.DebugRunningFlowView{
					InstanceID: flow.InstanceID,
					Protocol:   flow.Protocol,
					RemoteIP:   flow.RemoteIP,
					RemotePort: flow.RemotePort,
					State:      flow.State,
					Direction:  flow.Direction,
					Count:      flow.Count,
				})
			}
		}
		if cause.Request != nil {
			request := cause.Request
			values := url.Values{}
			values.Set("request_id", request.TelemetryID)
			if since != "" {
				values.Set("since", since)
			}
			view.Request = &dashboard.DebugRunningRequestView{
				TelemetryID:  request.TelemetryID,
				DeploymentID: request.DeploymentID,
				Route:        request.Route,
				Method:       request.Method,
				TraceID:      valueOrEmpty(request.TraceID),
				ReceivedAt:   request.ReceivedAt,
				Count:        request.Count,
				WakeID:       request.WakeID,
				InstanceID:   request.InstanceID,
				MatchDeltaMS: request.MatchDeltaMS,
				RequestURL:   "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode() + "#request-detail",
			}
		}
		views = append(views, view)
	}
	return views
}

func dashboardDebugRunningCauseLabel(code string) string {
	switch code {
	case api.DebugRunningReasonRequestActivity:
		return "Request activity"
	case api.DebugRunningReasonOpenConnection:
		return "Open connection"
	case api.DebugRunningReasonTailTasks:
		return "Background tasks"
	case api.DebugRunningReasonMinInstances:
		return "Configured minimum"
	case api.DebugRunningReasonPrewarmFloor:
		return "Prewarm floor"
	case api.DebugRunningReasonScaleInCooldown:
		return "Scale-in cooldown"
	case api.DebugRunningReasonWorkloadMode:
		return "Workload mode"
	case api.DebugRunningReasonStartupGrace:
		return "Startup grace"
	case api.DebugRunningReasonUnknownActivity:
		return "Activity signal unavailable"
	case api.DebugRunningReasonNoBlockerObserved:
		return "No blocker observed"
	default:
		return "Observed cause"
	}
}

func dashboardDebugRequestView(item api.DebugTelemetryRequestItem, slug, since, route, cursor string, filters debugTelemetryFilters) dashboard.DebugRequestView {
	values := debugTelemetryFilterValues(since, route, cursor, filters)
	values.Set("request_id", item.ID)
	return dashboard.DebugRequestView{
		ID:              item.ID,
		DeploymentID:    item.DeploymentID,
		Route:           item.Route,
		Method:          item.Method,
		Status:          item.Status,
		LatencyMS:       item.LatencyMS,
		Count:           item.Count,
		ColdBoot:        item.ColdBoot,
		TraceID:         valueOrEmpty(item.TraceID),
		WakeID:          item.WakeID,
		InstanceID:      item.InstanceID,
		ReceivedAt:      item.ReceivedAt,
		GuestRuntime:    valueOrEmptyGuestRuntime(item.Guest),
		GuestDurationMS: valueOrGuestDuration(item.Guest),
		GuestOutcome:    valueOrEmptyGuestOutcome(item.Guest),
		GuestErrorClass: valueOrEmptyGuestErrorClass(item.Guest),
		ConsumerID:      item.ConsumerID,
		DetailURL:       "/dashboard/apps/" + url.PathEscape(slug) + "/debug?" + values.Encode(),
	}
}

func debugTelemetryFilterValues(since, route, cursor string, filters debugTelemetryFilters) url.Values {
	values := url.Values{"since": []string{since}}
	if route != "" {
		values.Set("route", route)
	}
	if filters.DeploymentID != "" {
		values.Set("deployment_id", filters.DeploymentID)
	}
	if filters.Status != 0 {
		values.Set("status", strconv.Itoa(filters.Status))
	}
	if filters.ColdBoot != nil {
		values.Set("cold_boot", strconv.FormatBool(*filters.ColdBoot))
	}
	if filters.ConsumerID != "" {
		values.Set("consumer_id", filters.ConsumerID)
	}
	if filters.MinLatencyMS != 0 {
		values.Set("min_latency_ms", strconv.Itoa(filters.MinLatencyMS))
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	return values
}

func valueOrEmptyGuestRuntime(guest *api.DebugGuestExecutionEvidence) string {
	if guest == nil {
		return ""
	}
	return guest.Runtime
}

func valueOrGuestDuration(guest *api.DebugGuestExecutionEvidence) int {
	if guest == nil {
		return 0
	}
	return guest.DurationMS
}

func valueOrEmptyGuestOutcome(guest *api.DebugGuestExecutionEvidence) string {
	if guest == nil {
		return ""
	}
	return guest.Outcome
}

func valueOrEmptyGuestErrorClass(guest *api.DebugGuestExecutionEvidence) string {
	if guest == nil {
		return ""
	}
	return guest.ErrorClass
}

func dashboardDebugRegressionView(item api.DebugRegressionItem, slug, since string, filters debugTelemetryFilters) dashboard.DebugRegressionView {
	values := debugTelemetryFilterValues(since, item.Route, "", filters)
	values.Set("deployment_id", item.DeploymentID)
	return dashboard.DebugRegressionView{
		DeploymentID:    item.DeploymentID,
		Route:           item.Route,
		State:           item.State,
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

func dashboardDebugRegressionCompareURL(slug, since, source, route string, deployments []dashboard.DebugDeploymentView, filterArgs ...debugTelemetryFilters) string {
	var filters debugTelemetryFilters
	if len(filterArgs) > 0 {
		filters = filterArgs[0]
	}
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
		values := debugTelemetryFilterValues(since, "", "", filters)
		values.Set("compare", "1")
		values.Set("compare_source", source)
		values.Set("compare_mirror", deployment.ID)
		values.Set("compare_route", route)
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
	identifier, err := normalizeDebugRequestIdentifier(rawID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	row, err := s.store.GetRequestTelemetryByAppAndIdentifier(ctx, sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID:         stringToPgUUID(app.ID),
		Identifier:    identifier,
		ReceivedFrom:  pgtype.Timestamptz{Time: now.Add(-since), Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("request telemetry was not found or has aged out of retention")
	}
	if err != nil {
		log.Warn("dashboard renderAppDebug: get request", "account_id", acct.ID, "app_id", app.ID, "request_id", rawID, "err", err)
		return fmt.Errorf("request evidence is temporarily unavailable")
	}

	item := debugTelemetryGetRowToItem(row)
	request := dashboardDebugRequestView(item, app.Slug, data.Since, data.Route, data.Cursor, debugTelemetryFilters{
		DeploymentID: data.DeploymentID,
		Status:       data.Status,
		ColdBoot:     data.ColdBoot,
		ConsumerID:   data.ConsumerID,
		MinLatencyMS: data.MinLatencyMS,
	})
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
	waterfall, waterfallComplete := buildDebugWaterfall(spans)
	criticalPath := buildDebugCriticalPath(spans)
	if criticalPath != nil && truncated {
		criticalPath.Complete = false
	}
	dependencyLatency, dependencyLatencyTruncated := buildDebugDependencyLatency(spans)
	dependencyViews := make([]dashboard.DebugDependencyLatencyView, 0, len(dependencyLatency))
	for _, dependency := range dependencyLatency {
		dependencyViews = append(dependencyViews, dashboard.DebugDependencyLatencyView{
			Type:            dependency.Type,
			Kind:            dependency.Kind,
			Name:            dependency.Name,
			Calls:           dependency.Calls,
			Errors:          dependency.Errors,
			TotalDurationMS: dependency.TotalDurationMS,
			MaxDurationMS:   dependency.MaxDurationMS,
		})
	}

	var matching *dashboard.DebugRegressionView
	var regressionErr error
	regRows, err := s.store.ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID:   stringToPgUUID(app.ID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
	})
	if err == nil {
		for _, regRow := range regRows {
			reg := debugRegressionRowToItem(regRow)
			if reg.DeploymentID == item.DeploymentID && reg.Route == item.Route {
				view := dashboardDebugRegressionView(reg, app.Slug, data.Since, debugTelemetryFilters{
					DeploymentID: data.DeploymentID,
					Status:       data.Status,
					ColdBoot:     data.ColdBoot,
					ConsumerID:   data.ConsumerID,
					MinLatencyMS: data.MinLatencyMS,
				})
				matching = &view
				break
			}
		}
	} else {
		regressionErr = err
		log.Warn("dashboard renderAppDebug: get request regression enrichment", "account_id", acct.ID, "app_id", app.ID, "request_id", rawID, "err", err)
	}
	var apiRegression *api.DebugRegressionItem
	if matching != nil {
		apiRegression = &api.DebugRegressionItem{DeploymentID: matching.DeploymentID, Route: matching.Route, P95MS: matching.P95MS, P95BaseMS: matching.P95BaseMS, AffectedCount: matching.AffectedCount, Factor: matching.Factor, FirstDetectedAt: matching.FirstDetectedAt, LastDetectedAt: matching.LastDetectedAt, State: matching.State}
	}
	explanation := buildDebugEvidenceExplanation(item, apiRegression, spans)
	if regressionErr != nil {
		explanation = buildDebugEvidenceDegradedExplanation(spans)
	}
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
	correlation := buildDebugRequestCorrelation(item, timeline, spans)
	correlationViews := make([]dashboard.DebugCorrelationStageView, 0, len(correlation.Stages))
	for _, stage := range correlation.Stages {
		correlationViews = append(correlationViews, dashboard.DebugCorrelationStageView{
			Phase:         stage.Phase,
			Status:        stage.Status,
			StartedAt:     stage.StartedAt,
			CompletedAt:   stage.CompletedAt,
			DurationMS:    stage.DurationMS,
			EvidenceCount: stage.EvidenceCount,
			Reason:        stage.Reason,
			Approximate:   stage.Approximate,
		})
	}
	evidence := api.DebugRequestEvidenceResponse{
		Request:                    item,
		Regression:                 apiRegression,
		Timeline:                   timeline,
		Correlation:                correlation,
		DependencyLatency:          dependencyLatency,
		DependencyLatencyTruncated: dependencyLatencyTruncated,
		Spans:                      spans,
		SpansTruncated:             truncated,
		Explanation:                explanation,
	}
	explanation = debugger.Synthesize(evidence)
	findingViews := make([]dashboard.DebugEvidenceFindingView, 0, len(explanation.Findings))
	for _, finding := range explanation.Findings {
		findingViews = append(findingViews, dashboard.DebugEvidenceFindingView{
			Code: finding.Code, Title: finding.Title, Detail: finding.Detail, Confidence: finding.Confidence,
		})
	}
	recommendationViews := make([]dashboard.DebugEvidenceRecommendationView, 0, len(explanation.Recommendations))
	for _, recommendation := range explanation.Recommendations {
		recommendationViews = append(recommendationViews, dashboard.DebugEvidenceRecommendationView{
			Action: recommendation.Action, Detail: recommendation.Detail,
		})
	}
	data.Selected = &dashboard.DebugRequestDetailView{
		Request:                    request,
		Regression:                 matching,
		Timeline:                   timelineViews,
		Correlation:                correlationViews,
		CorrelationComplete:        correlation.Complete,
		CriticalPath:               dashboardDebugCriticalPathView(criticalPath),
		Waterfall:                  waterfall,
		WaterfallComplete:          waterfallComplete && !truncated,
		DependencyLatency:          dependencyViews,
		DependencyLatencyTruncated: dependencyLatencyTruncated,
		Spans:                      spanViews,
		SpansTruncated:             truncated,
		Explanation:                explanation.Headline,
		EvidenceStatus:             explanation.Diagnosis,
		Findings:                   findingViews,
		Recommendations:            recommendationViews,
		GeneratedAt:                now.Format(time.RFC3339),
	}
	return nil
}

const debugCriticalPathMaxSpans = 32

type debugCriticalTimedSpan struct {
	span  api.DebugTelemetrySpan
	start time.Time
	end   time.Time
}

func buildDebugCriticalPath(spans []api.DebugTelemetrySpan) *api.DebugRequestCriticalPath {
	timed := make([]debugCriticalTimedSpan, 0, len(spans))
	complete := true
	for _, span := range spans {
		start, startErr := time.Parse(time.RFC3339Nano, span.StartTime)
		end, endErr := time.Parse(time.RFC3339Nano, span.EndTime)
		if startErr != nil || endErr != nil || end.Before(start) {
			complete = false
			continue
		}
		timed = append(timed, debugCriticalTimedSpan{span: span, start: start, end: end})
	}
	if len(timed) == 0 {
		return nil
	}

	byID := make(map[string]int, len(timed))
	children := make(map[string][]int, len(timed))
	for index, span := range timed {
		byID[span.span.SpanID] = index
		if span.span.ParentSpanID != "" {
			children[span.span.ParentSpanID] = append(children[span.span.ParentSpanID], index)
		}
	}

	exclusive := make([]time.Duration, len(timed))
	for index, parent := range timed {
		intervals := make([][2]time.Time, 0, len(children[parent.span.SpanID]))
		for _, childIndex := range children[parent.span.SpanID] {
			child := timed[childIndex]
			start, end := child.start, child.end
			if start.Before(parent.start) {
				start = parent.start
			}
			if end.After(parent.end) {
				end = parent.end
			}
			if end.After(start) {
				intervals = append(intervals, [2]time.Time{start, end})
			}
		}
		sort.Slice(intervals, func(i, j int) bool {
			if !intervals[i][0].Equal(intervals[j][0]) {
				return intervals[i][0].Before(intervals[j][0])
			}
			return intervals[i][1].Before(intervals[j][1])
		})
		covered := time.Duration(0)
		if len(intervals) > 0 {
			currentStart, currentEnd := intervals[0][0], intervals[0][1]
			for _, interval := range intervals[1:] {
				if !interval[0].After(currentEnd) {
					if interval[1].After(currentEnd) {
						currentEnd = interval[1]
					}
					continue
				}
				covered += currentEnd.Sub(currentStart)
				currentStart, currentEnd = interval[0], interval[1]
			}
			covered += currentEnd.Sub(currentStart)
		}
		wall := parent.end.Sub(parent.start)
		exclusive[index] = wall - covered
		if exclusive[index] < 0 {
			exclusive[index] = 0
		}
	}

	var best []int
	var bestDuration time.Duration
	var bestEnd time.Time
	bestLeafID := ""
	for leafIndex, leaf := range timed {
		chain := make([]int, 0, debugCriticalPathMaxSpans)
		seen := make(map[string]struct{}, debugCriticalPathMaxSpans)
		current := leafIndex
		for len(chain) < debugCriticalPathMaxSpans {
			span := timed[current]
			if _, ok := seen[span.span.SpanID]; ok {
				complete = false
				break
			}
			seen[span.span.SpanID] = struct{}{}
			chain = append(chain, current)
			parentID := span.span.ParentSpanID
			if parentID == "" {
				break
			}
			parentIndex, ok := byID[parentID]
			if !ok {
				complete = false
				break
			}
			current = parentIndex
		}
		if len(chain) == debugCriticalPathMaxSpans && timed[chain[len(chain)-1]].span.ParentSpanID != "" {
			complete = false
		}
		if len(chain) == 0 {
			continue
		}
		root := timed[chain[len(chain)-1]]
		duration := leaf.end.Sub(root.start)
		if duration < 0 {
			duration = 0
		}
		if best == nil || duration > bestDuration || (duration == bestDuration && leaf.end.After(bestEnd)) || (duration == bestDuration && leaf.end.Equal(bestEnd) && leaf.span.SpanID < bestLeafID) {
			best = append(best[:0], chain...)
			bestDuration = duration
			bestEnd = leaf.end
			bestLeafID = leaf.span.SpanID
		}
	}
	if len(best) == 0 {
		return nil
	}

	// The parent walk above is leaf-to-root; expose the path in causal order.
	for left, right := 0, len(best)-1; left < right; left, right = left+1, right-1 {
		best[left], best[right] = best[right], best[left]
	}
	path := &api.DebugRequestCriticalPath{
		DurationMS: bestDuration.Milliseconds(),
		Complete:   complete && len(timed) == len(spans),
		SpanCount:  len(best),
		Spans:      make([]api.DebugCriticalPathSpan, 0, len(best)),
	}
	for _, index := range best {
		span := timed[index]
		exclusiveMS := exclusive[index].Milliseconds()
		path.Spans = append(path.Spans, api.DebugCriticalPathSpan{
			SpanID:         span.span.SpanID,
			ParentSpanID:   span.span.ParentSpanID,
			Name:           span.span.Name,
			Kind:           span.span.Kind,
			DependencyType: span.span.DependencyType,
			DependencyKind: span.span.DependencyKind,
			Status:         span.span.Status,
			StartTime:      span.span.StartTime,
			EndTime:        span.span.EndTime,
			DurationMS:     span.end.Sub(span.start).Milliseconds(),
			ExclusiveMS:    exclusiveMS,
		})
		if exclusiveMS > path.SlowestExclusiveMS {
			path.SlowestExclusiveMS = exclusiveMS
			path.SlowestSpanID = span.span.SpanID
			path.SlowestSpanName = span.span.Name
		}
	}
	return path
}

func dashboardDebugCriticalPathView(path *api.DebugRequestCriticalPath) *dashboard.DebugCriticalPathView {
	if path == nil {
		return nil
	}
	view := &dashboard.DebugCriticalPathView{
		DurationMS:         path.DurationMS,
		Complete:           path.Complete,
		SpanCount:          path.SpanCount,
		SlowestSpanID:      path.SlowestSpanID,
		SlowestSpanName:    path.SlowestSpanName,
		SlowestExclusiveMS: path.SlowestExclusiveMS,
		Spans:              make([]dashboard.DebugCriticalPathSpanView, 0, len(path.Spans)),
	}
	for _, span := range path.Spans {
		view.Spans = append(view.Spans, dashboard.DebugCriticalPathSpanView{
			SpanID:         span.SpanID,
			ParentSpanID:   span.ParentSpanID,
			Name:           span.Name,
			Kind:           span.Kind,
			DependencyType: span.DependencyType,
			DependencyKind: span.DependencyKind,
			Status:         span.Status,
			StartTime:      span.StartTime,
			EndTime:        span.EndTime,
			DurationMS:     span.DurationMS,
			ExclusiveMS:    span.ExclusiveMS,
		})
	}
	return view
}

func buildDebugWaterfall(spans []api.DebugTelemetrySpan) ([]dashboard.DebugWaterfallSpanView, bool) {
	type timedSpan struct {
		span       api.DebugTelemetrySpan
		start      time.Time
		end        time.Time
		depth      int
		offsetPct  string
		widthPct   string
		durationMS int64
	}

	if len(spans) == 0 {
		return []dashboard.DebugWaterfallSpanView{}, true
	}
	timed := make([]timedSpan, 0, len(spans))
	complete := true
	for _, span := range spans {
		start, startErr := time.Parse(time.RFC3339Nano, span.StartTime)
		end, endErr := time.Parse(time.RFC3339Nano, span.EndTime)
		if startErr != nil || endErr != nil || end.Before(start) {
			complete = false
			continue
		}
		duration := end.Sub(start)
		if span.DurationNanos > 0 {
			duration = time.Duration(span.DurationNanos)
		}
		if duration < 0 {
			duration = 0
		}
		timed = append(timed, timedSpan{
			span:       span,
			start:      start,
			end:        end,
			durationMS: int64(duration / time.Millisecond),
		})
	}
	if len(timed) == 0 {
		return []dashboard.DebugWaterfallSpanView{}, false
	}
	sort.SliceStable(timed, func(i, j int) bool {
		if !timed[i].start.Equal(timed[j].start) {
			return timed[i].start.Before(timed[j].start)
		}
		if !timed[i].end.Equal(timed[j].end) {
			return timed[i].end.After(timed[j].end)
		}
		return timed[i].span.SpanID < timed[j].span.SpanID
	})

	traceStart := timed[0].start
	traceEnd := timed[0].end
	for _, span := range timed[1:] {
		if span.start.Before(traceStart) {
			traceStart = span.start
		}
		if span.end.After(traceEnd) {
			traceEnd = span.end
		}
	}
	total := traceEnd.Sub(traceStart)
	if total <= 0 {
		total = time.Millisecond
	}
	byID := make(map[string]string, len(timed))
	for _, span := range timed {
		byID[span.span.SpanID] = span.span.ParentSpanID
	}
	for i := range timed {
		depth := 0
		current := timed[i].span.SpanID
		seen := map[string]struct{}{}
		for {
			parent := byID[current]
			if parent == "" {
				break
			}
			if _, ok := seen[parent]; ok {
				complete = false
				break
			}
			seen[parent] = struct{}{}
			if _, ok := byID[parent]; !ok {
				complete = false
				break
			}
			depth++
			current = parent
			if depth >= 32 {
				complete = false
				break
			}
		}
		offset := timed[i].start.Sub(traceStart)
		width := timed[i].end.Sub(timed[i].start)
		offsetPct := float64(offset) / float64(total) * 100
		widthPct := float64(width) / float64(total) * 100
		if offsetPct < 0 {
			offsetPct = 0
		}
		if offsetPct > 100 {
			offsetPct = 100
		}
		if widthPct > 100 {
			widthPct = 100
		}
		if width > 0 && widthPct < 0.5 {
			widthPct = 0.5
		}
		timed[i].depth = depth
		timed[i].offsetPct = fmt.Sprintf("%.2f", offsetPct)
		timed[i].widthPct = fmt.Sprintf("%.2f", widthPct)
	}

	views := make([]dashboard.DebugWaterfallSpanView, 0, len(timed))
	for _, span := range timed {
		views = append(views, dashboard.DebugWaterfallSpanView{
			Name:         span.span.Name,
			Kind:         span.span.Kind,
			Status:       span.span.Status,
			DBStatement:  span.span.DBStatement,
			SpanID:       span.span.SpanID,
			ParentSpanID: span.span.ParentSpanID,
			StartTime:    span.span.StartTime,
			EndTime:      span.span.EndTime,
			DurationMS:   span.durationMS,
			Depth:        span.depth,
			OffsetPct:    span.offsetPct,
			WidthPct:     span.widthPct,
		})
	}
	return views, complete && len(timed) == len(spans)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
