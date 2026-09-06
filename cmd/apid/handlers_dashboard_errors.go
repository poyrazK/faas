package main

// Dashboard surface for the automatic app-error grouping API (ADR-096 / G3).
// The page keeps the summary, fingerprint drill-down, and oldest redacted
// sample together so a customer can move from a spike to a concrete request
// without copying JSON into another tool.

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// parseAppErrorsPath recognizes the dashboard's per-app errors page. The
// trailing slash is accepted to match the other dashboard app subpages.
func parseAppErrorsPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	const suffix = "/errors"
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	slug := strings.TrimSuffix(rest, suffix)
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// renderAppErrors renders the grouped error summary and, when ?fingerprint=
// is present, its bounded request history and oldest redacted sample.
func (s *server) renderAppErrors(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	ctx := r.Context()
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	appCount, err := s.store.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		log.Warn("dashboard renderAppErrors: count deployed apps", "account_id", acct.ID, "err", err)
	}
	data := dashboard.AppErrorsData{AppSlug: app.Slug, AppStatus: string(app.Status), Plan: string(acct.Plan), PlanAllowed: acct.Plan.AppErrorsAllowed(), Limit: api.AppErrorsSummaryDefaultLimit}
	if !data.PlanAllowed {
		data.ErrorMessage = "Automatic error grouping is available on Hobby and higher plans."
		if err := s.renderAppErrorsPage(w, r, log, acct, appCount, data); err != nil {
			renderProblem(w, log, err)
		}
		return
	}
	until, since, clamped, err := dashboardAppErrorsWindow(r, acct.Plan)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid window", err.Error()))
		return
	}
	limit, err := parseAppErrorsLimit(r.URL.Query().Get("limit"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid limit", err.Error()))
		return
	}
	data.Limit, data.WindowStart, data.WindowEnd, data.WindowClamped = limit, since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano), clamped
	data.SummaryURL = dashboardAppErrorsURL(app.Slug, since, until, limit, "", "")
	data.Window24hURL = dashboardAppErrorsWindowURL(app.Slug, 24*time.Hour)
	data.Window7dURL = dashboardAppErrorsWindowURL(app.Slug, 7*24*time.Hour)
	curC, curLS, curFP, err := decodeSummaryCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid cursor", err.Error()))
		return
	}
	rows, err := s.store.ListAppErrorGroups(ctx, buildAppErrorsSummaryParams(acct.ID, app.ID, since, until, curC, curLS, curFP, limit))
	if err != nil {
		data.ErrorMessage = "Error data is temporarily unavailable. Please try again shortly."
		log.Warn("dashboard renderAppErrors: list summary", "account_id", acct.ID, "app_id", app.ID, "err", err)
	} else {
		data.Items = projectDashboardErrorGroups(rows, app.Slug, since, until, limit)
		if len(rows) == limit && len(rows) > 0 {
			last := rows[len(rows)-1]
			data.NextSummaryURL = dashboardAppErrorsURL(app.Slug, since, until, limit, encodeErrorsCursor(errorsCursorShape{Count: last.Count, LastSeenAt: last.LastSeenAt.UTC().Format(time.RFC3339Nano), Fingerprint: last.Fingerprint}), "")
		}
	}
	if fingerprint := strings.TrimSpace(r.URL.Query().Get("fingerprint")); fingerprint != "" {
		if !isValidFingerprint(fingerprint) {
			http.NotFound(w, r)
			return
		}
		if cursor := r.URL.Query().Get("detail_cursor"); cursor != "" {
			if _, _, err := decodeDrilldownCursor(cursor); err != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "validation_failed", "invalid cursor", err.Error()))
				return
			}
		}
		data.SelectedFingerprint = fingerprint
		data.Detail = s.dashboardErrorDetail(ctx, log, acct, app, fingerprint, limit, r.URL.Query().Get("detail_cursor"), since, until)
	}
	if err := s.renderAppErrorsPage(w, r, log, acct, appCount, data); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardAppErrorsWindow(r *http.Request, plan api.Plan) (until, since time.Time, clamped bool, err error) {
	until, since, clamped, err = parseAppErrorsSummaryWindow(r.URL.Query().Get("since"), r.URL.Query().Get("until"))
	if err != nil {
		return until, since, clamped, err
	}
	retentionCap := time.Duration(plan.AppErrorsRetentionDays()) * 24 * time.Hour
	if until.Sub(since) > retentionCap {
		clamped = true
		since = until.Add(-retentionCap)
	}
	return until, since, clamped, nil
}

func (s *server) dashboardErrorDetail(ctx stdctx, log *slog.Logger, acct state.Account, app state.App, fingerprint string, limit int, cursor string, since, until time.Time) *dashboard.ErrorDetail {
	curRA, curRI, err := decodeDrilldownCursor(cursor)
	if err != nil {
		return &dashboard.ErrorDetail{Fingerprint: fingerprint, TriageState: "open"}
	}
	rows, err := s.store.ListAppErrorRequests(ctx, buildAppErrorRequestsParams(acct.ID, app.ID, fingerprint, curRA, curRI, limit))
	if err != nil {
		log.Warn("dashboard renderAppErrors: list detail", "account_id", acct.ID, "app_id", app.ID, "fingerprint", fingerprint, "err", err)
		return &dashboard.ErrorDetail{Fingerprint: fingerprint, TriageState: "open"}
	}
	detail := &dashboard.ErrorDetail{Fingerprint: fingerprint, TriageState: "open", Requests: projectDashboardErrorRequests(rows)}
	if len(rows) > 0 {
		detail.ErrorClass, detail.Route, detail.HTTPStatus = rows[0].ErrorClass, rows[0].Route, rows[0].HTTPStatus
	}
	if len(rows) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		detail.NextRequestsURL = dashboardAppErrorsURL(app.Slug, since, until, limit, fingerprint, encodeErrorsCursor(errorsCursorShape{ReceivedAt: last.ReceivedAt.UTC().Format(time.RFC3339Nano), RequestID: last.RequestID.String()}))
	}
	sample, err := s.store.GetAppErrorSample(ctx, buildAppErrorSampleParams(acct.ID, app.ID, fingerprint))
	if err == nil {
		detail.Sample = projectDashboardErrorSample(sample)
	} else if !errors.Is(err, state.ErrNotFound) {
		log.Warn("dashboard renderAppErrors: get sample", "account_id", acct.ID, "app_id", app.ID, "fingerprint", fingerprint, "err", err)
	}
	return detail
}

func projectDashboardErrorGroups(rows []state.AppErrorGroup, slug string, since, until time.Time, limit int) []dashboard.ErrorSummaryItem {
	apiRows := projectAppErrorSummaryRows(rows)
	items := make([]dashboard.ErrorSummaryItem, 0, len(apiRows))
	for _, row := range apiRows {
		items = append(items, dashboard.ErrorSummaryItem{
			Fingerprint: row.Fingerprint, ErrorClass: row.ErrorClass, Route: row.Route, HTTPStatus: row.HTTPStatus,
			Count: row.Count, RequestCount: row.RequestCount, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
			SampleMessage: row.SampleMessage, TriageState: "open",
			DetailURL: dashboardAppErrorsURL(slug, since, until, limit, row.Fingerprint, ""),
		})
	}
	return items
}

func projectDashboardErrorRequests(rows []state.AppErrorRequestRow) []dashboard.ErrorRequestItem {
	apiRows := projectAppErrorRequestRows(rows)
	items := make([]dashboard.ErrorRequestItem, 0, len(apiRows))
	for _, row := range apiRows {
		items = append(items, dashboard.ErrorRequestItem{RequestID: row.RequestID, ReceivedAt: row.ReceivedAt, Route: row.Route, HTTPStatus: row.HTTPStatus, ErrorClass: row.ErrorClass, SampleMessage: row.SampleMessage, DeploymentID: row.DeploymentID})
	}
	return items
}

func projectDashboardErrorSample(row state.AppErrorSampleRow) *dashboard.ErrorSample {
	item := projectAppErrorRequestRows([]state.AppErrorRequestRow{row.AppErrorRequestRow})[0]
	return &dashboard.ErrorSample{RequestID: item.RequestID, ReceivedAt: item.ReceivedAt, Route: item.Route, HTTPStatus: item.HTTPStatus, ErrorClass: item.ErrorClass, SampleMessage: item.SampleMessage, DeploymentID: item.DeploymentID, HeadersSample: parseHeadersSample(row.HeadersSample), RedactionsApplied: row.Redactions}
}

func dashboardAppErrorsURL(slug string, since, until time.Time, limit int, fingerprint, cursor string) string {
	values := url.Values{}
	values.Set("since", since.UTC().Format(time.RFC3339Nano))
	values.Set("until", until.UTC().Format(time.RFC3339Nano))
	values.Set("limit", fmtInt(limit))
	if fingerprint != "" {
		values.Set("fingerprint", fingerprint)
	}
	if cursor != "" {
		cursorName := "cursor"
		if fingerprint != "" {
			cursorName = "detail_cursor"
		}
		values.Set(cursorName, cursor)
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/errors?" + values.Encode()
}

func dashboardAppErrorsWindowURL(slug string, span time.Duration) string {
	until := time.Now().UTC()
	return dashboardAppErrorsURL(slug, until.Add(-span), until, api.AppErrorsSummaryDefaultLimit, "", "")
}

func fmtInt(n int) string {
	return strconv.Itoa(n)
}

func (s *server) renderAppErrorsPage(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, appCount int, data dashboard.AppErrorsData) error {
	view, _ := AccountFrom(r.Context())
	return dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: data.AppSlug + " errors", Body: "errors", Account: dashboardAccountView(view, appCount), Data: data})
}
