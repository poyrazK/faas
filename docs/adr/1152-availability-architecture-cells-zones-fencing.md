# ADR-1152 · Availability architecture: regional cells, three-zone Postgres HA, fenced compute

- **Status:** proposed
- **Date:** 2026-10-13
- **Decision:** Gregale becomes a set of regional **cells**. Each cell spans
  three availability zones and runs its own self-managed Postgres HA cluster
  (Patroni + etcd, one synchronous standby), an active-active edge, and
  compute nodes that hold expiring leases and fence themselves. The data
  plane stays available through control-plane and database outages by
  serving last-known state. Control-plane writes and at-most-once workloads
  stay consistent: they stop rather than diverge. Planned operations
  (platform releases, node drains, Postgres switchovers, Firecracker
  upgrades) must not fail requests.
- **Why:** Owner goal for public release (2026-10-13): partition
  tolerance, zero downtime and acceptable scaling, across multiple regions
  and zones, with Postgres run by us. Today one host (fsn-1) holds every
  control-plane component, the only public origin and the only database.
  Losing it stops all traffic, including traffic to apps that are already
  running (see Context).
- **Consequences:** a per-cell minimum of three control nodes and three
  compute nodes, one of each per zone; a second region doubles that. A
  financial-model addendum must land before hardware is ordered. New
  surfaces: a `postgres_ha` Ansible role, a lease/epoch primitive, node
  leases, a cell registry and a global directory. Several accepted ADRs are
  superseded or amended (listed below).
- **Rejected alternatives:** managed Postgres, one cluster stretched across
  regions, a distributed SQL database, a second coordination store for
  leases, ADR-083's DNS leader election as the HA mechanism, and keeping
  maintenance windows. See the end of this ADR.
- **Builds on:** ADR-062 (per-node schedd), ADR-066 (live migration),
  ADR-137/174 (node lifecycle), ADR-208 (rollout handoff), ADR-420/421/422
  (service and ownership recovery), ADR-472 (vmmd restart quarantine),
  ADR-510 (snapshot backing identity)
- **Supersedes (when accepted):** spec §16 leans "maintenance windows" and
  "FSN + HEL pair"; ADR-083 as the HA mechanism; the ADR-137 heartbeat-gap
  trigger for recovery
- **Amends (when accepted):** spec §3 ADR-006 (Postgres topology), §2
  (architecture overview), §8 (off-box backups via pgBackRest), §13 (RAM
  ledger for dedicated control nodes), §14 (new acceptance gates), CLAUDE.md
  ("Gregale runs a single control-plane node today")

## Context

### Targets (owner-approved 2026-10-13)

| Objective | Target |
|---|---|
| Warm request availability, per cell, monthly | 99.95% |
| Wake success, per cell, monthly | 99.9% |
| Control-plane API availability | 99.9% |
| Node or zone loss | RPO 0, RTO ≤ 60 s |
| Region loss | RPO ≤ 60 s, RTO ≤ 30 min (manual promotion at first) |
| Planned operations | zero failed requests; long-lived streams get a close signal and can resume |

### Where main is today (e1bfe3368f)

Production (`deploy/manifest/production/gcp-live.template.yaml`) is one
control-plane host plus two compute hosts.

- **One failure domain for everything stateful.** fsn-1 runs Postgres,
  Caddy (the only origin, `origin.gregale.dev`, pinned by
  `deploy/cloudflare/public-timeout-worker/worker.mjs:130-145`),
  gatewayd-public, apid, meterd and the control-plane schedd that claims
  apps and fires cron.
  - If fsn-1 dies, Cloudflare has no origin and every app goes dark,
    including apps already running on fsn-2/fsn-3.
  - ADR-083's leader election is built but disabled. It could not survive
    fsn-1's loss in any case: it reads Postgres and elects compute nodes,
    which do not run gatewayd-public.
- **Postgres has no HA.** It is one distro cluster with no standby, no
  pooler (`deploy/ansible/roles/pgbouncer/defaults/main.yml`:
  `faas_pgbouncer_enabled: false`) and no `archive_timeout`.
  - The data-loss window is unbounded at low write rates.
    `docs/runbooks/PostgresBackup.md` claims 5–30 s.
  - Recovery is a manual restart or a manual restore.
  - Spec §8 already named pgBackRest; `deploy/ansible/roles/postgres` defers it.
- **The data plane fails with the database.**
  - gatewayd-public discards its last-known compute endpoints on any
    refresh error (`cmd/gatewayd-public/compute_gateway_pool.go:62-66`),
    so tenant traffic fails about 5 s into a Postgres outage.
  - Custom-domain and tenant-surface lookups have no stale tier
    (`cmd/gatewayd-internal/backend.go`).
  - After an outage longer than 90 s, the stale-peer observer
    (`pkg/sched/heartbeat.go:404`) sees every node as stale. It starts
    fleet-wide recovery.
- **Partitions are unfenced.**
  - A node cut off from the database keeps running VMs and serving its
    stale routes. Peers mark it unavailable after 90 s and recreate its
    workloads elsewhere.
  - Job and app-task retries are dispatched while the original VM still
    runs. Lost-VM cleanup (`pkg/sched/lost_vms.go`) runs only when a node
    pings its own vmmd, so it never fires on the peer that did the
    recreate.
  - `CmdFaultInjector.Partition` (`pkg/e2etest/fault.go:165`) has no
    callers, and most two-node failure tests skip
    (`cmd/e2e/twonode_failure_safe_metal_test.go`).
- **Planned operations fail requests.**
  - A vmmd restart quarantines the VMs it did not start (ADR-472), so
    every compute upgrade needs a full drain. The drain pauses VMs for
    seconds, does not wait for in-flight requests, and cuts streams after 25 s.
  - guest-init ships in drive0, so almost every release retires every
    snapshot (ADR-510). The Firecracker-version sweep marks snapshots
    stale on every node at once (`pkg/state/pgstore.go:18052`).
  - Migrations run at boot in five daemons. There is no
    backward-compatibility rule, and platform rollback on a failed gate is
    off (`.github/workflows/cd-platform.yml`, `rollback_on_gate_failure`).
- **Scale has never been measured across hosts.**
  - Postgres connections limit a fleet to about 12 compute nodes on
    today's control host.
  - meterd scans every app every minute.
  - gatewayd-public spreads requests round-robin with no app affinity.
    Route caches and wake coalescing are therefore split N ways.

## Decision

### 1. Consistency is chosen per plane

| Plane | During a partition or database outage | Mechanism |
|---|---|---|
| Serving requests to warm instances | **Available.** Serve last-known routes and targets for a bounded time | Stale tiers on every routing cache (§5) |
| Wakes and admission | **Consistent.** The minority side returns 503 with `Retry-After` | Cell Postgres quorum (§3) |
| Customer API writes | **Consistent** | Cell Postgres quorum |
| At-most-once work: cron, jobs, app tasks, workflow steps, singleton services, realtime channel ownership | **Consistent and fenced.** Stops on the minority side | Node leases and fencing epochs (§4) |
| Metering and billing | Eventually consistent and idempotent | Existing ledger catch-up |

We do not build our own consensus. Each cell's Postgres is the only
linearizable store; leases and epochs live in it, written in the same
transaction as the work they guard.

### 2. Topology: regions are cells, zones are the failure domain inside a cell

A **cell** is one region's complete Gregale. Every customer resource is
owned by exactly one cell. A cell contains:

- **Control nodes**, three, one per zone. Each runs a Patroni Postgres
  member, an etcd member, HAProxy, pgbouncer, Caddy and gatewayd-public,
  apid, and the lease-holding singletons (meterd, cron/claim scheduler,
  apid timers). Prometheus and Alertmanager run as a pair on two of them.
- **Compute nodes**, at least one per zone plus one spare per zone for
  drains (N+1). Each runs vmmd, schedd, gatewayd-internal, builderd and imaged.
- **Object storage** in the region, multi-zone, for snapshots, artifacts
  and backups.
- **Edge:** a Cloudflare Load Balancer pool of the cell's gatewayd-public
  nodes, health-checked on `/readyz`, active-active. This replaces
  ADR-083's DNS flip.

We do not stretch one cluster across regions, for two reasons:

- **Latency.** A cold wake makes several sequential Postgres reads and
  compare-and-swap writes (`pkg/sched/engine.go`, wake path).
  Synchronous commits across regions would add one inter-region round
  trip to each of those writes, against a 350 ms p95 wake target.
- **Blast radius.** A region-to-region partition would then split one
  database.

Cells keep each region's failures inside that region, and adding cells is
how Gregale scales out (§7).

**The global layer is small and off the hot path.**
- A directory holds identities, billing customers, org → home cell, and
  hostname → cell.
  - Writes are rare and may cross regions.
  - Each cell keeps a read-only replica of what its hot path needs, for
    example API-key hashes for local auth.
- Routing works without the directory being up:
  - The existing public Worker resolves the hostname → cell mapping from
    Workers KV and overrides the origin to that cell's pool.
  - A miss, or a stale entry, lands on any cell's gatewayd-public. That
    cell forwards across cells using its directory replica.
  - KV is an optimization; a cell that is reachable always routes
    correctly.

**First tenancy unit: the org's home cell.** All of an org's apps live in
its home cell. A per-app region is a follow-up ADR (see open decisions).

**Region disaster recovery:**
- Each cell's Patroni cluster streams asynchronously to a Patroni
  `standby_cluster` in a paired region.
- pgBackRest keeps a second repository in that region.
- Snapshots are a cache and cold boot always works (ADR-005). To evacuate
  a region: promote the standby cluster, re-point the cell's edge pool,
  and let apps cold-boot.

### 3. Postgres per cell: self-run Patroni

- **Members:** three Postgres 16 members, one per zone, managed by
  Patroni. etcd is the distributed configuration store, with one member
  per zone on the control nodes.
- **Settings:** `synchronous_mode: true` and `synchronous_node_count: 1`,
  so losing any one member or zone loses no committed data.
  `use_pg_rewind: true`. The Linux softdog watchdog is required, so an
  isolated former primary cannot keep accepting writes.
- **Failover time:** Patroni's defaults (`ttl` 30 s, `loop_wait` 10 s)
  put automatic failover at about 30–40 s, inside the 60 s RTO.
- **Client access** is node-local on every control and compute node:
  - HAProxy health-checks Patroni's `/primary` endpoint.
  - pgbouncer in transaction mode serves ordinary queries.
  - LISTEN uses a small session pool through HAProxy.
  - Every DSN becomes localhost.
  - `max_connections` is fixed per cell, so a node join never restarts
    Postgres (it does today, via `postgres_capacity`).
- **Backups:** pgBackRest replaces the custom `archive_command`.
  - Settings: `archive_timeout = 60s`, asynchronous archiving, one
    repository in regional object storage and a second in the paired
    region, point-in-time recovery.
  - A restore drill runs automatically every month and records evidence.
- **Upgrades:**
  - Minor upgrades roll through replicas first, then switch over, with a
    write pause target under 5 s.
  - Major upgrades use logical-replication blue/green, or `pg_upgrade
    --link` with a declared write pause.
  - In both cases the data plane keeps serving warm traffic (§5); a write
    pause is not an outage.
- **Ownership:** a new `postgres_ha` Ansible role replaces the `postgres`
  role, with runbooks for failover, switchover, member rebuild, etcd
  quorum loss and standby-cluster promotion.

### 4. Leases and fencing epochs

**The lease primitive.** One primitive in `pkg/state`: `leases(name, holder,
epoch, expires_at)`.
- Expiry uses the database's `now()`.
- `epoch` comes from a sequence and increases on every change of holder.
- Every fenced write includes `epoch = $current` in the same statement or
  transaction.

The primitive is used for three things:

- **Control-plane singletons:** meterd's sampler and alert evaluator, the
  cron and claim scheduler (today the control-plane schedd with an empty
  `FAAS_NODE_NAME`), and apid's plain timers. Replicas run hot and only
  the lease holder acts.
- **Node leases:** every compute node holds `node/<name>`.
  - It renews every 10 s, with a 30 s TTL.
  - A node that has not renewed for TTL minus a safety margin, measured
    on its own monotonic clock, fences itself:
    - it stops at-most-once work, rejects new wakes, and stops claiming
      jobs;
    - it keeps serving stateless HTTP to warm instances, which duplicate
      harmlessly.
  - Peers recover a node's workloads only when (a) its lease expired by
    the database clock plus a takeover margin, and (b) the observer itself
    has been connected for at least one TTL. Condition (b) prevents the
    post-outage recovery storm.
  - Lease expiry replaces the 90 s heartbeat gap as the recovery trigger.
- **Side effects outside Postgres** carry the epoch:
  - job and app-task completion;
  - conditional object-storage writes;
  - idempotency keys on outbound webhooks.

**Rejoining.** On re-acquiring its lease, a node immediately destroys every
VM it does not own. This replaces the peer-side `DestroyLostVMs`.
`recovering → active` requires three minutes of continuous renewals, and
repeated flaps back off exponentially up to 30 minutes.

**Supporting changes:**
- gRPC keepalives on every overlay dial, so a dead peer fails fast
  instead of holding a 180 s migration lease.
- The lease timings go in `pkg/api/limits.go`, with every other limit.

### 5. Static-stability rules for the data plane

1. **Keep last-known-good.** A refresh error never discards last-known-good
   state. Fix `compute_gateway_pool.go:62-66` first.
2. **Stale tier everywhere.** Every routing cache has a bounded stale tier,
   including custom-domain, tenant-surface and environment hosts.
3. **LISTEN is a hint.**
   - Every consumer already should have a safety ticker (the
     `pkg/db/notify_hub.go` contract). Consumers that lack one get one:
     the cluster signing-key loaders, vmmd's egress watcher, the DNS
     handoff and SSE fan-in.
   - Reconnecting the hub triggers an immediate resync pass.
4. **No decisions from stale observation.** No recovery decision is made
   from evidence gathered while the observer was disconnected.
5. **Buffer, don't drop.** Activity and usage signals are buffered across
   outages, so the reaper does not park instances that served traffic
   during one.
6. **Wait for dependencies.** Daemons wait for their dependencies instead
   of crash-looping into a failed systemd unit.

### 6. Zero-downtime contract for planned operations

These operations must complete under load with zero failed requests:
- a platform release;
- a compute node drain or reboot;
- a control node reboot;
- a Postgres switchover;
- a Firecracker or kernel upgrade.

Required mechanisms:

- **Lossless instance drain.** Reuse ADR-208's handoff for every instance
  that leaves a node: stop routing to it, collect gateway
  acknowledgements, wait for in-flight requests to reach zero, then
  migrate or park. Long-lived streams get a WebSocket close frame or
  HTTP/2 GOAWAY with a reconnect hint, and realtime resume becomes
  generally available.
- **Non-destructive vmmd restart.** Re-adopt running Firecracker
  processes and their bridges instead of quarantining them (supersedes
  ADR-472's restart quarantine), so a node upgrade does not need a drain.
- **Listener continuity.**
  - gatewayd-internal and schedd become socket-activated, like
    gatewayd-public and apid already are.
  - gatewayd-public retries a request on another compute gateway when it
    was refused before any byte reached a guest.
  - Waiters in the WakeGate get `503 Retry-After` on shutdown, never a reset.
  - `TimeoutStopSec` matches each daemon's drain budget.
- **Snapshot continuity.**
  - guest-init moves out of drive0, or becomes versioned so it is
    backward compatible, so releases stop retiring snapshots.
  - Firecracker staleness is scoped to the node or release that changed.
  - Upgraded nodes re-snapshot hot apps in the background before traffic
    is routed to them.
- **Schema discipline.**
  - Expand/contract migrations.
  - A linter requiring `CREATE INDEX CONCURRENTLY` outside a transaction,
    `lock_timeout` on DDL, and no drop or rename in the release that
    stops using a column.
  - A CI job that runs the previous release's binaries against the new
    schema.
  - One migration runner per release, not five daemons migrating at boot.
- **Version skew.** `buf breaking` on every proto, and capability
  negotiation between daemons for mixed-release windows.
- **Release safety.** Each platform release canaries on one node per cell,
  with automatic rollback on SLO burn. `rollback_on_gate_failure` turns on
  once the gate has run green.

### 7. Scaling rules

**Within a cell**, the initial target is 50 compute nodes, 50,000 apps and
5,000 requests/s. It is validated by load tests, not assumed:

- pgbouncer everywhere (§3).
- meterd samples in pages under its lease, or nodes pre-aggregate
  instance-seconds.
- gatewayd-public routes each app to the gatewayd-internal on its owner
  node. That gives one route cache and one wake coalescer per app. The
  owner schedd hands an already-WAKING instance to a second caller instead
  of starting another.
- Generalize `control_plane_change_log` into a sequenced log that
  consumers read by cursor, with `pg_notify` as a wake-up hint only.
- Replicate each snapshot to two nodes in different zones, not to every
  node in the region.
- Partition `usage_minutes` and `audit_log`.
- Remove single-box constants from code paths that should use per-node
  sizing, for example the 38,080 MB eviction threshold in
  `pkg/sched/reaper.go`.

**Beyond a cell**, add cells. A cell is both the unit of scale and the
unit of blast radius.

## Phases and exit criteria

| Phase | Scope | Exit criterion (recorded under `docs/ops/evidence/`) |
|---|---|---|
| P0 · Proof | SLO dashboards. Native drills for partition, zone loss, Postgres failover and node drain, run nightly (wire `CmdFaultInjector.Partition`, un-skip the two-node tests). Multi-host load test. | Every drill automated, with a baseline recorded |
| P1 · Static stability | §5, `archive_timeout`, pgbouncer, per-node Firecracker staleness | Postgres down for 5 min: zero errors on warm `*.gregale.dev` and custom-domain traffic; no recovery storm afterwards |
| P2 · Cell HA across zones | §3 Postgres HA; at least 2 edges behind a Cloudflare Load Balancer; N apid replicas (upload spool in object storage); leases for singletons; monitoring off the database hosts; cell registry and `orgs.home_cell` seams (one cell) | Kill any one node or zone under load: RTO ≤ 60 s, RPO 0 |
| P3 · Fenced compute | §4 node leases, self-fencing, epochs, rejoin cleanup, keepalives, hysteresis | Partition drill: zero duplicate at-most-once executions; stateless duplicates bounded by the lease TTL |
| P4 · Zero-downtime operations | §6 | Platform release, node drain and Firecracker upgrade under load: zero failed requests; no cold-boot storm |
| P5 · Scale within a cell | §7 | Load test at the §7 targets within SLO |
| P6 · Second region | Directory, Worker KV routing with cross-cell forwarding, standby cluster, region-evacuation runbook | Region-loss drill: RPO ≤ 60 s, RTO ≤ 30 min, other cells unaffected |

**Public-release gate:**
- P0, P1 and P2, plus P4's lossless drain.
- P3 must land before jobs, cron, services or realtime are marketed as
  generally available.
- P6 may follow launch. The P2 seams ensure customer data is never global
  by accident, which would block P6 later.

## Consequences

- **Footprint:**
  - Per cell: 3 control nodes and at least 6 compute nodes (3 zones × N+1).
    Today the fleet is 1 + 2.
  - A second region doubles this.
  - The financial-model addendum (CLAUDE.md, source of truth 3) must
    price both before P2 hardware.
- **Operations:**
  - Patroni, etcd and pgBackRest join the on-call surface, with runbooks
    and monthly drills.
  - The control nodes stop co-hosting tenant VMs. §13's RAM ledger gains
    a control-node budget.
- **Code:**
  - Gateways: last-known-good state and stale tiers.
  - Notify hub: resync on reconnect.
  - Leases and node fencing in `pkg/state` and `pkg/sched`.
  - vmmd re-adoption.
  - Instance drain, reusing the ADR-208 handoff.
  - guest-init packaging.
  - Migration tooling and linters.
  - gatewayd-public: affinity routing and retry.
  - meterd paging.
  - Cell and directory seams in apid.
- **Superseded:**
  - Spec §16 "maintenance windows" and "FSN + HEL pair at Gate A".
  - ADR-083 as the HA mechanism (Cloudflare Load Balancing replaces its
    DNS flip).
  - The ADR-137 heartbeat-gap recovery trigger.
  - ADR-472's restart quarantine, once P4 re-adoption ships.

## Rejected alternatives

- **Managed Postgres (Cloud SQL, AlloyDB).** It would tie the control
  plane to one cloud, while Gregale must deploy on any bare-metal x86_64
  host (spec preamble). Owner decision on 2026-10-13: self-run.
- **One cluster stretched across regions.** Inter-region commit latency on
  every wake, and one database for every region's partitions.
- **Distributed SQL (CockroachDB, Yugabyte, Spanner).** A rewrite of 652
  tables, the sqlc layer and the `pg_notify` contracts, with no
  availability gain inside a region over Patroni.
- **etcd or Consul for application leases.** A second source of truth: the
  fencing check must commit in the same transaction as the fenced write.
  etcd stays private to Patroni.
- **ADR-083's lexicographic-minimum DNS election.** It depends on the
  database it is meant to survive, and on DNS TTLs. It is active-passive,
  and it elects nodes that do not run the edge.
- **Maintenance windows (spec §16 lean).** Incompatible with the
  zero-downtime target.

## Open decisions for the owner

1. **Region tenancy:** the org's home cell (proposed) or per-app region
   from day one. Per-app region requires account data shared across cells.
2. **The second region** and its pairing for standby clusters.
3. **etcd placement:** co-located on control nodes (proposed) or on three
   small dedicated nodes.
4. **Major Postgres upgrades:** logical-replication blue/green, or
   `pg_upgrade --link` with a declared write pause.
