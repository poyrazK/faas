# ADR-1174: Configurable subscription delivery capacity

Status: Accepted
Date: 2026-10-13
Related: ADR-614, ADR-719, ADR-962

## Decision

Existing plan ceilings, durable capacity waits, least-recently-served routing
preferences and capacity health metrics already provide backpressure. Add a lower
operator-configured ceiling for one application subscription rather than duplicate
those mechanisms. Define live delivery exactly as ADR-614: trusted delivery slots
whose invocations are pending or dispatching, including replay descendants. This
is not a new limit on ingestion, routing leases or handler running concurrency.

Add PUT delivery-control/capacity with required max_pending_deliveries, an explicit
non-null integer. Zero restores the current plan default; positive values must be
no greater than the current plan consumer ceiling. Negative, omitted, null and
oversized values fail validation. Use existing owned app/subscription lookup,
deploy-write/admin scope, MFA, no-store response and bounded control timeout.
Expose Go/Node/Python SDK calls and CLI subscription-capacity --max-pending N --yes.

Persist a nonnegative max_pending_deliveries column, default zero, on the existing
operational subscription controls table. Updating it preserves pause, drain-rate,
window and circuit state. Retained controls survive subscription removal, as with
existing controls. Existing subscriptions automatically retain plan defaults.
Changes do not mutate accepted event snapshots or deployment configuration.

The effective ceiling is min(configured positive value, current plan ceiling),
otherwise the plan ceiling. A plan downgrade clamps enforcement without discarding
the saved setting. Raising the plan later can restore that saved ceiling; zero
tracks the plan. Lowering below current usage does not evict/cancel work: new
admission waits until usage is strictly below the effective limit. App/account
plan ceilings remain independent gates and do not reserve capacity for siblings.

Serialize capacity configuration changes under the existing account capacity
mutex before app/subscription locks. Routing admission reads the live override
under that mutex. PostgreSQL replay triggers apply the same effective ceiling to
new children and terminal-to-live retries, and MemStore mirrors that policy.
Live-to-live retries keep their slot. Preserve keep-latest subtraction and the
existing duplicate/cancellation exceptions; all mutation/checkpoint atomicity and
lease guards remain in force. Execution recovery preflight reports the same limit.

Delivery-control status reports configured max_pending_deliveries, effective
max_pending_deliveries and live_deliveries alongside existing routing backlog
counts. These are live observations, not reservations; live delivery counts differ
from pending/processing routing recipients. Legacy work without trusted slots is
excluded as in ADR-614. Existing capacity_consumer waits, health counts, watch
metrics and five-second polling apply to configured ceilings without new labels.

A capacity wait remains pending and spends no routing-failure budget. Age limits
can still expire waiting work. Replays retain deterministic delivery identities and
share the ceiling. Least-recently-served eligible account/consumer selection and
captured ordering lanes remain unchanged; neither strict round-robin nor global
FIFO is promised. Handler execution remains at least once and consumers must
retain their normal application deduplication behavior.

## Rollout and validation

Apply the additive migration before upgrading readers/writers. Upgrade all routing
and replay writers before relying on custom ceilings; older routing writers know
only plan limits. The migration replaces the replay guard and downgrade restores
the earlier guard before dropping the new column. Downgrade loses saved overrides
and returns to plan defaults. No migration is executed as part of implementation.

The clone schema registry needs the new operational column, and SDK coverage needs
one new PUT route mapping. Prepare these separately under the user's registry
restriction and apply only with explicit approval. Builds, SDK generation/source
checks, SQLC parity and static review provide implementation validation; no local
tests are added or run. Concurrency and database behavior remain for CI qualification.
