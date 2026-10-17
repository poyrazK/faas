# ADR-1399 · Shared rate limits for application outbox claim reservations

Date: 2026-10-15
Status: Accepted

## Context

Worker concurrency and retry backoff do not bound aggregate publishing pressure
across processes. Applications need durable app-scoped pacing without spending
attempts when capacity is unavailable or confusing throttling with stalled work.

## Decision

Store a versioned token bucket and an independent audited command namespace in
application-owned Postgres tables. Use database-clock integer millisecond credit,
a configurable token interval and burst, and a row lock through claim commit.
Debit one token atomically with the lease and attempt increment; do not hold the
bucket lock through HTTP. Rollback, empty polls and blocked maintenance do not
debit; committed reservations are never refunded, including uncertain commits.

Use the existing publishing scope advisory lock to fence configuration changes
against claims. Require revision guards and replayable UUID commands, committing
configuration, history sequence and typed receipt atomically. Preserve fractional
tokens across enabled configuration changes; enabling from disabled grants the
configured burst. Retain the scope row independently of publication pruning.

Expose capacity wait evidence, bounded command history, worker callbacks and
snapshot health with ThrottledDue. Suppress due/stall confirmation while throttled
without falsely resolving incidents. Pause takes priority. Existing deadlines,
policies and acceptance/ordering contracts remain unchanged.

Use explicit opt-in application DDL and the existing outbox static SQLC validation
exception. This is neither a platform migration nor a platform plan quota.

## Consequences

All supported workers sharing the database and scope coordinate capacity. Older
workers ignore the bucket and require coordinated replacement. Applications own
schema installation, access policy and retention. Limits apply to claim
reservations, so committed HTTP can overlap or bunch and crashes can waste tokens.
Clock rollback fails closed; no control extends the retry horizon. Capacity is
not a concurrency bound or proof of consumer completion.

See [configuration and recovery behavior](../event-outbox-rate-limit.md).
