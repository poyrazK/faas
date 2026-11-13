# ADR-20261113183052: Wait after async CLI invocation

Status: Accepted
Date: 2026-11-13
Related: spec §6.7 (durable async-job contract)

## Decision

`gregale invocation run <app> --wait` submits once through the existing async
endpoint, then uses the invocation wait poller for the accepted ID. It never
submits again because of a polling error, timeout or interruption. Poll through
the configured API client, not an arbitrary receipt `status_url`. Reject missing
or invalid receipt IDs and status records whose ID differs from the requested ID.

`--wait` implies async submission, so callers do not have to also pass `--async`.
Reject an explicitly contradictory `--async=false --wait` before any submission.
Explicit `--timeout` and `--interval` require `--wait`. Resolve the submission
mode and validate these combinations before payload reads or network access.
The run-and-wait and standalone wait commands share duration validation.
Timeout is nonnegative (default zero, meaning indefinite) and polling interval
is positive (default one second). The timeout begins after acceptance and bounds
only local waiting; server execution and retry policy are unchanged.

Completed returns zero; failed, cancelled and dead_letter return one. Local wait
timeout returns 124; Ctrl-C returns 130. Neither cancels remote work. Polling
HTTP errors retain invocation wait's existing exit mapping. Preserve the accepted
ID and a shell-quoted, profile-aware `invocation wait` resume command in timeout,
interruption and polling error diagnostics. A timeout may print the last known
status; when no status was read, do not invent one. Losing the submission response
does not justify an automatic second submission.

JSON stdout contains the invocation record, without a preceding receipt or
progress document. JSON diagnostics go to stderr and include the actual exit
code. Timeout, interruption and polling failures include the CLI-local
`recovery` extension: `invocation_id`, `inspect_command` and `resume_command`.
The commands are read-only, POSIX-quoted and profile-aware; flag-shaped IDs use
an argument separator. Credential redaction
applies to every recovery field as well as the Problem. Preserve server
remediation and transport error classification. Invalid or missing submission
IDs do not produce a recovery target. Standalone wait also emits this extension,
including a JSON diagnostic on Ctrl-C; its target is caller-supplied and does
not prove acceptance, existence or current execution. Human standalone Ctrl-C
remains silent. The CLI does not execute recovery commands. Retain current
credential/endpoint overrides when resuming because commands read connection
configuration afresh. Human progress names the accepted invocation on stderr.
Async invocation without `--wait` and synchronous invoke keep their contracts.

## Verification

Table tests cover all terminal outcomes in human and JSON mode, payload/routing
preservation, flags before submission, timeout before/after a status read,
polling HTTP/transport/decode failures, invalid receipts and mismatched status
IDs. Every submitted wait path asserts a single POST. Recovery assertions cover
typed targets, redaction and preservation of server remediation. A built-CLI
journey times out, executes the returned inspect and resume commands with a
quoted opaque ID and a flag-shaped ID, and verifies only the original invocation
is read. Another
journey sends a real Ctrl-C during run-and-wait and standalone wait while the
first status request is blocked, checks exit 130 and typed recovery, and proves
the request closes without cancellation or another submission. The
live journey sends a harmless health request through the durable queue and
compares the final result with the stored invocation ledger record.
