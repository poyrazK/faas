// adr: 650 — separate process liveness, SQL readiness and sampled failures.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIHealthChecks struct {
	Engine   string `json:"engine"`
	SQL      string `json:"sql"`
	Database string `json:"database"`
	Refresh  string `json:"refresh"`
}

type dataAPIHealthAdmission struct {
	Active        *int `json:"active"`
	Queued        *int `json:"queued"`
	Capacity      *int `json:"capacity"`
	QueueCapacity *int `json:"queue_capacity"`
}

type dataAPIHealthProbe struct {
	State     string                  `json:"state"`
	HTTP      int                     `json:"http_status,omitempty"`
	Live      *bool                   `json:"live,omitempty"`
	Ready     *bool                   `json:"ready,omitempty"`
	Checks    *dataAPIHealthChecks    `json:"checks,omitempty"`
	Admission *dataAPIHealthAdmission `json:"admission,omitempty"`
	Pool      *dataAPIHealthPool      `json:"pool,omitempty"`
}

type dataAPIHealthReport struct {
	Version          int                           `json:"version"`
	App              string                        `json:"app"`
	Time             string                        `json:"time"`
	Status           string                        `json:"status"`
	Partial          bool                          `json:"partial"`
	PartialReasons   []string                      `json:"partial_reasons"`
	CurrentFindings  []string                      `json:"current_findings"`
	RecentFindings   []string                      `json:"recent_findings"`
	Liveness         dataAPIHealthProbe            `json:"liveness"`
	Readiness        dataAPIHealthProbe            `json:"readiness"`
	Diagnostics      *dataAPIDiagnosticsReport     `json:"diagnostics,omitempty"`
	ConnectionBudget *api.DataAPIConnectionBudgets `json:"connection_budget,omitempty"`
}

func cmdDataAPIHealth(args []string) int {
	fs := newFlagSet("data-api health", flag.ContinueOnError)
	since := fs.String("since", "15m", "retained request log lookback duration or RFC3339 timestamp")
	timeout := fs.Duration("timeout", api.DataAPIHealthReportTimeout, "entire report deadline (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *timeout <= 0 || *timeout > api.DataAPIHealthMaxTimeout {
		PrintUsage(os.Stderr, "usage: gregale data-api health NAME [--since DURATION|RFC3339] [--timeout DURATION]", "data-api")
		return 1
	}
	normalized, err := normalizeLogsSince(*since, logsSourceRuntime, time.Now())
	if err != nil {
		return printErr("Invalid health time window", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not inspect Data API app", err)
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return printErr("Could not inspect Data API health", err)
	}
	// No account key, application JWT, cookies, redirects or response bodies
	// from failed public probes are forwarded into the health report.
	public := &http.Client{Timeout: api.DataAPIHealthHTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	live := make(chan dataAPIHealthProbe, 1)
	budget := make(chan api.DataAPIConnectionBudgets, 1)
	go func() { budget <- readDataAPIConnectionBudget(ctx, client, fs.Arg(0)) }()
	go func() {
		live <- probeDataAPIHealth(ctx, public, strings.TrimSuffix(endpoint, "/healthz")+"/livez", true)
	}()
	readiness := probeDataAPIHealth(ctx, public, endpoint, false)
	liveness := <-live
	report := describeDataAPIHealth(fs.Arg(0), liveness, readiness)
	attachDataAPIConnectionBudget(&report, <-budget)
	collector := newDataAPIDiagnosticsCollector(normalized, "")
	collector.report.App, collector.report.Source, collector.report.Coverage = fs.Arg(0), "hosted", "retained_sample"
	input, err := client.StreamAppLogs(ctx, fs.Arg(0), "", false, api.LogFilter{Since: normalized})
	if err != nil {
		collector.partial("stream_unavailable")
	} else {
		stopRead := context.AfterFunc(ctx, func() { _ = input.Close() })
		readDataAPIDiagnostics(ctx, input, true, collector)
		stopRead()
		_ = input.Close()
	}
	collector.interrupted(ctx.Err())
	collector.finish()
	report.Diagnostics = &collector.report
	if collector.report.Partial {
		report.Partial = true
		report.PartialReasons = append(report.PartialReasons, "recent_diagnostics_partial")
	}
	if collector.report.Failures.Overloaded > 0 {
		report.RecentFindings = append(report.RecentFindings, "overload_observed")
	}
	if collector.report.Failures.Timeouts > 0 {
		report.RecentFindings = append(report.RecentFindings, "query_timeouts_observed")
	}
	if collector.report.Failures.Unavailable > 0 {
		report.RecentFindings = append(report.RecentFindings, "unavailability_observed")
	}
	if collector.report.Failures.RefreshPaused > 0 {
		report.RecentFindings = append(report.RecentFindings, "refresh_pauses_observed")
	}
	if collector.report.Failures.RefreshUnavailable > 0 {
		report.RecentFindings = append(report.RecentFindings, "refresh_failures_observed")
	}
	if collector.report.Failures.ResponseTooLarge > 0 {
		report.RecentFindings = append(report.RecentFindings, "response_limits_observed")
	}
	if collector.report.Failures.RateLimited > 0 {
		report.RecentFindings = append(report.RecentFindings, "rate_limits_observed")
	}
	if collector.report.Failures.ConcurrencyLimited > 0 {
		report.RecentFindings = append(report.RecentFindings, "user_concurrency_limits_observed")
	}
	if jsonOutput {
		if err := writeJSON(report); err != nil {
			return printErr("Could not write health report", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s: %s\nLiveness: %s; readiness: %s\n", report.App, report.Status, report.Liveness.State, report.Readiness.State)
		if checks := report.Readiness.Checks; checks != nil {
			_, _ = fmt.Fprintf(osStdout, "Engine: %s; SQL: %s; database connectivity: %s; refresh: %s\n", checks.Engine, checks.SQL, checks.Database, checks.Refresh)
		}
		if err := writeDataAPIPool(report.Readiness.Pool); err != nil {
			return printErr("Could not write pool observations", err)
		}
		if err := writeDataAPIConnectionBudget(report.ConnectionBudget); err != nil {
			return printErr("Could not write connection budget", err)
		}
		_, _ = fmt.Fprintf(osStdout, "Current findings: %s\nRecent findings (retained sample): %s\n", strings.Join(report.CurrentFindings, ", "), strings.Join(report.RecentFindings, ", "))
		_, _ = fmt.Fprintf(osStdout, "Recent requests: %d; overload responses: %d; query timeouts: %d\n", collector.report.Summary.Requests, collector.report.Failures.Overloaded, collector.report.Failures.Timeouts)
		if err := writeDataAPIDatabaseTimings(collector.report.DataRequests.DatabaseTimings); err != nil {
			return printErr("Could not write database timing observations", err)
		}
		if report.Partial {
			_, _ = fmt.Fprintf(osStdout, "Partial report: %s\n", strings.Join(report.PartialReasons, ", "))
		}
	}
	if report.Status != "healthy" || report.Partial {
		return 1
	}
	return 0
}

func probeDataAPIHealth(ctx context.Context, client *http.Client, address string, liveness bool) dataAPIHealthProbe {
	result := dataAPIHealthProbe{State: "unreachable"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return result
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return result
	}
	defer func() { _ = response.Body.Close() }()
	result.HTTP = response.StatusCode
	result.State = "rejected"
	if response.StatusCode == http.StatusNotFound {
		result.State = "unsupported"
		return result
	}
	if response.StatusCode != http.StatusOK && (liveness || response.StatusCode != http.StatusServiceUnavailable) {
		return result
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.DataAPIHealthMaxBytes+1))
	result.State = "invalid"
	if err != nil || len(body) > api.DataAPIHealthMaxBytes {
		return result
	}
	var payload struct {
		Version   int                     `json:"version"`
		Live      *bool                   `json:"live"`
		Ready     *bool                   `json:"ready"`
		Checks    *dataAPIHealthChecks    `json:"checks"`
		Admission *dataAPIHealthAdmission `json:"admission"`
		Pool      json.RawMessage         `json:"pool"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return result
	}
	if payload.Version != 1 || payload.Live == nil || !*payload.Live {
		result.State = "unsupported"
		return result
	}
	if !liveness && !validDataAPIHealth(payload.Ready, payload.Checks, payload.Admission, response.StatusCode) {
		return result
	}
	result.Live = payload.Live
	result.State = "ok"
	if !liveness {
		result.Ready, result.Checks, result.Admission = payload.Ready, payload.Checks, payload.Admission
		result.Pool = parseDataAPIHealthPool(payload.Pool, *payload.Admission.Capacity)
		if !*payload.Ready {
			result.State = "unready"
		}
	}
	return result
}

func validDataAPIHealth(ready *bool, c *dataAPIHealthChecks, a *dataAPIHealthAdmission, status int) bool {
	if ready == nil || c == nil || a == nil || a.Active == nil || a.Queued == nil || a.Capacity == nil || a.QueueCapacity == nil {
		return false
	}
	if !slices.Contains([]string{"reachable", "unreachable"}, c.Engine) || !slices.Contains([]string{"ready", "unready", "unknown"}, c.SQL) || !slices.Contains([]string{"reachable", "unknown"}, c.Database) || !slices.Contains([]string{"disabled", "active", "paused", "unavailable"}, c.Refresh) {
		return false
	}
	// The engine pool is configurable (ADR-20261021180534); coordinated
	// refresh keeps one connection of it.
	capacity := api.DataAPIMaxPoolConnections
	if c.Refresh != "disabled" {
		capacity--
	}
	if *a.Capacity < 1 || *a.Capacity > capacity || *a.QueueCapacity != api.DataAPIMaxQueuedRequests || *a.Active < 0 || *a.Active > *a.Capacity || *a.Queued < 0 || *a.Queued > api.DataAPIMaxQueuedRequests {
		return false
	}
	expectedReady := c.SQL == "ready" && c.Refresh != "unavailable"
	expectedReachable := c.SQL == "ready" || c.Refresh == "active" || c.Refresh == "paused"
	return *ready == expectedReady && (*ready == (status == http.StatusOK)) && (c.Database == "reachable") == expectedReachable
}

func describeDataAPIHealth(app string, live, ready dataAPIHealthProbe) dataAPIHealthReport {
	r := dataAPIHealthReport{Version: 1, App: app, Time: time.Now().UTC().Format(time.RFC3339Nano), Status: "unknown", Liveness: live, Readiness: ready,
		PartialReasons: []string{}, CurrentFindings: []string{}, RecentFindings: []string{}}
	for name, probe := range map[string]dataAPIHealthProbe{"liveness": live, "readiness": ready} {
		if probe.State != "ok" && probe.State != "unready" {
			r.Partial = true
			r.PartialReasons = append(r.PartialReasons, name+"_"+probe.State)
		}
	}
	slices.Sort(r.PartialReasons)
	if ready.Ready == nil {
		r.CurrentFindings = append(r.CurrentFindings, "readiness_unknown")
		return r
	}
	r.Status = "healthy"
	if !*ready.Ready {
		r.Status = "unavailable"
	}
	c := ready.Checks
	if c.Engine != "reachable" {
		r.CurrentFindings = append(r.CurrentFindings, "engine_"+c.Engine)
	}
	if c.SQL != "ready" {
		r.CurrentFindings = append(r.CurrentFindings, "sql_"+c.SQL)
	}
	if c.Database == "unknown" {
		r.CurrentFindings = append(r.CurrentFindings, "database_connectivity_unknown")
	}
	if c.Refresh == "paused" || c.Refresh == "unavailable" {
		r.CurrentFindings = append(r.CurrentFindings, "refresh_"+c.Refresh)
	}
	if a := ready.Admission; *a.Active == *a.Capacity {
		r.CurrentFindings = append(r.CurrentFindings, "admission_saturated")
	}
	if ready.Pool == nil {
		ready.Pool = &dataAPIHealthPool{State: "unsupported"}
		r.Readiness.Pool = ready.Pool
	}
	describeDataAPIPool(&r, ready.Pool)
	if r.Status == "healthy" && (len(r.CurrentFindings) > 0 || r.Partial) {
		r.Status = "degraded"
	}
	return r
}
