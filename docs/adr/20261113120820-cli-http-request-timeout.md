# ADR-20261113120820: Invocation-scoped CLI HTTP request timeout

Date: 2026-11-13
Status: Accepted

## Context

The CLI normally caps JSON API requests at 30 seconds, while uploads and
some operations have longer defaults. A customer on a slow connection cannot
currently select a larger budget for a single invocation, or fail faster in
an automation script. A command's `--timeout` often controls a server-side
operation or a polling loop and does not select the HTTP transport budget.

## Decision

Accept the prefix `gregale --http-timeout DURATION <command>` and its inline
form. Require a positive Go duration, reject invalid values before requests,
and restore invocation state on every return path. The last prefix wins.
There is no environment variable or persisted configuration for this flag.

The SDK exposes `NewClientWithRequestTimeout`. A positive explicit budget
sets the ordinary client's whole-request timeout and response-header wait,
also covering uploads. Operation-specific derived clients retain that budget
instead of extending it to their defaults. Transport wrappers, authentication,
redirect policy, TLS, proxy selection, and existing retry policy remain intact.
Non-positive SDK constructor values retain existing defaults; the CLI rejects
them so a typo cannot silently disable deadlines.

SSE streams inherit the configured response-header wait and retain an
unbounded body lifetime controlled by the caller context. Caller deadlines,
dial and TLS limits can finish sooner. This flag is per HTTP exchange; it
does not impose an overall polling/retry deadline, change server execution
budgets, or change private operator/OIDC clients with separate policies.

Help, all four shell completions, generated reference and configuration docs
describe the prefix and streaming behavior. Errors never echo invalid flag
contents; successful stdout is unchanged. No API wire format, platform quota,
financial-model limit, or deployment requirement changes.

## Validation

SDK tests exercise headers, complete response bodies, uploads, caller
cancellation, operation-specific clients, wrappers and streaming lifetime.
External CLI tests cover prefix parsing, state restoration, unchanged stdout,
image argument forwarding and completion. GA row 156 includes isolated
built-CLI and read-only deployed API journeys.
