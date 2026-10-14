# ADR 1050: Priority-aware consumer group claims

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add an immutable optional `priority_metadata_key` at consumer-group creation.
Empty keeps sequence-based claim behavior. Validate the configured key using
existing metadata key rules (at most 64 bytes). Freeze an exact single-digit
metadata value 0..9 on snapshot assignment; absent, empty, signed, padded,
whitespace, out-of-range and malformed values default to zero. Larger priorities
are preferred. Retry and dead-letter replay preserve the frozen priority.

Priority applies within the bounded materialized snapshot window, not to all
retained history. Before an opted-in selection, fill free snapshot capacity using
sequential assignment, filters and deadline processing. Share the existing
128-message assignment/skip budget across the batch and never exceed the
128-outstanding-snapshot bound. Higher-priority messages beyond this window may
wait behind earlier assignments. Snapshot capacity, pause/drain, claim rate limits,
lease limits, retry availability, deadlines and entity ordering remain authoritative.
Existing eligible snapshots remain claimable across a source retention gap;
when none are eligible, the normal explicit gap recovery response still applies.

Select highest priority, then oldest sequence among eligible ready messages.
Persist a modulo-eight successful-lease turn under the group lock. Every eighth
successful lease selects the oldest eligible sequence regardless of priority.
Failed selections, renewals and settlement do not advance the turn. Transactions
roll lease/fairness changes back together. Batch, account and scoped-worker claims
share this turn across workers/replicas. Oldest selection provides regular progress
for continuously eligible older messages; it cannot bypass delayed retries,
entity predecessors, active leases, dead-policy blockers or capacity constraints.
No immediate global highest-priority or completion-time guarantee is made.

Expose account-owned GET group `priority`, with read scope and MFA, for the
immutable field, fairness interval, observational next-turn hint and materialized
ready/leased/dead counts by priority. No new worker permission or scoped read route.
Counts include delayed/blocked ready snapshots and leased snapshots awaiting expiry
maintenance. Empty priorities are omitted. The inspector shows this same view.
Generated Node/Python clients accept the creation field and expose the read route.

Use sqlc for policy, priority freezing, selection, fairness advancement and backlog
queries. No new background task, webhook or telemetry retention. Migration
20261012102700515 defaults existing groups/snapshots to disabled/zero. Apply it
before the server, and upgrade all apid replicas before using priority groups:
older binaries claim by sequence and do not advance priority fairness. Downgrade
removes scheduling preference but preserves message/lease state. Resync retains the
turn with the same group UUID. A newly created group starts seven priority turns
followed by its first oldest turn.

Runtime tests and migration application are deferred at the user's request.
Builds and generation/spec checks do not establish database execution evidence.
