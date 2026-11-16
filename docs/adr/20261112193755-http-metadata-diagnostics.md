# ADR-20261112193755: Opt-in CLI HTTP metadata diagnostics

Date: 2026-11-12
Status: Accepted

## Context

CLI failures often need a request ID and time to response headers to correlate
with server observations. Dumping an HTTP exchange exposes API keys, sealed
configuration, signed URLs, or app output and breaks machine-readable stdout.

## Decision

- Add the prefix-only `gregale --debug-http` boolean. Capture an invocation's
  stderr writer in each CLI API transport and close the recorder on return;
  restore root wiring even on parsing and command errors.
- Emit versioned JSON lines on stderr: a request start and a response-header
  or transport-error record paired by a recorder-local sequence number.
  Serialize recorder writes so concurrent requests keep complete JSON records.
- Record standard method, constant route template or redacted resource family,
  UTC timestamp, numeric status, and monotonic elapsed milliseconds to headers.
  Copy only one platform `X-Faas-Request-Id` matching a 32-hex or UUID shape.
  Report errors with a closed vocabulary instead of raw error strings.
- Never copy URL hosts, userinfo, query strings, path parameter values, request
  or response bodies, or other headers. Unknown routes stay redacted, including
  custom base paths; unknown methods use `OTHER`.
- Observe existing RoundTrip calls without consuming bodies or changing API
  retries, redirects, timeouts, headers, or stdout. Body-stream failures after
  headers remain the caller's responsibility. Diagnostic writer failures stop
  observation without changing the API result. Existing command warnings and
  errors retain their behavior and may share stderr.

## Consequences

Support captures can pair a slow or failed attempt with a platform request ID
without a payload dump. Records intentionally sacrifice arbitrary route and
header detail for redaction. This is client-only and opt-in, with no persistent
configuration, new API, or new plan limit. It diagnoses CLI API transports;
external programs and direct app HTTP clients are outside its scope.

## Validation

External CLI tests cover stdout parity, prefix parsing, state restoration,
transport errors, streaming bodies, concurrent records and redaction. Built-CLI
journeys exercise isolated success/failure captures and the deployed account's
read-only usage summary. Completion, reference and focused transport regression
checks cover the user-facing surfaces.
