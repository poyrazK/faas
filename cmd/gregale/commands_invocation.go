// commands_invocation.go — `gregale invoke <slug> [--async] [--payload J]`
// (Tier C). The day-1 functional smoke test ("is this app
// reachable?"): POST /v1/apps/{slug}/invoke synchronously drains
// the request through the same handler the dashboard's "Test"
// button uses. `--async` flips to /invoke/async and returns the
// status_url, or polls the accepted ID to a terminal result with `--wait`.
//
// Auth: authLimited → requireMFA → requireScope(ScopesDeployWriteSurface).
// Surface the server's APIError verbatim; no --no-mfa escape.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func cmdInvoke(args []string) int {
	fs := newFlagSet("invoke", flag.ContinueOnError)
	async := fs.Bool("async", false, "enqueue via /invoke/async; returns the status_url unless --wait")
	wait := fs.Bool("wait", false, "wait for the invocation to finish (implies --async)")
	timeout := fs.Duration("timeout", 0, "stop waiting after this duration (0 waits indefinitely; requires --wait)")
	interval := fs.Duration("interval", time.Second, "time between status checks (requires --wait)")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying the same async submission (requires --async or --wait)")
	payload := fs.String("payload", "", "JSON payload (or @file for file body, - for stdin)")
	method := fs.String("method", "", "HTTP method override (defaults to handler's)")
	path := fs.String("path", "", "URL path override (defaults to handler's)")
	onSuccess := fs.String("on-success-webhook", "", "app webhook id for completed invocation callbacks")
	onFailure := fs.String("on-failure-webhook", "", "app webhook id for failed or dead-lettered callbacks")
	workPolicy := fs.String("work-policy", "", "named app work policy (requires --async or --wait, and --work-key)")
	workKey := fs.String("work-key", "", "JSON string, number, or boolean application key")
	workFairnessKey := fs.String("work-fairness-key", "", "JSON scalar shared by related work keys, such as a tenant ID")
	// Go's flag parser stops at the first positional token. Reorder the
	// documented `<slug> [flags]` form before parsing so flags-first and
	// positional-first invocations share the same validation path.
	flags, positional := splitArgsForFlags(args, "async", "wait")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	asyncMode, err := resolveInvokeWaitFlags(fs, *async, *wait, *timeout, *interval)
	if err != nil {
		return printErr("Invalid invocation wait options", err)
	}
	key, err := invokeIdempotencyKey(fs, asyncMode, *idempotencyKey)
	if err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	if len(positional) > 1 || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale invocation run [--async] [--idempotency-key KEY] [--wait [--timeout D] [--interval D]] [--payload <json>|@file|-] [--method M] [--path P] [--work-policy NAME --work-key JSON [--work-fairness-key JSON]] [--on-success-webhook ID] [--on-failure-webhook ID] [<slug>]", "invoke")
		return 1
	}
	slug := ""
	if len(positional) == 1 {
		slug = positional[0]
	} else {
		var resolveErr error
		slug, resolveErr = resolveRequiredAppSlug("")
		if resolveErr != nil {
			if errors.Is(resolveErr, errProjectContextNotFound) {
				PrintUsage(os.Stderr, "usage: gregale invocation run [--async] [--idempotency-key KEY] [--wait [--timeout D] [--interval D]] [--payload <json>|@file|-] [--method M] [--path P] [--work-policy NAME --work-key JSON [--work-fairness-key JSON]] [--on-success-webhook ID] [--on-failure-webhook ID] [<slug>] (or run `gregale link <project-slug>`)", "invoke")
				return 1
			}
			return printErr("Could not read local project context", resolveErr)
		}
	}
	body, err := resolvePayload(*payload)
	if err != nil {
		return printErr("Invalid payload", err)
	}
	req := api.InvokeRequest{
		Payload: body,
		Method:  *method,
		Path:    *path,
	}
	if *onSuccess != "" || *onFailure != "" {
		req.Destinations = &api.InvocationDestinations{
			OnSuccess: *onSuccess,
			OnFailure: *onFailure,
		}
	}
	if *workPolicy != "" || *workKey != "" || *workFairnessKey != "" {
		if !asyncMode || *workPolicy == "" || *workKey == "" {
			return printErr("Invalid work policy", errors.New("--work-policy and --work-key must be used together with --async or --wait"))
		}
		if _, err := workpolicy.CanonicalScalar(json.RawMessage(*workKey)); err != nil {
			return printErr("Invalid work key", err)
		}
		req.Work = &api.InvokeWork{Policy: *workPolicy, Key: json.RawMessage(*workKey)}
		if *workFairnessKey != "" {
			if _, err := workpolicy.CanonicalScalar(json.RawMessage(*workFairnessKey)); err != nil {
				return printErr("Invalid work fairness key", err)
			}
			req.Work.FairnessKey = json.RawMessage(*workFairnessKey)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *wait {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
	}
	if asyncMode {
		resp, err := client.InvokeAppAsync(api.ContextWithIdempotencyKey(ctx, key), slug, req)
		if err != nil {
			return printErr("Invoke (async) failed", err)
		}
		if *wait {
			return waitForAcceptedInvocation(ctx, client, resp.ID, *timeout, *interval)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(os.Stdout, "Async invocation %s queued. Poll with `gregale invocation get %s` or via %s", resp.ID, resp.ID, resp.StatusURL)
		return 0
	}
	resp, err := client.InvokeApp(context.Background(), slug, req)
	if err != nil {
		return printErr("Invoke failed", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(resp)); code != 0 {
			return code
		}
	} else {
		PrintOK(os.Stdout, "Invocation %s status=%s", resp.ID, resp.Status)
		if len(resp.Result) > 0 {
			_, _ = fmt.Fprintln(os.Stdout, string(resp.Result))
		}
		if resp.Error != "" {
			_, _ = fmt.Fprintln(os.Stderr, resp.Error)
			// `invoke h14-py --path /` sent POST to a GET-only route and
			// answered only the app's 405 page (Hunt 14 H14-64).
			if *method == "" && strings.Contains(resp.Error, "HTTP 405") {
				_, _ = fmt.Fprintln(os.Stderr, "invoke: the route refused the method; invoke sends POST unless --method is given (for example --method GET)")
			}
		}
	}
	// A synchronous invoke that came back `failed` is a failed smoke
	// test, so it must not exit 0. The HTTP call succeeded (the
	// server returned 200 with a terminal state in the body), which
	// is why the `err != nil` arm above does not catch this — the
	// failure is in the payload, not the transport. Exiting 0 here
	// made `gregale invoke` useless as a CI gate: a parked or broken
	// app reported success. Result (if any) is still printed above so
	// the operator sees the handler's own error body.
	if !invokeStatusOK(resp.Status) {
		_, _ = fmt.Fprintf(os.Stderr, "invoke: terminal state %q — see `gregale invocation get %s` for the error\n", resp.Status, resp.ID)
		return 1
	}
	return 0
}

// invokeStatusOK reports whether a synchronous invoke's terminal
// state should exit 0. The vocabulary is the invocations_state_check
// CHECK in migrations/00064_invocations_dead_letter.sql:
// pending|dispatching|completed|failed|cancelled|dead_letter.
//
// Only `completed` is success. The non-terminal states (pending /
// dispatching) are treated as failure too: a synchronous invoke that
// returns without reaching a terminal state has not demonstrated the
// app works, which is the one question this command exists to answer.
func invokeStatusOK(status string) bool {
	return status == "completed"
}

// resolvePayload accepts three payload shapes:
//
//	--payload '{"k":"v"}'   inline JSON
//	--payload @path.json     file body
//	--payload -              stdin (EOF-terminated)
//
// Empty payload is valid (handler accepts a zero-body invocation,
// returns 200 with status=completed + empty result).
func resolvePayload(s string) ([]byte, error) { return resolveJSONFlag("--payload", s) }

// resolveJSONFlag reads a JSON flag value (inline, @file, or - for stdin) and
// names flag in the validation error, so `send --data` and `triggers --config`
// do not tell the user to fix a --payload they never passed.
func resolveJSONFlag(flag, s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	var b []byte
	source := "inline"
	switch {
	case s[0] == '@':
		source = s[1:]
		read, err := os.ReadFile(s[1:])
		if err != nil {
			return nil, fmt.Errorf("read payload file %q: %w", s[1:], err)
		}
		b = read
	case s == "-":
		source = "stdin"
		read, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		b = read
	default:
		b = []byte(s)
	}
	// The body is sent as a JSON value. Without this check an invalid
	// payload surfaced as "marshal request: json: error calling MarshalJSON
	// for type json.RawMessage: invalid character ...".
	if len(bytes.TrimSpace(b)) > 0 && !json.Valid(b) {
		return nil, jsonFlagSyntaxError(flag, source, b)
	}
	return b, nil
}
