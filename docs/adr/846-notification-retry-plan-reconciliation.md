# ADR-846: Notification retry plan reconciliation

Date: 2026-10-09
Status: Accepted

## Context

Selected retry plans can partially execute or lose responses after a committed job request. Operators need one read-only receipt identifying retained decisions and the outcomes of their original retry generations.

## Decision

Add `gregale events notification-retry-reconcile --file PLAN [--wait [--timeout DURATION]]`. Require the existing strict prepared-plan format and bounds. Compose only the existing saved-decision GET API; never submit, generate request IDs, refresh generation guards, or select additional deliveries. Existing SDK methods suffice; no endpoint, schema, migration, or SDK wire changes are required.

Read every selected request sequentially with the existing five-second per-read deadline. Validate app/job/request identity, the full canonical target intent, queued generation increments, and skipped decision semantics. Pin the first accepted immutable decision across polling rounds. Report a mismatched or changing decision as an error without accepting its evidence.

Emit one final JSON receipt regardless of --json. Each job identifies its decision state (decided, missing, read_failed, invalid_observation, or not_observed), outcome status, counts, and last valid observation. Mark whether that observation belongs to the current round. A 404 is missing/inconclusive, not proof of nonexecution; other read failures are errors. Preserve prior valid observations for inspection when a later read fails. Aggregate counts include only current validated observations. Counts cover observed targets, not unobserved requests; inspect per-job states for missing evidence.

Reuse original-generation outcome assessment: queued and skipped are decisions, while succeeded, failed, pending, and unknown describe delivery evidence. Later-generation success cannot prove original-generation success. All-skipped requests and unknown retained outcomes are inconclusive. Success requires every job to have known successful queued outcomes (skipped targets may coexist). Read errors take aggregate precedence, then known failure, inconclusive evidence, pending, and success. Each job remains separately visible even when the aggregate has a higher-priority status.

Without --wait, make one read round. With --wait, poll only while the aggregate is pending, using the existing five-second poll interval and default five-minute overall deadline. Stop on known failure, missing/unknown evidence, or read error rather than masking these conditions with an indefinite wait. Timeout must be positive and requires --wait when explicitly supplied. Ctrl+C cancels timers and reads while retaining a receipt. Exit codes are 0 succeeded, 1 failed/error, 2 pending/inconclusive, 3 timed out, and 130 interrupted.

## Consequences

Uncertain execution can be investigated without another POST. Observations are live, sequential per-job reads rather than an atomic cross-job snapshot. Missing receipts after pruning remain inconclusive; plans do not extend retention. No tests are added or run under the user's standing instruction.
