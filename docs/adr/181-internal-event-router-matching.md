# ADR-181: Internal event-router subscription matching

- Status: Accepted
- Date: 2026-09-17
- Issue: #1278, Workstream B

## Context

ADR-180 established a tenant-scoped publish envelope, but a durable event
ledger is not useful until a router can decide which subscriptions receive an
event. Matching must be deterministic in memory and reusable by a later
Postgres-backed selector. It must also make tenant isolation part of the
matching contract rather than relying on every caller to remember a separate
account predicate.

## Decision

`pkg/events.Subscription` is the transport-independent matching contract.
Each subscription names an `account_id`, `source`, `type`, and optional JSON
`filter`.

- `account_id` must equal the envelope account before any content is exposed.
- `source` and `type` support exact values and edge wildcards (`billing.*`,
  `*.paid`, or `*invoice*`). Interior wildcards are rejected.
- Filters are conjunctive JSON objects. Nested keys address envelope fields
  such as `data.amount`; scalar values are exact matches.
- `$eq`, `$prefix`, `$suffix`, `$gt`, `$gte`, `$lt`, and `$lte` are supported.
  Numeric comparisons use rational arithmetic so `100` and `100.0` compare
  equal without float rounding.
- Invalid patterns and filters return errors; a valid non-match returns
  `(false, nil)`.

The matcher is deliberately storage-agnostic. The next routing increment can
use the same contract for an in-memory index and a Postgres candidate query,
then apply `Subscription.Match` as the authoritative final predicate.

## Consequences

The publish ingress and manifest path now feed the schedd router. Schedd uses
an account-scoped, source/type candidate query with keyset pagination and a
bounded batch, then applies `Subscription.Match` as the authoritative filter
check before enqueueing deterministic async invocation ids. Terminal delivery
rows retain the `event_subscription` origin in the unified DLQ and use the
existing replay path. This keeps a malformed filter from silently broadening
a tenant's fan-out while bounding scheduler work for large subscription sets.
