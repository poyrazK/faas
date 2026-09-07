// commands_invocation.go — `gregale invoke <slug> [--async] [--payload J]`
// (Tier C). The day-1 functional smoke test ("is this app
// reachable?"): POST /v1/apps/{slug}/invoke synchronously drains
// the request through the same handler the dashboard's "Test"
// button uses. `--async` flips to /invoke/async and returns the
// status_url so the operator can poll via `gregale invocations get`.
//
// Auth: authLimited → requireMFA → requireScope(ScopesDeployWriteSurface).
// Surface the server's APIError verbatim; no --no-mfa escape.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdInvoke(args []string) int {
	fs := flag.NewFlagSet("invoke", flag.ContinueOnError)
	async := fs.Bool("async", false, "fire-and-forget via /invoke/async; returns the status_url")
	payload := fs.String("payload", "", "JSON payload (or @file for file body, - for stdin)")
	method := fs.String("method", "", "HTTP method override (defaults to handler's)")
	path := fs.String("path", "", "URL path override (defaults to handler's)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale invoke [--async] [--payload <json>|@file|-] [--method M] [--path P] <slug>", "invoke")
		return 1
	}
	slug := fs.Arg(0)
	body, err := resolvePayload(*payload)
	if err != nil {
		return printErr("Invalid payload", err)
	}
	req := api.InvokeRequest{
		Payload: body,
		Method:  *method,
		Path:    *path,
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *async {
		resp, err := client.InvokeAppAsync(context.Background(), slug, req)
		if err != nil {
			return printErr("Invoke (async) failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(os.Stdout, "Async invocation %s queued. Poll with `gregale invocations get %s` or via %s", resp.ID, resp.ID, resp.StatusURL)
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
		_, _ = fmt.Fprintf(os.Stderr, "invoke: terminal state %q — see `gregale invocations get %s` for the error\n", resp.Status, resp.ID)
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
func resolvePayload(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if s[0] == '@' {
		b, err := os.ReadFile(s[1:])
		if err != nil {
			return nil, fmt.Errorf("read payload file %q: %w", s[1:], err)
		}
		return b, nil
	}
	if s == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return b, nil
	}
	return []byte(s), nil
}
