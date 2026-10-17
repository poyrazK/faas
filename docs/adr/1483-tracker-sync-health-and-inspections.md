# ADR-1483: Durable sync health and read-only recovery inspections

Date: 2026-10-15

Status: Accepted

## Context

The tracker review queue exposes current holds and conflicts but worker completions
overwrite earlier outcomes. Owners need failure evidence and a fresh status check
without accidentally resuming delegation or replaying an uncertain write.

## Decision

Record bounded per-link receipts for completed worker checks, enabled/disabled
policies, reviewed inbound pulls, and scoped owner-requested inspections. Insert
and prune receipts in the same transaction as each worker or policy action. Use
the central `IssueTrackerSyncHistoryMax` limit (50 recent receipts per link).
Reference deletion cascades this history. Store lifecycle metadata, policy revision,
direction and stable reason codes, never credential values or raw provider errors.
Claim identity is unique per link for worker receipts. Stale, expired or duplicate
worker completions cannot append history or update health. Finish locks app before
policy to follow the existing inbound lock order. Receipt insertion failure rolls
back the completion or lifecycle action. Keep last successful automatic check time
separately from the bounded history; only completed matched/applied checks without
pending intent advance it. Configuration and inspection are not automatic success.
Do not invent historical success when migrating existing policies.

Expose owner-only local health/history pages from issue links and the app review
queue. Loading these pages makes no provider requests. Separate retryable check
failures from paused policies and unknown write outcomes, and label historical
success as evidence rather than proof of current provider state.

The existing explicit provider-preview form becomes the Check now control. Record
its successful inspection or sanitized failure under the app intent fence and
policy revision captured before the read. Do not change its delegated baseline,
outcome, pending intent, lifecycle, schedule or enabled state. A concurrent edit
invalidates the receipt/review. Failed inspection recording is best effort when
returning an existing provider error. Successful reviews require their receipt to
persist. Inspections before a policy exists retain the original preview behavior.

All directions now reject re-enabling over live worker claims or unresolved pending
writes. Resuming still requires an explicit fresh scoped provider verification and
aligned lifecycle snapshot. Remove the older GitHub outbound retry exception:
unknown GitHub and Linear writes are observed, not redispatched. Matching the exact
intended provider result confirms the write; an unchanged baseline retains pending
intent. An explicit disable still ends delegation and clears its active pending
state, but records the old intent in the bounded disable receipt. Warn that an
already dispatched provider write may still complete. Resetting delegation is an
owner review decision, not an automatic recovery retry.

## Consequences

History and last success remain available during provider failures, and owners
can inspect a paused policy without granting it new authority. The history covers
recent completed operations, not every started/crashed attempt or a permanent
audit archive. Older receipts are pruned; last success survives pruning. There are
no automatic conflict resumes or new provider write paths. Existing public API
and SDK policy schemas remain unchanged; these controls are dashboard surfaces.
