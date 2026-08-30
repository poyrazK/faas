// commands_invocations.go — `gregale invocations <list|get>` (Tier C)
// plus the Tier B `--replay` extension on `get` (issue #315).
//
// The Tier C list/get surface closes the audit gap for
// /v1/invocations (issue #394 follow-up: the per-account
// invocation ledger was SDK-only). The dashboard renders the same
// list under the invocation-log panel; this leaf is the scriptable
// twin.
//
// `gregale invocations get <id> --replay` extends the Tier C get
// shape with the Tier B replay action (POST
// /v1/invocations/{id}/replay, server enforces the failed/dead_letter
// state allow-list). The Tier C read half (`gregale invocations get
// <id>`) replaces the standalone Tier B `gregale invocation <id>`
// command that previously occupied this file.
//
// Auth: self (route is auth + requireMFA + ScopesReadSurface).
// Replay: requires ScopesDeployWriteSurface (server-enforced).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// invocationCmdUsage is the top-of-failure-line shown for
// `gregale invocations get` errors. Mirrors PrintUsage's docs URL
// convention (output.go:144) so the line carries the stable docs
// site pointer.
const invocationGetCmdUsage = "usage: gregale invocations get [--json|--replay] <id>"

// invocationCmdDocsTopic is the docs topic slug appended to
// PrintUsage when it emits the trailing "Docs:" row. Kept
// on the plural "invocations" namespace since the singular form is
// gone (issue #315 collapsed into `invocations get`).
const invocationCmdDocsTopic = "invocations"

func cmdInvocations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale invocations <list|get [--replay]>", "invocations")
		return 1
	}
	if strings.HasPrefix(args[0], "-") {
		return cmdInvocationsList(args)
	}
	switch args[0] {
	case subList:
		return cmdInvocationsList(args[1:])
	case "get":
		return cmdInvocationsGet(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown invocations subcommand %q\n", args[0])
	return 1
}

// cmdInvocationsList implements `gregale invocations list
// [--before <cursor>] [--limit N]`. Newest first. Auth surface same
// as cmdAuditEventsList but the server emits invocation rows, not
// audit-event rows, so the renderer shape is different.
func cmdInvocationsList(args []string) int {
	fs := flag.NewFlagSet("invocations list", flag.ContinueOnError)
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	limit := fs.Int("limit", 50, "max rows (1..100; server caps at 100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale invocations list [--before C] [--limit N]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListInvocations(context.Background(), *before, *limit)
	if err != nil {
		return printErr("Could not list invocations", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Invocations) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no invocations)")
		return 0
	}
	for _, inv := range resp.Invocations {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", inv.ID, inv.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), inv.State, inv.Method, inv.Path)
	}
	return 0
}

// cmdInvocationsGet fetches one invocation by id. Same posture as
// cmdAuditEventsGet: the list is newest-first capped at 100, so a
// deeply old row is unreachable via scrolling.
//
// --replay (issue #315, tier-2 DX) re-issues a failed or dead_letter
// invocation. The new invocation's payload/method/path match the
// original verbatim; the operator renders the original row first
// (so they can confirm the target id), then the new
// AsyncInvokeResponse underneath. JSON mode emits a single
// {"original": ..., "replay": ...} envelope so scripts have a stable
// shape regardless of --replay presence.
func cmdInvocationsGet(args []string) int {
	fs := flag.NewFlagSet("invocations get", flag.ContinueOnError)
	replay := fs.Bool("replay", false, "re-issue a failed invocation (returns the new async invocation)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, invocationGetCmdUsage, invocationCmdDocsTopic)
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	inv, err := client.GetInvocation(ctx, id)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			renderAPIError(os.Stderr, ae)
			return exitCodeForStatus(ae.Problem.Status)
		}
		return printErr("Could not fetch invocation", err)
	}
	if *replay {
		resp, err := client.ReplayInvocation(ctx, id)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) {
				renderAPIError(os.Stderr, ae)
				return exitCodeForStatus(ae.Problem.Status)
			}
			return printErr("Could not replay invocation", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{
				"original": inv,
				"replay":   resp,
			}))
		}
		renderInvocation(osStdout, inv)
		_, _ = fmt.Fprintln(osStdout)
		renderReplayResponse(osStdout, resp)
		return 0
	}
	if jsonOutput {
		return jsonOut(writeJSON(inv))
	}
	renderInvocation(osStdout, inv)
	return 0
}

// renderInvocation writes the human-mode labelled block for an
// api.Invocation. Mirrors the dashboard panel's per-invocation
// detail view so a customer toggling between terminal and browser
// sees the same labels.
//
// Empty Optional fields (InstanceID, ScheduledAt, Result, etc.) are
// omitted so the block stays terse for in-progress rows. CompletedAt
// formats as "<rfc3339> (<ago>)" — the ago delta is what a customer
// usually wants ("how long ago did this finish?") and the absolute
// timestamp is the fallback for scripts / cross-references.
func renderInvocation(w io.Writer, inv api.Invocation) {
	_, _ = fmt.Fprintf(w, "Invocation: %s\n", inv.ID)
	_, _ = fmt.Fprintf(w, "App:        %s\n", inv.AppID)
	if inv.InstanceID != "" {
		_, _ = fmt.Fprintf(w, "Instance:   %s\n", inv.InstanceID)
	}
	_, _ = fmt.Fprintf(w, "Source:     %s\n", inv.Source)
	_, _ = fmt.Fprintf(w, "State:      %s\n", inv.State)
	_, _ = fmt.Fprintf(w, "Method:     %s\n", inv.Method)
	_, _ = fmt.Fprintf(w, "Path:       %s\n", inv.Path)
	_, _ = fmt.Fprintf(w, "Attempts:   %d\n", inv.Attempts)
	_, _ = fmt.Fprintf(w, "Created:    %s\n", inv.CreatedAt.UTC().Format(time.RFC3339))
	if inv.ScheduledAt != nil {
		_, _ = fmt.Fprintf(w, "Scheduled:  %s\n", inv.ScheduledAt.UTC().Format(time.RFC3339))
	}
	if inv.CompletedAt != nil {
		ago := time.Since(*inv.CompletedAt).Truncate(time.Millisecond)
		_, _ = fmt.Fprintf(w, "Completed:  %s (%s ago)\n",
			inv.CompletedAt.UTC().Format(time.RFC3339), ago)
	}
	if inv.LastError != "" {
		_, _ = fmt.Fprintf(w, "Last err:   %s\n", oneLine(inv.LastError))
	}
	if len(inv.Payload) > 0 {
		_, _ = fmt.Fprintf(w, "Payload:    %s\n", oneLine(string(inv.Payload)))
	}
	if len(inv.Result) > 0 {
		_, _ = fmt.Fprintf(w, "Result:     %s\n", oneLine(string(inv.Result)))
	}
}

// renderReplayResponse writes the two-line replay summary that
// follows the original row when --replay is set. Mirrors the
// AsyncInvokeResponse wire shape so the customer sees the same
// fields the SDK polls. Status URL is rendered as a relative path
// because the dashboard's invocation page lives on the same host;
// the SDK call returned the path verbatim from the server.
func renderReplayResponse(w io.Writer, r api.AsyncInvokeResponse) {
	_, _ = fmt.Fprintf(w, "Replay id:        %s\n", r.ID)
	_, _ = fmt.Fprintf(w, "Replay status:    %s\n", r.StatusURL)
	_, _ = fmt.Fprintln(w, "Poll with:        gregale invocations get", r.ID)
}

// oneLine collapses a multi-line string into a single line for the
// labelled-block view. Newlines become spaces so a stack-trace dump
// or a JSON string with embedded '\n' doesn't break the column
// alignment. Long payloads (>120 chars) are truncated with "…" so a
// stray base64 blob doesn't blow past the terminal width — the JSON
// path carries the full string verbatim.
//
// Truncation honours rune boundaries: a byte slice at max-1 can
// split a multibyte UTF-8 sequence and emit invalid UTF-8 to a
// terminal that handles all-writes-as-bytes. We always truncate on
// a rune boundary so the output is always valid UTF-8. (A 4-byte
// CJK glyph at the cut means we keep one fewer rune than the byte
// ceiling — acceptable; the cap is a terminal-width hint, not a
// wire contract.)
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	const max = 120
	// Rune count is what the terminal sees; byte length would
	// over-count multibyte sequences and trip the cap on short
	// UTF-8 strings.
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	var b strings.Builder
	b.Grow(max + 3) // worst-case "…" + slack
	count := 0
	for _, r := range s {
		if count == max-1 {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteString("…")
	return b.String()
}
