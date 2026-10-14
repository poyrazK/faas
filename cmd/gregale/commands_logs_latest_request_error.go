package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type requestErrorSelection struct {
	Type             string `json:"type"`
	App              string `json:"app_slug"`
	Profile          string `json:"profile"`
	RequestedSince   string `json:"requested_since"`
	EffectiveSince   string `json:"effective_since,omitempty"`
	WindowStart      string `json:"window_start,omitempty"`
	WindowEnd        string `json:"window_end,omitempty"`
	RetentionClamped bool   `json:"retention_clamped"`
	RouteFilter      string `json:"route_filter,omitempty"`
	Pages            int    `json:"pages"`
	Rows             int    `json:"rows_inspected"`
	RequestID        string `json:"request_id,omitempty"`
	TelemetryID      string `json:"telemetry_id,omitempty"`
	Status           int    `json:"status,omitempty"`
	Route            string `json:"route,omitempty"`
	ReceivedAt       string `json:"received_at,omitempty"`
	Count            int    `json:"represented_requests,omitempty"`
	Message          string `json:"message"`
}

func selectLatestHTTPRequestError(ctx context.Context, client *api.Client, slug, since, route string) (*api.DebugTelemetryRequestItem, requestErrorSelection, error) {
	receipt := requestErrorSelection{Type: "selection", App: slug, Profile: currentProfile(), RequestedSince: since, RouteFilter: route}
	opts := api.DebugTelemetryListOptions{Since: since, Route: route, Limit: 200}
	seen := map[string]bool{}
	var selected *api.DebugTelemetryRequestItem
	var newest time.Time
	for range 50 {
		page, err := client.ListAppDebugRequestsWithOptions(ctx, slug, opts)
		if err != nil {
			return nil, receipt, fmt.Errorf("read HTTP telemetry: %w", err)
		}
		receipt.Pages++
		receipt.Rows += len(page.Requests)
		if receipt.Pages == 1 {
			receipt.EffectiveSince = page.Since
			receipt.WindowStart = page.WindowStart
			receipt.WindowEnd = page.WindowEnd
		}
		if page.WindowStart != receipt.WindowStart || page.WindowEnd != receipt.WindowEnd {
			return nil, receipt, errors.New("HTTP telemetry window changed while paging; reduce --since and retry observation")
		}
		receipt.RetentionClamped = receipt.RetentionClamped || page.RetentionClamped
		for _, row := range page.Requests {
			if row.Status < 500 || row.Status > 599 || route != "" && row.Route != route {
				continue
			}
			when, err := time.Parse(time.RFC3339Nano, row.ReceivedAt)
			if err != nil {
				return nil, receipt, errors.New("a recorded 5xx response has an invalid timestamp; newest error cannot be established")
			}
			if selected == nil || when.After(newest) {
				candidate := row
				selected = &candidate
				newest = when
			}
		}
		if page.NextCursor == "" {
			if !page.Complete {
				return nil, receipt, errors.New("HTTP telemetry coverage is incomplete; newest error cannot be established")
			}
			return selected, receipt, nil
		}
		if seen[page.NextCursor] {
			return nil, receipt, errors.New("HTTP telemetry cursor repeated; newest error cannot be established")
		}
		seen[page.NextCursor] = true
		opts.Cursor = page.NextCursor
	}
	return nil, receipt, errors.New("HTTP telemetry search reached 50 pages; narrow --since or --route")
}

func latestHTTPRequestError(slug, since, route string) int {
	if since == "" {
		since = "15m"
	}
	normalized, err := normalizeLogsSince(since, logsSourceHTTP, time.Now())
	if err != nil {
		return printErr("Invalid request error window", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	selected, receipt, err := selectLatestHTTPRequestError(ctx, client, slug, normalized, route)
	if err != nil {
		return failureLogReadError(ctx, "Could not select latest HTTP request error", err)
	}
	if selected == nil {
		receipt.Message = "No recorded HTTP 5xx response in the searched retained window. This does not establish that every request succeeded or was captured."
		if code := renderRequestErrorSelection(receipt); code != 0 {
			return code
		}
		return 1
	}
	id := selected.RequestID
	if id == "" {
		id = selected.ID
	}
	if id == "" {
		return printErr("Request identifier unavailable", errors.New("newest recorded error has no public request ID or telemetry row ID; an older row was not substituted"))
	}
	row, err := client.GetAppDebugRequest(ctx, slug, id)
	if err != nil {
		return failureLogReadError(ctx, "Could not read selected HTTP request", err)
	}
	if selected.ID != "" && row.ID != selected.ID || selected.RequestID != "" && row.RequestID != selected.RequestID || row.Status != selected.Status || row.Route != selected.Route || row.ReceivedAt != selected.ReceivedAt {
		return printErr("Selected HTTP request changed", errors.New("request lookup did not match the selected error; repeat read-only observation"))
	}
	receipt.RequestID, receipt.TelemetryID, receipt.Status, receipt.Route, receipt.ReceivedAt, receipt.Count = row.RequestID, row.ID, row.Status, row.Route, row.ReceivedAt, row.Count
	receipt.Message = "Newest recorded 5xx telemetry row in the searched retained window. Aggregated rows may represent multiple requests; runtime logs are separate evidence."
	if code := renderRequestErrorSelection(receipt); code != 0 {
		return code
	}
	return emitHTTPLogQueryRows([]api.DebugTelemetryRequestItem{row})
}

func renderRequestErrorSelection(receipt requestErrorSelection) int {
	if jsonOutput {
		return jsonOut(json.NewEncoder(osStdout).Encode(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "HTTP error search: app=%s; profile=%s; requested window=%s; route=%s\nRetained window: %s to %s; effective lookback=%s; retention clamped=%t\n%s\n", oneLine(receipt.App), oneLine(receipt.Profile), oneLine(receipt.RequestedSince), oneLine(receipt.RouteFilter), oneLine(receipt.WindowStart), oneLine(receipt.WindowEnd), oneLine(receipt.EffectiveSince), receipt.RetentionClamped, receipt.Message)
	if receipt.Status != 0 {
		_, _ = fmt.Fprintf(osStdout, "Selected: status=%d; route=%s; request=%s; row=%s; received=%s; represented requests=%d\n", receipt.Status, oneLine(receipt.Route), oneLine(receipt.RequestID), oneLine(receipt.TelemetryID), oneLine(receipt.ReceivedAt), receipt.Count)
	}
	return 0
}
