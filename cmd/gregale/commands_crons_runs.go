// gregale crons runs — execution history for one cron (issue #791 / PR B).
//
// The endpoint (GET /v1/crons/{id}/runs, shipped in PR #795) returns a
// newest-first page of cron fires with a server-computed duration_ms
// and a normalized outcome (success / failed / timeout / dead_letter /
// running). This file is the CLI surface for that endpoint.
//
// UX shape (one line per run):
//
//	<run-id>  2026-08-10T09:00:00Z  success   1.2s
//	<run-id>  2026-08-09T21:00:00Z  timeout   30.0s   invoke: gateway timeout
//	<run-id>  2026-08-10T12:00:00Z  running   —
//
// Glyphs are gated by Enabled() (output.go:55-63) so piped output
// strips them — see the writeStatus helper. Always emit glyphs via the
// gate, never via fmt.Fprintf literals — that is the package-wide rule
// enforced by lint_tripwires_test.go.
//
// Subcommand vocab mirrors webhooks deliveries (noun-style verb that
// matches the URL path segment, not a hyphenated `runs-list`). The
// dispatch case lives in commands2.go:cmdCrons.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdCronsRuns implements `gregale crons runs <id> [--before <cursor>]
// [--limit N] [--run <task-id>]`. Mirrors cmdInvocationsList (commands_invocations.go:70):
// same flag shape, same `(no rows)` empty-result sentinel, same JSON
// envelope via writeJSON.
//
// The id is validated locally against cronIDPattern (commands2.go:1314)
// BEFORE the network round-trip — a bad id returns 1 with zero server
// calls, same posture as cmdCronsUpdate. We never invent a local "cron
// not found" branch on the 404 path: leak the SDK error verbatim so
// customers see the same diagnostic they'd get from cURL.
func cmdCronsRuns(args []string) int {
	fs := newFlagSet("crons-runs", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor (last id of the prior page)")
	limit := fs.Int("limit", 10, "max rows (1..100; server caps at 100)")
	runID := fs.String("run", "", "show full details and captured output for one command run")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale crons runs <id> [--before C] [--limit N] [--run TASK-ID]")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 100); err != nil {
		fmt.Fprintln(os.Stderr, "usage: gregale crons runs <id> [--before C] [--limit N] (1 <= N <= 100)")
		return 1
	}
	id := pos[0]
	if !cronIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale crons runs <id> [--before C] [--limit N] [--run TASK-ID]")
		return 1
	}
	if *runID != "" {
		if !fireNowRequestIDPattern.MatchString(*runID) || *before != "" || *limit != 10 {
			fmt.Fprintln(os.Stderr, "usage: gregale crons runs <id> --run <task-id>")
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *runID != "" {
		task, err := client.GetCronCommandRun(context.Background(), id, *runID)
		if err != nil {
			return printErr("Could not load cron run details", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(task))
		}
		renderCronCommandRunDetails(osStdout, osStderr, task)
		return 0
	}
	resp, err := client.ListCronRuns(context.Background(), id, *before, *limit)
	if err != nil {
		return printErr("Could not list cron runs", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Runs) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no runs)")
		return 0
	}
	for _, r := range resp.Runs {
		renderCronRun(osStdout, r)
	}
	return 0
}

func renderCronCommandRunDetails(stdout, stderr io.Writer, task api.AppTaskResponse) {
	_, _ = fmt.Fprintf(stdout, "task: %s\nstatus: %s\ndeployment: %s\nattempts: %d/%d\n",
		task.ID, task.Status, task.DeploymentID, task.AttemptCount, task.RetryMax+1)
	if task.ExitCode != nil {
		_, _ = fmt.Fprintf(stdout, "exit_code: %d\n", *task.ExitCode)
	}
	if task.RetryAt != nil {
		_, _ = fmt.Fprintf(stdout, "retry_at: %s\n", *task.RetryAt)
	}
	if task.StartedAt != nil {
		_, _ = fmt.Fprintf(stdout, "started_at: %s\n", *task.StartedAt)
	}
	if task.FinishedAt != nil {
		_, _ = fmt.Fprintf(stdout, "finished_at: %s\n", *task.FinishedAt)
	}
	if task.Failure != nil {
		_, _ = fmt.Fprintf(stderr, "failure: %s: %s\n", task.Failure.Code, oneLine(task.Failure.Message))
	}
	if task.StdoutTail != "" {
		_, _ = fmt.Fprintln(stdout, "stdout:")
		_, _ = fmt.Fprint(stdout, task.StdoutTail)
		if !strings.HasSuffix(task.StdoutTail, "\n") {
			_, _ = fmt.Fprintln(stdout)
		}
	}
	if task.StderrTail != "" {
		_, _ = fmt.Fprintln(stderr, "stderr:")
		_, _ = fmt.Fprint(stderr, task.StderrTail)
		if !strings.HasSuffix(task.StderrTail, "\n") {
			_, _ = fmt.Fprintln(stderr)
		}
	}
	if task.OutputTruncated {
		_, _ = fmt.Fprintf(stderr, "warning: captured output was truncated at %d bytes\n", task.MaxOutputBytes)
	}
}

// renderCronRun writes one human-mode line for a single cron run.
//
// Column order is intentional:
//
//	glyph  run_id  started_at (RFC3339)  outcome  duration  error
//
// Glyph + space are emitted via writeStatus so the TTY/NO_COLOR gate
// at output.go:55-97 strips them when stdout is a pipe. Do not print
// literal ✓/✗/→ here — the lint tripwire rejects it.
func renderCronRun(w io.Writer, r api.CronRun) {
	glyph := cronRunGlyph(r.Outcome)
	ts := r.StartedAt.Format("2006-01-02T15:04:05Z07:00")
	dur := formatCronDuration(r.DurationMs)
	errStr := ""
	if r.Error != "" {
		errStr = oneLine(r.Error)
	}
	prefix := ""
	if Enabled() {
		prefix = glyph + " "
	}
	_, _ = fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%s\n",
		prefix, r.ID, ts, string(r.Outcome), dur, errStr)
}

// cronRunGlyph maps the API outcome enum to the UX §3.2 glyph set:
// ✓ done, ✗ failed, → in progress. The default branch is intentional:
// if a new outcome is added to the enum server-side (e.g. `paused`),
// it falls through to ✗ rather than silently rendering no glyph. The
// Plan-review agent flagged this trade-off explicitly; silent
// misclassification was judged worse than a wrong glyph for an
// unknown token.
//
// The literal glyph strings live in output.go (GlyphOK / GlyphFail /
// GlyphProgress) so the lint tripwire
// (TestLintTripwire_NoGlyphLiteralOutsideOutput) keeps a single
// allow-listed file for every leading ✓/✗/→ in the package.
func cronRunGlyph(o api.CronRunOutcome) string {
	switch o {
	case api.CronRunSuccess:
		return GlyphOK
	case api.CronRunRunning:
		return GlyphProgress
	default:
		return GlyphFail
	}
}

// formatCronDuration renders a server-emitted duration_ms *int64 into
// the human-mode "duration" column. Banded policy (locked in PR B):
//
//	nil         → "—"
//	<1000       → "Nms"        (e.g. 0ms, 500ms)
//	<60_000     → "N.Ns"       (one decimal; 30.0s not 30s)
//	≥60_000     → "Ns" whole   (90s, 3600s — never minute-collapsing)
//
// Minutes are deliberately avoided: "30s" vs "30m" is the boundary
// ambiguity that kubectl describe and gh run view also steer clear of.
// Zero ms is rendered as "0ms" (not "0.0s") so the ms band visibly
// spans the full sub-second range.
func formatCronDuration(ms *int64) string {
	if ms == nil {
		return "—"
	}
	v := *ms
	switch {
	case v < 1000:
		return fmt.Sprintf("%dms", v)
	case v < 60_000:
		// %%.1f would force trailing ".0"; use %.1f which gives
		// "1.2" / "30.0" — both forms read naturally next to the
		// outcome token.
		return fmt.Sprintf("%.1fs", float64(v)/1000.0)
	default:
		return fmt.Sprintf("%ds", v/1000)
	}
}
