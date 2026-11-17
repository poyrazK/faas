# ADR-20261110180000: Warm snapshots are opt-in

- Status: Accepted (owner decision 2026-11-10)
- Date: 2026-11-10
- Amends: ADR-071/074 (warm-snapshot tier; Pro and Scale on by default)
- Related: ADR-159 (immutable captures, reused without recapture),
  snapshot app-compatibility review 2026-11-09

## Context

Pro and Scale apps got `warm_snapshot_enabled = true` at creation. A warm
snapshot is taken at the first park after an instance has served the
request and ready-time gates (5 requests, 2 s), and ADR-159 then reuses it,
without recapture, for every wake and every instance until a new deploy or
invalidation. It therefore carries the memory and `/tmp` (guest RAM) that
those first requests left: in-memory sessions, caches, rate-limit counters,
per-user data. Every woken instance starts from that state, and state
written after the capture is lost at each park. Most apps assume neither,
and few customers know the setting exists.

The init snapshot, taken right after readiness and before any request,
carries no request-derived state.

## Decision

1. New apps default to `warm_snapshot_enabled = false` on every plan
   (`Limits.WarmSnapshotNewAppDefault`, read by `CreateApp`). The plan gate
   (`WarmSnapshotEnabled` / `Plan.WarmSnapshotAllowed`) is unchanged, so Pro
   and Scale apps opt in with `gregale app update --warm-snapshot`, the
   create request, or the API.
2. Existing apps keep their stored setting. No data migration.
3. The docs say what a warm snapshot carries and when to opt in.

## Consequences

- New Pro and Scale apps wake from the init snapshot: no request-derived
  state is restored, at the cost of framework warm-up on the first request
  after each wake.
- Pro and Scale parked footprint grows more slowly (no second snapshot per
  app unless opted in).
- Existing opted-in apps behave as before until their owners turn it off.
