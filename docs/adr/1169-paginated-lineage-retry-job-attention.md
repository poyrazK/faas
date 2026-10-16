# ADR-1169: Paginated lineage retry-job attention

Status: Accepted
Date: 2026-10-13
Related: ADR-1158, ADR-1167, ADR-1168

## Decision

Extend the existing scoped retained job listing with optional attention=any,
stalled or retention_risk. Empty means ordinary listing. Nonempty attention
restricts candidates to pending/paused, not-yet-expired jobs created by the
observation time. Combine with the existing status filter; completed/cancelled
status plus attention is invalid. Any is the union of pending inactivity of at
least 15 minutes and active expiry within 24 hours, using the shared diagnostic
limits. Paused jobs do not qualify as stalled but can have retention risk.

Apply filters before the existing limit+1 keyset query. Preserve newest original
creation time and descending request UUID order and cursor format. Read target
ownership and rows together in a read-only repeatable-read transaction, matching
the memory store's mutex. Evaluate each page against one captured observation
clock. The list is not capped at the health diagnostic scan or sample limits;
operators can continue through all matching pages.

Attach page coverage, filter, observation time, has_more and warning limits only
for attention requests. has_more matches next_after and reports another observed
matching row, not a complete backlog count. Attach per-job metadata using the
same health evaluator: inactivity, known/unknown progress, remaining cases,
retention age/risk, and original creation guard. Add optional last_progress_at to
ordinary summaries. No case selections/results, event payloads or command receipts
are exposed.

Every page is a live observation, not a frozen warning set or reservation. Job
activity, status and warning membership can change between pages. Preserve status
and attention filters while paging; restart from the first page to catch jobs
that enter a warning above the current cursor. A final page or omitted job does
not prove the whole consumer is healthy at a later time. Guards support deliberate
existing controls after review; listing never mutates or reserves jobs.

Expose optional Go variadic options, generated Node/Python query parameters and
CLI --attention. Existing Go calls without options remain valid. Go/CLI reject
attention replies lacking compatible page metadata, preventing silent fallback
when older API readers ignore the new parameter. Other clients should check the
metadata when operating against mixed versions. Existing routes, scopes/MFA,
limits, SQL indexes and SDK coverage mapping remain sufficient; no migration or
compliance registry change is needed.
