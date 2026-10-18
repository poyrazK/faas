# ADR: Coordinated Node event preparation

- Status: Accepted
- Date: 2026-10-17

## Context

Node processing capabilities fence short final transactions, but preparation
outside the transaction requires independent renewal of the application claim
and platform acknowledgment lease. Application code currently coordinates these
lifecycles itself, risking platform expiry during long preparation or overlapping
local renewal with the final transaction's processing-row lock.

## Decision

Add opt-in `EventAcknowledgmentWorker.runPreparedDurable`, using the existing
processing schema, receipt/intent protocol and platform endpoints. Reconcile a
saved receipt before renewal or preparation. For missing receipts, confirm the
platform capability, monitor its lease, acquire a local claim and monitor local
renewal while side-effect-free preparation runs. Renew each lease serially at one
third of its configured duration; independent loops avoid database latency
serializing the platform monitor. The first failure signals cooperative stop.

Require an explicit preparation timeout covering startup, claim, preparation,
joining local renewal and a final local refresh. Check a monotonic budget at phase
boundaries as well as setting an abort timer. Limit timer-backed durations to
positive whole milliseconds representable by Node timers; local duration must
allow a positive one-third interval. Keep the full operation under the existing
worker guard and optional parent signal/deadline.

Stop/join local renewal before the short row-locking completion transaction,
then refresh its lease for a completion margin. Continue the single platform
monitor through the existing atomic business-write, receipt, claim-removal and
intent commit. Share intent staging between ordinary and coordinated paths rather
than starting a second platform monitor. Receipt replay skips both callbacks.

Await active preparation and driver calls during cancellation and cleanup.
Preserve unknown claim, renewal and completion commits, and preserve confirmed
completion despite cancellation or a late renewal failure. No automatic negative
disposition, retry scheduler, claim release or receipt deletion is added.

## Consequences

Applications supply bounded, side-effect-free preparation, short transactional
business code, explicit schema installation, timeouts, durable jobs and exclusive
ownership. Helpers cannot keep a VM awake, interrupt blocked JavaScript/SQL or
provide atomicity between platform expiry and a local SQL commit. Prepared data
is process-local and must be recomputed after an admitted restart without a
receipt. External effects still require idempotency or an outbox.

The example opts new jobs into version 4 explicitly and retains earlier manual
lease jobs on version 3. No platform migration, route, compliance registry or
DDL change is needed. See the [coordinator guide](../event-acknowledgment-node-preparation.md).

## Validation

Use Node build/generation, schema-source parity, package/declaration inclusion and
static documentation checks. No new tests, assertion changes or local test runs
are included. Hosted acceptance is required before release for expiry/takeover,
renewal loss, cancellation, rollback, concurrent claims and uncertain commits.
