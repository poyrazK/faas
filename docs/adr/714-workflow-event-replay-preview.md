# ADR-714 · Workflow event replay preview

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add a bounded, read-only workflow-name preview over retained event envelopes and their immutable captured workflow recipients.
- **Why:** Operators need to inspect workflow trigger matches, routing checkpoints, and durable admission deduplication before a future workflow replay policy is designed.
- **Consequences:** The API and `gregale events workflow-replay-preview` scan retained events by platform acceptance time. They evaluate the captured workflow trigger filter, expose only event metadata and receipt links, and report whether a workflow admission receipt and retained run exist. The preview does not admit workflow runs, enqueue delivery, or evaluate current definitions for events where the workflow was not captured. `potential_admission_count` is an estimate and does not account for current quotas or target availability. Retention is a live view and does not guarantee a complete archive.
- **Rejected alternatives:** Reusing the subscription preview endpoint would mix ordinary subscription declarations with immutable workflow snapshots. Starting runs during preview would make inspection produce application side effects. Historical workflow backfill remains a separate decision because it needs an explicit replay policy, admission limits, and run deduplication semantics.

## Contract

`GET /v1/apps/{slug}/workflow-event-replay-preview` uses the existing read
scope, MFA, app ownership, and no-store response policy. Required
`workflow_name`, `from`, and `until` select one app workflow and a half-open
range of platform acceptance time. The exclusive cutoff is fixed by the first
page; each page examines at most 100 retained event envelopes, including
envelopes that did not capture this workflow. A page may have no matches and
still return `next_after`.

The cursor binds account, app, workflow name, range, cutoff, and the last
`(created_at, outbox_id)` tuple. It does not bind the page size. Pagination is a
live retained view: pruning or delayed acceptance commits can change rows
between pages, so the cursor is not a frozen export.

Membership comes only from the event's immutable `recipient_snapshot`; current
workflow definitions are not consulted. A non-null snapshot without the named
app workflow is `not_captured`; a legacy null snapshot is `unknown`. For a
captured recipient, the preview evaluates the stored source, type, and trigger
filter with the same matcher used by workflow admission. Returned matches
include the latest routing checkpoint, whether a durable row exists in
`workflow_event_receipts`, and the run ID/status when its run remains retained.
An admission receipt continues to deduplicate the `(outbox_id, recipient_id)`
pair after `workflow_runs` retention removes its linked run. Missing admission
receipts count as potential admissions only when the captured trigger filter
matches; they do not promise quota, account, app, or deployment eligibility at
some later replay time.

Responses never return event payloads or captured workflow definitions. Counts
are page-local. `earliest_retained_at` is account-wide, `history_complete` is
always false, and the preview does not pin rows or change routing state. No
workflow replay job, new admission path, or retry policy is introduced.

## Validation

Qualification covers filter parity, matching and filtered snapshots, events
where the workflow was not captured, legacy unknown snapshots, continuation,
admitted runs, durable deduplication after run pruning, account/app isolation,
no-store responses, and unchanged run creation during preview.
