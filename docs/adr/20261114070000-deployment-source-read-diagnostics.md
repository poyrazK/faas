# ADR-20261114070000 · Deployment source read diagnostics

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [deployment timelines](20261113180002-post-deployment-monitoring-timeline.md)
  and [bounded notification fault history](20261114060000-deployment-notification-fault-evidence.md).

Explain unavailable evidence from the two existing operator API reads using
reviewed reason codes and response status. Preserve last success, cumulative
read failures, cancellation and recovery information through later polls and
terminal handling. Keep source-read failures unknown in the original three
checks; they must not independently establish a confirmed platform fault.

Return typed failures from the shared bounded observation reader. Keep existing
safe CLI messages, but attach fixed categories for invalid URLs, cancellation,
deadlines/timeouts, unavailable transport, HTTP authentication/authorization,
missing endpoints, throttling, refused redirects, server/other HTTP failures,
body read failures, oversize responses and invalid decoding. Preserve only
numeric response status and reviewed metadata. Do not retain or unwrap raw
transport/decoder errors, copy bodies/headers, or parse error text for a status.
Unknown error implementations use `unavailable`, with no invented HTTP status.

Add optional `sources` with fixed serving-health and notification-health
summaries to schema-version-1 reports. Count one read per source per poll,
although the notification source supports two checks. Record attempted, failed,
cancelled and consecutive failed reads; original last read/success/failure and
recovery clocks; the latest failure reason/status; and optional individual read
start/completion/duration. Use monotonic elapsed time for duration when available.
Never fabricate reads before collection or update clocks during terminal saves.

Read states are `not_read`, `succeeded`, `failed` and `cancelled`. A decoded
response satisfying existing basic projection checks can recover the read while
health remains stale, partial or degraded. Serving contract rejection or missing
notification generation time is an invalid response; do not turn optional
advisory validation or partially usable notification fields into a new gate.
Cancellation has its own count and preserves an open failure streak. Successful
collection after failures records read recovery, independent of health recovery.

Use `source_<reason>` for affected unknown checks and optional numeric
`source_http_status` in current checks and their timeline transitions. Retain
reason and status changes, including 502 to 503 within `server_error`, while
deduplicating repeated identical failures. Reuse the existing 256-transition
limit and two fixed summaries rather than introduce another event quota.
Summaries survive rollover; source HTTP status and health transitions survive
report-envelope fitting. Copy retained metadata so later reads cannot rewrite
earlier reports. Render the same safe information in human and JSON output.

Preserve the two existing GETs, credential handling, redirect refusal, response
and HTTP timeout limits, polling/wait budgets, qualification, fault flags, exit
codes and notification/rollback behavior. Verify status classes, context/body
errors, unsafe untyped errors, invalid contracts, redaction, readable stale
evidence, failure/recovery and cancellation counts, status transitions, rollover,
terminal paths, private CLI reports, shared bundle compatibility and the maximum
report envelope with both source summaries and historical statuses populated.
Production convergence remains separate.
