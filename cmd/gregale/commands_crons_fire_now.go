// gregale crons run / crons fire-now — async fire-now UX (issue #791
// PR-D / ADR-090 §"Sub-decision 7").
//
// Two surfaces, two subcommands (split because fire-now is async —
// the POST returns 202 with a request_id, and the customer polls
// until the row reaches a terminal state). The verb split mirrors the
// URL split: `POST /v1/crons/{id}/run` is the producer,
// `GET /v1/cron-fire-now-requests/{request_id}` is the consumer.
//
// UX shape (human mode):
//
//	$ gregale crons run 0123...cdef
//	Fire-now request enqueued: 4567...89ab  (poll via `crons fire-now`)
//
//	$ gregale crons fire-now 4567...89ab
//	→  2026-08-10T09:00:00Z  pending  (no terminal stamp)
//
//	$ gregale crons fire-now 4567...89ab   # after schedd stamps
//	✓  2026-08-10T09:00:05Z  succeeded  invocation: abc...0123
//
// Glyphs are gated by Enabled() (output.go:55-63) so piped output
// strips them — see the writeStatus helper. Always emit glyphs via
// the gate, never via fmt.Fprintf literals — that is the package-wide
// rule enforced by lint_tripwires_test.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fireNowRequestIDPattern is the 32-hex shape used by the API for
// fire-now request ids (migrations/00194, uuid.NewString() with hyphens
// stripped — same generator as cron ids). Validated locally BEFORE the
// network round-trip so a bad id returns 1 with zero server calls,
// matching cmdCronsRuns / cmdCronsRun / cmdCronsUpdate.
var fireNowRequestIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// fire-now row status constants. Mirrors state.FireNowStatus
// (pkg/state/types.go:1067-1076) byte-for-byte; pkg/state is not
// importable from this binary (cmd/gregale ↔ pkg/state would form a
// cycle — pkg/api is the only allowed surface). goconst trips on
// repeated string literals otherwise, so the values are declared
// once here and used everywhere in the file.
const (
	fireNowStatusPending   = "pending"
	fireNowStatusRunning   = "running"
	fireNowStatusSucceeded = "succeeded"
	fireNowStatusFailed    = "failed"
	fireNowStatusCancelled = "cancelled"
)

// cmdCronsRun implements `gregale crons run <id>`. POSTs to
// /v1/crons/{id}/run and prints the enqueued request_id. The id is
// validated locally against cronIDPattern (commands2.go) BEFORE the
// network round-trip — a bad id returns 1 with zero server calls,
// same posture as cmdCronsUpdate and cmdCronsRuns.
//
// Async-by-design: the POST returns 202 + request_id, and the row
// becomes terminal only after schedd dispatches the cron. We never
// poll from the explicit-ID path; interactive mode follows the request
// after reviewing and confirming the selected task.
func cmdCronsRun(args []string) int {
	fs := newFlagSet("crons-run", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a task, confirm one manual run, and follow its request")
	app := fs.String("app", "", "app slug for interactive task selection")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum interactive fire-now wait")
	flags, pos := splitArgsForFlags(args, "interactive")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *interactive {
		if len(pos) != 0 || *timeout <= 0 {
			return printErr("Invalid interactive run flags", fmt.Errorf("use crons run --interactive with optional --app and a positive --timeout; choose the task in the flow"))
		}
		if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
			return printErr("Interactive terminal required", fmt.Errorf("use crons run ID for scripts"))
		}
		slug, err := resolveReadAppTarget(*app)
		if err != nil {
			return readAppTargetError(err)
		}
		return cmdCronsRunInteractive(slug, *timeout)
	}
	if logsFlagWasSet(fs, "app") || logsFlagWasSet(fs, "timeout") {
		return printErr("Invalid run flags", fmt.Errorf("--app and --timeout require --interactive"))
	}
	if len(pos) != 1 {
		printCommandValidation(os.Stderr, "usage: gregale crons run <id>\n")
		return 1
	}
	id := pos[0]
	if !cronIDPattern.MatchString(id) {
		printCommandValidation(os.Stderr, "usage: gregale crons run <id>\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.FireCron(context.Background(), id)
	if err != nil {
		return printErr("Could not enqueue fire-now", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Fire-now request enqueued: %s (poll via `gregale crons fire-now %s`)\n",
		resp.RequestID, resp.RequestID)
	return 0
}

// cmdCronsFireNowGet implements `gregale crons fire-now <request-id>`.
// GETs /v1/cron-fire-now-requests/{request_id} and prints the row's
// terminal-or-pending state. The id is validated as a UUID-shape
// locally — a malformed id returns 1 with zero server calls. The
// handler returns 404 for missing / cross-account / bad-uuid in
// byte-identical bodies (IDOR-safe), so the CLI never invents a
// local branch that could leak existence.
func cmdCronsFireNowGet(args []string) int {
	fs := newFlagSet("crons-fire-now", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "follow this existing request until terminal status")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait duration")
	flags, pos := splitArgsForFlags(args, "wait")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		printCommandValidation(os.Stderr, "usage: gregale crons fire-now <request-id>\n")
		return 1
	}
	requestID := pos[0]
	if !fireNowRequestIDPattern.MatchString(requestID) {
		printCommandValidation(os.Stderr, "usage: gregale crons fire-now <request-id>\n")
		return 1
	}
	if *timeout <= 0 || logsFlagWasSet(fs, "timeout") && !*wait {
		return printErr("Invalid wait flags", fmt.Errorf("--timeout must be positive and requires --wait"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *wait {
		return followCronFireNowRequest(context.Background(), client, "", requestID, *timeout)
	}
	resp, err := client.GetFireCronRequest(context.Background(), requestID)
	if err != nil {
		// production-us hunt #4: `crons fire-now <cron-id>` reads as "fire
		// this cron now" and answered only "no such fire-now request". When
		// the id is a cron, say which command fires it.
		if isNotFound(err) {
			if cron, cronErr := client.GetCron(context.Background(), requestID); cronErr == nil && cron.ID != "" {
				return printErr("Not a fire-now request", fmt.Errorf("%s is a cron rule; fire it with `gregale crons run %s`, which prints the request id that `crons fire-now` follows", requestID, requestID))
			}
		}
		return printErr("Could not load fire-now request", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderFireNowStatus(osStdout, resp)
	return 0
}

// renderFireNowStatus writes one human-mode line for a fire-now row.
//
// Column order is intentional:
//
//	glyph  requested_at (RFC3339)  status  [invocation|task] [error]
//
// The glyph is from the same ✓/✗/→ set as crons runs. Pending/running
// rows show → (in progress); succeeded shows ✓; failed/cancelled show
// ✗. Invocation or task receipt (when present) is rendered, and Error
// renders one-line so a failure mode is visible at a glance.
func renderFireNowStatus(w io.Writer, r api.FireCronRequestResponse) {
	glyph := fireNowGlyph(r.Status)
	ts := r.RequestedAt
	extra := ""
	if r.InvocationID != nil && *r.InvocationID != "" {
		extra = "  invocation: " + *r.InvocationID
	}
	if r.TaskID != nil && *r.TaskID != "" {
		extra = "  task: " + *r.TaskID
	}
	if r.Error != nil && *r.Error != "" {
		extra += "  " + oneLine(*r.Error)
	}
	prefix := ""
	if Enabled() {
		prefix = glyph + " "
	}
	_, _ = fmt.Fprintf(w, "%s%s\t%s%s\n", prefix, ts, r.Status, extra)
}

// fireNowGlyph maps the row's status to the UX §3.2 glyph set:
// ✓ done, ✗ failed, → in progress. Terminal-succeeded → ✓; anything
// else terminal (failed/cancelled) → ✗; pending/running → →. The
// default branch is intentional: an unknown status falls through to
// → so an unrecognized non-terminal token doesn't masquerade as a
// success.
//
// The literal glyph strings live in output.go (GlyphOK / GlyphFail /
// GlyphProgress) so the lint tripwire
// (TestLintTripwire_NoGlyphLiteralOutsideOutput) keeps a single
// allow-listed file for every leading ✓/✗/→ in the package.
func fireNowGlyph(status string) string {
	switch status {
	case fireNowStatusSucceeded:
		return GlyphOK
	case fireNowStatusFailed,
		fireNowStatusCancelled:
		return GlyphFail
	default:
		// pending, running, or any future token.
		return GlyphProgress
	}
}
