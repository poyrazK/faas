package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// HTTP request rows are folded per minute by the gateway and written once the
// minute closes, so a request becomes readable roughly one to two minutes
// after it was served. `logs --follow --source http` polls a short trailing
// window and prints each row once, which also picks up a row for an older
// minute that was flushed late. Rows are insert-only, so a row id never
// changes content after it was printed.
var (
	// httpLogsFollowInterval is how often the follow loop re-reads the window.
	httpLogsFollowInterval = 10 * time.Second
	// httpLogsFollowMaxPolls bounds the loop in tests; 0 follows until
	// interrupted.
	httpLogsFollowMaxPolls = 0
)

const (
	// httpLogsFollowWindow is the trailing window every poll after the first
	// reads. It covers the minute bucket, the flush delay and missed polls.
	httpLogsFollowWindow = 5 * time.Minute
	// httpLogsFollowInitialWindow is the backlog shown when --since is absent.
	httpLogsFollowInitialWindow = "2m"
	// httpLogsFollowMaxPages bounds one poll; a busier window is reported
	// instead of read without limit.
	httpLogsFollowMaxPages = 10
	// httpLogsFollowMaxFailures ends the loop after this many polls in a row
	// failed.
	httpLogsFollowMaxFailures = 5
)

// httpLogsLagNote explains why a just-served request is not listed yet.
const httpLogsLagNote = "HTTP requests are recorded per minute and appear about a minute after that minute ends."

// followHTTPLogs implements `gregale logs <slug> --follow --source http`.
// Exits 130 on Ctrl-C like the runtime follow.
func followHTTPLogs(ctx context.Context, slug string, opts api.DebugTelemetryListOptions) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.Since == "" {
		opts.Since = httpLogsFollowInitialWindow
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Following HTTP requests for %s. %s Ctrl-C to exit.\n", slug, httpLogsLagNote)
	}
	seen := map[string]time.Time{}
	failures := 0
	warnedTruncated := false
	for poll := 1; ; poll++ {
		rows, truncated, pollErr := readHTTPLogsFollowWindow(ctx, client, slug, opts)
		switch {
		case ctx.Err() != nil:
			return 130
		case pollErr != nil && routeNotOffered(pollErr):
			return printHTTPLogsUnavailable()
		case pollErr != nil:
			failures++
			if poll == 1 || failures >= httpLogsFollowMaxFailures {
				return printErr("Could not query HTTP logs", pollErr)
			}
			PrintWarn(osStderr, "Could not read new HTTP requests (%v); retrying.", pollErr)
		default:
			failures = 0
			if code := emitNewHTTPLogFollowRows(rows, seen); code != 0 {
				return code
			}
			if truncated && !warnedTruncated && !jsonOutput {
				warnedTruncated = true
				PrintWarn(osStderr, "More HTTP requests arrived than one poll reads; some rows were skipped. Narrow with --route or --status, or read the window with --all instead of --follow.")
			}
		}
		pruneHTTPLogFollowSeen(seen, time.Now().Add(-2*httpLogsFollowWindow))
		if httpLogsFollowMaxPolls > 0 && poll >= httpLogsFollowMaxPolls {
			return 0
		}
		opts.Since = httpLogsFollowWindow.String()
		timer := time.NewTimer(httpLogsFollowInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 130
		case <-timer.C:
		}
	}
}

// readHTTPLogsFollowWindow reads up to httpLogsFollowMaxPages pages of the
// window and reports whether rows remained unread.
func readHTTPLogsFollowWindow(ctx context.Context, client *api.Client, slug string, opts api.DebugTelemetryListOptions) ([]api.DebugTelemetryRequestItem, bool, error) {
	var rows []api.DebugTelemetryRequestItem
	opts.Cursor = ""
	for page := 0; page < httpLogsFollowMaxPages; page++ {
		resp, err := client.ListAppDebugRequestsWithOptions(ctx, slug, opts)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, resp.Requests...)
		if resp.Complete || resp.NextCursor == "" || resp.NextCursor == opts.Cursor {
			return rows, false, nil
		}
		opts.Cursor = resp.NextCursor
	}
	return rows, true, nil
}

// emitNewHTTPLogFollowRows prints rows not printed before, oldest first.
func emitNewHTTPLogFollowRows(rows []api.DebugTelemetryRequestItem, seen map[string]time.Time) int {
	// seen is keyed by row and stamped with when a poll last returned it, not
	// with the row's minute: a row stays remembered while polls keep
	// returning it, however old its minute is (an initial --since 3d window).
	now := time.Now()
	fresh := make([]api.DebugTelemetryRequestItem, 0, len(rows))
	for _, row := range rows {
		key := httpLogFollowKey(row)
		_, printed := seen[key]
		seen[key] = now
		if printed {
			continue
		}
		fresh = append(fresh, row)
	}
	if len(fresh) == 0 {
		return 0
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if fresh[i].ReceivedAt != fresh[j].ReceivedAt {
			return fresh[i].ReceivedAt < fresh[j].ReceivedAt
		}
		return httpLogFollowKey(fresh[i]) < httpLogFollowKey(fresh[j])
	})
	events := make([]api.LogQueryEvent, 0, len(fresh))
	for _, row := range fresh {
		events = append(events, httpLogQueryEvent(row))
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(events))
	}
	for _, event := range events {
		renderHTTPLogQueryEvent(osStdout, event)
	}
	return 0
}

func httpLogFollowKey(row api.DebugTelemetryRequestItem) string {
	if row.ID != "" {
		return row.ID
	}
	return row.ReceivedAt + "|" + row.RequestID + "|" + row.Method + "|" + row.Route + "|" + strconv.Itoa(row.Status)
}

// runtimeFollowIdleHintAfter is how long `logs --follow` waits without a
// runtime line before it says what runtime logs contain.
var runtimeFollowIdleHintAfter = 20 * time.Second

// runtimeFollowIdleHintShown keeps the hint to once per process; a follow
// stream that reconnects after its idle timeout does not repeat it.
var runtimeFollowIdleHintShown atomic.Bool

// startRuntimeFollowIdleHint prints, once, why a runtime follow is quiet: an
// app that writes nothing to stdout or stderr (the hello-go starter, for one)
// shows no runtime lines however many requests it serves. markLine records
// that a line arrived; stop cancels the hint.
func startRuntimeFollowIdleHint(slug string) (markLine func(), stop func()) {
	var sawLine atomic.Bool
	fired := make(chan struct{})
	timer := time.AfterFunc(runtimeFollowIdleHintAfter, func() {
		defer close(fired)
		if sawLine.Load() || !runtimeFollowIdleHintShown.CompareAndSwap(false, true) {
			return
		}
		_, _ = fmt.Fprintf(osStderr, "No runtime output yet: runtime logs show only what %s writes to stdout and stderr. To watch its HTTP requests: %s logs %s --follow --source http\n",
			slug, recoveryCLI(), recoveryArgument(slug))
	})
	// stop waits for a hint already being written so it never lands after
	// the command returned.
	return func() { sawLine.Store(true) }, func() {
		if !timer.Stop() {
			<-fired
		}
	}
}

func pruneHTTPLogFollowSeen(seen map[string]time.Time, before time.Time) {
	for key, at := range seen {
		if at.Before(before) {
			delete(seen, key)
		}
	}
}
