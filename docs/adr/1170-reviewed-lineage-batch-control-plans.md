# ADR-1170: Reviewed lineage batch-control plans

Status: Accepted
Date: 2026-10-13
Related: ADR-1161, ADR-1162, ADR-1169

## Decision

Add CLI export/apply commands for fixed reviewed control selections. Export one
bounded attention page, not a dynamic paging loop or immediate control action.
Require a deliberate pause/resume/cancel action and a new destination. Validate
stable app/subscription/job scope and original creation/retention, then allocate
one fresh command UUID per selected job. Preserve the source filter, status,
observation and continuation hint in a private version-1 plan. Reject empty pages.
Limit plans to 200 distinct jobs and 128 KiB in pkg/api/limits.go.

Read plans strictly: one bounded regular JSON file, exact known non-null fields,
no duplicate/case-aliased keys, nonzero scope/plan/job/command UUIDs, original
creation guards, valid actions and unique jobs/commands. Normalize IDs/times only
in memory, never rewrite the reviewed file. The format is unsigned/editable;
review before execution and preserve job/action/guard/command identity after first
submission. Source filters are descriptive, not a live authorization or reservation.

Apply requires explicit app/subscription targets. Reject a different subscription
locally and resolve the supplied app to the saved stable UUID before any POST.
This adds read-scope requirements to existing deploy/write and MFA controls.
Execute entries sequentially in file order with independent existing control API
transactions, one per entry, bounded by per-request and total deadlines. Do not
refresh the job selection or manufacture new command UUIDs during apply.

Verify each reply's current job scope/incarnation/retention and the immutable
original command receipt before labeling it acknowledged. Report request/validation
errors conservatively as unconfirmed, continue independent entries, and mark
remaining entries not_attempted when cancelled/timed out or shared scope validation
fails. Reports contain current status/progress and original receipt metadata, not
case selections/results or payloads. Complete means every entry was attempted,
not that every command succeeded. Any unconfirmed/not-attempted entry exits 1;
Ctrl-C exits 130 after the partial report; all acknowledgments exit 0.

Repeating the same unchanged plan acknowledges known commands without reapplying
actions, even after later controls, while receipts remain retained. This is not an
atomic batch, rollback, durable client execution ledger or retention reservation.
New commands share the server's per-job receipt cap; no-op acknowledgments consume
slots. Terminal-state semantics remain existing API semantics, and stale guards or
expired jobs remain errors. Emit human or JSON result summaries without modifying
the plan. Reuse the private no-overwrite atomic artifact writer for export. No new
API, database schema, SDK operation or registry entry is needed.
