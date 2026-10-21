// Package dataapiusage serves Data API usage (ADR-20261019102108): the private
// runtime report, authenticated by workload identity, and the owner's view.
package dataapiusage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	usage "github.com/onebox-faas/faas/pkg/state/dataapiusage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// Recorder adds runtime reports; *usage.Store implements it.
type Recorder interface {
	Record(ctx context.Context, accountID, appID, reportID string, hours []usage.Hour, access []byte, now time.Time) (usage.MonthToDate, error)
}

// Reader serves owner views; *usage.Store implements it.
type Reader interface {
	Hours(ctx context.Context, appID string, from, to time.Time) ([]usage.Hour, error)
	MonthToDate(ctx context.Context, appID string, now time.Time) (usage.MonthToDate, error)
}

var reportID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// ReportHandler accepts runtime usage deltas. Only a gregale:data-api:usage
// workload token is accepted, never an owner key or application JWT; the
// token names the account, app and instance.
type ReportHandler struct {
	Store    Recorder
	Verifier *workloadidentity.Verifier
	Now      func() time.Time
}

func (h ReportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	now := h.now()
	claims, err := h.Verifier.Verify(bearer(r), workloadidentity.DataAPIUsageAudience, now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "a Data API usage-report workload token is required"))
		return
	}
	if h.Store == nil {
		unavailable(w)
		return
	}
	report, hours, err := decodeReport(w, r, now)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid usage report", err.Error()))
		return
	}
	var access []byte
	if report.Access != nil {
		if access, err = json.Marshal(report.Access); err != nil || report.Access.Validate() != nil || len(access) > api.DataAPIAccessConfigMaxBytes {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid usage report", "access settings are out of bounds"))
			return
		}
	}
	mtd, err := h.Store.Record(r.Context(), claims.AccountID, claims.AppID, report.ReportID, hours, access, now)
	switch {
	case errors.Is(err, usage.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "the workload's app no longer exists"))
		return
	case err != nil:
		unavailable(w)
		return
	}
	writeJSON(w, api.DataAPIUsageReportResponse{MonthToDate: monthToDate(mtd)})
}

func decodeReport(w http.ResponseWriter, r *http.Request, now time.Time) (api.DataAPIUsageReport, []usage.Hour, error) {
	var report api.DataAPIUsageReport
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.DataAPIUsageReportMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return report, nil, errors.New("the body must be one version 1 usage report")
	}
	if report.Version != 1 || !reportID.MatchString(report.ReportID) || len(report.Hours) > api.DataAPIUsageReportMaxHours {
		return report, nil, errors.New("version, report_id or hour count is out of bounds")
	}
	// Deltas may only land in recent hours: a restored or delayed runtime can
	// still report, but a report cannot rewrite history.
	earliest, latest := now.UTC().Truncate(time.Hour).Add(-api.DataAPIUsageReportMaxHours*time.Hour), now.UTC().Add(time.Hour)
	seen := map[time.Time]bool{}
	hours := make([]usage.Hour, 0, len(report.Hours))
	for _, h := range report.Hours {
		start := h.Hour.UTC()
		if !start.Equal(start.Truncate(time.Hour)) || start.Before(earliest) || start.After(latest) || seen[start] || !validCounts(h.DataAPIUsageCounts) {
			return report, nil, errors.New("hours must be unique, aligned, recent and non-negative within bounds")
		}
		seen[start] = true
		hours = append(hours, usage.Hour{Hour: start, Counts: fromAPI(h.DataAPIUsageCounts)})
	}
	return report, hours, nil
}

func validCounts(c api.DataAPIUsageCounts) bool {
	for _, v := range []int64{c.UserRequests, c.AnonymousRequests, c.ServerRequests, c.RejectedRequests, c.ErrorRequests, c.CappedRequests, c.RowsReturned, c.ResponseBytes} {
		if v < 0 || v > api.DataAPIUsageMaxDelta {
			return false
		}
	}
	return true
}

// QueryHandler serves an owner's usage view of one app; the caller has
// already resolved the app in the owner's account.
type QueryHandler struct {
	Store Reader
	Now   func() time.Time
}

// Serve writes the usage of app (by ID) as slug.
func (h QueryHandler) Serve(w http.ResponseWriter, r *http.Request, appID, slug string) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Store == nil {
		unavailable(w)
		return
	}
	since, granularity, err := parseWindow(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid usage window", err.Error()))
		return
	}
	usage, err := View(r.Context(), h.Store, appID, slug, h.now(), since, granularity)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, usage)
}

// View reads an app's usage over since, in hour or day buckets, with
// month-to-date totals.
func View(ctx context.Context, store Reader, appID, slug string, now time.Time, since time.Duration, granularity string) (api.DataAPIUsage, error) {
	now = now.UTC()
	step := time.Hour
	if granularity == "day" {
		step = 24 * time.Hour
	}
	from, to := now.Add(-since).Truncate(step), now.Truncate(time.Hour).Add(time.Hour)
	hours, err := store.Hours(ctx, appID, from, to)
	if err != nil {
		return api.DataAPIUsage{}, err
	}
	mtd, err := store.MonthToDate(ctx, appID, now)
	if err != nil {
		return api.DataAPIUsage{}, err
	}
	return view(slug, from, to, granularity, step, hours, mtd), nil
}

// parseWindow reads since (24h, 7d, 30d; default 7d, at most the query bound)
// and granularity (hour or day; default hour up to two days, else day).
func parseWindow(q map[string][]string) (time.Duration, string, error) {
	get := func(k string) string {
		if v := q[k]; len(v) == 1 {
			return v[0]
		}
		return ""
	}
	for k, v := range q {
		if (k != "since" && k != "granularity") || len(v) != 1 {
			return 0, "", errors.New("only one since and one granularity are accepted")
		}
	}
	since := 7 * 24 * time.Hour
	if raw := get("since"); raw != "" {
		var err error
		if since, err = parseSince(raw); err != nil {
			return 0, "", err
		}
	}
	granularity := get("granularity")
	switch granularity {
	case "":
		granularity = "hour"
		if since > 48*time.Hour {
			granularity = "day"
		}
	case "hour":
		if since > 48*time.Hour {
			return 0, "", errors.New("hourly views cover at most 48h")
		}
	case "day":
	default:
		return 0, "", errors.New("granularity must be hour or day")
	}
	return since, granularity, nil
}

func parseSince(raw string) (time.Duration, error) {
	invalid := errors.New("since must be whole hours (24h) or days (7d), at most " + strconv.Itoa(api.DataAPIUsageQueryMaxDays) + "d")
	unit := time.Hour
	switch {
	case strings.HasSuffix(raw, "d"):
		unit = 24 * time.Hour
	case !strings.HasSuffix(raw, "h"):
		return 0, invalid
	}
	n, err := strconv.Atoi(raw[:len(raw)-1])
	if err != nil || n < 1 || time.Duration(n)*unit > time.Duration(api.DataAPIUsageQueryMaxDays)*24*time.Hour {
		return 0, invalid
	}
	return time.Duration(n) * unit, nil
}

// view zero-fills buckets so charts and tables need no gap handling.
func view(slug string, from, to time.Time, granularity string, step time.Duration, hours []usage.Hour, mtd usage.MonthToDate) api.DataAPIUsage {
	out := api.DataAPIUsage{App: slug, From: from, To: to, Granularity: granularity, MonthToDate: monthToDate(mtd), Buckets: []api.DataAPIUsageBucket{}}
	index := map[time.Time]int{}
	for start := from; start.Before(to); start = start.Add(step) {
		index[start] = len(out.Buckets)
		out.Buckets = append(out.Buckets, api.DataAPIUsageBucket{Start: start})
	}
	var totals usage.Counts
	for _, h := range hours {
		totals = totals.Add(h.Counts)
		if i, ok := index[h.Hour.Truncate(step)]; ok {
			out.Buckets[i].DataAPIUsageCounts = toAPI(fromAPI(out.Buckets[i].DataAPIUsageCounts).Add(h.Counts))
		}
	}
	out.Totals = toAPI(totals)
	return out
}

func monthToDate(m usage.MonthToDate) api.DataAPIUsageMonthToDate {
	return api.DataAPIUsageMonthToDate{Month: m.Month.UTC().Format("2006-01"), Requests: m.Requests, AnonymousRequests: m.AnonymousRequests}
}

func fromAPI(c api.DataAPIUsageCounts) usage.Counts {
	return usage.Counts{UserRequests: c.UserRequests, AnonymousRequests: c.AnonymousRequests, ServerRequests: c.ServerRequests, RejectedRequests: c.RejectedRequests,
		ErrorRequests: c.ErrorRequests, CappedRequests: c.CappedRequests, RowsReturned: c.RowsReturned, ResponseBytes: c.ResponseBytes}
}

func toAPI(c usage.Counts) api.DataAPIUsageCounts {
	return api.DataAPIUsageCounts{UserRequests: c.UserRequests, AnonymousRequests: c.AnonymousRequests, ServerRequests: c.ServerRequests, RejectedRequests: c.RejectedRequests,
		ErrorRequests: c.ErrorRequests, CappedRequests: c.CappedRequests, RowsReturned: c.RowsReturned, ResponseBytes: c.ResponseBytes}
}

func (h ReportHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h QueryHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func unavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "data_api_usage_unavailable", "Data API usage unavailable", "usage storage is not available; retry later"))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
