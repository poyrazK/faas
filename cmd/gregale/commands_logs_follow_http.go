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
//
// The timings are package variables only so tests can shorten them; nothing
// writes them at run time.
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

// httpLogFollower is the state of one `logs --follow --source http` run.
type httpLogFollower struct {
	client *api.Client
	slug   string
	opts   api.DebugTelemetryListOptions
	// cutoff is the start of the first window, rounded down to its minute.
	// Later polls read a wider trailing window; rows older than the backlog
	// the user asked for are not printed as if they were new.
	cutoff          time.Time
	seen            map[string]time.Time
	failures        int
	warnedTruncated bool
}

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
	f := &httpLogFollower{client: client, slug: slug, opts: opts, seen: map[string]time.Time{}}
	if lookback, err := parsePositiveLogsDuration(opts.Since); err == nil {
		f.cutoff = time.Now().Add(-lookback).Truncate(time.Minute)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Following HTTP requests for %s. %s Ctrl-C to exit.\n", slug, httpLogsLagNote)
	}
	for poll := 1; ; poll++ {
		if code, done := f.poll(ctx, poll == 1); done {
			return code
		}
		if httpLogsFollowMaxPolls > 0 && poll >= httpLogsFollowMaxPolls {
			return 0
		}
		f.opts.Since = httpLogsFollowWindow.String()
		timer := time.NewTimer(httpLogsFollowInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 130
		case <-timer.C:
		}
	}
}

// poll reads the window once and prints new rows. done reports that the
// follow must end with code.
func (f *httpLogFollower) poll(ctx context.Context, first bool) (code int, done bool) {
	rows, truncated, err := readHTTPLogsFollowWindow(ctx, f.client, f.slug, f.opts)
	switch {
	case ctx.Err() != nil:
		return 130, true
	case err != nil && routeNotOffered(err):
		return printHTTPLogsUnavailable(), true
	case err != nil:
		f.failures++
		if first || f.failures >= httpLogsFollowMaxFailures {
			return printErr("Could not query HTTP logs", err), true
		}
		PrintWarn(osStderr, "Could not read new HTTP requests (%v); retrying.", err)
		return 0, false
	}
	f.failures = 0
	if code := emitNewHTTPLogFollowRows(rows, f.seen, f.cutoff); code != 0 {
		return code, true
	}
	if truncated && !f.warnedTruncated && !jsonOutput {
		f.warnedTruncated = true
		PrintWarn(osStderr, "More HTTP requests arrived than one poll reads; some rows were skipped. Narrow with --route or --status, or read the window with --all instead of --follow.")
	}
	pruneHTTPLogFollowSeen(f.seen, time.Now().Add(-2*httpLogsFollowWindow))
	return 0, false
}

// readHTTPLogsFollowWindow reads up to httpLogsFollowMaxPages pages of the
// window and reports whether rows remained unread. A cursor that does not
// advance counts as unread rows, not as the end of the window.
func readHTTPLogsFollowWindow(ctx context.Context, client *api.Client, slug string, opts api.DebugTelemetryListOptions) ([]api.DebugTelemetryRequestItem, bool, error) {
	var rows []api.DebugTelemetryRequestItem
	opts.Cursor = ""
	for page := 0; page < httpLogsFollowMaxPages; page++ {
		resp, err := client.ListAppDebugRequestsWithOptions(ctx, slug, opts)
		if err != nil {
			return nil, false, err
		}
		rows = append(rows, resp.Requests...)
		if resp.Complete || resp.NextCursor == "" {
			return rows, false, nil
		}
		if resp.NextCursor == opts.Cursor {
			return rows, true, nil
		}
		opts.Cursor = resp.NextCursor
	}
	return rows, true, nil
}

// emitNewHTTPLogFollowRows prints rows not printed before and not older than
// cutoff, oldest first.
func emitNewHTTPLogFollowRows(rows []api.DebugTelemetryRequestItem, seen map[string]time.Time, cutoff time.Time) int {
	// seen is keyed by row and stamped with when a poll last returned it, not
	// with the row's minute: a row stays remembered while polls keep
	// returning it, however old its minute is (an initial --since 3d window).
	now := time.Now()
	type freshRow struct {
		row api.DebugTelemetryRequestItem
		at  time.Time
		key string
	}
	fresh := make([]freshRow, 0, len(rows))
	for _, row := range rows {
		key := httpLogFollowKey(row)
		_, printed := seen[key]
		seen[key] = now
		if printed {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, row.ReceivedAt)
		if err == nil && at.Before(cutoff) {
			continue
		}
		fresh = append(fresh, freshRow{row: row, at: at, key: key})
	}
	if len(fresh) == 0 {
		return 0
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if !fresh[i].at.Equal(fresh[j].at) {
			return fresh[i].at.Before(fresh[j].at)
		}
		return fresh[i].key < fresh[j].key
	})
	events := make([]api.LogQueryEvent, 0, len(fresh))
	for _, item := range fresh {
		events = append(events, httpLogQueryEvent(item.row))
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

func pruneHTTPLogFollowSeen(seen map[string]time.Time, before time.Time) {
	for key, at := range seen {
		if at.Before(before) {
			delete(seen, key)
		}
	}
}

// runtimeFollowIdleHintAfter is how long `logs --follow` waits without a
// runtime line before it says what runtime logs contain. A variable only so
// tests can shorten it.
var runtimeFollowIdleHintAfter = 20 * time.Second

// runtimeFollowIdleHintDone is set once the hint printed or any runtime line
// arrived. A follow stream that reconnects after its idle timeout runs
// runLogs again; the hint must not appear then for an app that already wrote
// lines, or a second time.
var runtimeFollowIdleHintDone atomic.Bool

// startRuntimeFollowIdleHint prints, once, why an unfiltered runtime follow is
// quiet: an app that writes nothing to stdout or stderr (the hello-go
// starter, for one) shows no runtime lines however many requests it serves.
// markLine records that a line arrived; stop cancels the hint and waits for
// one already being written.
func startRuntimeFollowIdleHint(slug string) (markLine func(), stop func()) {
	fired := make(chan struct{})
	timer := time.AfterFunc(runtimeFollowIdleHintAfter, func() {
		defer close(fired)
		if !runtimeFollowIdleHintDone.CompareAndSwap(false, true) {
			return
		}
		_, _ = fmt.Fprintf(osStderr, "No runtime output yet: runtime logs show only what %s writes to stdout and stderr. To watch its HTTP requests: %s logs %s --follow --source http\n",
			slug, recoveryCLI(), recoveryArgument(slug))
	})
	return func() { runtimeFollowIdleHintDone.Store(true) }, func() {
		if !timer.Stop() {
			<-fired
		}
	}
}
