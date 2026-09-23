// commands_delayed_task.go — Tier D audit-gap close.
// `gregale delayed-task <add|list|get|cancel>` for issue #557 / ADR-072
// (deferred invocations — "fire this payload at 2026-09-01T00:00:00Z").
//
// Mirrors `commands_crons.go` for the dispatcher shape (the crons
// surface is the closest sibling: scheduled-task CRUD with create +
// read + delete). Reuses `resolvePayload` from commands_invocation.go
// for the `--payload` triple-shape resolver (inline JSON / @file / -).
//
// Verb surface:
//   - add     POST /v1/apps/{slug}/delayed-tasks (requires --app and exactly
//             one of --scheduled-at / --delay; target options are optional).
//   - get     GET /v1/delayed-tasks/{id} (account-scoped read, mirrors
//             GetDelayedTask's "GetDelayedTask" call).
//   - cancel  DELETE /v1/delayed-tasks/{id} (idempotent — a second
//             cancel is a no-op 200 on the server).
//
// --scheduled-at is RFC 3339 (UTC recommended); --delay is a whole-second Go
// duration. Invalid or past schedules fail locally so a typo is zero-latency.
//
// The create command exposes an optional caller-controlled idempotency key;
// reusing it after an uncertain response prevents duplicate scheduled work.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// delayedTaskIDPattern mirrors the 32-hex UUID shape every other
// audit-event-style leaf uses (commands_webhooks.go:390,
// commands_alerts.go:402). The delayed-task id is a 32-hex UUID per
// the handler (handlers_delayed_task.go); local validation lets the
// CLI return a clean 1-exit error instead of a 404 round-trip.
var delayedTaskIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// cmdDelayedTask dispatches `gregale delayed-task <add|list|get|cancel>`
// to the command leaves. Mirrors cmdCrons (commands_crons.go:40) for
// the dispatcher shape.
func cmdDelayedTask(args []string) int {
	parent, _ := lookupCliCommand("delayed-task")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task <add|list|get|cancel>", "delayed-task")
		return 1
	}
	switch args[0] {
	case subAdd:
		return cmdDelayedTaskAdd(args[1:])
	case subList:
		return cmdDelayedTaskList(args[1:])
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
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("delayed-task add", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	scheduledAt := fs.String("scheduled-at", "", "RFC3339 dispatch time (mutually exclusive with --delay)")
	delay := fs.String("delay", "", "relative delay such as 30m or 2h (mutually exclusive with --scheduled-at)")
	payload := fs.String("payload", "", "JSON payload (inline | @file | - for stdin; empty is valid)")
	method := fs.String("method", "POST", "HTTP method used to invoke the app")
	path := fs.String("path", "/", "app path invoked when the task becomes due")
	idempotencyKey := fs.String("idempotency-key", "", "stable key to reuse when retrying this create")
	var headers multiFlag
	fs.Var(&headers, "header", "request header as Name:Value (repeatable)")
	maxAttempts := fs.Int("max-attempts", 0, "maximum delivery attempts (0 uses the plan default)")
	retryBaseSeconds := fs.Float64("retry-base-seconds", 0, "base retry delay in seconds")
	retryMaxSeconds := fs.Float64("retry-max-seconds", 0, "maximum retry delay in seconds")
	retryJitterSeconds := fs.Float64("retry-jitter-seconds", 0, "retry jitter fraction (0..1)")
	retention := fs.Duration("retention", 0, "terminal result retention such as 1h or 168h")
	onSuccessWebhook := fs.String("on-success-webhook", "", "webhook subscription id for successful completion")
	onFailureWebhook := fs.String("on-failure-webhook", "", "webhook subscription id for terminal failure")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task add --app <slug> (--scheduled-at <RFC3339>|--delay <duration>) [--payload <json|@file|->]", "delayed-task")
		return 1
	}
	if !validateDelayedTaskAddFlags(app, scheduledAt, delay) {
		return 1
	}
	body, err := resolvePayload(*payload)
	if err != nil {
		return printErr("Invalid --payload", err)
	}
	req, err := delayedTaskRequestFromFlags(fs, body, *method, *path, headers, *maxAttempts, *retryBaseSeconds, *retryMaxSeconds, *retryJitterSeconds, *retention, *onSuccessWebhook, *onFailureWebhook)
	if err != nil {
		return printErr("Invalid delayed-task options", err)
	}
	if *scheduledAt != "" {
		when, err := time.Parse(time.RFC3339, *scheduledAt)
		if err != nil {
			return printErr("Invalid --scheduled-at", fmt.Errorf("--scheduled-at must be RFC 3339; got %q (%w)", *scheduledAt, err))
		}
		if !when.After(time.Now()) {
			return printErr("Invalid --scheduled-at", fmt.Errorf("--scheduled-at must be in the future; got %s", when.Format(time.RFC3339)))
		}
		req.ScheduledAt = when
	} else {
		d, err := time.ParseDuration(*delay)
		if err != nil || d < time.Second || d%time.Second != 0 {
			return printErr("Invalid --delay", fmt.Errorf("--delay must be a positive whole-second duration; got %q", *delay))
		}
		req.DelaySeconds = int64(d / time.Second)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CreateDelayedTaskWithIdempotencyKey(context.Background(), *app, req, *idempotencyKey)
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Delayed task %s scheduled for %s.", resp.ID, resp.ScheduledAt.Format(time.RFC3339))
	return 0
}

func delayedTaskRequestFromFlags(fs *flag.FlagSet, payload json.RawMessage, method, path string, headerValues []string, maxAttempts int, retryBaseSeconds, retryMaxSeconds, retryJitterSeconds float64, retention time.Duration, onSuccessWebhook, onFailureWebhook string) (api.DelayedTaskRequest, error) {
	req := api.DelayedTaskRequest{Payload: payload, Method: method, Path: path}
	headers, err := delayedTaskHeaderJSON(headerValues)
	if err != nil {
		return req, err
	}
	req.Headers = headers
	req.RetryPolicy, err = delayedTaskRetryPolicyFromFlags(fs, maxAttempts, retryBaseSeconds, retryMaxSeconds, retryJitterSeconds)
	if err != nil {
		return req, err
	}
	if flagWasSet(fs, "retention") {
		if retention < 0 || retention%time.Second != 0 {
			return req, fmt.Errorf("--retention must be a non-negative whole-second duration")
		}
		seconds := int(retention / time.Second)
		req.RetentionSeconds = &seconds
	}
	if onSuccessWebhook != "" || onFailureWebhook != "" {
		req.Destinations = &api.InvocationDestinations{OnSuccess: onSuccessWebhook, OnFailure: onFailureWebhook}
	}
	return req, nil
}

func delayedTaskRetryPolicyFromFlags(fs *flag.FlagSet, maxAttempts int, baseSeconds, maxSeconds, jitterSeconds float64) (*api.RetryPolicyDTO, error) {
	set := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-attempts", "retry-base-seconds", "retry-max-seconds", "retry-jitter-seconds":
			set = true
		}
	})
	if !set {
		return nil, nil
	}
	policy := &api.RetryPolicyDTO{
		MaxAttempts: maxAttempts, BaseSeconds: baseSeconds,
		MaxSeconds: maxSeconds, JitterSeconds: jitterSeconds,
	}
	if policy.MaxAttempts < 0 || policy.MaxAttempts > 25 {
		return nil, fmt.Errorf("--max-attempts must be between 0 and 25")
	}
	if policy.BaseSeconds < 0 || math.IsNaN(policy.BaseSeconds) || math.IsInf(policy.BaseSeconds, 0) {
		return nil, fmt.Errorf("--retry-base-seconds must be finite and non-negative")
	}
	if policy.MaxSeconds < 0 || math.IsNaN(policy.MaxSeconds) || math.IsInf(policy.MaxSeconds, 0) {
		return nil, fmt.Errorf("--retry-max-seconds must be finite and non-negative")
	}
	if policy.BaseSeconds > 0 && policy.MaxSeconds > 0 && policy.MaxSeconds < policy.BaseSeconds {
		return nil, fmt.Errorf("--retry-max-seconds must be at least --retry-base-seconds")
	}
	if policy.JitterSeconds < 0 || policy.JitterSeconds > 1 || math.IsNaN(policy.JitterSeconds) || math.IsInf(policy.JitterSeconds, 0) {
		return nil, fmt.Errorf("--retry-jitter-seconds must be between 0 and 1")
	}
	return policy, nil
}

func delayedTaskHeaderJSON(values []string) (json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || !api.CorsHeaderNamePattern.MatchString(name) {
			return nil, fmt.Errorf("--header %q must be Name:Value with a valid HTTP header name", raw)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("--header %q contains a newline", raw)
		}
		key := strings.ToLower(name)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("--header repeats %q", name)
		}
		seen[key] = struct{}{}
		headers[http.CanonicalHeaderKey(name)] = value
	}
	return json.Marshal(headers)
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// cmdDelayedTaskList shows one newest-first page for an app.
func cmdDelayedTaskList(args []string) int {
	fs := newFlagSet("delayed-task list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	before := fs.String("before", "", "pagination cursor from next_before")
	limit := fs.Int("limit", 20, "max rows (1..200)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *app == "" || *limit < 1 || *limit > 200 {
		PrintUsage(os.Stderr, "usage: gregale delayed-task list --app <slug> [--before <id>] [--limit <1..200>]", "delayed-task")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListDelayedTasks(context.Background(), *app, *before, *limit)
	if err != nil {
		return printErr("Could not list delayed tasks", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Tasks) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no delayed tasks)")
		return 0
	}
	for _, task := range resp.Tasks {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", task.ID, task.ScheduledAt.Format(time.RFC3339), task.State, task.Method, task.Path)
	}
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more — pass --before %s\n", resp.NextBefore)
	}
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
// a no-op 200 on the server. The returned state is authoritative:
// only "cancelled" means execution was prevented.
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
	resp, err := client.CancelDelayedTask(context.Background(), id)
	if err != nil {
		return printErr("Cancel failed", err)
	}
	cancelled := resp.State == "cancelled"
	if jsonOutput {
		if err := writeJSON(map[string]any{"id": id, "cancelled": cancelled, "state": resp.State}); err != nil {
			return printErr("Could not encode JSON", err)
		}
		if cancelled {
			return 0
		}
		return 1
	}
	if cancelled {
		PrintOK(osStdout, "Delayed task %s cancelled.", id)
		return 0
	}
	_, _ = fmt.Fprintf(osStderr, "Delayed task %s is %s; execution was not cancelled.\n", id, resp.State)
	return 1
}

// validateDelayedTaskAddFlags enforces the per-field presence gate
// shared by cmdDelayedTaskAdd's body. Returns true on success;
// otherwise fires printErr with the matching error and returns false.
// Extracted to keep cmdDelayedTaskAdd under the 50-line handler cap.
func validateDelayedTaskAddFlags(app, scheduledAt, delay *string) bool {
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale delayed-task add --app <slug> (--scheduled-at <RFC3339>|--delay <duration>) [--payload <J|@file|->]", "delayed-task")
		return false
	}
	if (*scheduledAt == "") == (*delay == "") {
		printErr("Invalid schedule", fmt.Errorf("exactly one of --scheduled-at or --delay is required"))
		return false
	}
	return true
}
