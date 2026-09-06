# ADR-160 — Cache successful empty edge-rule lookups

- **Status:** accepted
- **Date:** 2026-09-06
- **Amends:** ADR-091 gateway rule-cache behavior

## Context

The gateway consults one compiled host entry for all edge-rule kinds. The
cache discarded entries with no rules, so a normal host without custom rules
repeated its database lookup for each rule kind on every request. The old
empty-entry check also omitted throttle, budget and response-cache rules.
Live timing showed substantial rule-processing delay during bursts.

## Decision

Cache a successfully loaded empty host entry in the existing bounded LRU for
30 seconds. A nil result or failed database read is not a successful empty
result and must not be cached. Every kind, including route, treats a present
empty slice as a cache hit. Entries containing any supported rule kind retain
the existing positive-entry lifetime.

The existing edge-rule change notification invalidates positive and negative
entries immediately. A generation counter prevents a read started before an
invalidation from repopulating the cache afterward. The negative TTL bounds
staleness if a notification is missed; hits do not extend it. Cache capacity,
tenant ownership checks, rule order and matching behavior stay unchanged.

## Consequences and validation

No-rule hosts normally require one database read per cache lifetime instead
of one per rule kind per request. Empty entries share the existing LRU budget.
Concurrent first misses can still perform duplicate reads; this change does
not introduce background work or new request cancellation semantics.

Tests cover repeated empty-host requests, new rules after invalidation,
expiration, failed reads, invalidation during a read, and hosts containing only
the later rule kinds. Live latency is measured separately; removing these
reads does not alone prove the full-wake p95 target.
