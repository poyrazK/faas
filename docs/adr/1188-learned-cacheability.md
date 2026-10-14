# ADR-1188: Learned cacheability — measure whether a route is safe to cache, and for how long

- Status: Accepted
- Date: 2026-10-14
- Related: ADR-122 (edge response cache), ADR-127 (request telemetry), ADR-976 (route advisor), ADR-1187 (route awake report), ADR-124 (traffic mirroring)

## Context

On a scale-to-zero platform an edge cache hit never wakes the app, so a
cache rule is the strongest single lever on wakes, latency and cost. The
route advisor (ADR-976) and the awake report (ADR-1187) suggest cache rules
from a heuristic: anonymous, successful GET traffic with repeats inside the
proposed lifetime. Neither knows whether the response is actually the same
from one request to the next, so the customer has to guess a safe
`max-age`, and a route whose body changes on every request still gets a
cache suggestion.

The gateway already fingerprints responses for traffic mirroring
(ADR-124), and request telemetry already carries per-route, per-minute
rows to Postgres.

## Decision

**Observe.** For an anonymous GET (no Authorization, no cookie, no API
consumer or platform tenant identity, no Range, no upgrade or event
stream) on a route without a cache rule, gatewayd-internal installs a tee
that hashes the response body as it streams, with `hash/maphash` and a
random per-process seed. Nothing is buffered; hashing stops past the cache
entry size. When the response came from the app, the gateway compares the
hash with the last one it saw for the same URL (app, path, sorted query and
Accept-Encoding), held in a per-gateway LRU of
`RouteContentObserverMaxURLs` entries.

**Record only the outcome.** The request telemetry row gains
`content_outcome`, part of the collapse key and validated against a closed
set (`uncacheable`, `first`, `same_<w>`, `changed_<w>` with
`w` ∈ 60, 300, 3600 or `long`). `same_300` means the body was identical to
the previous response for its URL, which was seen between one and five
minutes earlier: a 300-second cache would have answered correctly.
`changed_300` means such a cache would have served a stale body.
`uncacheable` mirrors the edge cache's own store rules (status, Set-Cookie,
Cache-Control no-store or private, Vary, size). Bodies and hashes never
leave the gateway; an outcome apid does not know is stored as empty.

**Judge.** The advisor's route statistics count the outcomes. For each
window, cumulatively, a max-age is safe when at least
`RouteContentMinRepeats` repeats fell inside it, at least
`RouteContentMinBandRepeats` of them at that window's own spacing, and at
most `RouteContentMaxStaleShare` (1%) had changed. A cache suggestion
is withheld when more than `RouteContentUnsafeStaleShare` of repeats under a
minute apart changed, or more than `RouteContentMaxUncacheableShare` of
observed responses could not be stored. Otherwise it carries the evidence
and the measured safe max-age, with a caution when the requested max-age is
longer, or a hint when a longer cache would be safe.

## Consequences

- Cache suggestions become evidence-backed, and routes whose content
  changes too fast stop getting them.
- The evidence is per gateway: two gateways compare independently, and a
  restart forgets the last hashes. Low-traffic URLs may never repeat within
  a window, so a safe max-age can stay unmeasured.
- The tee adds a streaming hash to anonymous GET responses on routes without
  a cache rule; it never holds the body.
- Telemetry rows split by outcome for observed requests, a bounded increase
  in collapsed rows.

## Not decided here

Validating the max-age of existing cache rules, learning per-URL
differences inside one route template, and feeding the evidence into the
awake report's edge-cache suggestion.
