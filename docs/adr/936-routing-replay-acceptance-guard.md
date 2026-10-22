# ADR-936: Atomic acceptance guard for routing replay

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-935

## Context

A saved recovery action can outlive event receipt retention. Reaccepting the
same source/event identity must not allow a reviewed routing replay to mutate
its replacement. A receipt GET guard alone cannot protect a later POST.

## Decision

Add optional `expected_accepted_at` to selective fanout replay. Guarded replay
locks the authorized acceptance-snapshot receipt, compares its exact created_at
instant, and requeues only the selected failed recipient in the same transaction.
Mismatch returns 409 before age checks, replay history or delivery mutation.
Unavailable authorized snapshot evidence returns 404. The memory store uses its
existing mutex for equivalent comparison and mutation. Existing unguarded replay
and backfill retry paths retain their behavior; guarded replay excludes backfill.

Original recipient routing recovery offers include the exact acceptance instant.
Commit recovery apply requires that fresh offer's guard to match the saved
publication before submitting its body. Missing guards remain skipped, which
also supports older servers. Explicit expiry overrides remain outside automatic
Commit recovery apply. Scoped authorization and existing replay eligibility are
preserved. Receipt identity locking prevents replacement between comparison and
commit; the guard is not a per-generation optimistic concurrency token.

Routing idempotency keys include event source/ID, subscriber and guarded
acceptance and reviewed observation time in addition to action kind/URL so independent consumers and events do
not collide on their shared replay endpoint. Existing execution replay keys keep
their contract. Ambiguous writes still stop apply for inspection, without retry.

## Consequences

The report/plan/apply path supports original routing failures while keeping each
consumer independent. Historical plans without a guard must be refreshed and
reviewed because the new guarded offer changes their action body. Atomic
PostgreSQL behavior and production failure scenarios still require qualification;
local builds and static checks do not establish that qualification.

ADR-937 adds a recipient generation guard and replaces observation time in routing idempotency keys.
