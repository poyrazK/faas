# Operator collection history and cadence coverage

Status: Accepted, 2026-10-16. Extends collection maintenance health, recurrence
and response timing with prospective retained outcome evidence.

## Decision

Persist immutable accepted collection outcomes in a separate timestamp-keyed
journal. Successful outcome insertion, latest singleton status and episode
capture/recovery commit in one transaction/memory lock. Failures update latest
attempt status and insert a closed reason atomically under the same lock order.
The first accepted outcome wins at an equal timestamp; superseded older attempts
cannot rewrite history or current state. Do not backfill from existing singleton
timestamps. Reads never create receipts or change lifecycle/triage state.

Keep the existing failure method compatible with callers omitting a reason;
it then uses collection_failed. An optional single reason must belong to the
closed source_read_failed, snapshot_rejected, timeout, persistence_failed or
collection_failed set. Raw errors, rule text, labels, source URLs and payloads
never enter the journal. Collector read/decode errors, rejected snapshots and
persistence failures remain distinguishable without claiming a precise cause.
An expired poll receives a fresh bounded two-second failure-write context if
its parent remains live. Parent cancellation or a failed receipt write leaves
missing evidence. A late error after an ambiguous successful commit cannot
replace its same-timestamp success. No failed observation resolves an episode.

A read projects a half-open [since, as_of) cadence window, default 24 hours and
maximum 30 days. An accepted outcome supplies forward evidence for at most
60 seconds, ending at the next outcome if sooner. This is a freshness model,
not continuous availability, expected inventory or per-rule/metric coverage.
All other intervals are unknown/no_recent_outcome: before rollout, missed polls,
pruning and unrecordable outages are not retrospectively assigned causes.
Failure evidence also expires; a single failure cannot label an indefinite
outage. Adjacent intervals with the same state and reason coalesce. Preserve
microseconds so success, failure and unknown duration totals exactly partition
the full window. Count attempts inside [since, as_of] inclusive; a retained
carry-in outcome can contribute duration without increasing those counts.

State observed means the entire window has recent success evidence; partial
means some fresh evidence exists alongside failed or unknown duration; unknown
means no fresh duration evidence exists. Even observed does not establish a
complete incident history. Current disabled configuration does not rewrite past
evidence. Future receipts are excluded rather than extrapolated backward.

Retain outcomes for 30 days. Extend the existing collector-ordered retention
transaction to prune at most 5000 expired outcomes independently of its episode
batch; the existing pruned-episode count remains episode-only. Reads exclude
outcomes outside this horizon even while cleanup is behind. No lifetime count
or baseline coverage is inferred when receipts have expired or been removed.

GET /v1/admin/obs/collection-history and gregalectl obs collection-history
--since RFC3339 --json read one snapshot with a five-second budget. Read from
max(since-60s, as_of-30d) through as_of. Over-fetch one beyond 100000 outcomes
and reject overflow with a narrow-since 400; never sample. Complete coverage
precedes a newest-first preview of at most 200 merged intervals, with total
interval count and explicit truncation. Preserve operator scope, MFA and
allowlist checks; reject unknown, duplicate, empty or malformed query values
and dangling question marks. Storage errors are generic 503s.

Recurrence and response-time snapshots read this same bounded journal under
their existing transaction/mutex. Their added coverage metadata is optional for
compatibility with older servers, and new servers always supply it. An outcome
read overflow also fails those reports with a narrow-since 400. Existing episode
counts, timing samples and immutable identity semantics remain independent.
CLI output validates partitions and bounded previews, keeps missing metadata
unknown and never prints upstream error bodies. Synchronize SQLC, canonical and
embedded OpenAPI, private-route exclusions, DTO coverage and the Node client.
Frontend and rollout remain separately owned; this work stays local.

## Validation

Check memory/PostgreSQL order, first-outcome idempotency, failure reasons,
cancellation, carry-in boundaries, retention, exact and exceeded read bounds,
legacy unknown history, transaction rollback and a new PostgreSQL connection.
Exercise expired poll persistence, sanitized failure categories, interval
partition/merge/clipping, prospective unknown periods, coalesced truncation,
and same-snapshot analytic coverage. Verify authorization/MFA, read-only GETs,
query validation, limit and redacted storage failures, CLI arguments/JSON and
invalid evidence rejection. Run migration up/down/replay and constraint gates,
SQLC/OpenAPI/Node drift checks, focused regressions, policy checks and lint.
