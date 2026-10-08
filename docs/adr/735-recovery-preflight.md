# ADR-735: Read-only recovery preflight

Date: 2026-10-08
Status: Accepted

## Context

Recovery health explains current waits and control history explains operator decisions. Before resuming a frozen job, operators need to know which pending items remain recoverable and whether even an optimistic drain can fit before expiry.

## Decision

Expose GET `/v1/event-recoveries/{jobID}/preflight` with existing apps-read/admin scopes and MFA, and `events recovery-preflight JOB_ID`. Inspect all pending items up to the existing 10,000-item job limit and return a bounded first-100 sample of positions and classifications. Enforce account/job/app ownership. PostgreSQL reads in a repeatable-read read-only transaction with no row locks, replay calls, reservations or writes. Memory storage reads under its mutex without invoking replay helpers or response functions that mutate job state.

Classify each item as currently eligible, waiting, likely skipped, or unknown. Routing observations check retained receipt and captured recipient, frozen failure identity, routed state, legacy whole-receipt ownership, target availability and delivery deadline. Execution observations check the frozen invocation generation/state/attempts/timestamps, retained captured/backfilled receipt membership, replay-child ledgers, absolute work/start deadlines, dead-letter ledger and target/replay validity. Failed plain/keyed tenant-bound executions observe the tenant admission gate; dead-letter in-place replay leaves its tenant gate to the existing claim path. Unresolved tenant metadata is unknown rather than eligible.

For otherwise eligible execution candidates with inherited delivery slots, observe current pending/dispatching capacity at account/app/captured-consumer scope using the same plan limits and priority as admission. Aggregate slot counts once rather than querying them for each item. Each candidate is evaluated independently against the current snapshot: reporting several items as eligible does not reserve enough slots to admit all of them together. Do not expose payloads, raw errors, resolved work keys, or tenant/consumer identifiers in samples. Report counts by reason and capacity scope, with counts partitioning the pending selection.

Return the current rate, remaining lifetime, optimistic minimum delay until the last pending item can spend a permit, earliest drain time, and whether that instant strictly precedes expiry. Include all pending items because skips also consume permits. Account for spent permits in the current fixed one-second window and the scheduled next attempt. Immediate bursts can finish within the same window, so a one-item or fully available first-window batch has a zero-second rate-only minimum. If paused, explicitly assume immediate resume while retaining existing waits. Terminal or expired jobs have active=false and fits_before_expiry=false.

This is an observation and rate-only lower bound, not an admission promise or execution completion forecast. It excludes future capacity contention, target changes, receipt retention, handler runtime, worker latency, transaction overhead and further pauses. A true fits_before_expiry does not guarantee success; a false result means the current optimistic schedule already misses expiry. Eligible item counts do not override job state or expiry.

Use existing request timeouts, selection bounds and sample limits. Reads do not alter audits, progress, pacing windows, wait observations, expiry processing, lifecycle notifications or replay identity. All SQL is sqlc-generated. No tables, columns or migration are added.

## Consequences

Operators can choose whether to resume, adjust pacing or cancel using a concrete frozen-selection report. API and Go/Node/Python SDKs expose the same result. Terminal jobs return their empty pending selection without pretending a new recovery is authorized. Unknown cases remain explicit, and conditions can change immediately after the read.
