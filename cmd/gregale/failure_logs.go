// adr: 20261123230000

package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A failed deploy shows the app's last log lines with the failure: the
// 2026-11-23 agent eval's voting-app worker failed with "workload stopped
// during startup" and a pointer to `gregale logs`, while the cause
// (`permission denied for schema public`) sat in the app's log.

const (
	failureLogLines   = 20
	failureLogShown   = 10
	failureLogMaxLine = 512
	failureLogTimeout = 3 * time.Second
)

// failureLogClient reads an app's log page; *Client implements it.
type failureLogClient interface {
	StreamAppLogs(ctx context.Context, slug, deploymentID string, follow bool, opts api.LogFilter) (io.ReadCloser, error)
}

// recentFailureLogs reads the newest log lines of a failed deployment,
// falling back to the app's lines when the plan does not filter by
// deployment. Any error returns what it has: the failure is reported
// either way.
func recentFailureLogs(ctx context.Context, client failureLogClient, slug, deploymentID string) []api.LogExcerpt {
	if client == nil || slug == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, failureLogTimeout)
	defer cancel()
	lines, refused := readFailureLogs(ctx, client, slug, deploymentID)
	if refused && len(lines) == 0 && deploymentID != "" {
		// The Free plan refuses ?deployment= inside a successful stream
		// (plan_deployment_filter_not_allowed); the app's own lines are the
		// next best thing.
		lines, _ = readFailureLogs(ctx, client, slug, "")
	}
	return lines
}

// readFailureLogs reads one log page. refused reports a request the API
// turned down, as an HTTP error or an in-stream error event.
func readFailureLogs(ctx context.Context, client failureLogClient, slug, deploymentID string) (lines []api.LogExcerpt, refused bool) {
	body, err := client.StreamAppLogs(ctx, slug, deploymentID, false, api.LogFilter{})
	if err != nil {
		return nil, true
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	_, _ = consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
		switch e.Event {
		case "error":
			refused = true
			return true, 0
		case "degraded", "end":
			// No running instance, or the page ended.
			return true, 0
		case "", "log":
		default:
			// ping, gap and other control frames are not log lines.
			return false, 0
		}
		if line, ok := failureLogExcerpt(e.Data); ok {
			lines = append(lines, line)
			if len(lines) > failureLogLines {
				lines = lines[1:]
			}
		}
		return false, 0
	})
	return lines, refused
}

// failureLogExcerpt reads one log frame (api.LogEvent); a frame without a
// line is not a log line.
func failureLogExcerpt(data string) (api.LogExcerpt, bool) {
	var row api.LogEvent
	if json.Unmarshal([]byte(strings.TrimSpace(data)), &row) != nil {
		return api.LogExcerpt{}, false
	}
	message := strings.TrimRight(row.Line, "\r\n")
	if message == "" {
		return api.LogExcerpt{}, false
	}
	if len(message) > failureLogMaxLine {
		message = clip(message, failureLogMaxLine)
	}
	return api.LogExcerpt{Timestamp: row.WrittenAt, Level: row.Level, Source: row.Stream, Message: message}, true
}
