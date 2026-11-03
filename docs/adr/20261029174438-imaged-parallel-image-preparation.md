# ADR-20261029174438 · imaged prepares more than one deployment at a time

- **Status:** Proposed
- **Date:** 2026-10-29
- **Amends:** imaged's main loop (`pkg/imaged/loop.go`). Image preparation
  itself, its checkpoints and its fences are unchanged, and so is which
  component owns what.
- **Switch:** imaged `FAAS_IMAGED_PARALLEL_PREP`, default off.
- **Limit:** `api.ImagedImagePrepWorkers` (2).

## Context

imaged's loop prepared each deployment inline. It takes a `snapshot_boot`
from LISTEN and the two-second recovery pass, then:

- assembles the app layer,
- runs the secret, abuse and Grype scans,
- publishes the layer,
- hands off to the snapshot.

Until that preparation returned, the loop handled nothing else, so on a node
every deployment waited for the one ahead of it.

Production, 3 days to 2026-10-27:

- Image preparation: layer p50/p90 38/52 s, scan 82/147 s (Grype alone
  67/179 s).
- Builds that hit the cache finished in about 5 s and then waited for imaged:
  p90 174 s, max 576 s, 57 minutes in total.

Recent changes move work earlier, so more deployments reach imaged sooner:

- ADR-20261028102540 stops rescanning unchanged bases.
- ADR-20261029171525 hands the image over before the dependency-cache export.

The handler is already safe to call concurrently. The durable replay worker
(`db.RunNotificationOutboxForNode`) calls it beside the loop. Each preparation
holds the deployment's Postgres activation lock and fences its checkpoints.
Shared staging has its own locks: `runtimeBaseMu`, and `runtimeScanActive` for
runtime scans.

## Decision

With the switch on, the loop hands image preparation to a bounded worker set
of `ImagedImagePrepWorkers` slots:

- **Routed to workers:** a `snapshot_boot` this node owns, and each item of a
  recovery pass. A worker runs the same durable claim path
  (`dispatchNotification`) or the same `handleSnapshotBoot`.
- **Kept on the loop:** every other notification, and a `snapshot_boot` that
  is undecodable or belongs to a sibling node. These still run inline.
- **No duplicates:** a deployment already preparing in this process is not
  started again. A duplicate notification stays in the outbox for replay, and
  the next recovery pass re-reads the row.
- **Backpressure:** while every slot is busy, the loop waits for one, as it
  waited for inline work before.
- **Shutdown:** `Run` returns only after every started preparation has
  returned.

Without the switch, `newImagePrepWorkers` returns nil and the loop prepares
inline exactly as before.

**Resources.** The workers run inside imaged's unit (`MemoryMax=4G`). Two
preparations can mean two Grype processes and two staged layers on the node
disk at once. The limit stays at 2 until production shows the headroom.

**Metrics.** With the switch on, a recovery pass ends when its last item is
handed to a worker, not when the work finishes. The
`imaged_build_handoff_recovery_*` timestamps then show dispatch. Per-item work
outcomes (`imaged_build_handoff_recovery_work_total`) are still recorded when
each preparation returns.

## Rollout

1. Release imaged everywhere. Nothing changes yet.
2. On one compute node, read
   `/sys/fs/cgroup/system.slice/faas-imaged.service/memory.peak` and
   `memory.events` to see the peak of one preparation at a time. Then turn the
   switch on with a systemd drop-in, deploy two apps at once, and check:
   - both reach `snapshot_prepare` without waiting for each other,
   - the unit's `memory.peak` stays under 4 GiB with no `oom_kill`,
   - the imaged stage latency compares well with the other node.
3. If it holds, make the switch the default in a follow-up.

## Consequences

- Expected: a deployment's time in imaged no longer includes the
  preparations queued ahead of it, up to the worker count.
- Higher peak memory and temporary disk use for imaged on a busy node,
  bounded by the worker count.
- A `deployment_changed` notification can now be handled while the same
  deployment's preparation runs. Before, that could already happen through the
  replay worker, and the activation lock and status checks cover it.
