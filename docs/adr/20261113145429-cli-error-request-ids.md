# ADR-20261113145429: Request IDs in ordinary CLI API errors

Date: 2026-11-13
Status: Accepted

## Context

The API sends a correlation header, but the SDK error decoder drops it.
Customers copying an ordinary CLI failure cannot supply the request ID needed
to find the corresponding server logs without enabling HTTP diagnostics.

## Decision

Retain one validated `X-Faas-Request-Id` from an API error response as private
client metadata on `api.Problem`. `WithRequestID` and `RequestID` provide access
without changing the API problem body or adding a forwarded response header.
Re-emitting an SDK problem through a server must preserve that server's own ID.

The shared `ResponseRequestID` validator accepts only a single 32-character hex
ID or hyphenated UUID shape, matching the HTTP diagnostics contract. Preserve
case; reject whitespace, arbitrary text, comma lists and duplicate values,
including differently cased header-map keys. Error JSON cannot supply this
metadata. HTTP diagnostics use the same validator.

CLI redaction preserves the validated ID separately from its JSON copy and
omits any ID matching a known credential. Text errors add `Request ID: <id>`;
JSON errors add optional `request_id` alongside their existing fields. Missing
or invalid headers, local validation failures and pre-response transport errors
add no ID. Successful stdout, error codes, exit codes and retry policy retain
their existing behavior. No additional API request is needed.

Invocation-wait JSON errors retain the same validated correlation metadata
alongside their structured recovery fields, for both standalone waits and
submit-and-wait. They continue to use the dedicated wait exit-code contract.

Each failed attempt is decoded independently. An exhausted retry reports the
final response's ID; a final response without an ID never inherits an earlier
one. JSON and non-JSON API error responses follow the same rule.

## Validation

SDK tests cover header validation, decoder paths, problem-body and forwarding
compatibility, and final-attempt identity. External CLI tests cover text and
JSON output, redaction, credential-shaped IDs, missing IDs, successful output
and local/transport errors. GA row 156 includes isolated built-CLI and read-only
deployed API error journeys. Invocation-wait coverage checks correlation and
recovery together, including omission of missing or invalid headers.
