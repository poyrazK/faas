# ADR-20261113132310: Opt-in retries for selected CLI reads

Date: 2026-11-13
Status: Accepted

## Context

A temporary API or network failure currently ends ordinary CLI reads. Scripts
must implement their own retry loops, which can accidentally repeat mutations
or ignore rate-limit delays. The SDK already has separate policies for traffic
policy checks, approvals and some operations; those retain their contracts.

## Decision

Add the prefix `--http-retries N` and `--http-retries=N`, accepting 0–5 additional
attempts. The default is zero. The last prefix wins; state is restored on every
return, with no environment variable or persisted setting. Invalid counts fail
before requests without echoing the rejected value.

The SDK exposes a copying `WithReadRetries` method and an immutable
`ReadRetryAttemptFromContext` observation API. A closed route set covers JSON
app listing/detail/status/usage, deployment listing/latest/summary/detail/stages,
and account compute usage summary. A GET elsewhere does not prove replay
safety: invocations, work acquisition, streams and all other paths are excluded.
The flag adds no retries to mutations or uploads.

Retry selected connection interruptions and per-attempt timeouts, generic
502/503/504 failures with known transient problem codes, and 429 rate limits
with an authoritative `Retry-After`. Use the actual HTTP status and headers,
not a problem body's claimed status. Authentication, authorization, validation,
quota, TLS verification, invalid JSON and unknown problem codes are excluded.

Use the shared equal-jitter backoff (250 ms base, 5 s cap). A valid delta-seconds
or HTTP-date `Retry-After` is a lower bound. Multiple, malformed or excessive
values end retries with the original error; a required wait above 30 seconds
is never shortened to retry early. Response bodies close before backoff.
Caller cancellation stops waits and further sends. HTTP client deadlines are
per attempt; a caller context can bound the whole operation.

`--debug-http` keeps schema version 1 and its request/response pairs, adding
optional attempt, maximum-attempt and prior-delay fields for eligible opted-in
reads. Records contain no URL, query, credentials, body or arbitrary server
text. Successful stdout and the final error's existing representation remain
unchanged. No API wire format, server execution budget or platform quota changes.

## Validation

SDK tests pin response classification, actual status, rate-limit delays,
cancellation, budget exhaustion, body ownership, transport errors and excluded
operations. External CLI tests pin parsing, invocation isolation, diagnostics,
stdout, forwarded arguments and completion. GA row 156 adds built-CLI isolated
and read-only deployed API journeys.
