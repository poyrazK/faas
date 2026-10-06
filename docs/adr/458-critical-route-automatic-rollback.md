# ADR-458: Opt-in automatic recovery for critical route error regressions

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-122, ADR-232, ADR-454, ADR-456, ADR-457

## Context

A critical route guard stops advancement but leaves the current candidate share
serving. A failed checkout can remain exposed while deployment-wide aggregates
are healthy. Customers need an explicit policy to restore serving stable traffic.

## Decision

Add `on_regression: hold | abort` to the saved app route health guard. Omitted
replacement intent means hold, preserving existing callers and configurations.
Changing only this action increments the CAS revision and observation anchor.
Report mode remains observational. Abort applies only to independently confirmed
per-route 5xx regression in both existing closed windows. Latency-only violations,
mixed windows and sparse/unavailable evidence retain existing health holds.

The existing canary worker requests a fresh check through APID's loopback action
listener before mirror, aggregate and dwell gates. The request pins the expected
step; it contains no historical decision, route counts, policy or recovery target.
APID reads current intent and observations under the canonical lease, account,
app, candidate and sibling locks. The serving predecessor must be unique, older,
in the same environment, and outside an active canary. No new polling worker or
customer credential is introduced. Lookup, telemetry or persistence failure
holds progression. Existing aggregate protection continues independently.

Reuse the existing exact candidate/predecessor recovery transaction, extracting
its transaction body without changing ordinary, service or emergency recovery
semantics. APID checks the lease after lock waits and again before commit. Save
abort evidence, the traffic restoration, deployment audit, notification baseline,
recipient snapshot, webhook outbox and gateway cache invalidation atomically.
Failure rolls back all effects. History remains bounded by existing limits.

A committed abort history entry has `purpose: abort`, worker source, decision
status aborted, the prior candidate share and requested share zero. It captures
the actual evaluation policy and current observation anchor. Other history stays
compatible with omitted purpose. Periodic healthy, disabled and unknown checks
do not create history. Aborted candidates cannot resume through a retry.

Emit `routes.health.aborted` through the existing signed app webhook pipeline.
The metadata-only payload links the saved decision and, if present, the comparable
prior hold. It describes committed recovery; webhook receipt never authorizes a
mutation. Stage serialization and terminal deployment state deduplicate concurrent
workers and restarts. Account and platform receivers remain excluded.

Expose the opt-in with `routes health set --on-regression abort`; publish customer
intent, saved outcomes and event vocabulary in OpenAPI and Go/Node/Python SDKs.
The private recovery request/response remain off the public API and public SDKs.
All new production queries use sqlc. The additive migration follows the existing
route-health dependencies and is replay-safe; existing rows default to hold.

## Validation

Pure tests distinguish confirmed errors from latency-only and unknown evidence.
Real PostgreSQL checks cover exact restoration, scope isolation, stale stages,
configuration and anchor changes, missing/expired leases, ambiguous predecessors,
rollback after traffic/audit/history/outbox failures, concurrent callers and restart.
API checks enforce action-token isolation and fresh evaluation without response
caching. Worker checks cover early recovery and fail-closed transport behavior.
CLI/SDK checks preserve legacy defaults and explain committed abort evidence.
