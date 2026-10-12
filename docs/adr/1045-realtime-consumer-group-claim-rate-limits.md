# ADR 1045: Shared consumer-group claim rate limits

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Add account/MFA GET/PUT `claim-rate` beneath each consumer-group route. Read uses
read scope; PUT uses admin scope and requires both `rate_per_second` and `burst`.
Default rate zero disables admission throttling. Enabled rates are 1–1000 new
leases per second with burst 1–32. Configuration applies to all account/scoped
workers and replicas for the immutable group UUID. Worker credentials cannot edit
policies. Policy reads expose cumulative decimal-string `throttled_claim_requests`
and an observed `retry_after_milliseconds`, also shown in the selected inspector.

Persist a GCRA theoretical arrival time on the group. Under the existing group
row lock, admission is allowed when database time reaches `tat - (burst-1)*spacing`.
After a successful lease, set `tat = max(now,tat)+spacing`. Spacing is the reciprocal
rate rounded up to whole PostgreSQL microseconds, preventing precision rounding
from increasing the configured rate. Idle time replenishes at most one burst.
The shared lock serializes admissions across processes; no in-memory bucket or
replica-local allowance exists. Each successful newly issued lease consumes one
credit, including retry/replay claims. Renewals, settlements, replay operations,
deadline expiry and unsuccessful/no-work leases consume no credit. Disabled groups
avoid additional per-claim rate reads/writes.

A batch can commit partial claims and stop on rate throttling. Return required
`rate_throttled` and `retry_after_milliseconds` fields, zero when not throttled.
Single claims with no admission return 429 problem code
`realtime_group_rate_limited`, rounded-up `Retry-After` seconds and matching
`retry_after_seconds` in the body. Other 429 causes retain their existing codes.
Long polls return immediately on throttle instead of repeatedly consuming database
attempts while waiting. Retry hints are observations, not reservations or fairness
guarantees: another worker can consume available credit first. Paused/draining
claims do not check the rate; credit replenishes up to the burst while paused.

Increment `throttled_claim_requests` once per committed batch invocation stopped
by the limiter, including partial batches. It counts decisions/polling attempts,
not withheld messages, and may increment before discovering whether work would
have been ready. It never increments merely for status reads. Transactions that
roll back undo counters, admissions, assignment and newly issued leases together.
Existing worker credential attempt budgets are separate and still apply.

Identical PUTs preserve the arrival time and counters. Changed enabled policies
retain existing debt clamped to the new burst window; disabling clears arrival
time, and enabling starts with a full burst. Counter history survives these edits.
Database wall time is authoritative; a backward clock jump can temporarily extend
throttling, while forward time replenishes only up to the burst. Group deletion and
recreation naturally allocate a new rate namespace and reset counters.

## SDK and rollout

Generated Node/Python account clients expose policy routes and batch fields.
The shared Node consumer helper recognizes only this specific single-claim 429
and waits for its hint. Batch workers process admitted claims and wait the precise
millisecond hint before asking again. Active handlers continue renewing/settling;
shutdown cancels waits. Waits are capped at one minute before observing admission
again. Single-route helpers use HTTP second precision; use batch claims for finer
rate pacing. Other errors retain their existing handling.

New policy/admission queries use generated sqlc methods and a migration-owned
group table projection. Apply migration
`20261012102700503_realtime_consumer_group_claim_rates.sql` and upgrade all replicas
before enabling a policy. Older replicas do not enforce it. Upgrade workers before
expecting automatic pacing; legacy workers may stop on a single-route 429.
Rollback removes rate enforcement and counters, leaving active leases and settlement
validity untouched. Upgrade rollout coordination remains required.

Go/Node builds, OpenAPI parity, Python static checks and sqlc regeneration parity
are used for this change. Runtime tests, migration execution and inspector rendering
remain deferred at the user's request.
