# ADR: Node acknowledgment intent recovery health

- Status: Accepted
- Date: 2026-10-17

## Context

Node durable acknowledgment intents recover callbacks independently of ingestion
and business processing. Operators need to see unfinished callback backlog when a
recovery host stops, without exposing callback capabilities or claiming work.
The Go SDK already defines bounded local recovery health semantics.

## Decision

Add a Node health read using the same eligibility, stall and coverage definitions
as Go. Sample only retained pending/unconfirmed intents by owner scope, using
one SELECT, snapshot and materialized database clock. Limit the sample and read
one extra row to detect truncation. Classify live leases separately, and begin
stall age only after creation, retry schedule and claim expiry all permit work.
Expose invalid timestamp metadata and completeness with the counts and ages.

Use bounded PostgreSQL integer aggregates and validate the returned metadata before
publishing an immutable summary. Include normalized owner scope and the database
observation time, and copy no unrecognized fields. Share the existing timestamp
validation and sanitized intent storage errors. Keep acquisition/query timeouts,
observation scheduling and metrics/notification transport application-owned.
Cancellation remains cooperative and never releases an active driver query early.

## Consequences

No schema, API, claim or callback changes are needed. No polling or Node alert
state is installed. Partial samples and invalid metadata cannot establish healthy
recovery. Coverage describes only retained unfinished intents; it proves neither
business processing nor platform delivery, and adds no ordering or deduplication
guarantee. This read does not decode numeric replay-generation fences and can
inspect shared Go/Node stores beyond the Node callback worker's safe-integer range.

See the [Node summary guide](../event-acknowledgment-intent-health.md#node-summary).

## Validation

Use SDK generation/build, contract parity, package/export inclusion and static
documentation/source checks. No new tests, assertion changes or local test runs
are part of this increment. Database/runtime behavior requires hosted acceptance
before release.
