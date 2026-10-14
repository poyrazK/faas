package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type mcpDiagnosticFailure struct {
	RequestID   string  `json:"request_id,omitempty"`
	TraceID     *string `json:"trace_id,omitempty"`
	Status      int     `json:"status"`
	Outcome     string  `json:"outcome"`
	LatencyMS   int     `json:"latency_ms"`
	Count       int     `json:"count"`
	ReceivedAt  string  `json:"received_at"`
	InspectPath string  `json:"inspect_path,omitempty"`
}
type mcpDiagnosticsReport struct {
	RetentionClamped    bool                   `json:"retention_clamped"`
	App                 string                 `json:"app"`
	Route               string                 `json:"route"`
	Since               string                 `json:"since"`
	WindowStart         string                 `json:"window_start"`
	WindowEnd           string                 `json:"window_end"`
	Complete            bool                   `json:"complete"`
	NextCursor          string                 `json:"next_cursor,omitempty"`
	RepresentedRequests int                    `json:"represented_requests"`
	Outcomes            map[string]int         `json:"outcomes"`
	Failures            []mcpDiagnosticFailure `json:"failures"`
}

func cmdMCPDiagnostics(args []string) int {
	fs := newFlagSet("mcp-diagnostics", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	route := fs.String("route", "/mcp", "retained route label for the MCP endpoint")
	since := fs.String("since", "1h", "request telemetry window")
	cursor := fs.String("cursor", "", "opaque cursor for the next page")
	limit := fs.Int("limit", 200, "retained rows per page (1 to 200)")
	timeout := fs.Duration("timeout", 30*time.Second, "API deadline")
	if fs.Parse(args) != nil {
		return 1
	}
	duration, err := time.ParseDuration(*since)
	if fs.NArg() != 0 || *app == "" || *route == "" || err != nil || duration <= 0 || *limit < 1 || *limit > 200 || *timeout <= 0 || *timeout > time.Hour {
		return printErr("MCP diagnostics", errors.New("invalid app, route, since, limit or timeout"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	page, err := client.ListAppDebugRequestsWithOptions(ctx, *app, api.DebugTelemetryListOptions{Since: *since, Route: *route, Cursor: *cursor, Limit: *limit})
	if err != nil {
		return printErr("MCP diagnostics", err)
	}
	report := summarizeMCPDiagnostics(*app, *route, page)
	if jsonOutput {
		return writeJSONStdout(report)
	}
	_, _ = fmt.Fprintf(osStdout, "MCP diagnostics for %s (%s): %d represented requests in this page\n", *app, *route, report.RepresentedRequests)
	for _, failure := range report.Failures {
		_, _ = fmt.Fprintf(osStdout, "  %s: HTTP %d, %d ms, %d requests; request=%s\n    Inspect: %s\n", failure.Outcome, failure.Status, failure.LatencyMS, failure.Count, failure.RequestID, failure.InspectPath)
	}
	if report.RetentionClamped {
		_, _ = fmt.Fprintln(osStdout, "Requested window was clamped to plan retention.")
	}
	if !report.Complete {
		_, _ = fmt.Fprintf(osStdout, "Partial page; continue with --cursor %s\n", report.NextCursor)
	}
	_, _ = fmt.Fprintln(osStdout, "HTTP success does not prove RPC success. Use request inspection for retained execution evidence and logs.")
	return 0
}
func summarizeMCPDiagnostics(app, route string, page api.DebugTelemetryListResponse) mcpDiagnosticsReport {
	report := mcpDiagnosticsReport{App: app, Route: route, Since: page.Since, WindowStart: page.WindowStart, WindowEnd: page.WindowEnd, RetentionClamped: page.RetentionClamped, Complete: page.Complete, NextCursor: page.NextCursor, Outcomes: map[string]int{}, Failures: []mcpDiagnosticFailure{}}
	for _, row := range page.Requests {
		count := row.Count
		if count < 1 {
			count = 1
		}
		outcome := api.MCPHTTPOutcome(row.Status)
		report.RepresentedRequests += count
		report.Outcomes[outcome] += count
		if outcome == "http_success" {
			continue
		}
		id := row.RequestID
		if id == "" {
			id = row.ID
		}
		path := ""
		if id != "" {
			path = "/v1/apps/" + url.PathEscape(app) + "/debug/requests/" + url.PathEscape(id) + "/evidence"
		}
		report.Failures = append(report.Failures, mcpDiagnosticFailure{RequestID: row.RequestID, TraceID: row.TraceID, Status: row.Status, Outcome: outcome, LatencyMS: row.LatencyMS, Count: count, ReceivedAt: row.ReceivedAt, InspectPath: path})
	}
	return report
}
