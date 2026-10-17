# ADR-1408 · Shared token-bucket pacing for consumer admission

Date: 2026-10-15
Status: Accepted

## Context

Subscription controls already provide durable pause and fixed one-second drain
caps. Consumers also need configurable smooth pacing and burst capacity, shared
across workers, with revision guards and replayable audit evidence.

## Decision

Extend the operational subscription delivery-control row with disabled-by-default
integer millisecond token credit, refill anchor, interval, burst and revision/history
counters. Reuse the consumer advisory lock and admission transaction. Check pause,
circuit and legacy rate limits before consuming a bucket token; debit only with
successful admission. Preserve existing deferred-attempt refunds and deadlines.

Add a separate bounded operational rate-command receipt table with 30-day retention
and 128 unexpired commands per subscription. Require UUID command IDs and explicit
revision guards. Commit applied or rejected receipt evidence with sequence allocation;
replay retained commands before checking new guards or retention capacity. Expose
configuration through existing delivery-control status plus mutation, lookup and
bounded history endpoints, covered by existing app ownership, MFA and scope checks.

Use the database clock in production. Preserve fractional credit on enabled changes
without granting a fresh burst; enabling from disabled starts full. A future anchor
fails closed. Reuse subscription_rate_limited waiting reasons in existing backlog
and consumer-health paths. Retain legacy per-second caps as independent constraints.

## Consequences

Consumers recover independently at their configured pace. Admission reservations
remain distinct from invocation execution and exact HTTP timing. Old workers must
be replaced before enforcement can be trusted. Receipt expiry ends command replay
protection; authoritative control counters survive receipt pruning. No delivery
retention or retry horizon is extended. Schema installation is a normal platform
migration, and operational clone classifications preserve existing clone behavior.

See [API, recovery behavior and rollout](../event-subscription-rate-limits.md).
