# ADR-20261113183052: Wait after async CLI invocation

Status: Accepted
Date: 2026-11-13
Related: spec §6.7 (durable async-job contract)

## Decision

`gregale invoke <app> --async --wait` submits once through the existing async
endpoint, then uses the invocation wait poller for the accepted ID. It never
submits again because of a polling error, timeout or interruption. Poll through
the configured API client, not an arbitrary receipt `status_url`. Reject missing
or invalid receipt IDs and status records whose ID differs from the requested ID.

`--wait` requires `--async`. Explicit `--timeout` and `--interval` require
`--wait`. Validate these combinations and durations before any submission.
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
code. Human progress names the accepted invocation on stderr. Existing async
invocation without `--wait`, synchronous invoke and standalone invocation wait
keep their output contracts. Credential redaction still applies to diagnostics.

## Verification

Table tests cover all terminal outcomes in human and JSON mode, payload/routing
preservation, flags before submission, timeout before/after a status read,
polling HTTP failures, invalid receipts and mismatched status IDs. Every submitted
wait path asserts a single POST. A built-CLI journey sends a real Ctrl-C while
the first status request is blocked, checks exit 130 and resumable acceptance,
and proves the request closes without cancellation or another submission. The
live journey sends a harmless health request through the durable queue and
compares the final result with the stored invocation ledger record.
