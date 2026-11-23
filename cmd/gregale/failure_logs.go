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
	body, err := client.StreamAppLogs(ctx, slug, deploymentID, false, api.LogFilter{})
	if err != nil && deploymentID != "" {
		body, err = client.StreamAppLogs(ctx, slug, "", false, api.LogFilter{})
	}
	if err != nil {
		return nil
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	var lines []api.LogExcerpt
	_, _ = consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
		switch e.Event {
		case "degraded", "end", "error":
			// No running instance, the page ended, or the stream failed.
			return true, 0
		}
		if line, ok := failureLogExcerpt(e.Data); ok {
			lines = append(lines, line)
			if len(lines) > failureLogLines {
				lines = lines[1:]
			}
		}
		return false, 0
	})
	return lines
}

// failureLogExcerpt reads one log frame: the platform's JSON record, or a
// plain line.
func failureLogExcerpt(data string) (api.LogExcerpt, bool) {
	data = strings.TrimSpace(data)
	if data == "" {
		return api.LogExcerpt{}, false
	}
	var record struct {
		Line   *string `json:"line"`
		Stream string  `json:"stream"`
		TS     string  `json:"ts"`
		Level  string  `json:"level"`
	}
	excerpt := api.LogExcerpt{Message: data}
	if strings.HasPrefix(data, "{") && json.Unmarshal([]byte(data), &record) == nil && record.Line != nil {
		excerpt = api.LogExcerpt{Timestamp: record.TS, Level: record.Level, Source: record.Stream, Message: *record.Line}
	}
	excerpt.Message = strings.TrimRight(excerpt.Message, "\r\n")
	if excerpt.Message == "" {
		return api.LogExcerpt{}, false
	}
	if len(excerpt.Message) > failureLogMaxLine {
		excerpt.Message = clip(excerpt.Message, failureLogMaxLine)
	}
	return excerpt, true
}
