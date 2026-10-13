# ADR 1042: Automatic consumer-group retry policies

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Persist one replaceable retry policy per group. GET/PUT the account
`/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy`
route with app ownership and MFA; PUT requires admin scope. Worker credentials
retain their existing retry permission and cannot edit policies.

Default disabled preserves server settlement behavior (omitted delay means zero).
Require explicit `enabled`, `base_delay_seconds`, `max_delay_seconds` and `jitter`
fields in PUT. Delays range from 1 to 300 seconds, with base no greater than cap.
An optional list contains at most 32 unique exact case-sensitive printable ASCII
permanent failure codes, each 1–64 bytes. All other failure codes remain retryable;
empty failure codes normalize to `handler_failed` before policy evaluation.

On a reported retry failure, first check enabled permanent codes: matching work
moves straight to dead, using existing atomic dead-letter webhooks and retention
clocks. Otherwise, omitted delay uses `min(cap, base * 2^(attempt-1))`. When jitter
is enabled, choose uniformly between floor(delay/2) and delay inclusive, in integer
seconds, with cryptographic randomness. Small delays can select zero. Explicit
`retry_after_seconds`, including zero, bypasses backoff but never overrides permanent
classification or the existing attempt limit. Disabled policies ignore permanent
codes and preserve immediate omitted-delay retries. Lease expiry/reclamation keeps
its existing behavior: no worker-reported failure is available to classify.

Policy edits and settlement serialize under the group row lock. Edits affect only
future settlements and do not reschedule ready work, reset attempts, change leases,
release ordering predecessors or bypass pause/drain. Ordered delayed work keeps
blocking successors under the existing ordering rules. Randomness failures abort
settlement, leaving the current lease unchanged. Existing dead-letter/replay and
fencing behavior remains intact.

Settlement returns `available_at` for ready retries. Policy reads and the inspector
show the earliest future `available_at` of ready snapshots as nullable `next_retry_at`.
It is an observation, not a delivery promise: ordering, pauses, capacity and active
predecessors can delay claims further. Ready work already due is not a delayed retry.

## SDK and rollout

Generated Node/Python account clients expose policy configuration and new response
fields. Node `consumeRealtimeGroup` and the scoped worker helper share
`useGroupRetryPolicy: true` to omit their default one-second retry delay. Explicit
`retryAfterSeconds` wins, including zero. Default false preserves helper behavior.
Other clients opt in by omitting the settlement delay. Go state settlement uses an
optional pointer to distinguish omission from zero.

Apply migration `20261012102700497_realtime_retry_policy.sql` before serving the new
binary. Upgrade all replicas before enabling policies; older replicas do not honor
them. Rollback removes configuration but preserves already scheduled timestamps.
Go/Node builds and static contract checks are required; runtime tests, database
migration execution and inspector rendering remain deferred at user request.
