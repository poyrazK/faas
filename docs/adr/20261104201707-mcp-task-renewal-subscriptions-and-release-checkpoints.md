# ADR-20261104201707: MCP Task renewal, subscriptions and release checkpoints

## Status

Accepted; extends ADR-20261104195018 and ADR-20261104143950.

## Context

Node, Go and Python heartbeat queries could evaluate expiry before waiting for
a row lock and then renew an already expired lease. Node removed expired Task
IDs from subscriptions without releasing their slots or finishing Task-only
streams. The generic Node release checkpoint recorded only the original worker
IDs, so using it for a later release could leave the intervening generation
running while reporting success.

## Decision

1. Heartbeat renewal uses the existing lease-row lock and a materialized clock
   reading taken after acquisition. Lease and Task TTL must both remain live at
   that instant. The namespace execution lock continues to serialize capacity.
   Local monotonic execution deadlines remain independent of database requests.
2. When no accessible Tasks remain, Node removes the Task watcher, releases its
   subscription slot and closes its database listener when no other watchers
   remain. The HTTP stream completes when it has no other filters. Resource and
   catalog subscriptions on a combined stream continue unchanged.
3. Persist version 1 release checkpoints with a release ID, original worker IDs,
   start time, replacement IDs and completion. Save replacement IDs before
   readiness or drain. Retrying reuses them, and a completed retry verifies
   readiness without invoking deployment hooks. A new ID may start only after
   the prior checkpoint completes, capturing the current workers rather than
   reusing an obsolete original generation. Start hooks receive the stable ID
   for idempotency when a process dies before its response can be recorded.
4. The generic CLI requires `releaseId` when `checkpointPath` is set. Native
   adapters derive it from their existing source fingerprint. Reject malformed,
   future-version or legacy checkpoints before migration or deployment hooks;
   unversioned state cannot establish release identity or recorded replacements.
   Preserve it for recovery using the original tools or explicit reconciliation.

No platform database schema, quota or Task wire-format changes. Completion is
reported only after saving the completion checkpoint succeeds.

## Validation

PostgreSQL tests hold a row lock through both lease and Task expiry in all three
runtimes and verify renewal fails and expired leases remain recoverable. Node
tests exhaust all 64 Task subscription slots, expire their Tasks and verify slot
reuse, single cleanup and correct Task-only versus combined SSE behavior. Release
tests cover interrupted drain, completion-save failure, completed retries, a
second rollout through the same checkpoint file, pending-ID conflicts, invalid
state and stale replacements; the CLI test exercises persisted checkpoints and
actual child hooks with migration credentials removed.
