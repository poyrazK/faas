package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdTrace locates one W3C trace id through the durable account-scoped trace
// index. The API joins retained request evidence with all durable invocation
// lifecycle rows so the CLI can show cross-service propagation without app
// fan-out.
func cmdTrace(args []string) int {
	if len(args) != 1 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(osStderr, "usage: gregale trace <trace-id>", "trace")
		return 1
	}
	traceID := strings.TrimSpace(args[0])
	if !validTraceID(traceID) {
		return printErr("Invalid trace id", fmt.Errorf("trace id must be 32 lowercase hexadecimal characters"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	result, err := client.GetAccountTrace(ctx, traceID)
	if err != nil {
		return printErr("Could not look up trace", err)
	}

	if jsonOutput {
		code := jsonOut(writeJSON(result))
		if code != 0 {
			return code
		}
		if len(result.Matches) == 0 && len(result.Invocations) == 0 {
			return 1
		}
		return 0
	}

	if len(result.Matches) == 0 && len(result.Invocations) == 0 {
		if result.Partial {
			_, _ = fmt.Fprintf(osStdout, "Trace %s was not found; some trace data could not be queried.\n", traceID)
		} else {
			_, _ = fmt.Fprintf(osStdout, "No retained request evidence for trace %s.\n", traceID)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "TRACE %s · %d app match(es) · %d invocation(s) · %d span(s)\n", traceID, len(result.Matches), len(result.Invocations), len(result.Spans))
		_, _ = fmt.Fprintln(osStdout, "MATCHES")
		for _, match := range result.Matches {
			request := match.Request
			_, _ = fmt.Fprintf(osStdout, "  %s · %s %s · HTTP %d · %d ms · telemetry row %s\n",
				match.App, request.Method, request.Route, request.Status, request.LatencyMS, request.ID)
		}
		if len(result.Invocations) > 0 {
			_, _ = fmt.Fprintln(osStdout, "INVOCATIONS")
			for _, inv := range result.Invocations {
				_, _ = fmt.Fprintf(osStdout, "  %s · %s %s · state=%s · attempts=%d · created_at=%s",
					inv.App, inv.Source, inv.ID, inv.State, inv.Attempts, inv.CreatedAt)
				if duration, ok := invocationDuration(inv); ok {
					_, _ = fmt.Fprintf(osStdout, " · duration=%s", formatTraceDuration(duration))
				}
				_, _ = fmt.Fprintln(osStdout)
			}
		}
		renderTraceWaterfall(osStdout, result)
		if len(result.Spans) == 0 {
			_, _ = fmt.Fprintln(osStdout, "span tree: no linked OTel spans")
		} else {
			_, _ = fmt.Fprintln(osStdout, "SPAN TREE")
			roots := buildDebugTraceRoots(result.Spans)
			for i, root := range roots {
				renderDebugTraceNode(osStdout, root, "", i == len(roots)-1, make(map[*debugTraceNode]bool))
			}
			if result.SpansTruncated {
				_, _ = fmt.Fprintln(osStdout, "span tree truncated to the slowest retained spans")
			}
		}
	}
	for _, item := range result.Errors {
		_, _ = fmt.Fprintf(osStderr, "trace %s: %s\n", item.App, item.Detail)
	}
	if result.Partial {
		return 3
	}
	if len(result.Matches) == 0 && len(result.Invocations) == 0 {
		return 1
	}
	return 0
}

func validTraceID(value string) bool {
	if len(value) != 32 || value == "00000000000000000000000000000000" {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && value == strings.ToLower(value)
}

type traceWaterfallEntry struct {
	start   time.Time
	end     time.Time
	label   string
	pending bool
}

// renderTraceWaterfall renders a bounded, relative timeline from the safe
// timestamps already present in the account trace response. It deliberately
// keeps labels metadata-only: no payloads, headers, or customer attributes
// are introduced by the CLI.
func renderTraceWaterfall(w io.Writer, result api.AccountTraceLookupResponse) {
	entries := make([]traceWaterfallEntry, 0, len(result.Spans)+len(result.Invocations))
	for _, span := range result.Spans {
		start, end, ok := spanWindow(span)
		if !ok {
			continue
		}
		entries = append(entries, traceWaterfallEntry{
			start: start,
			end:   end,
			label: fmt.Sprintf("%s [%s]", span.Name, span.Kind),
		})
	}
	for _, inv := range result.Invocations {
		created, err := time.Parse(time.RFC3339Nano, inv.CreatedAt)
		if err != nil {
			continue
		}
		completed, err := time.Parse(time.RFC3339Nano, inv.CompletedAt)
		pending := inv.CompletedAt == "" || err != nil
		if pending {
			completed = created
		}
		entries = append(entries, traceWaterfallEntry{
			start:   created,
			end:     completed,
			label:   fmt.Sprintf("%s · %s %s · state=%s", inv.App, inv.Source, inv.ID, inv.State),
			pending: pending,
		})
	}
	if len(entries) == 0 {
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].start.Equal(entries[j].start) {
			return entries[i].end.Before(entries[j].end)
		}
		return entries[i].start.Before(entries[j].start)
	})
	origin := entries[0].start
	_, _ = fmt.Fprintln(w, "WATERFALL (relative to earliest retained event)")
	for _, entry := range entries {
		startMS := entry.start.Sub(origin).Seconds() * 1000
		if entry.pending {
			_, _ = fmt.Fprintf(w, "  %8.2fms · pending   | %s\n", startMS, entry.label)
			continue
		}
		duration := entry.end.Sub(entry.start)
		if duration < 0 {
			duration = 0
		}
		_, _ = fmt.Fprintf(w, "  %8.2fms → %-8s | %s\n", startMS, formatTraceDuration(duration), entry.label)
	}
}

func invocationDuration(inv api.AccountTraceInvocation) (time.Duration, bool) {
	created, err := time.Parse(time.RFC3339Nano, inv.CreatedAt)
	if err != nil || inv.CompletedAt == "" {
		return 0, false
	}
	completed, err := time.Parse(time.RFC3339Nano, inv.CompletedAt)
	if err != nil || completed.Before(created) {
		return 0, false
	}
	return completed.Sub(created), true
}

func spanWindow(span api.DebugTelemetrySpan) (time.Time, time.Time, bool) {
	start, startErr := time.Parse(time.RFC3339Nano, span.StartTime)
	end, endErr := time.Parse(time.RFC3339Nano, span.EndTime)
	duration, durationOK := traceDurationFromNanos(span.DurationNanos)
	if startErr != nil && endErr != nil {
		return time.Time{}, time.Time{}, false
	}
	if startErr != nil {
		if !durationOK {
			return time.Time{}, time.Time{}, false
		}
		start = end.Add(-duration)
	}
	if endErr != nil {
		if !durationOK {
			return time.Time{}, time.Time{}, false
		}
		end = start.Add(duration)
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

func traceDurationFromNanos(nanos uint64) (time.Duration, bool) {
	const maxDurationNanos = uint64(1<<63 - 1)
	if nanos > maxDurationNanos {
		return 0, false
	}
	return time.Duration(nanos), true
}

func formatTraceDuration(duration time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(duration)/float64(time.Millisecond))
}
