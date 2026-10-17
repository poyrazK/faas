# ADR-1406 · Full retained pending-recipient monitoring

Date: 2026-10-15
Status: Accepted

## Context

The rate preview intentionally samples at most 1,000 pending recipients. Its
completeness requirement makes growth and approaching-deadline alerts unknown
above that bound, exactly when operators may need them most. Removing the model's
bound would increase memory use and imply unsupported admission predictions.

## Decision

Add a read-only subscription pending-summary operation that aggregates all
retained captured pending application recipients in one database snapshot. Reuse
the existing routing projection, effective-deadline function, ownership target and
control reads. Return exact bounded counts, earliest effective deadline, observation
and coverage metadata, and current controls without payloads or identities.
Use a five-second context budget plus a transaction-local four-second statement
timeout. Return no partial result on timeout. Mark unusable-clock evidence incomplete.

Use full summaries for Go watch growth and approaching-deadline conditions, with
new metric identities to prevent mixing sampled historical proof and full counts.
Keep modeled deadline risk tied to the separately bounded rate preview. Preserve
sustained warning/recovery confirmation, unknown semantics, immutable contributor
summaries and the existing durable alert outbox. A failed model does not discard
an otherwise valid full summary. An unsupported summary endpoint remains unknown.

## Consequences

Summary-enabled watches make one read-only summary request per health poll, plus
a separate preview when modeled-risk monitoring is enabled. Reads reserve no
capacity and require no schema migration. Aggregation time can still exceed its
budget on very large datasets; unknown coverage is explicit rather than a
fabricated count. This is a scoped bounded-time scan, not a materialized ledger or
an unbounded response. Existing sampled-metric checkpoints require a new session
for the changed growth/deadline metric selection. Node/Python expose the typed
summary operation; this change does not add their own watch implementations.

See [coverage and operation semantics](../event-subscription-pending-summary.md).
