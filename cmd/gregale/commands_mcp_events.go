package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpEventFilter struct{ tool, outcome, request string }
type mcpEventStream struct {
	filter         mcpEventFilter
	partial, ended bool
}

func cmdMCPEvents(args []string) int {
	fs := newFlagSet("mcp-events", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked app)")
	tool := fs.String("tool", "", "registered tool name")
	outcome := fs.String("outcome", "", "event outcome")
	request := fs.String("request", "", "server-generated MCP request ID")
	since := fs.String("since", "", "lookback duration or RFC3339 timestamp")
	deployment := fs.String("deployment", "", "deployment ID or vN")
	follow := fs.Bool("follow", false, "follow live events")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || (*tool != "" && !mcphosting.ValidEventToolName(*tool)) ||
		(*outcome != "" && !mcphosting.ValidEventOutcome(*outcome)) || (*request != "" && !mcphosting.ValidEventRequestID(*request)) {
		return printErr("MCP events", errors.New("invalid tool, outcome, request ID or positional arguments"))
	}
	lowerBound, err := normalizeLogsSince(*since, logsSourceRuntime, time.Now())
	if err != nil {
		return printErr("MCP events", err)
	}
	slug, err := resolveAppFlagOrContext(*app)
	if err != nil {
		return printErr("MCP events", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runMCPEvents(ctx, slug, *deployment, lowerBound, *follow, mcpEventFilter{*tool, *outcome, *request})
}

func runMCPEvents(ctx context.Context, slug, deployment, since string, follow bool, filter mcpEventFilter) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	deployment, err = resolveDeploymentRef(ctx, client, slug, deployment)
	if err != nil {
		return printErr("MCP events deployment", err)
	}
	// Account credentials go only to the existing control-plane logs endpoint.
	body, err := client.StreamAppLogs(ctx, slug, deployment, follow, api.LogFilter{Grep: "mcp_", Since: since})
	if err != nil {
		return printErr("MCP event stream", err)
	}
	defer func() { _ = body.Close() }()
	decoder := api.NewDecoder(body)
	decoder.SetCloseFn(body.Close)
	defer func() { _ = decoder.Close() }()
	stream := mcpEventStream{filter: filter}
	code, streamErr := consumeLogStream(ctx, decoder.Events(), decoder.Errors(), stream.visit)
	if streamErr != nil {
		return printErr("MCP event stream closed", errors.New("log stream interrupted; results are incomplete"))
	}
	if !stream.ended && code == 0 {
		_, _ = fmt.Fprintln(osStderr, "MCP events: log stream interrupted; results are incomplete.")
		return 3
	}
	if stream.partial && code == 0 {
		return 3
	}
	return code
}

func (stream *mcpEventStream) visit(frame api.Event) (bool, int) {
	switch frame.Event {
	case "gap":
		stream.partial = true
		_, _ = fmt.Fprintln(osStderr, "MCP events: retention gap; earlier events are unavailable.")
	case "error", "degraded":
		_, _ = fmt.Fprintln(osStderr, "MCP events: log stream unavailable; results are incomplete.")
		return true, 3
	case "end":
		stream.ended = true
		var end struct {
			Reason string `json:"reason"`
		}
		if !strings.HasPrefix(strings.TrimSpace(frame.Data), "{") || json.Unmarshal([]byte(frame.Data), &end) != nil || end.Reason != "" {
			_, _ = fmt.Fprintln(osStderr, "MCP events: log stream ended early; results are incomplete.")
			return true, 3
		}
		if stream.partial {
			return true, 3
		}
		return true, 0
	case "log":
		if err := emitMCPLogEvent(frame.Data, stream.filter); err != nil {
			return true, printErr("Write MCP event", err)
		}
	}
	return false, 0
}

func emitMCPLogEvent(data string, filter mcpEventFilter) error {
	var log api.LogEvent
	if json.Unmarshal([]byte(data), &log) != nil {
		return nil //nolint:nilerr // Invalid/non-MCP log rows are intentionally skipped.
	}
	event, err := mcphosting.ParseExecutionEvent(log.Line)
	if err != nil || (filter.tool != "" && event.Tool != filter.tool) ||
		(filter.outcome != "" && event.Outcome != filter.outcome) || (filter.request != "" && event.RequestID != filter.request) {
		return nil //nolint:nilerr // Invalid/nonmatching diagnostics are intentionally skipped.
	}
	if jsonOutput {
		return json.NewEncoder(osStdout).Encode(event)
	}
	_, err = fmt.Fprintf(osStdout, "%s tool=%s outcome=%s reason=%s duration=%dms request=%s\n", event.Event, event.Tool, event.Outcome, event.Reason, *event.DurationMS, event.RequestID)
	return err
}
