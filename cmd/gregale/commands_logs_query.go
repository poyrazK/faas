package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	logsSourceRuntime    = "runtime"
	logsSourceHTTP       = "http"
	traceLogsLookupLimit = 200
	// httpLogsCapability owns the request telemetry routes HTTP logs read
	// (GET /v1/apps/{slug}/debug/requests).
	httpLogsCapability = "production-debugger"
)

// printHTTPLogsUnavailable explains an HTTP log query on an account whose
// production debugger is not released: apid answers its routes as absent, and
// the CLI printed only "Not found: no such API route" (#5709).
func printHTTPLogsUnavailable() int {
	PrintUsage(osStderr, "HTTP request logs (--source http, or --status, --route, --request, --trace, --limit, --all) are part of the production debugger, which is not enabled for this account; run 'gregale logs <app>' for runtime logs", "logs")
	return 2
}

// logsHTTPOnlyFlagsSet lists the HTTP-request-only flags the caller passed;
// any of them selects HTTP logs when --source is not given.
func logsHTTPOnlyFlagsSet(fs *flag.FlagSet) []string {
	var set []string
	for _, name := range []string{"status", "route", "request", "trace", "limit", "all"} {
		if logsFlagWasSet(fs, name) {
			set = append(set, "--"+name)
		}
	}
	return set
}

func logsFlagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func normalizeLogsSource(raw string, httpQueryRequested bool) (string, error) {
	source := strings.ToLower(strings.TrimSpace(raw))
	if source == "" {
		if httpQueryRequested {
			return logsSourceHTTP, nil
		}
		return logsSourceRuntime, nil
	}
	if source != logsSourceRuntime && source != logsSourceHTTP {
		return "", fmt.Errorf("--source must be one of: runtime, http")
	}
	return source, nil
}

// normalizeLogsSince accepts the friendly lookback form used by the unified
// command and translates it to the form understood by each existing backend:
// runtime SSE expects an absolute RFC3339 timestamp, while request telemetry
// expects a positive duration (including the API's Nd day alias).
func normalizeLogsSince(raw, source string, now time.Time) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if timestamp, err := time.Parse(time.RFC3339, raw); err == nil {
		if source == logsSourceHTTP {
			lookback := now.Sub(timestamp)
			if lookback <= 0 {
				return "", fmt.Errorf("--since timestamp must be in the past")
			}
			return lookback.String(), nil
		}
		if !timestamp.Before(now) {
			return "", fmt.Errorf("--since timestamp must be in the past")
		}
		return timestamp.UTC().Format(time.RFC3339Nano), nil
	}

	lookback, err := parsePositiveLogsDuration(raw)
	if err != nil {
		return "", fmt.Errorf("--since must be a positive duration (for example 15m or 3d) or an RFC3339 timestamp")
	}
	if source == logsSourceHTTP {
		return raw, nil
	}
	return now.UTC().Add(-lookback).Format(time.RFC3339Nano), nil
}

func parsePositiveLogsDuration(raw string) (time.Duration, error) {
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration, nil
	}
	if len(raw) > 1 && raw[len(raw)-1] == 'd' {
		days, err := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
		day := int64(24 * time.Hour)
		if err == nil && days > 0 && days <= int64(^uint64(0)>>1)/day {
			return time.Duration(days * day), nil
		}
	}
	return 0, fmt.Errorf("duration must be positive")
}

func runHTTPLogsQuery(ctx context.Context, slug, deploymentID, requestID, traceID, route, since string, status, limit int, all bool, now time.Time) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if traceID != "" {
		// The endpoint is account-wide, so fetch its maximum bounded page before
		// applying this app's filter locally.
		result, traceErr := client.GetAccountTraceWithLimit(ctx, traceID, traceLogsLookupLimit)
		if traceErr != nil {
			return printErr("Could not query HTTP logs by trace", traceErr)
		}
		events := make([]api.LogQueryEvent, 0)
		for _, event := range result.Logs {
			if event.App != slug || event.Source != api.LogSourceHTTP ||
				!httpLogEventMatches(event, deploymentID, route, since, status, now) {
				continue
			}
			events = append(events, event)
		}
		if len(events) > limit {
			events = events[:limit]
		}
		if code := emitHTTPLogQueryEvents(events); code != 0 {
			return code
		}
		if result.LogsTruncated {
			PrintWarn(osStderr, "Trace results contain more than the bounded HTTP log page; this app's output may be incomplete.")
		}
		if result.Partial {
			for _, item := range result.Errors {
				_, _ = fmt.Fprintln(osStderr, traceLookupErrorLine(item))
			}
			return 3
		}
		return 0
	}

	var rows []api.DebugTelemetryRequestItem
	var page *api.DebugTelemetryListResponse
	if requestID != "" {
		row, getErr := client.GetAppDebugRequest(ctx, slug, requestID)
		if getErr != nil {
			if routeNotOffered(getErr) {
				return printHTTPLogsUnavailable()
			}
			return printErr("Could not get HTTP request log", getErr)
		}
		if httpLogRowMatches(row, deploymentID, route, since, status, now) {
			rows = append(rows, row)
		}
	} else if all {
		return streamAllHTTPLogsQuery(ctx, client, slug, api.DebugTelemetryListOptions{
			Since:        since,
			Route:        route,
			DeploymentID: deploymentID,
			Status:       status,
			Limit:        limit,
		})
	} else {
		resp, listErr := client.ListAppDebugRequestsWithOptions(ctx, slug, api.DebugTelemetryListOptions{
			Since:        since,
			Route:        route,
			DeploymentID: deploymentID,
			Status:       status,
			Limit:        limit,
		})
		if listErr != nil {
			if routeNotOffered(listErr) {
				return printHTTPLogsUnavailable()
			}
			return printErr("Could not query HTTP logs", listErr)
		}
		rows = resp.Requests
		page = &resp
	}

	code := emitHTTPLogQueryRows(rows)
	if code != 0 {
		return code
	}
	if page != nil {
		renderHTTPLogQueryPageWarnings(osStderr, *page)
	}
	return 0
}

// streamAllHTTPLogsQuery emits each bounded page before fetching the next one.
// A retained telemetry window can contain far more rows than fit in memory.
func streamAllHTTPLogsQuery(ctx context.Context, client *api.Client, slug string, opts api.DebugTelemetryListOptions) int {
	for {
		page, err := client.ListAppDebugRequestsWithOptions(ctx, slug, opts)
		if err != nil {
			if routeNotOffered(err) {
				return printHTTPLogsUnavailable()
			}
			return printErr("Could not query all HTTP logs", err)
		}
		if code := emitHTTPLogQueryRows(page.Requests); code != 0 {
			return code
		}
		if page.RetentionClamped && opts.Cursor == "" {
			PrintWarn(osStderr, "HTTP log window was clamped to the plan's telemetry retention.")
		}
		if page.Complete || page.NextCursor == "" {
			return 0
		}
		if page.NextCursor == opts.Cursor {
			return printErr("Could not query all HTTP logs", fmt.Errorf("debug requests cursor did not advance"))
		}
		opts.Cursor = page.NextCursor
		if err := ctx.Err(); err != nil {
			return printErr("Could not query all HTTP logs", err)
		}
	}
}

func emitHTTPLogQueryRows(rows []api.DebugTelemetryRequestItem) int {
	events := make([]api.LogQueryEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, httpLogQueryEvent(row))
	}
	return emitHTTPLogQueryEvents(events)
}

func emitHTTPLogQueryEvents(events []api.LogQueryEvent) int {
	if jsonOutput {
		return jsonOut(writeNDJSON(events))
	}
	if len(events) == 0 {
		_, _ = fmt.Fprintln(osStderr, "No HTTP requests matched these filters.")
		return 0
	}
	for _, event := range events {
		renderHTTPLogQueryEvent(osStdout, event)
	}
	return 0
}

func httpLogEventMatches(event api.LogQueryEvent, deploymentID, route, since string, status int, now time.Time) bool {
	if deploymentID != "" && event.DeploymentID != deploymentID {
		return false
	}
	if route != "" && event.Route != route {
		return false
	}
	if status != 0 && event.Status != status {
		return false
	}
	if since == "" {
		return true
	}
	lookback, err := parsePositiveLogsDuration(since)
	if err != nil {
		return false
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		return false
	}
	return !receivedAt.Before(now.Add(-lookback))
}

func renderHTTPLogQueryPageWarnings(w io.Writer, page api.DebugTelemetryListResponse) {
	if page.RetentionClamped {
		PrintWarn(w, "HTTP log window was clamped to the plan's telemetry retention.")
	}
	if !page.Complete {
		PrintWarn(w, "More HTTP log rows are available; rerun with --all to read every retained page.")
	}
}

func httpLogRowMatches(row api.DebugTelemetryRequestItem, deploymentID, route, since string, status int, now time.Time) bool {
	if deploymentID != "" && row.DeploymentID != deploymentID {
		return false
	}
	if route != "" && row.Route != route {
		return false
	}
	if status != 0 && row.Status != status {
		return false
	}
	if since == "" {
		return true
	}
	lookback, err := parsePositiveLogsDuration(since)
	if err != nil {
		return false
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, row.ReceivedAt)
	if err != nil {
		return false
	}
	return !receivedAt.Before(now.Add(-lookback))
}

func httpLogQueryEvent(row api.DebugTelemetryRequestItem) api.LogQueryEvent {
	traceID := ""
	if row.TraceID != nil {
		traceID = *row.TraceID
	}
	return api.LogQueryEvent{
		ID:           row.ID,
		Timestamp:    row.ReceivedAt,
		Source:       api.LogSourceHTTP,
		DeploymentID: row.DeploymentID,
		InstanceID:   row.InstanceID,
		RequestID:    row.RequestID,
		TraceID:      traceID,
		Route:        row.Route,
		Method:       row.Method,
		Status:       row.Status,
		Message:      fmt.Sprintf("%s returned %d in %dms", requestLine(row.Method, row.Route), row.Status, row.LatencyMS),
		LatencyMS:    row.LatencyMS,
		Count:        row.Count,
		ColdBoot:     row.ColdBoot,
	}
}

func renderHTTPLogQueryEvent(w io.Writer, event api.LogQueryEvent) {
	requestID := event.RequestID
	if requestID == "" {
		requestID = event.ID
	}
	_, _ = fmt.Fprintf(w, "%s http status=%d method=%s route=%q latency=%dms request=%s deployment=%s",
		event.Timestamp, event.Status, event.Method, event.Route, event.LatencyMS, requestID, event.DeploymentID)
	if event.TraceID != "" {
		_, _ = fmt.Fprintf(w, " trace=%s", event.TraceID)
	}
	if event.Count > 1 {
		_, _ = fmt.Fprintf(w, " count=%d", event.Count)
	}
	if event.ColdBoot {
		_, _ = fmt.Fprint(w, " cold_boot=true")
	}
	_, _ = fmt.Fprintln(w)
}

// validateHTTPLogRoute rejects a bare path for --route. Request telemetry
// stores route labels as "METHOD /path" and the filter is an exact match, so
// "/orders" matched nothing and the command printed nothing (H10-44).
func validateHTTPLogRoute(route string) error {
	if !strings.HasPrefix(route, "/") {
		return nil
	}
	return fmt.Errorf("--route matches a route label with its method, such as \"GET %s\" or \"POST %s\"", route, route)
}
