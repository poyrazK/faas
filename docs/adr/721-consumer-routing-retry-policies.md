# ADR-721: Consumer routing retry policies

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Store optional application subscription routing retry policies and capture them with publication and backfill recipients.
- **Why:** Consumers have different recovery budgets, and synchronized retry schedules amplify an outage.

## Policy and compatibility

Policies bound attempts (1–100), initial and maximum exponential backoff (whole
milliseconds, up to one hour), and an optional duration budget (up to seven
days). The maximum backoff must be at least the initial delay. Zero duration
means attempts alone bound retries. Full jitter chooses a delay between one
millisecond and the capped delay; its hash includes receipt, subscription,
replay generation, and attempt so it is reproducible across restarts.

Omitted policies and old receipt snapshots retain 12 attempts, a five-second
initial delay, a five-minute cap, no duration bound, and no jitter. Manifest
retry blocks and CLI configuration enable jitter by default. API replacement
uses explicit values (omitted jitter is false). Defaults are not retroactively
read from live subscription settings.

This policy governs routing before invocation admission. Invocation execution
retry policies, workflow execution, ordering, and deterministic invocation
identity keep their existing contracts. Workflow and object notification
recipients have no application subscription policy and use legacy routing
settings. Pre-snapshot dynamic receipts retain legacy retry behavior.

## Duration ledger

Each routing generation persists `retry_spent_ms` and `retry_generation` in
recipient progress. The budget charges routing attempt time when that attempt
fails, and reserves each scheduled backoff. Another retry is scheduled only
when its full delay fits within the remaining duration budget. A final attempt
can finish even if its operation takes longer than the remaining budget; a
successful admission is retained. This is an active retry budget, not an
acceptance-to-delivery deadline. Time waiting for pause, pacing, capacity,
ordering, lease recovery, or worker availability adds no budget cost. Lost
leases still consume the existing claim-based attempt budget.

Capacity and subscription control outcomes preserve the ledger. Replay starts
a fresh attempt and duration budget under the original captured policy. API
reset or later deployment changes only future publications and newly created
backfill jobs. Existing backfill jobs retain their job creation snapshot.

Terminal progress and retained attempt history expose `retry_stop_reason`:
`non_retryable`, `max_attempts`, or `max_duration`. `failure_code` and `retryable`
continue describing the underlying routing error; exhausting retries does not
prevent an explicit operator recovery. Receipt diagnostics also expose the
captured policy and spent duration budget. History byte accounting includes
stop reason text under the existing retention limits.

## Intent updates

The optional policy is configuration on the account/app-owned subscription.
GET, PUT, and DELETE use the existing read/deploy scopes and MFA boundary.
Manifest reconciliation is authoritative: omitting a retry block restores
legacy settings. Deployment compensation restores previous policies only if
the staged value still matches, avoiding overwriting a concurrent operator
update. Configuration schema registration includes the new field; operational
receipt history is excluded from environment cloning.

Whole-receipt fallback scheduling uses the earliest pending recipient due time;
independent routing uses each recipient's durable next attempt. Both paths use
one policy calculation and exclude capacity/control waits from attempt budgets.
