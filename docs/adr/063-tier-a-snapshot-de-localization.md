# ADR-063 · Tier A: snapshot de-localization (residual local-cache semantics)

- **Status:** **Accepted** (revised 2026-08-26; issue #1054 follow-on)
- **Date:** 2026-08-01
- **Issue:** Phase 2 / Gate A — record the snapshot locality decision
  taken as a side effect of Tier 1's OCI storage rollout
- **Decision:** Snapshots are **node-local caches** backed by a
  shared OCI registry (ADR-054). The node-local `snapshothipd`
  worker asynchronously prepositions both restore blobs on active
  compute nodes in the snapshot's region. Cross-node wake still
  pulls on demand when no ready replica exists; cold boot remains
  the correctness fallback (ADR-005).

## Context

PR #457 (Tier 1) shipped OCI storage end-to-end
(ADR-054): `imaged` publishes base + per-app layers to the OCI
registry; `vmmd` reads them. The snapshot persistence path
(ADR-018 — `snapshot_written` from schedd → imaged) was untouched:
each schedd still owns its local snapshot blob (file path under
`/srv/fc/snapshots/<deployment_id>.snap`).

Phase 2 / Gate A adds a new question: when a wake lands on
schedd-A for an app whose owner is schedd-B, does schedd-A
re-snapshot locally, or pull from schedd-B's cache?

## Decision

**Shared authoritative blob plus asynchronous regional cache
prepositioning.** The snapshot is a cache; cold-boot (no snapshot)
is always legal (ADR-005).

Specifically:

- `vmmd` publishes snapshot memory and vmstate through the shared
  storage backend when `FAAS_STORAGE_BACKEND=oci`; `snap/` is no
  longer in the default OCI local-prefix list.
- `snapshot_replicas` is a durable per-(snapshot,node) queue. A global
  `snapshot_fanout_events` stream records one event per usable snapshot and
  each node has a durable cursor. The node-local `vmmd` consumes only events
  newer than its cursor, claims work with row locking, drains both blobs
  through the read-through cache, and marks the pair `ready` only after both
reads succeed. vmmd is used because the compute-only Ansible role runs vmmd on
every compute box; M9 deploys a node-local schedd beside it for ownership and
liveness.
- `snapshot_origins` records the producing node and region. New
  snapshots fan out only inside that region; legacy rows without
  origin metadata remain eligible for safe catch-up.
- A wake prefers a node with a ready local replica, while retaining
  the normal capacity and cold-boot fallback rules.
- A wake on a non-owner schedd is rejected before reaching vmmd
  (`pkg/scheddgrpc` ownership guard returns `codes.FailedPrecondition`
  on mismatch). The gateway's per-node dial cache routes the
  customer's request to the owner schedd via `apps.node_id`.
- If a replica is missing, stale, or its registry pull fails, the
  owner falls back to on-demand shared-backend restore and then
  cold-boot (`CreateColdBoot`) if restore is unavailable.
- The worker is intentionally embedded in vmmd for the first
  implementation. Its durable queue and storage interfaces are
  provider-neutral, so a future standalone systemd unit can reuse
  the same state machine without introducing peer IP/SSH coupling.
- Snapshot publication appends one fan-out event instead of making every
  worker scan the complete snapshots table each second. A newly joined node
  replays the event stream from cursor zero, while origin and node-activation
  triggers repair locality and activation races. Cursor replay is the recovery
  guard for workers that were offline during publication.

## Consequences

- Prepositioned cross-node wake latency is the local-cache restore
  path; the acceptance target is p50 ≤200 ms after `ready`.
  A cache miss remains owner RTT + shared-backend restore or cold
  boot and is observable through the fan-out metric.
- Snapshot disk usage scales with the configured local cache budget
  on each active node. The cache is bounded and evictable; the OCI
  registry remains the durable source.
- The OCI registry is the durable source of truth for the base
  + per-app layers; snapshots remain ephemeral.
- "Snapshot exists for every owned app" becomes "snapshot exists
  for every owned app *on the owner*". Invariant §6.2-3
  ("a live snapshot OR a cold-bootable rootfs") is unchanged
  because every owned app always has a cold-bootable rootfs in
  the OCI registry.

## Rejected alternatives

- **Cross-node snapshot replication (rsync + warm standby).**
  Rejected: it couples boxes to private addresses and provider
  storage. The shared-backend queue provides the same locality
  benefit without a peer transfer protocol.
- **Centralized snapshot server.** Rejected: contradicts the
  multi-box posture; one snapshot server is one failure domain.
- **OCI as the snapshot blob (in addition to layers).** Accepted as
  the authoritative transport, not as a truth source: Firecracker's
  snapshot restore is kernel-version-pinned (`snapshots.fc_version`)
  and FC upgrades drop snapshots anyway (ADR-005). The local cache
  remains an optimization.

## Implementation status

Issue #1054's first implementation is wired in PR-A:

- migration `00471_snapshot_replicas.sql` adds the durable queue and
  regional origin metadata;
- `pkg/snapshothipd` performs bounded, retryable cache warming from vmmd;
- `snapshothipd_fanout_total{outcome,region}` exposes the pre-instantiated
  closed `{ready, failed}` outcome set;
- `snapshothipd_fanout_latency_seconds{region}` records durable queue-to-ready
  latency, with a 100 ms reconciliation cadence so the ≤200 ms acceptance
  target is observable rather than hidden behind a one-second poll;
- reclaimed replica jobs carry a per-claim lease token, so a late worker
  cannot mark a newer retry ready or failed;
- scheduler placement prefers ready replicas without making them a
  correctness dependency.

The `snapshots.storage_key` column is already PostgreSQL `text` from
migration `00022_snapshots_storage_key.sql`, so it accepts shared-registry
keys without a second type-widening migration. OCI registry references stay
in the storage backend configuration; the database continues to store the
provider-neutral logical key (`snap/<deployment>/mem`).

The two-node metal drill still must measure the ≤200 ms prepositioned
wake target and run the 100-cycle leak check before the M9 acceptance
row can be marked green.
