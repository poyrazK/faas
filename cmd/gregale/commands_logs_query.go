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
	logsSourceRuntime = "runtime"
	logsSourceHTTP    = "http"
)

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

func runHTTPLogsQuery(ctx context.Context, slug, deploymentID, requestID, route, since string, status, limit int, all bool, now time.Time) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	var rows []api.DebugTelemetryRequestItem
	var page *api.DebugTelemetryListResponse
	if requestID != "" {
		row, getErr := client.GetAppDebugRequest(ctx, slug, requestID)
		if getErr != nil {
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
	if jsonOutput {
		enc := make([]api.LogQueryEvent, 0, len(rows))
		for _, row := range rows {
			enc = append(enc, httpLogQueryEvent(row))
		}
		return jsonOut(writeNDJSON(enc))
	}
	for _, row := range rows {
		renderHTTPLogQueryEvent(osStdout, httpLogQueryEvent(row))
	}
	return 0
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
	requestID := ""
	if row.TraceID != nil {
		requestID = *row.TraceID
	}
	return api.LogQueryEvent{
		ID:           row.ID,
		Timestamp:    row.ReceivedAt,
		Source:       api.LogSourceHTTP,
		DeploymentID: row.DeploymentID,
		InstanceID:   row.InstanceID,
		RequestID:    requestID,
		TraceID:      requestID,
		Route:        row.Route,
		Method:       row.Method,
		Status:       row.Status,
		Message:      fmt.Sprintf("%s %s returned %d in %dms", row.Method, row.Route, row.Status, row.LatencyMS),
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
	if event.Count > 1 {
		_, _ = fmt.Fprintf(w, " count=%d", event.Count)
	}
	if event.ColdBoot {
		_, _ = fmt.Fprint(w, " cold_boot=true")
	}
	_, _ = fmt.Fprintln(w)
}
