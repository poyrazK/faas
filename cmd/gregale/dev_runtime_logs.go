package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devRuntimeLogRetryInitial = time.Second
	devRuntimeLogRetryMax     = 5 * time.Second
)

func followDevRuntimeLogsWithDiagnostics(ctx context.Context, client *Client, slug string, onDiagnostic func(devDiagnostic)) {
	retry := devRuntimeLogRetryInitial
	seen := make(map[string]struct{})
	report := func(d devDiagnostic) {
		if onDiagnostic == nil || d.Code == "" {
			return
		}
		if _, ok := seen[d.Code]; ok {
			return
		}
		seen[d.Code] = struct{}{}
		onDiagnostic(d)
	}
	for {
		err := streamDevRuntimeLogsWithDiagnostics(ctx, client, slug, report)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			PrintWarn(osStderr, "runtime log stream interrupted: %v; reconnecting", err)
		} else {
			PrintWarn(osStderr, "runtime log stream ended; reconnecting")
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if retry < devRuntimeLogRetryMax {
			retry *= 2
			if retry > devRuntimeLogRetryMax {
				retry = devRuntimeLogRetryMax
			}
		}
	}
}

func streamDevRuntimeLogs(ctx context.Context, client *Client, slug string) error {
	return streamDevRuntimeLogsWithDiagnostics(ctx, client, slug, nil)
}

func streamDevRuntimeLogsWithDiagnostics(ctx context.Context, client *Client, slug string, onDiagnostic func(devDiagnostic)) error {
	body, err := client.StreamAppLogs(ctx, slug, "", true, api.LogFilter{})
	if err != nil {
		return fmt.Errorf("open app log stream: %w", err)
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-dec.Events():
			if !ok {
				return nil
			}
			done, eventErr := renderDevRuntimeLogEventWithDiagnostics(event, onDiagnostic)
			if eventErr != nil {
				return eventErr
			}
			if done {
				return nil
			}
		case streamErr := <-dec.Errors():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if streamErr != nil && !errors.Is(streamErr, io.EOF) {
				return streamErr
			}
			// The decoder may have buffered events before publishing EOF.
			// Drain them so a clean disconnect never drops the last lines.
			for {
				select {
				case event, ok := <-dec.Events():
					if !ok {
						return nil
					}
					done, eventErr := renderDevRuntimeLogEventWithDiagnostics(event, onDiagnostic)
					if eventErr != nil {
						return eventErr
					}
					if done {
						return nil
					}
				default:
					return nil
				}
			}
		}
	}
}

func renderDevRuntimeLogEventWithDiagnostics(event api.Event, onDiagnostic func(devDiagnostic)) (bool, error) {
	switch event.Event {
	case "log":
		if event.Data != "" {
			_, _ = fmt.Fprintln(osStdout, formatDevRuntimeLog(event.Data))
			var entry api.LogEvent
			line := event.Data
			if err := json.Unmarshal([]byte(event.Data), &entry); err == nil && entry.Line != "" {
				line = entry.Line
			}
			if onDiagnostic != nil {
				if diagnostic, ok := classifyDevRuntimeLog(line); ok {
					onDiagnostic(diagnostic)
				}
			}
		}
	case "gap":
		PrintWarn(osStderr, "runtime log gap: some earlier lines were not retained")
	case "degraded":
		return false, errors.New("scheduler temporarily unavailable")
	case "end":
		return true, nil
	}
	return false, nil
}

func formatDevRuntimeLog(data string) string {
	var event api.LogEvent
	if err := json.Unmarshal([]byte(data), &event); err == nil && event.Line != "" {
		stream := event.Stream
		if stream == "" {
			stream = "stdout"
		}
		return fmt.Sprintf("runtime %s | %s", stream, event.Line)
	}
	return "runtime | " + data
}
