# ADR-453: Retained route checks and finding regressions

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-446, ADR-448, ADR-449, ADR-451, ADR-452

## Context

Aggregate route safety transitions report the first violation and eventual
recovery. A second route can become unsafe while a deployment remains violated,
without producing another transition. The latest result also replaces the
specific evidence referenced by an earlier notification.

## Decision

Retain immutable completion snapshots identified by the existing fenced request
UUID. Keep at most 20 entries and 64 MiB of compact encoded JSON per deployment;
prune oldest entries in the completion transaction. Each entry is bounded at
32 MiB and contains checked_at, the original check and its finding comparison.
A summary-only history list supports newest-first cursor pagination (default 5,
maximum 10). Exact reads and cursors return 404 after retention eviction.
Do not backfill earlier checks. Retention is diagnostic evidence, not a permanent
audit trail, and history never becomes current canary gate evidence.

Maintain a separate bounded last-known finding baseline on the existing check
job, independent of retention. Identify findings by method, path, requirement
and declared expectation. Compare only available captured evidence with the
same saved revision and normalized intent hash. First checks and changed intent
establish observations without claiming regressions or fixes. Unknown findings
retain prior known verdicts; unavailable inventory or account eligibility retains
the entire baseline. Removed findings leave observation without being resolved.
A later reappearance is compared as a newly observed finding.

Known violated findings following satisfied or previously unobserved findings
are newly_violated. A known pass following a violation is resolved. Changes in
configured actual value, code or selected rule IDs are changed even when status
stays the same. Rule order and explanatory copy alone do not create changes.
Duplicate finding identities or the existing 10,000-finding bound make a check
ambiguous and preserve the baseline. A baseline exceeding the existing 16 MiB
result budget yields tracking_limit. No alert uses either incomplete comparison.
Delta detail is bounded at 2 MiB with explicit truncation and exact summary
counts. Per-finding before_check_id identifies its last known evidence, which
can predate compared_to_check_id and can outlive retained history.

Read APIs enforce account/app/deployment ownership, nondeleted apps, read scope,
completed MFA and current captured endpoint discovery entitlement. Notifications
exclude routes, intent and raw policy; authenticated exact history reads expose
the already allowlisted check findings. Go clients allow bounded 32 MiB reads
for full entries and automatic results while keeping ordinary response caps.

Completion takes the existing account/app NO KEY UPDATE locks, then locks the
fenced job baseline. Atomically compute the comparison, publish the latest check,
update the baseline, insert history, prune and enqueue notifications. Recheck
lease validity on the final update. Rejected leases and failed history writes
publish nothing. Memory storage performs the same steps under its mutex and
isolates returned values.

Keep existing routes.requirements.violated/recovered aggregate semantics. Add
routes.requirements.changed only when the previous aggregate safety state was
violated and a comparable completion contains newly violated findings. Initial
violations produce the existing event only. Quiet repeats and transient unknowns
do not duplicate alerts. The completion trigger snapshots matching enabled app
subscriptions into the existing durable outbox; its event/request uniqueness
preserves deduplication. Include comparison counts, input provenance, latest
result_path and exact history_path. The relay retains its at-least-once signed
webhook delivery contract.

## Consequences

Customers can identify an additional route regression during an existing
incident, compare expected and actual policy, and inspect the original retained
notification evidence. Storage, response size and evaluation remain bounded.
Intent edits and disappearing routes cannot masquerade as finding fixes.
A pruned entry cannot be fetched, and historical evidence cannot authorize a
traffic increase. The feature adds no application requests, policy edits,
workers or VM lifecycle operations.
