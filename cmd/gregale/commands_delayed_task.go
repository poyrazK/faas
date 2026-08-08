// commands_delayed_task.go — Tier D audit-gap close.
// `gregale delayed-task <add|get|cancel>` for issue #557 / ADR-072
// (deferred invocations — "fire this payload at 2026-09-01T00:00:00Z").
//
// Mirrors `commands_crons.go` for the dispatcher shape (the crons
// surface is the closest sibling: scheduled-task CRUD with create +
// read + delete). Reuses `resolvePayload` from commands_invocation.go
// for the `--payload` triple-shape resolver (inline JSON / @file / -).
//
// Verb surface:
//   - add     POST /v1/apps/{slug}/delayed-tasks (requires --scheduled-at
//             + --app + optional --payload).
//   - get     GET /v1/delayed-tasks/{id} (account-scoped read, mirrors
//             GetDelayedTask's "GetDelayedTask" call).
//   - cancel  DELETE /v1/delayed-tasks/{id} (idempotent — a second
//             cancel is a no-op 200 on the server).
//
// --scheduled-at is RFC 3339 (UTC recommended). Past timestamps fail
// closed at the handler with `invalid_scheduled_at`; we mirror the
// gate locally so a CLI typo is zero-latency.
//
// --idempotency-key is intentionally NOT exposed here — the SDK's
// CreateDelayedTask does not thread an IdempotencyKey today, and the
// server auto-mints a stable key from (account, scheduled_at, payload
// hash) so an at-least-once retry is safe. Re-evaluate when a real
// operator request lands.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// delayedTaskIDPattern mirrors the 32-hex UUID shape every other
// audit-event-style leaf uses (commands_webhooks.go:390,
// commands_alerts.go:402). The delayed-task id is a 32-hex UUID per
// the handler (handlers_delayed_task.go); local validation lets the
// CLI return a clean 1-exit error instead of a 404 round-trip.
var delayedTaskIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// cmdDelayedTask dispatches `gregale delayed-task <add|get|cancel>`
// to the three leaves. Mirrors cmdCrons (commands_crons.go:40) for
// the dispatcher shape.
func cmdDelayedTask(args []string) int {
	parent, _ := lookupCliCommand("delayed-task")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task <add|get|cancel>", "delayed-task")
		return 1
	}
	switch args[0] {
	case subAdd:
		return cmdDelayedTaskAdd(args[1:])
	case subInfo:
		// `get` surfaces as `info` for grep-friendly parallelism with
		// cmdAlerts (commands_alerts.go:53); the API verb is GET so
		// we accept both spellings at the CLI boundary.
		return cmdDelayedTaskGet(args[1:])
	case subGet:
		return cmdDelayedTaskGet(args[1:])
	case "cancel":
		return cmdDelayedTaskCancel(args[1:])
	}
	fmt.Fprintf(os.Stderr, "gregale delayed-task: unknown subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdDelayedTaskAdd implements `gregale delayed-task add --app <slug>
// --scheduled-at <RFC3339> [--payload <J|@file|->]`
// (POST /v1/apps/{slug}/delayed-tasks).
//
// The --app + --scheduled-at combo uses splitArgsForFlags because
// the operator may legitimately pass `--payload -` after a positional
// (Go's flag.Parse would silently drop it otherwise — see
// splitArgsForFlags, commands5.go:1010). Empty payload is valid
// (handler accepts zero-body deferred tasks).
func cmdDelayedTaskAdd(args []string) int {
	// splitArgsForFlags: Go's flag.Parse halts at the first non-flag
	// positional, so `gregale delayed-task add demo --scheduled-at
	// 2030-01-01T00:00:00Z --payload J` would silently drop --scheduled-at
	// and --payload. The reorder helper pulls flags to the front so the
	// parser sees them. Mirrors cmdAppSecurity (commands_app_security.go:42)
	// + cmdWakeTimeline (commands_wake_timeline.go:54).
	flags, _ := splitArgsForFlags(args)
	fs := flag.NewFlagSet("delayed-task add", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	scheduledAt := fs.String("scheduled-at", "", "RFC3339 dispatch time (required; must be in the future)")
	payload := fs.String("payload", "", "JSON payload (inline | @file | - for stdin; empty is valid)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if !validateDelayedTaskAddFlags(app, scheduledAt) {
		return 1
	}
	body, err := resolvePayload(*payload)
	if err != nil {
		return printErr("Invalid --payload", err)
	}
	when, err := time.Parse(time.RFC3339, *scheduledAt)
	if err != nil {
		return printErr("Invalid --scheduled-at", fmt.Errorf("--scheduled-at must be RFC 3339; got %q (%w)", *scheduledAt, err))
	}
	if !when.After(time.Now()) {
		return printErr("Invalid --scheduled-at", fmt.Errorf("--scheduled-at must be in the future; got %s", when.Format(time.RFC3339)))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CreateDelayedTask(context.Background(), *app, api.DelayedTaskRequest{
		Payload:     body,
		ScheduledAt: when,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Delayed task %s scheduled for %s.", resp.ID, resp.ScheduledAt.Format(time.RFC3339))
	return 0
}

// cmdDelayedTaskGet implements `gregale delayed-task get <id>` (account-scoped
// GET /v1/delayed-tasks/{id}). Single positional id, --json returns
// the response, human mode prints id + scheduled_at + state.
func cmdDelayedTaskGet(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task get <id>", "delayed-task")
		return 1
	}
	id := args[0]
	if !delayedTaskIDPattern.MatchString(id) {
		return printErr("Invalid delayed-task id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetDelayedTask(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch delayed-task", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "id:", resp.ID)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "scheduled_at:", resp.ScheduledAt.Format(time.RFC3339))
	if resp.State != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "state:", resp.State)
	}
	return 0
}

// cmdDelayedTaskCancel implements `gregale delayed-task cancel <id>`
// (DELETE /v1/delayed-tasks/{id}). Idempotent — a second cancel is
// a no-op 200 on the server; we mirror that posture by returning
// 0 in both the success and the already-cancelled cases.
func cmdDelayedTaskCancel(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task cancel <id>", "delayed-task")
		return 1
	}
	id := args[0]
	if !delayedTaskIDPattern.MatchString(id) {
		return printErr("Invalid delayed-task id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.CancelDelayedTask(context.Background(), id); err != nil {
		return printErr("Cancel failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": id, "cancelled": true}))
	}
	PrintOK(osStdout, "Delayed task %s cancelled.", id)
	return 0
}

// validateDelayedTaskAddFlags enforces the per-field presence gate
// shared by cmdDelayedTaskAdd's body. Returns true on success;
// otherwise fires printErr with the matching error and returns false.
// Extracted to keep cmdDelayedTaskAdd under the 50-line handler cap.
func validateDelayedTaskAddFlags(app, scheduledAt *string) bool {
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale delayed-task add --app <slug> --scheduled-at <RFC3339> [--payload <J|@file|->]", "delayed-task")
		return false
	}
	if *scheduledAt == "" {
		printErr("Missing --scheduled-at", fmt.Errorf("--scheduled-at is required (RFC 3339)"))
		return false
	}
	return true
}
