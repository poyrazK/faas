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
	// devRuntimeLogParkedRetry is how often the follower looks for the
	// environment waking up again after the stream ended because it parked.
	devRuntimeLogParkedRetry = 15 * time.Second
)

// devStreamEnd is why one log stream ended.
type devStreamEnd int

const (
	devStreamEnded  devStreamEnd = iota // a clean end (timeout backstop or similar)
	devStreamParked                     // the environment has no running instance
)

// devLogFollower carries state across reconnects: the newest sequence
// number printed per instance, so the retained lines a reconnect replays are
// not printed twice, and whether the parked notice was shown.
type devLogFollower struct {
	lastSeq      map[string]int64
	onDiagnostic func(devDiagnostic)
}

func newDevLogFollower(onDiagnostic func(devDiagnostic)) *devLogFollower {
	return &devLogFollower{lastSeq: map[string]int64{}, onDiagnostic: onDiagnostic}
}

// JSON mode follows the same stream for diagnostics, without rendering raw
// application lines. Join on stop so no output survives the command's return.
func startDevRuntimeLogs(ctx context.Context, client *Client, slug string, once, noLogs bool, onDiagnostic func(devDiagnostic)) func() {
	if once || noLogs {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if !jsonOutput {
		PrintProgress(osStdout, "runtime logs attached (Ctrl-C to stop watching)")
	}
	go func() {
		defer close(done)
		followDevRuntimeLogsWithDiagnostics(ctx, client, slug, onDiagnostic)
	}()
	return func() {
		cancel()
		<-done
	}
}

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
	follower := newDevLogFollower(report)
	parked := false
	for {
		started := time.Now()
		end, err := follower.stream(ctx, client, slug)
		if ctx.Err() != nil {
			return
		}
		delay := retry
		switch {
		case err == nil && end == devStreamParked:
			// A parked developer environment is normal between edits; say
			// so once and look again slowly instead of warning every few
			// seconds until the next request wakes it.
			if !parked && !jsonOutput {
				PrintProgress(osStdout, "runtime logs paused: the environment is parked until its next request")
			}
			parked = true
			delay = devRuntimeLogParkedRetry
		case err != nil:
			parked = false
			PrintWarn(osStderr, "runtime log stream interrupted: %v; reconnecting", err)
		default:
			parked = false
		}
		if time.Since(started) > devRuntimeLogRetryMax {
			retry = devRuntimeLogRetryInitial // a stream that ran a while resets the backoff
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay == retry && retry < devRuntimeLogRetryMax {
			retry = min(retry*2, devRuntimeLogRetryMax)
		}
	}
}

func streamDevRuntimeLogs(ctx context.Context, client *Client, slug string) error {
	_, err := newDevLogFollower(nil).stream(ctx, client, slug)
	return err
}

func (f *devLogFollower) stream(ctx context.Context, client *Client, slug string) (devStreamEnd, error) {
	body, err := client.StreamAppLogs(ctx, slug, "", true, api.LogFilter{})
	if err != nil {
		return devStreamEnded, fmt.Errorf("open app log stream: %w", err)
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()

	for {
		select {
		case <-ctx.Done():
			return devStreamEnded, ctx.Err()
		case event, ok := <-dec.Events():
			if !ok {
				return devStreamEnded, nil
			}
			end, done, eventErr := f.render(event)
			if eventErr != nil || done {
				return end, eventErr
			}
		case streamErr := <-dec.Errors():
			if ctx.Err() != nil {
				return devStreamEnded, ctx.Err()
			}
			if streamErr != nil && !errors.Is(streamErr, io.EOF) {
				return devStreamEnded, streamErr
			}
			// The decoder may have buffered events before publishing EOF.
			// Drain them so a clean disconnect never drops the last lines.
			for {
				select {
				case event, ok := <-dec.Events():
					if !ok {
						return devStreamEnded, nil
					}
					end, done, eventErr := f.render(event)
					if eventErr != nil || done {
						return end, eventErr
					}
				default:
					return devStreamEnded, nil
				}
			}
		}
	}
}

// render prints one stream event. done reports the stream's terminal event.
func (f *devLogFollower) render(event api.Event) (devStreamEnd, bool, error) {
	switch event.Event {
	case "log":
		if event.Data == "" {
			return devStreamEnded, false, nil
		}
		var entry api.LogEvent
		parsed := json.Unmarshal([]byte(event.Data), &entry) == nil && entry.Line != ""
		if parsed && entry.Seq > 0 {
			// A reconnect replays the instance's retained lines; print each
			// line once.
			if entry.Seq <= f.lastSeq[entry.InstanceID] {
				return devStreamEnded, false, nil
			}
			f.lastSeq[entry.InstanceID] = entry.Seq
		}
		if !jsonOutput {
			_, _ = fmt.Fprintln(osStdout, formatDevRuntimeLog(event.Data))
		}
		line := event.Data
		if parsed {
			line = entry.Line
		}
		if f.onDiagnostic != nil {
			if diagnostic, ok := classifyDevRuntimeLog(line); ok {
				f.onDiagnostic(diagnostic)
			}
		}
	case "gap":
		PrintWarn(osStderr, "runtime log gap: some earlier lines were not retained")
	case "degraded":
		return devStreamEnded, false, errors.New("scheduler temporarily unavailable")
	case "end":
		var end struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(event.Data), &end) == nil && end.Reason == "not_found" {
			return devStreamParked, true, nil
		}
		return devStreamEnded, true, nil
	}
	return devStreamEnded, false, nil
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
