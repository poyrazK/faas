# ADR: Node processing recovery inspection

- Status: Accepted
- Date: 2026-10-17

## Context

Application-owned Node processing leases and coordinated preparation fence final
business writes. Operators still need to distinguish retained receipt evidence
from active, expired, absent or conflicting claims after busy or interrupted
jobs. Platform delivery health and local callback-intent health cover different
state and cannot inspect application-database processing claims.

## Decision

Add `inspectEventConsumerProcessing(pool, input, { signal })` for one validated
original consumer request. Use one parameterized SELECT, MVCC statement snapshot
and materialized database clock for indexed receipt and processing-row lookups.
Compare full stored receipt identity/input and claim input digest inside SQL.
Return only presence/equality evidence and bounded UTC timestamps; never select
claim tokens, response bodies or rejected identity/digest values.

Return frozen primitive metadata with receipt and claim evidence plus a combined
state. Any conflict takes precedence, then matching receipt, matching claim and
absence. Matching claims expose expiry and nonnegative whole remaining
milliseconds rounded down. Preserve PostgreSQL microseconds and compare expiry
to the captured database clock without client-clock or Date rounding. Invalid or
unrepresentable evidence fails through the sanitized processing-store error path.

The query takes no processing/advisory lock and makes no mutation. Matching
receipt evidence does not validate saved response bytes; actual completion still
uses the existing receipt protocol. Inspection neither grants a capability nor
proves rollback, process termination or future admission. A concurrent commit,
renewal or takeover can invalidate the observation immediately.

## Consequences

Applications authenticate diagnostic access, supply trusted scope and protected
original bytes, install both schemas and bound driver calls. The inspector
performs no cross-consumer enumeration, platform RPC, automatic disposition,
claim release or retry scheduling. The existing claim and completion fences
continue to decide recovery under exclusive durable-job ownership.

The protected-job example adds an observation-only path for manual version 3 and
coordinated version 4 jobs, without requiring acknowledgment-token validation or
changing their processing modes. No platform schema, route, migration, DDL or
registry update is needed. See the [inspection guide](../event-consumer-processing-inspection.md).

## Validation

Use Node build/generation, package/declaration inclusion and static source,
schema and documentation checks. No tests, assertion changes or local test runs
are included. Hosted acceptance remains required for snapshot races, timestamp
boundaries, conflict precedence, cancellation and metadata-only reads before release.
