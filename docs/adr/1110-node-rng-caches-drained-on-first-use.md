# ADR-1110: Node RNG caches are drained on first use after a restore

- Status: Accepted
- Date: 2026-10-11
- Related: ADR-680 (restore userspace RNG reseed)
- Amends: ADR-680 (the Node reseed reply no longer drains caches itself)

## Context

ADR-680 makes every restored Node process reseed before the instance serves.
For Node that reply does three things: `RAND_poll()` through the N-API addon,
a reseed of the `Math.random` replacement, and a drain of Node's userspace
caches. `crypto.randomUUID` (and webcrypto's `crypto.randomUUID`, which shares
the batch) serves from a 128-entry batch and `crypto.randomInt` from a 6 KiB
buffer, so the preload calls them 128 and 1024 times. Without the drain those
calls would replay values captured in the snapshot.

The drain is the expensive part of the reply. Measured on the metal host
(`TestMetalRestoreReseedTimingBench`, node:22 rootfs, 1024 MiB guest,
2026-10-11):

| restore-time reply | resume_hook p50 | first request p50 |
|---|---|---|
| reseed off (no barrier) | 51 ms | 53 ms |
| poll + Math.random + drain (ADR-680) | 112 ms | 48 ms |
| poll + Math.random, no drain (unsafe) | 89 ms | 24 ms |

The 1,152 calls run on a heap that is still faulting back in from the
snapshot. Every Node app pays this on every restore, including the many that
never call `randomUUID` or `randomInt`.

### Rejected: empty the caches before capture

The first design drained the caches just before the capture and held the
Node main thread inside the addon until the capture ended, so the restore
could skip the drain. `TestMetalRestoreUserspaceRNGDiffers` failed: both
restores of one snapshot served the same `randomUUID`.

Node's caches cannot be emptied from JavaScript. `getBufferedUUID` refills
only when its counter is 0 and then advances it modulo 128. A 128-call drain
from position `p` crosses 0 once, refills there, and ends at `p` again. The
batch then holds entries drawn during the drain, and those entries are in the
snapshot. The `randomInt` buffer behaves the same way. ADR-680's drain is safe
only because it runs after `RAND_poll()`, so the refill it crosses draws from
the reseeded generator.

The freeze also saved little time. The first request after the restore paid
for the refill instead: resume_hook p50 88 ms, first request p50 63 ms.

## Decision

The restore marks the caches stale. The first call that reads a stale cache
drains it.

1. At load, the preload wraps `crypto.randomUUID`, `crypto.randomInt` and
   webcrypto's `Crypto.prototype.randomUUID`. Each wrapper keeps the
   original's name and length.
2. The reseed reply keeps `RAND_poll()` and the `Math.random` reseed. Instead
   of draining, it sets `stale.uuid` and `stale.int`.
3. A wrapper whose cache is stale clears the flag, drains that cache with
   the original function, then forwards the call. The drain still runs after
   `RAND_poll()`: the flag is set only by a reseed reply that already
   reseeded OpenSSL. `randomInt` drains with a range of 1. That range accepts
   every 6-byte entry and returns 0, so each call consumes exactly one entry
   and allocates no heap number.

## Consequences

- Restores no longer run the drain inside the barrier. An app that never
  calls these functions never pays the drain. An app that does pays it once,
  on its first call after each restore. On the metal bench that is still
  cheaper than ADR-680's drain in the barrier. The table gives p50 medians
  over three interleaved 12-restore runs on 2026-10-11, as resume_hook /
  first request:

  | app | ADR-680 drain | first-use drain | saved per wake |
  |---|---|---|---|
  | node (randomUUID + randomInt every request) | 137 / 47 ms | 102 / 30 ms | ~52 ms |
  | node-plain (no crypto calls) | 109 / 41 ms | 101 / 26 ms | ~23 ms |

  Draining with `randomInt(0, 1)` in the barrier, without the wrappers,
  measured 107 / 49 ms for node. The wrappers are what move the cost.
- Code that captured the original function before the preload ran bypasses
  the wrapper. The preload runs first via `--require`, so only Node's own
  internals can hold such a reference. Today these are webcrypto, whose
  prototype method is wrapped, and blob URL IDs (`URL.createObjectURL`,
  `internal/url`). Blob URL IDs can therefore repeat across restores of one
  snapshot. They name objects inside one process and are not secrets.
  ADR-680's restore-time drain covered them.
- Coverage is otherwise unchanged from ADR-680. Worker threads have their
  own isolates and caches. An async `randomInt` fill in flight at capture has
  the same exposure as before.

## Verification

- Runtime (real node + addon): a reseed marks both caches stale. Each
  wrapper clears its own cache's flag, crypto and webcrypto `randomUUID`
  share one batch, and the wrappers keep the originals' names and lengths.
- Metal: `TestMetalRestoreUserspaceRNGDiffers` now also asserts that
  `randomInt` differs across restores of one snapshot. Its reseed-off control
  requires it to replay. `TestMetalRestoreReseedTimingBench` gains a
  `node-plain` app that never reads the caches.
