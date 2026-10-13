# ADR 1036: Consumer-group drain mode

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Persist `draining` alongside consumer-group pause state, constrained to imply a
non-null pause timestamp. Account-admin/MFA or an explicit scoped-worker `drain`
grant starts drain under the group lock. New claims stop through existing pause
behavior. Issued leases may continue renewing and settling. Return `draining`,
`drained`, `active_leases` and `paused_at` from initiation; expose `draining` and
`drained` in group status and the inspector. Drain is idempotent and remains
active after completion. Resume clears drain and pause; ordinary pause cancels
drain while keeping claims paused. Existing credentials gain no drain permission.

Compute drained as drain mode active with zero unexpired leased snapshots. Ready,
delayed, dead and unassigned messages remain untouched for resume. Lease clocks
continue, so expiration can produce drained status before a crashed or uncooperative
handler stops external effects. This is a lease-ownership observation, not a
processing receipt or proof of process termination. Deployment tooling must stop
old workers and wait for handlers to exit; effects still need idempotency/fencing.
The initiating state and later observations can change if another operator pauses
or resumes the group. No fleet-wide deployment orchestration is implied.

Add Node account/scoped helpers to initiate drain and poll status with a default
30-second timeout covering all requests and waits, plus caller cancellation.
Scoped helpers require drain and status grants. Do not automatically resume on
success, timeout, cancellation or network errors. Detect a pause/resume observed
before completion and report cancellation. Active lease renewal can prolong drain
indefinitely, so callers choose a suitable timeout. Timeout stops observation,
not the persistent drain operation. Retention continues during drain.

## Deployment and validation

Apply `20261012102700484_realtime_consumer_group_drain.sql` after the pause and
worker-credential migrations before activation. Allow twelve worker operations.
Downgrade rejects incompatible drain grants/usage and retains paused state. Go/Node
builds and static generated Node/Python specification checks are the validation
scope. Runtime, migration and UI checks remain deferred by request.
