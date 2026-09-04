// commands_wake_timeline.go — Tier D audit-gap close.
// `gregale wake-timeline <slug> <wake-id> [--since RFC3339] [--limit N] [--all] [--verbose]`.
// Mirrors cmdInvocationsGet (commands_invocations.go) for the read shape
// and cmdDeploymentsAll (commands_deployments.go:118) for the walker.
//
// Two positional args are required: <slug> + <wake-id>. --since / --limit
// thread through to the SDK; --all walks every page via the
// next_cursor / since loop (the SDK does not expose a ListWakeTimelineAll
// helper — the loop lives in the CLI).
//
// Output modes:
//   - --json (single page): writeJSON(resp) — preserves envelope so
//     `next_cursor` survives for follow-up `?since=` calls.
//   - --json (--all walker): NDJSON of WakeTimelineEvent records.
//   - human (single page): labelled header + compact timing summary + one row per event.
//   - human (--all): same per-event rows streamed across pages; if a
//     follow-up page exists, prints `... more — pass --since <cursor>`.
//   - human (--verbose): additionally prints vmmd's implementation-level
//     restore phases below each `wake.restore_breakdown` row.
//
// The response-side closed enum (`WakeTimelineEvent.Kind`, ≥20 wake.* values
// from pkg/events/wake.go) is intentionally NOT gated client-side on this
// read path — operators want to see whatever the server surfaces, including
// legacy non-wake.* kinds.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// wakeTimelineDefaultLimit mirrors the SDK's documented default. The
// handler rejects limit > 1000 explicitly (handlers_wake_timeline.go:114);
// we cap the CLI default at 50 to keep a single page readable in a
// terminal without scrolling, matching the deployments list path.
const wakeTimelineDefaultLimit = 50

// wakeTimelineMaxLimit mirrors the SDK's documented page-size cap. The
// handler rejects limit > 1000 (handlers_wake_timeline.go:114); we
// mirror 1000 as the CLI ceiling so an operator cannot blow past the
// server-side gate via a higher --limit (zero-latency failure).
const wakeTimelineMaxLimit = 1000

// cmdWakeTimeline implements `gregale wake-timeline <slug> <wake-id>
// [--since RFC3339] [--limit N] [--all] [--verbose]`. Issue #517 / PR-C /
// ADR-064.
// Mirrors cmdInvocationsGet (single positional read with optional
// flags) + cmdDeploymentsAll (cursor walker).
func cmdWakeTimeline(args []string) int {
	// splitArgsForFlags: Go's flag.Parse halts at the first non-flag
	// positional, so `gregale wake-timeline <slug> <wake-id> --limit 100`
	// would silently drop --limit/--since/--all and use the default
	// page size + single-page mode. The reorder helper pulls flags to
	// the front so the parser sees them. Mirrors cmdDelayedTaskAdd
	// (commands_delayed_task.go:118) + cmdAppSecurity
	// (commands_app_security.go:42).
	flags, pos := splitArgsForFlags(args)
	fs := flag.NewFlagSet("wake-timeline", flag.ContinueOnError)
	since := fs.String("since", "", "RFC3339 timestamp; rows with `at >= since` returned (cursor when paging)")
	limit := fs.Int("limit", wakeTimelineDefaultLimit, "page size (1..1000)")
	all := fs.Bool("all", false, "walk every page via next_cursor / since (ignores --limit for the call count)")
	verbose := fs.Bool("verbose", false, "show detailed vmmd restore phases in human output (JSON is always complete)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 2 {
		PrintUsage(os.Stderr, "usage: gregale wake-timeline <slug> <wake-id> [--since RFC3339] [--limit N] [--all] [--verbose]", "wake-timeline")
		return 1
	}
	if *limit < 1 || *limit > wakeTimelineMaxLimit {
		return printErr("Invalid --limit", fmt.Errorf("--limit must be in [1, %d]; got %d", wakeTimelineMaxLimit, *limit))
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			return printErr("Invalid --since", fmt.Errorf("--since must be RFC 3339; got %q (%w)", *since, err))
		}
	}
	slug := pos[0]
	wakeID := pos[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *all {
		return cmdWakeTimelineAll(ctx, client, slug, wakeID, *limit, *verbose)
	}
	page, err := client.ListWakeTimeline(ctx, slug, wakeID, *since, *limit)
	if err != nil {
		return printErr("Could not fetch wake timeline", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	renderWakeTimelinePageWithOptions(osStdout, page, *verbose)
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "... more — pass --since %s\n", page.NextCursor)
	}
	return 0
}

// cmdWakeTimelineAll walks every page of the wake timeline by feeding
// the response's next_cursor back as the next request's `since`. Stops
// when next_cursor is empty or the server returns no events on the
// next page (defensive). Mirrors cmdDeploymentsAll for the loop
// shape but emits per-event rows so the operator sees a single
// chronological narrative across pages.
func cmdWakeTimelineAll(ctx context.Context, client *api.Client, slug, wakeID string, limit int, verbose bool) int {
	cursor := ""
	for {
		page, err := client.ListWakeTimeline(ctx, slug, wakeID, cursor, limit)
		if err != nil {
			return printErr("Could not fetch wake timeline", err)
		}
		if jsonOutput {
			for _, ev := range page.Events {
				if code := jsonOut(writeJSON(ev)); code != 0 {
					return code
				}
			}
		} else {
			renderWakeTimelinePageWithOptions(osStdout, page, verbose)
		}
		if page.NextCursor == "" || len(page.Events) == 0 {
			return 0
		}
		cursor = page.NextCursor
	}
}

// renderWakeTimelinePage writes one page of events to w with the compact
// customer-facing timing summary. Header row identifies the wake + page
// boundary; one row per event follows in the order the server returned them
// (at ASC — forward narrative). Use renderWakeTimelinePageWithOptions with
// verbose=true to include implementation-level restore phases.
//
// ADR-123: each wake.boot_started / wake.boot_completed row gains a
// trailing `trigger=… q=N c=N` context line (only when the fields are
// present) so operators can answer "why did this instance start?"
// without leaving the CLI. Pre-ADR-123 events render as before.
func renderWakeTimelinePage(w io.Writer, p api.WakeTimelineResponse) {
	renderWakeTimelinePageWithOptions(w, p, false)
}

func renderWakeTimelinePageWithOptions(w io.Writer, p api.WakeTimelineResponse, verbose bool) {
	_, _ = fmt.Fprintf(w, "wake %s app %s limit %d:\n", p.WakeID, p.AppID, p.Limit)
	renderSummaryHeader(w, p.Events)
	renderWakeTimingSummary(w, p.Events)
	for _, ev := range p.Events {
		_, _ = fmt.Fprintf(w, "  %s  %-9s  %s\n", ev.At, ev.Actor, ev.Kind)
		if ctx := renderContextSuffix(ev); ctx != "" {
			_, _ = fmt.Fprintf(w, "        %s\n", ctx)
		}
		if verbose {
			if breakdown := renderRestoreBreakdown(ev); breakdown != "" {
				_, _ = fmt.Fprintf(w, "        %s\n", breakdown)
			}
		}
	}
}

// renderWakeTimingSummary renders the stable, customer-useful subset of wake
// timings. Low-level vmmd phases stay behind --verbose; this line gives users
// the answer to "why was this cold wake slow?" without making jailer/chroot
// implementation details part of the default CLI contract.
func renderWakeTimingSummary(w io.Writer, events []api.WakeTimelineEvent) {
	var (
		queueAcceptedAt  time.Time
		admittedAt       time.Time
		restoreMs        int64
		readinessMs      int64
		proxyFirstByteMs int64
		wakeToRunningMs  int64
		haveRestore      bool
		haveReadiness    bool
		haveProxy        bool
		haveWakeRunning  bool
	)
	for _, ev := range events {
		switch ev.Kind {
		case "wake.queue_accepted":
			queueAcceptedAt = timelineTime(ev.At)
		case "wake.admitted":
			admittedAt = timelineTimeValue(ev.Data["admitted_at"])
			if admittedAt.IsZero() {
				admittedAt = timelineTime(ev.At)
			}
		case "wake.restore_breakdown":
			if ms, ok := timelineMillis(ev.Data["total_ms"]); ok {
				restoreMs = ms
				haveRestore = true
			}
		case "wake.readiness_200":
			if ms, ok := timelineMillis(ev.Data["elapsed_ms"]); ok {
				readinessMs = ms
				haveReadiness = true
			}
		case "wake.proxy_first_byte":
			if ms, ok := timelineMillis(ev.Data["latency_ms"]); ok {
				proxyFirstByteMs = ms
				haveProxy = true
			}
		case "wake.boot_completed":
			startedAt := timelineTimeValue(ev.Data["started_at"])
			completedAt := timelineTimeValue(ev.Data["completed_at"])
			if !startedAt.IsZero() && !completedAt.IsZero() && !completedAt.Before(startedAt) {
				wakeToRunningMs = completedAt.Sub(startedAt).Milliseconds()
				haveWakeRunning = true
			}
		}
	}
	if !haveRestore {
		return
	}
	parts := []string{}
	if !queueAcceptedAt.IsZero() && !admittedAt.IsZero() && !admittedAt.Before(queueAcceptedAt) {
		parts = append(parts, fmt.Sprintf("queue_to_admit=%dms", admittedAt.Sub(queueAcceptedAt).Milliseconds()))
	}
	parts = append(parts, fmt.Sprintf("restore=%dms", restoreMs))
	if haveReadiness {
		parts = append(parts, fmt.Sprintf("readiness=%dms", readinessMs))
	}
	if haveProxy {
		parts = append(parts, fmt.Sprintf("first_byte=%dms", proxyFirstByteMs))
	}
	if haveWakeRunning {
		parts = append(parts, fmt.Sprintf("wake_to_running=%dms", wakeToRunningMs))
	}
	_, _ = fmt.Fprintf(w, "  cold start: %s\n", strings.Join(parts, " "))
}

// renderSummaryHeader emits a single-line trigger histogram for the
// wake.boot_started events in this slice. ADR-123: operators want a
// quick "what woke this app?" answer before reading the per-event
// rows. Stable trigger order via sort.Strings so output is
// deterministic across runs (matters for golden-file tests).
func renderSummaryHeader(w io.Writer, events []api.WakeTimelineEvent) {
	counts := make(map[string]int)
	for _, ev := range events {
		if ev.Kind != "wake.boot_started" {
			continue
		}
		t, _ := ev.Data["trigger"].(string)
		if t == "" {
			continue
		}
		counts[t]++
	}
	if len(counts) == 0 {
		return
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	_, _ = fmt.Fprintf(w, "  triggers: %s\n", strings.Join(parts, " "))
}

// renderContextSuffix returns the trailing context line for one
// event — the wake-boot trigger, queue depth, and concurrency at
// admit (the three ADR-123 fields). Empty string when none are
// present (legacy or non-boot events). JSON numbers from the SDK
// surface as float64; coerce to int before printing.
func renderContextSuffix(ev api.WakeTimelineEvent) string {
	trigger, _ := ev.Data["trigger"].(string)
	var queued, conc int
	if q, ok := ev.Data["queued_count"].(float64); ok {
		queued = int(q)
	}
	if c, ok := ev.Data["concurrency_at_admit"].(float64); ok {
		conc = int(c)
	}
	if trigger == "" && queued == 0 && conc == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	if trigger != "" {
		parts = append(parts, "trigger="+trigger)
	}
	if queued != 0 {
		parts = append(parts, fmt.Sprintf("q=%d", queued))
	}
	if conc != 0 {
		parts = append(parts, fmt.Sprintf("c=%d", conc))
	}
	return strings.Join(parts, " ")
}

// renderRestoreBreakdown renders the vmmd-side phase timings carried by
// wake.restore_breakdown. JSON mode exposes the same keys without loss; the
// human verbose form keeps the exact total first, followed by the smaller
// phases that explain it. Values arrive as float64 from encoding/json, but
// accepting the integer forms also keeps this renderer straightforward to
// unit-test.
func renderRestoreBreakdown(ev api.WakeTimelineEvent) string {
	if ev.Kind != "wake.restore_breakdown" {
		return ""
	}
	fields := []struct {
		label string
		key   string
	}{
		{label: "total", key: "total_ms"},
		{label: "chroot", key: "chroot_ms"},
		{label: "materialize_mem", key: "materialize_mem_ms"},
		{label: "materialize_vmstate", key: "materialize_vmstate_ms"},
		{label: "resolve_images", key: "resolve_images_ms"},
		{label: "stage_drives", key: "stage_drives_ms"},
		{label: "stage_snapshot", key: "stage_snapshot_ms"},
		{label: "helper", key: "helper_ms"},
		{label: "start_jailer", key: "start_jailer_ms"},
		{label: "bind_tun", key: "bind_tun_ms"},
		{label: "load_snapshot", key: "load_snapshot_ms"},
		{label: "resume_hook", key: "resume_hook_ms"},
		{label: "wait_ready", key: "wait_ready_ms"},
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		ms, ok := timelineMillis(ev.Data[field.key])
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%dms", field.label, ms))
	}
	if len(parts) == 0 {
		return ""
	}
	return "restore " + strings.Join(parts, " ")
}

func timelineMillis(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case int32:
		return int64(n), true
	default:
		return 0, false
	}
}

func timelineTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func timelineTimeValue(v any) time.Time {
	s, ok := v.(string)
	if !ok {
		return time.Time{}
	}
	return timelineTime(s)
}
