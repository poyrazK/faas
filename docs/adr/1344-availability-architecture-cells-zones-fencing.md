# ADR-1344 · Availability architecture: regional cells, three-zone Postgres HA, fenced compute

- **Status:** proposed
- **Date:** 2026-10-13
- **Decision:** Gregale's regions run as independent **cells**, using the
  model already built on branch `codex/regional-deployment-20261001`:
  - a cell's control plane and database are bound to one region;
  - apps are either pinned to one region or global with targets in
    several regions;
  - a global directory pushes signed grants and ingress snapshots.

  This ADR adds what that branch leaves out:
  - within each cell, three availability zones and a self-run Postgres HA
    cluster (Patroni + etcd, one synchronous standby);
  - the same HA for the directory database;
  - global ingress served from every cell edge instead of one node;
  - expiring node leases with self-fencing, which respect the branch's
    rule that only signed stop proof releases quota;
  - static stability of the data plane;
  - a zero-downtime contract for planned operations.

  **Owner decisions (2026-10-13):**
  - US East, where production runs today, is the first cell and the
    directory's home. An EU cell follows after launch.
  - etcd is co-located on the control nodes.
  - Major Postgres upgrades use logical blue/green.
  - Order: single-region HA first; the regional branch lands dark in
    parallel; multi-region is activated after launch.

  **Revised 2026-10-14:** the public release moves to OVH bare metal in
  about a month. It launches with one OVH control node, two compute nodes
  and a GCP warm standby with manual failover (§8). The three-zone cell
  follows after launch.

  **Revised 2026-10-15:** the scaling roadmap owns the request path and
  PgBouncer; this ADR owns availability, fencing, cells and the app edge
  on compute nodes (§9, ADR-1343).
- **Why:** owner goal for public release (2026-10-13): partition tolerance,
  zero downtime and acceptable scaling across regions and zones, with
  Postgres run by us. Today a single host (fsn-1) holds every
  control-plane component, the only public origin and the only database.
  The regional branch decides how regions relate, but every cell and the
  directory are still single-host, single-database deployments.
- **Consequences:**
  - Footprint: a per-cell minimum of three control nodes and six compute
    nodes, plus a three-node directory deployment. A financial-model
    addendum must land before hardware is ordered.
  - New surfaces: a `postgres_ha` Ansible role, a lease/epoch primitive,
    node leases, and replicated ingress snapshots.
  - Several accepted ADRs are superseded or amended (listed below).
- **Rejected alternatives:** managed Postgres; one cluster stretched
  across regions; a distributed SQL database; a second coordination store
  for leases; ADR-083's DNS leader election; maintenance windows;
  redesigning regional tenancy here. See the end of this ADR.
- **Builds on:**
  - Main: ADR-062 (per-node schedd), ADR-066 (live migration),
    ADR-137/174 (node lifecycle), ADR-208 (rollout handoff),
    ADR-420/421/422 (service and ownership recovery), ADR-472 (vmmd
    restart quarantine), ADR-510 (snapshot backing identity).
  - Regional branch ADRs (numbers 713–802, to be renumbered before they
    land):
    - regional compute placement and the region registry (719/720);
    - regional service recovery capacity and placement headroom (717/718);
    - control-plane cell identity (729);
    - the global app directory and regional bindings (730);
    - the signed quota protocol (740–746, 760, 772–775);
    - host incarnations and operation fences (747/748);
    - native provenance and retirement (757–783);
    - global ingress (784–786);
    - global provisioning (797);
    - the global usage ledger (798–802).
- **Supersedes (when accepted):** spec §16 leans "maintenance windows" and
  "FSN + HEL pair"; ADR-083 as the HA mechanism; the ADR-137
  heartbeat-gap trigger for recovery.
- **Amends (when accepted):**
  - spec §3 ADR-006 (Postgres topology);
  - spec §2 (architecture overview);
  - spec §8 (off-box backups via pgBackRest);
  - spec §13 (RAM ledger for dedicated control nodes);
  - spec §14 (new acceptance gates);
  - CLAUDE.md ("Gregale runs a single control-plane node today");
  - the regional branch's ADR-717/718 certificate (§2) and its
    single-node directory and ingress deployment (§2).

## Context

### Targets (owner-approved 2026-10-13)

| Objective | Target |
|---|---|
| Warm request availability, per cell, monthly | 99.95% |
| Wake success, per cell, monthly | 99.9% |
| Control-plane API availability | 99.9% |
| Node or zone loss | RPO 0, RTO ≤ 60 s |
| Region loss | RPO ≤ 60 s, RTO about 2–4 h: rebuild from off-region backups, no warm standby |
| Planned operations | zero failed requests; long-lived streams get a close signal and can resume |

### Where main is today (e1bfe3368f)

Production (`deploy/manifest/production/gcp-live.template.yaml`) is one
control-plane host plus two compute hosts.

- **One failure domain for everything stateful.** fsn-1 runs Postgres,
  Caddy (the only origin, `origin.gregale.dev`, pinned by
  `deploy/cloudflare/public-timeout-worker/worker.mjs:130-145`),
  gatewayd-public, apid, meterd and the control-plane schedd that claims
  apps and fires cron.
  - If fsn-1 dies, every app goes dark, including apps already running on
    the compute hosts.
  - ADR-083's leader election is built but disabled, and could not
    survive fsn-1's loss: it reads Postgres and elects compute nodes,
    which do not run gatewayd-public.
- **Postgres has no HA.** It is one cluster with no standby, no pooler
  (`deploy/ansible/roles/pgbouncer/defaults/main.yml`) and no
  `archive_timeout`.
  - The data-loss window is unbounded at low write rates.
    `docs/runbooks/PostgresBackup.md` claims 5–30 s.
  - Recovery is manual.
  - Spec §8 already named pgBackRest.
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
  - A node cut off from the database keeps running VMs and serving stale
    routes. Peers recreate its workloads after 90 s.
  - Job and app-task retries run while the original VM still runs.
  - Lost-VM cleanup (`pkg/sched/lost_vms.go`) never fires on the peer
    that did the recreate.
  - `CmdFaultInjector.Partition` (`pkg/e2etest/fault.go:165`) has no
    callers.
- **Planned operations fail requests.**
  - A vmmd restart quarantines the VMs it did not start (ADR-472), so a
    compute upgrade needs a full drain.
  - The drain pauses VMs for seconds and does not wait for in-flight
    requests.
  - guest-init ships in drive0, so most releases retire every snapshot
    (ADR-510). The Firecracker sweep marks snapshots stale on every node
    at once (`pkg/state/pgstore.go:18052`).
  - Migrations have no backward-compatibility rule. Platform rollback is
    off (`.github/workflows/cd-platform.yml`).
- **Scale has never been measured across hosts.**
  - Postgres connections cap a fleet at about 12 compute nodes on today's
    control host.
  - meterd scans every app every minute.
  - gatewayd-public routes round-robin with no app affinity.

### What the regional branch adds, and what it leaves open

The branch `codex/regional-deployment-20261001` is at 10379354c6: 98
commits, 1,294 files, +137.7k lines and 90 ADRs. It forked at 2442c913cf
and is dark by default. Main is 218 commits ahead of it.

**Already decided there:**
- **Region pinning.** `apps.compute_region` is immutable, and the
  scheduler never spills into another region (719).
- **Cells.** A cell's database carries an immutable cell ID and region.
  Every daemon checks `FAAS_CONTROL_PLANE_CELL_ID` and
  `FAAS_CONTROL_PLANE_CELL_REGION` when it opens a connection (729).
- **Global directory.** It is a separate, unbound database behind
  directory apid. It holds cells, global apps whose `routing_policy` is
  `all_selected_regions` (at most one target per region), and owners
  (730/737).
- **Push-only delivery.** Regions never query the directory on the
  runtime path. The directory pushes Ed25519-signed quota grants (740–746,
  772–775) and probes cells with signed challenges.
- **Directory loss.** Regional `<slug>.<cell apps_domain>` serving
  survives a directory outage (730, 785).
- **Global ingress.** The directory signs a ~5 s snapshot of healthy
  targets. gatewayd-internal consumes it and proxies round-robin to
  healthy regions under a 30 s lease (784–786).
- **Billing.** Cells sign per-minute usage, and a global ledger imports
  it idempotently (798–802).
- **Recovery capacity.** Each region keeps enough headroom to recover
  from losing one host, with at least two healthy hosts (717/718).
- **Host incarnations.** A per-host epoch changes on each kernel boot, and
  fences reject stale permits (747/748).
- **Stop proof.** Capacity is released only by a signed `CapacityStop`
  backed by native proof that the VM is gone. Elapsed time is never
  evidence of absence (740, 746, 758/759).

**Left open there, and decided here:**
- No Postgres HA in a cell or for the directory. Directory HA is not
  designed, and its publication journal is a singleton (786).
- Global ingress runs on one gatewayd-internal next to the directory,
  and a failed request is not retried in another region.
- Zones are labels used only to break placement ties.
- No TTL leases or self-fencing. Hosts are fenced only by an
  authenticated retirement RPC, so an unreachable host keeps its hold
  indefinitely (759).
- vmmd restart recovery still quarantines; it does not re-adopt running VMs.
- Activation is frozen by a hard `CHECK (NOT admission_enabled)`.
  Billing production integration, global caps, shared organizations and
  native KVM qualification are unfinished.

## Decision

### 1. Consistency is chosen per plane

| Plane | During a partition or database outage | Mechanism |
|---|---|---|
| Serving requests to warm instances | **Available.** Serve last-known routes and targets for a bounded time | Stale tiers on every routing cache (§5) |
| Wakes and admission | **Consistent.** The minority side returns 503 with `Retry-After` | Cell Postgres quorum (§3) |
| Customer API writes | **Consistent** | Cell or directory Postgres quorum |
| At-most-once work: cron, jobs, app tasks, workflow steps, singleton services, realtime channel ownership | **Consistent and fenced.** Stops on the minority side | Node leases and fencing epochs (§4) |
| Regional quota capacity | **Conservative.** Released only by signed stop proof | The branch's quota protocol, unchanged |
| Metering and billing | Eventually consistent and idempotent | Ledger catch-up, plus the branch's signed usage minutes |

We do not build our own consensus. Each cell's Postgres and the directory's
Postgres are the only linearizable stores. Leases and epochs live in them,
written in the same transaction as the work they guard.

### 2. Topology: the regional branch's cells, made highly available

**Cell contents.** A cell keeps the regional branch's definition. This ADR
specifies what runs inside it:

- **Control nodes**, three, one per zone. Each runs a Patroni Postgres
  member, an etcd member, HAProxy, pgbouncer, Caddy and gatewayd-public
  for the platform hosts (apex, `api.`, `operations.`, `s3.`), apid, and
  the lease-holding singletons (meterd, cron/claim scheduler, apid
  timers). Prometheus and Alertmanager run as a pair on two of them.
- **Compute nodes**, at least one per zone plus one spare per zone for
  drains (N+1).
- **Object storage** in the region, multi-zone, for snapshots, artifacts
  and backups.
- **Edge:** designed in ADR-1343 (§9). A Cloudflare Tunnel connector and
  gatewayd-public run on every compute node and serve the cell's apps
  domain; the platform hosts stay on the control
  nodes. It replaces ADR-083's DNS flip. This ADR only requires that
  losing any one node or zone leaves the edge serving.

**Zones are a failure domain, not a tie-break.**
- Placement spreads an app's instances and service replicas across zones.
- The branch's host-loss certificate (717/718) becomes a zone-loss
  certificate:
  - It changes from `R ≤ C − L − max_i(S_i − L_i)` over hosts to the same
    bound taken over zones.
  - It keeps the per-host bound.
  - It requires healthy hosts in at least two zones.

**The directory is HA too.**
- Its home region is US East. It runs as a three-zone deployment of the
  same shape: Patroni + etcd and N directory apid replicas. Its off-region
  backups follow the same rule as a cell's.
- **Initial placement.** The directory may start as a separate, unbound
  database on the US East cell's Patroni cluster, which the branch's
  identity checks allow.
  - **Gain:** saves three nodes.
  - **Cost:** losing the US East cluster then also stops directory writes
    and global-ingress refresh. The EU cell's regional apps keep serving
    (730).
  - The directory moves to its own cluster before an EU cell serves
    global apps.
- Its singleton workers run under §4 leases: the ingress publication
  journal (786), the quota delivery worker (772/775) and provisioning
  reconciliation (797).
- A directory outage already leaves regional serving intact (730). With
  HA it also stops being an outage for global apps.

**Global ingress is served from every edge.**
- The directory's signed snapshot (784–786) is pushed to every cell, using
  the same signed-delivery pattern as quota grants. It is not written to
  one local file.
- Every cell's gatewayd-internal verifies and serves global hostnames.
- Cloudflare steers the global apps domain to the nearest healthy cell
  edge.
- A request refused before any byte reached a guest is retried once in
  another eligible region; today the branch never retries across regions.
- The snapshot lease stays bounded (30 s). Positive serving eligibility
  stays gated by the branch's admission freeze until its exit criteria
  are met.

**No stretched cluster.** We do not stretch one cell across regions, for
two reasons:
- **Latency.** A cold wake makes several sequential Postgres reads and
  compare-and-swap writes (`pkg/sched/engine.go`). Each would pay an
  inter-region round trip.
- **Blast radius.** A partition between regions would split one database.

**Region disaster recovery: off-region backups, no standby machines**
(owner decision).

Zones cover host, rack and building failures. Two risks remain that zones
cannot cover:
- **Region-wide outages.** Zones in one region share the provider's
  regional control plane, network and storage.
- **Our own mistakes.** Synchronous replicas copy a bad migration or
  delete instantly; only point-in-time recovery undoes it.

Off-region backups cover both:
- **Database.** pgBackRest's second repository lives in a paired region
  and receives WAL at least every 60 s (`archive_timeout`).
- **Artifacts.** The app layers and build artifacts that cold boot needs
  replicate to the paired region. Snapshots are a cache (ADR-005) and do
  not need to.
- **Pairing:**
  - US East pairs with a second US region, so US data stays in the US.
  - The EU cell pairs with a second EU region, so EU data stays in the EU.
- **Recovery.** To recover a lost region:
  1. stand the cell up in the paired region from the manifest;
  2. restore Postgres to the latest WAL;
  3. re-point the cell's edge pool;
  4. let apps cold-boot.

  Cell identity (729) is preserved, because the restored database is the
  same cell.
- **Targets:** RPO ≤ 60 s, RTO about 2–4 hours.
- A warm `standby_cluster` in the paired region is the upgrade path if an
  SLA later needs about 30 minutes; it is not built now.

### 3. Postgres per cell and for the directory: self-run Patroni

- **Members:** three Postgres 16 members, one per zone, managed by
  Patroni. etcd is the configuration store, one member per zone.
- **etcd placement** (owner decision): one member on each control node,
  next to its Postgres member.
  - etcd gets its own disk or partition and I/O priority above Postgres,
    so a slow Postgres fsync cannot delay etcd heartbeats and cause
    needless failovers.
  - Losing a control node loses one Postgres member and one etcd member
    together; quorum survives at 2 of 3.
  - etcd fsync latency is alerted on. If it alerts, or a cell grows past
    20 compute nodes, etcd moves to dedicated nodes in a follow-up.
- **Settings:** `synchronous_mode: true` and `synchronous_node_count: 1`,
  so losing any one member or zone loses no committed data.
  `use_pg_rewind: true`. The Linux softdog watchdog is required.
- **Failover time:** Patroni's defaults (`ttl` 30 s, `loop_wait` 10 s) put
  automatic failover at about 30–40 s.
- **Client access** is node-local on every node:
  - HAProxy checks Patroni's `/primary` endpoint.
  - pgbouncer runs in transaction mode.
  - LISTEN uses a small session pool.
  - Every DSN becomes localhost.
  - The cell-identity checks on pool checkout (729) continue to apply.
  - `max_connections` is fixed, so a node join never restarts Postgres.
- **Backups:** pgBackRest replaces the custom `archive_command`.
  - `archive_timeout = 60s`, asynchronous archiving, a repository in
    regional object storage and another in the paired region,
    point-in-time recovery.
  - An automated monthly restore drill.
  - A restore keeps the cell's identity, as 729 already notes.
- **Upgrades:**
  - Minor upgrades roll replicas first, then switch over (write pause
    under 5 s).
  - Major upgrades use logical-replication blue/green (owner decision):
    - build a new-version cluster, replicate logically, freeze
      migrations, then cut over with a write pause of seconds;
    - parked apps can still wake, which an in-place `pg_upgrade` write
      pause of minutes would not allow.
  - **Timing.** The tooling is built before the first major upgrade.
    Postgres 16 is supported until November 2028.
  - **CI check, starting now:** every table must have a primary key or an
    explicit replica identity, so the requirement cannot regress before
    the tooling exists.
  - The data plane keeps serving warm traffic throughout (§5).
- **Ownership:** a new `postgres_ha` Ansible role replaces the `postgres`
  role, with runbooks for failover, switchover, member rebuild, etcd
  quorum loss and restoring a cell into its paired region.

### 4. Leases and fencing epochs

**The lease primitive.** One primitive in `pkg/state`:
`leases(name, holder, epoch, expires_at)`, in a dedicated table.
- Expiry uses the database's `now()`.
- `epoch` comes from a sequence and increases on every change of holder.
- Fenced writes include `epoch = $current`.
- The claim and stale-finish pattern of the branch's quota delivery outbox
  is the template.

The primitive is used for:

- **Singletons** in every cell and in the directory. Their leases are on
  only in a cell with more than one control node; a one-node cell runs its
  singletons without a lease, as before, so it has no lease to lose when
  Postgres blips.
- **Node leases.** Each compute node holds `node/<name>`.
  - It renews every 10 s with a 30 s TTL, in its own table, not on
    `compute_nodes.last_heartbeat_at`. That row takes the regional
    service-capacity policy lock (717).
  - The lease records the host incarnation epoch (747/748) next to the
    DB-issued epoch. The incarnation identifies the boot; the DB epoch
    fences the holder.
  - **Self-fence:** a node that has not renewed for TTL minus a safety
    margin, on its own monotonic clock:
    - stops at-most-once work, rejects new wakes, and stops claiming jobs;
    - keeps serving stateless HTTP to warm instances.
  - **Takeover:** peers recover a node's workloads only when (a) its lease
    expired by the database clock plus a margin, and (b) the observer has
    been connected for at least one TTL. Condition (b) prevents the
    post-outage recovery storm.
  - Lease expiry replaces the 90 s heartbeat gap as the recovery trigger.
    The 717/718 certificate uses the same liveness definition, so
    "healthy" and "alive" never diverge.

**Rules from the regional branch that leases must keep:**
- **Expiry never releases regional quota permits or seats.** Only a
  signed `CapacityStop` backed by native retirement proof does (740, 746,
  758/759). Re-placing a fenced node's workloads therefore keeps the old
  host charged until proof arrives. That loss of capacity during a
  partition is accepted, and the zone-loss certificate sizes for it.
- **Lock ordering.** New epoch checks on instance writes follow the
  branch's write-fence lock ordering (745).
- **Epoch on side effects.** Side effects outside Postgres carry the
  epoch: job and app-task completion, conditional object-storage writes,
  and outbound webhook idempotency keys.

**Rejoining.**
- On re-acquiring its lease, a node immediately destroys every VM it does
  not own. Where regional quota applies, it produces the native
  retirement proof the branch requires.
- `recovering → active` requires three minutes of continuous renewals,
  and repeated flaps back off up to 30 minutes.
- gRPC keepalives go on every overlay dial.
- The lease timings go in `pkg/api/limits.go`.

### 5. Static-stability rules for the data plane

1. **Keep last-known-good.** A refresh error never discards
   last-known-good state. gatewayd-public's compute inventory serves the
   last successful read for up to 10 minutes and refreshes in the
   background.
2. **Stale tier for ordinary and custom-domain routes.** Both use the
   ADR-190 tier (10 minutes); a custom domain's per-request validation
   error falls back to it instead of returning 404, and each successful
   validation restarts the window. Two cases deliberately stay
   fail-closed: registered-environment hosts, whose target moves on
   promotion, and the platform-tenant suspension guard, which must not
   serve a possibly suspended customer. Global-ingress hosts keep the
   regional branch's 30-second snapshot lease.
3. **LISTEN is a hint.** Consumers driven only by notifications subscribe
   with `db.SubscribeWithResync`. They receive a synthetic resync
   notification after the LISTEN connection is re-established, and after
   one of their deliveries is dropped, and they reload from the table.
   This covers the cluster signing-key loaders (schedd and
   gatewayd-internal), vmmd's egress watcher, the DNS handoff and
   gatewayd-public's compute inventory. apid's SSE fan-in is not covered:
   events lost there are visible to clients and need a replayable event
   log, not a reload.
4. **No decisions from stale observation.** Once a schedd has seen the
   store unavailable, its liveness verdicts based on heartbeat age (the
   stale-peer observer and the dead-node sweep) wait until it has read
   the store without interruption for a full staleness window. The
   evidence is:
   - a failed store read;
   - a boot-time wait for Postgres;
   - store errors while it waited as a standby for the
     central-scheduler lease.

   A schedd that started against a healthy store gives verdicts at once.
   Holding on every start would delay dead-node recovery after each
   restart and failover with no outage to justify it. One case is left
   open: a schedd that restarts within one heartbeat interval after an
   outage it did not see.
5. **Buffer, don't drop.** Gateway activity whose report fails is kept
   and retried for up to 15 minutes, longer than the longest plan idle
   timeout.
6. **Wait for dependencies.** Under a `Type=notify` unit, a daemon whose
   database is unreachable at startup waits while activating, extending
   its start timeout, instead of exiting into the
   `StartLimitBurst=5` restart limit. `FaasStuckActivating` pages after
   five minutes. Configuration errors still fail fast.

### 6. Zero-downtime contract for planned operations

These operations must complete under load with zero failed requests:
- a platform release;
- a compute node drain or reboot;
- a control node reboot;
- a Postgres switchover;
- a Firecracker or kernel upgrade.

Required mechanisms:

- **Lossless instance drain.** Use ADR-208's handoff for every departing
  instance: stop routing to it, collect gateway acknowledgements, wait for
  in-flight requests to reach zero, then migrate or park. Streams get a
  WebSocket close frame or HTTP/2 GOAWAY with a reconnect hint.
- **Non-destructive vmmd restart.**
  - Re-adopt running Firecracker processes and their bridges instead of
    quarantining them.
  - Use the regional branch's native provenance and inventory evidence
    (757–783) as the proof of ownership that re-adoption requires.
  - This supersedes ADR-472's restart quarantine.
- **Listener continuity.**
  - gatewayd-internal and schedd become socket-activated.
  - gatewayd-public retries requests that were refused before any byte
    reached a guest.
  - WakeGate waiters get `503 Retry-After`.
  - `TimeoutStopSec` matches each daemon's drain budget.
- **Snapshot continuity.**
  - guest-init moves out of drive0, or becomes versioned so it is
    backward compatible.
  - Firecracker staleness is scoped to the node that changed. Phase 1
    removes imaged's fleet-wide startup sweep: imaged runs on every
    compute node, so one upgraded node retired every node's snapshots.
    Wake already refuses a snapshot of another Firecracker version in
    both schedd and vmmd, and a new capture now replaces a live row of
    another version. Follow-up: stamp `fc_version` from the capturing
    vmmd rather than schedd, record each node's version from its
    heartbeat, and check the target node's version at wake.
  - Upgraded nodes re-snapshot hot apps before traffic is routed to them.
- **Schema discipline.**
  - Expand/contract migrations, with a linter for `CREATE INDEX
    CONCURRENTLY`, `lock_timeout` and deferred drops.
  - A CI job running the previous release's binaries against the new
    schema.
  - One migration runner per release.
  - The branch's `BEFORE TRUNCATE` triggers on `apps` and `deployments`
    stay compatible with test helpers and migrations.
- **Version skew.** `buf breaking` on every proto, and capability
  negotiation between daemons.
- **Release safety.** Each release canaries on one node per cell, with
  automatic rollback on SLO burn.

### 7. Scaling rules

**Within a cell**, the initial target is 50 compute nodes, 50,000 apps and
5,000 requests/s, validated by load tests:

- pgbouncer everywhere (§3), turned on by the scaling roadmap's Stage 1
  (§9).
- meterd samples in pages under its lease. The branch's signed usage
  minutes are produced from the same sealed minute, aligned with ADR-790
  closed-minute catch-up.
- gatewayd-public routes each app to its owner node's gatewayd-internal.
  The owner schedd hands a WAKING instance to a second caller instead of
  starting another.
- `control_plane_change_log` becomes a sequenced log read by cursor, with
  `pg_notify` as a hint only.
- Each snapshot is replicated to two nodes in different zones, not to
  every node in the region.
- `usage_minutes` and `audit_log` are partitioned.
- Single-box constants such as the 38,080 MB eviction threshold in
  `pkg/sched/reaper.go` are removed.

**Beyond a cell**, add cells. A cell is the unit of scale and blast radius.

### 8. Launch topology (owner decision 2026-10-14)

The public release moves from the GCP beta to OVH bare metal about one month
after this decision. The three-node cell in §2 and §3 does not fit that
window, so the cell launches with one control node and a warm standby on a
second provider. Tracking: epic #4700.

- **OVH, one cell:**
  - one control node on a small bare-metal machine. It runs Postgres, apid,
    schedd, meterd, imaged, Prometheus and the guaranteed builder VM, so it
    needs KVM, NVMe and at least 32 GB of RAM;
  - two compute nodes (OVH Rise S), on the same vRack private network as
    the control node;
  - the public edge for apps (a Cloudflare Tunnel connector and
    gatewayd-public) runs on the two compute nodes (ADR-1343). The control node serves only the
    platform hosts. On the GCP beta the 2-vCPU control plane saturated at
    about 157 req/s with the edge on it, while the compute nodes sat
    75–90% idle (scaling roadmap, measured 2026-10-14).
- **GCP, warm standby outside the cell:**
  - a Postgres async streaming replica of the OVH primary over an
    encrypted link, plus the second pgBackRest repository;
  - the control-plane daemons installed but stopped;
  - it is not in the manifest's `control_plane` group, so the singleton
    leases stay off (§4) and it never serves traffic while the OVH node is
    up.
- **Failover is manual**, by runbook: promote the replica, start the
  daemons, point the platform hosts' Cloudflare origin and the compute
  nodes' control-plane endpoints at GCP. The app edge on the compute
  nodes keeps serving warm traffic throughout (§5).
  Targets: RPO of seconds (replication lag) and RTO of about 15–30 minutes.
  The runbook is drilled before launch.
- **Replica implementation** (2026-10-16):
  - The manifest's `postgresql.standby` (`name`, `address`, `ssh_host`)
    declares the standby. It is rendered as its own `postgres_standby`
    inventory group and converged by `deploy/ansible/postgres_standby.yml`.
  - Replication is plain physical streaming. The primary holds a
    `faas_standby` slot capped by `max_slot_wal_keep_size`, and admits the
    `replicator` role from the standby's overlay address only.
  - The standby's `pg_basebackup` seed refuses to wipe a cluster that may
    be a promoted primary.
  - The second backup repository is a separate decision; the replica does
    not depend on it.
  - Alerts: `FaasPostgresStandby*`; runbook:
    `docs/runbooks/FaasPostgresStandby.md`.
- **Why not two control nodes (OVH + GCP)?**
  - etcd and Patroni need three members to survive losing one. With two,
    losing either stops automatic failover, and a cut between the
    providers can leave both sides acting as primary.
  - Synchronous replication would put the cross-provider round trip on
    every commit; asynchronous replication with automatic failover can
    lose writes.
  - apid on GCP would reach the OVH database across the internet for every
    query.
  - The manifest rejects two control nodes (one, or an odd number of at
    least three).
- **Next step to automatic failover** (after launch): two OVH control
  nodes running Postgres plus an etcd-only witness on GCP as the third
  vote. Then the full §2/§3 cell.
- **Capacity check before ordering:** the RAM admission ceiling (47,600 MB
  per node, `pkg/api/limits.go`) assumes a 64 GB compute node. A Rise S
  with less RAM needs its own per-node budget, and admission must hold
  back the CPU the edge daemons use (ADR-1343 §4).

### 9. Relation to the scaling roadmap (2026-10-15)

The scaling roadmap ("Gregale scaling roadmap", 2026-10-15) removes the
throughput limits: control-plane work off the request path, then a split
control plane, then cells. Its branches so far are
`perf/thaw-timing-data`, `perf/wake-dedupe-reads`,
`perf/wake-pipeline-reads`, `perf/batch-request-id-journal` (ADR-1340) and
`design/rate-limit-token-leasing` (ADR-1341). The two plans overlap, so
each topic has one owner:

| Topic | Owner |
|---|---|
| Request path: batched request-ID journal (ADR-1340), local rate-limit allowance (ADR-1341) | Scaling roadmap |
| Public edge on every compute node, DNS split, node-local gateway routing | This ADR's work, designed in ADR-1343. The owner moved it from the scaling roadmap's Stage 1 on 2026-10-15. |
| PgBouncer | Scaling roadmap (Stage 1) |
| Wake-path reads, the schedd owner cache, the frozen fast path | Scaling roadmap |
| Hot/intent database split, read replicas, telemetry store, the sharded scheduler | Scaling roadmap (Stage 2) |
| Static stability during a Postgres outage (P1) | This ADR |
| Control-plane HA and failover: the §8 standby, then a witness, then the three-node cell | This ADR. It is the scaling roadmap's Stage 2 "make the control plane highly available". |
| Singleton leases, node leases and fencing epochs (§4, P2–P3) | This ADR |
| Zero-downtime operations, including withdrawing a node's tunnel connector before a drain (P4) | This ADR |
| Cells, the directory and multi-region (§2, P6) | This ADR. The scaling roadmap's Stage 3 builds on it. |

Interfaces between the two:
- The scaling roadmap's rewrite of gatewayd-public's compute-gateway pool
  builds on P1's last-known inventory and resync (§5).
- Once P3 lands, the owner cache's single guarded wake write also checks
  the node-lease epoch.
- ADR-1343 (edge placement) replaces this ADR's earlier Cloudflare Load
  Balancer design for the app edge.

## Phases and exit criteria

| Phase | Scope | Exit criterion (recorded under `docs/ops/evidence/`) |
|---|---|---|
| P0 · Proof | SLO dashboards. Native drills for partition, zone loss, Postgres failover and node drain, run nightly (wire `CmdFaultInjector.Partition`). Multi-host load test. | Every drill automated, with a baseline recorded |
| P1 · Static stability | §5, `archive_timeout` and WAL-archive alerts, per-node Firecracker staleness. PgBouncer is turned on by the scaling roadmap (Stage 1, §9), not here. | Postgres down 5 min: zero errors on warm traffic; no recovery storm afterwards |
| P2 · Cell HA across zones | §3 Patroni; the public edge on every compute node (ADR-1343); N apid replicas; leases for singletons; monitoring off the database hosts; off-region backups of the database and artifacts; land the branch's cell identity (729) dark | Kill any one node or zone under load: RTO ≤ 60 s, RPO 0. Region-rebuild drill: restore the cell into its paired region within RPO ≤ 60 s and RTO about 2–4 h |
| P3 · Fenced compute | §4 node leases, self-fencing, epochs, rejoin cleanup, keepalives, hysteresis, zone-loss certificate | Partition drill: zero duplicate at-most-once executions; quota never released without stop proof |
| P4 · Zero-downtime operations | §6 | Release, drain and Firecracker upgrade under load: zero failed requests; no cold-boot storm |
| P5 · Scale within a cell | §7 | Load test at the §7 targets within SLO |
| R · Land the regional branch | Renumber its ADRs past the highest number on main, then land it in dark, dependency-ordered PRs. Order: regions, builds, cell identity and directory, image releases, quota protocol, vmmd/sched/fcvm runtime, ingress, provisioning, billing. New apid code goes in `pkg/apid/<domain>` (ADR-982). | Each PR default-off with CI green; no behaviour change while dark |
| P6 · Activate multi-region (EU cell after launch) | EU cell with off-region backups in a second EU region; directory HA (§2), every-edge ingress, the branch's remaining work: sealed usage minutes, meterd wiring, global caps and egress, shared organizations, native KVM regional acceptance. Then lift `CHECK (NOT admission_enabled)`. | Region-loss drill: other cells unaffected; global app keeps serving from the surviving region; the lost cell is restored from off-region backups within the RPO/RTO targets |

**Order** (owner decision):
- P1 → P2 → P3 → P4 in the US East cell.
- R runs in parallel as dark PRs, which stops the regional branch from
  drifting further.
- P6 activates multi-region after launch.

**Public-release gate** (revised 2026-10-14 for the OVH launch, §8):
- P1, the §8 launch topology with a passed manual-failover drill to the
  GCP standby, a passed restore from the off-site pgBackRest repository,
  and as much of P4's lossless drain as is ready.
- P2's multi-node code merges dark before launch (singleton leases are off
  on a one-node cell, §4); the three-node cell follows after launch.
- P3 must land before jobs, cron, services or realtime are marketed as
  generally available.

## Consequences

- **Footprint:**
  - At launch, US East has 3 control nodes and at least 6 compute nodes
    (3 zones × N+1). Off-region backups add object storage only, no
    machines. Today the fleet is 1 + 2.
  - At P6:
    - the EU cell adds the same shape plus EU off-region backups;
    - the directory starts inside the US East cluster, and later moves to
      3 nodes of its own.
  - The financial-model addendum (CLAUDE.md, source of truth 3) must
    price this before P2 hardware.
- **Operations:**
  - Patroni, etcd and pgBackRest join on-call, with runbooks and monthly
    drills.
  - Control nodes stop co-hosting tenant VMs. §13's RAM ledger gains a
    control-node budget.
- **Code:**
  - Gateways: last-known-good state and stale tiers.
  - Notify hub: resync on reconnect.
  - Leases and node fencing in `pkg/state` and `pkg/sched`.
  - vmmd re-adoption on top of the branch's provenance evidence.
  - Instance drain, reusing the ADR-208 handoff.
  - guest-init packaging.
  - Migration tooling.
  - gatewayd-public affinity routing and retry.
  - meterd paging.
  - Ingress snapshot push to every cell.
- **Superseded:**
  - Spec §16 "maintenance windows" and "FSN + HEL pair at Gate A".
  - ADR-083 as the HA mechanism.
  - The ADR-137 heartbeat-gap recovery trigger.
  - ADR-472's restart quarantine, once P4 re-adoption ships.
- **Amended (regional branch, at landing):**
  - The 717/718 certificate moves to zones and to lease liveness.
  - The 784–786 single-node ingress becomes every-edge.
  - The 786 publication journal runs under a lease.

## Rejected alternatives

- **Managed Postgres (Cloud SQL, AlloyDB).** It ties the control plane to
  one cloud, while Gregale must deploy on any bare-metal x86_64 host
  (spec preamble). Owner decision on 2026-10-13: self-run.
- **One cluster stretched across regions.** Inter-region commit latency on
  every wake, and one database for every region's partitions. The
  regional branch already chose independent cells.
- **Distributed SQL (CockroachDB, Yugabyte, Spanner).** A rewrite of 652
  tables, the sqlc layer and the `pg_notify` contracts, with no
  availability gain inside a region over Patroni.
- **etcd or Consul for application leases.** The fencing check must
  commit with the fenced write. etcd stays private to Patroni.
- **Dedicated etcd nodes from day one.** They give cleaner isolation, but
  add three machines per deployment. Co-location with a separate disk and
  I/O priority covers launch, with a size and latency trigger for moving
  later.
- **`pg_upgrade --link` for major upgrades.** A write pause of minutes
  means parked apps cannot wake. On a scale-to-zero platform most apps
  are parked, so this breaks the zero-downtime contract.
- **Multi-region before launch.** It is the longest path to launch, and
  it puts the least-qualified surface live on day one.
- **Warm standby cluster per region at launch.** It cuts region recovery
  from hours to about 30 minutes. But it needs always-on machines and
  failover drills for a rare event. It is the upgrade path when an SLA
  requires it.
- **Zones only, with backups kept in-region.** Zones share a region's
  control plane, network and storage. A regional outage would mean
  waiting it out, and a permanent loss of the region would lose data.
- **Releasing quota on lease expiry.** It would break the branch's
  guarantee that capacity is released only by proof of absence.
  Conservative charging during partitions is the accepted cost.
- **ADR-083's lexicographic-minimum DNS election.** It depends on the
  database it is meant to survive, and it is active-passive.
- **Maintenance windows (spec §16 lean).** Incompatible with the
  zero-downtime target.
- **Redesigning regional tenancy, the directory or billing here.** The
  regional branch already decides them. This ADR only adds availability
  properties.

## Owner decisions (2026-10-13)

| Question | Decision |
|---|---|
| Regions | US East first (production today), and it is the directory's home. EU cell after launch. |
| etcd placement | Co-located on the control nodes, on its own disk and with I/O priority. Moves to dedicated nodes on a size or latency trigger. |
| Major Postgres upgrades | Logical-replication blue/green, built before the first major upgrade. The replica-identity CI check starts now. |
| Order of work | Single-region HA (P1–P4) first, the regional branch dark in parallel (R), multi-region activation (P6) after launch. |
| Region loss | Off-region backups only, in a second US region: the database's second pgBackRest repository plus replicated cold-boot artifacts. No warm standby. RPO ≤ 60 s, RTO about 2–4 h. |

**To confirm:** which second US region holds the backups. This is a
storage location only; no machines run there.

### Revised 2026-10-14

| Question | Decision |
|---|---|
| Provider | Public release on OVH bare metal, about one month out. The GCP deployment stays the beta until then. |
| Launch topology | §8: one OVH control node and two OVH Rise S compute nodes in one cell, plus a GCP warm standby outside the cell. |
| Region loss | Replaces "off-region backups only": the GCP standby holds an async Postgres replica and the second pgBackRest repository, with manual failover (RPO of seconds, RTO about 15–30 min). |
| Regions | The first cell is the OVH launch location (to confirm). US East applied while production ran on GCP. |

**To confirm:** the OVH datacenter, and the RAM of the Rise S and the
control-node machine.

### Revised 2026-10-15

| Question | Decision |
|---|---|
| Overlap with the scaling roadmap | One owner per topic (§9): the scaling roadmap owns the request path and PgBouncer; this ADR owns static stability, control-plane HA, leases and fencing, zero-downtime operations, cells and the app edge on compute nodes (ADR-1343). |
| App edge at launch | On the two OVH compute nodes (§8), replacing this ADR's earlier Cloudflare Load Balancer design. |
