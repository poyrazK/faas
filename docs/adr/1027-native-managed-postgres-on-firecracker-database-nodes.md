# ADR-1027: Native managed PostgreSQL on Firecracker database nodes

Proposed, 2026-10-12. Internal build; not customer-visible until the
qualification gate in "Phasing" passes.

## Context

Managed PostgreSQL is served by the Neon adapter behind the provider boundary
of ADR-155. Two facts make that the wrong long-term backend:

- **Economics.** At the canary's Neon rates (10,600 millicents per CU-hour,
  ~$0.39 per GiB-month of storage, ~$0.22 per GiB-month of history) the
  bundled storage alone exceeds plan revenue at full use for Pro (150 GiB,
  ~$58 vs €29) and Scale (1,000 GiB, ~$387 vs €99). A typical small database
  (3 GB, active ~8 % of the time, the activity measured for production apps
  over 7 days in October 2026) costs ~€2.49 per month on Neon. One OVH
  ADVANCE-2 with 4×1.92 TB NVMe (~€386/month) is estimated to hold
  1,000–2,000 such databases as microVMs, about €0.20–0.50 each.
- **Latency.** Neon runs in another provider and region. From production-us
  compute every round trip to it costs ~34 ms and a new TLS connection ~106 ms
  before authentication. Compute is moving to OVH bare metal; databases on
  the same private network answer in under a millisecond.

The owner's requirements: no shared Postgres processes between tenants
(VM-grade isolation), storage and compute on the same node for now,
scale-to-zero, and good tenant density.

## Decision

Add a second managed-PostgreSQL backend, driver `native`, that runs **one
Firecracker microVM per database on dedicated database nodes with local
NVMe**. Neon stays registered; placement is pinned per database (ADR-155), so
existing databases are unaffected and new databases use whichever backend the
operator makes default for their region.

### Database node and VM shape

- New node role `database`. It runs `vmmd`, `pgproxyd` and the node agent
  functions below. Database nodes never run app or builder VMs.
- Every database is its own microVM, launched through jailer like every other
  guest (unique uid/gid, chroot, seccomp, cgroup v2 scope).
- **No network interface.** The guest has vsock only: client sessions enter
  over vsock from `pgproxyd`, and WAL leaves over vsock to the host. A
  compromised guest cannot reach any network, other guests or object storage.
- drive0 is the shared read-only base (guest kernel userland, PostgreSQL
  major-version binaries, `guest-init`). drive1 is the database's
  **persistent volume**, attached with Firecracker `cache_type: Writeback` so
  guest flushes reach the host (the default `Unsafe` ignores them).
- virtio-rng is always attached. The free-page-reporting balloon (ADR-907)
  and the shared pmem base (ADR-908) are used where available.
- Postgres runs as `guest-init`'s child in a new database boot mode. The
  database accepts connections only on its local socket; `guest-init` bridges
  one vsock port to it.

### Scale to zero without snapshots

Database VMs are **never memory-snapshotted**. Park is `CHECKPOINT`, a fast
Postgres shutdown and VM exit; wake is a cold boot. This avoids the userspace
RNG replay of restored guests (ADR-481), any mismatch between a memory image
and a volume that moved on, and snapshot disk cost. Invariant 3 holds
trivially: a database is always cold-bootable from drive0 plus its volume.
Cold-boot latency is engineered instead: minimal boot arguments, `guest-init`
starting Postgres directly, a clean checkpoint at park, and later a small pool
of freshly booted, never-reused spare VMs that adopt a tenant volume at wake.

### Persistent volumes

`vmmd` owns the volume lifecycle (create, attach, detach, resize, delete,
move) alongside the VMs that use them; no other component touches Firecracker,
jailer or volume files. Volumes are bound into the jail chroot, never copied
into the tmpfs jail root. The first backend is a sparse file per database on
the node's data filesystem; ZFS (per-volume quota, compression, encryption,
snapshots and send/receive for moves) follows as a second backend.

### Ownership

- `apid` remains the only writer of customer intent (`managed_postgres_*`
  tables) through the `native` driver.
- `schedd` owns the database runtime state machine (parked, waking, running,
  parking) and admission against the database node's RAM ceiling, exactly as
  it does for app instances. Wakes are requested from `schedd`; nothing calls
  `vmmd` around it.
- `pgproxyd` is the only public listener on a database node. This amends the
  rule that `gatewayd-public` is the only public listener on a node: the rule
  becomes one public listener per node role (`gatewayd-public` on compute and
  single-box nodes, `pgproxyd` on database nodes).

### Connection proxy

`pgproxyd` terminates TLS 1.3, routes by SNI (`<database>.db.<domain>`),
authenticates the client with stored SCRAM-SHA-256 verifiers **before**
requesting a wake (unauthenticated traffic never boots a VM), rate-limits per
database and per source, and splices the session to the guest over vsock.
Transaction pooling follows later.

### Durability, metering and security

- WAL is shipped over vsock to the node and on to object storage; base
  backups and point-in-time restore into a new database follow (ADR-155,
  ADR-677 restore proofs). Standard databases accept up to the archive
  interval (60 s) of loss on node failure; a production class adds a standby
  VM on a second node.
- Metering maps cgroup CPU time to `compute_unit_seconds`, volume bytes to
  `storage_byte_seconds` and proxy bytes to `egress_bytes`, so the existing
  usage ledger and guardrails apply unchanged.
- Customers get no superuser. The existing role model (ADR-461) applies, with
  an extension allowlist. Free and unverified accounts are placed on separate
  database nodes from paying accounts.

### ADR-066 amendment

Application compute stays stateless and the stateful base-image denylist is
unchanged: customers still cannot deploy PostgreSQL images as apps. Database
nodes are the one sanctioned stateful workload, operated by the platform.

### vmmd surface

A database node enables database VMs with a `[database_vms]` table in
`vmmd.toml` (`kernel_path`, one base per major under `bases`, optional
`jail_base` default `/srv/fc/dbjail`, `volume_root` default
`/srv/db/volumes`, `parent_cgroup` default `faas.slice/faas-db.slice`).
Without it the database RPCs return UNIMPLEMENTED. vmmd exposes
`CreateDatabaseVolume`, `DeleteDatabaseVolume`, `StartDatabase`,
`StopDatabase` and `DatabaseControl` (status and bootstrap only; stop always
goes through `StopDatabase` so the jail is torn down with the VM). Database
jails take their uid from the same allocator as app VMs, live under their
own chroot base so the app-VM orphan sweep never touches them, and are
recovered by jail id after a vmmd restart.

### schedd surface

schedd's database scheduler (`pkg/dbsched`) owns `database_vms`, the runtime
state of each native database (provisioning, parked, waking, running,
parking, deleting, failed), and reads `database_nodes`, the database-node
inventory that app placement never reads. Placement picks the active node
with the most free volume capacity; wake admission charges the VM's billable
memory against the node's admission ceiling. Both run in a transaction that
locks the node row, so concurrent schedd replicas cannot overcommit a node.
Running databases idle past `idle_timeout` are parked by a sweep; a restart
settles databases left waking or parking.

The superuser has no password: it authenticates only by peer inside the
guest, so the control plane manages roles through the host-only `admin_sql`
control op and no superuser secret is stored anywhere.

schedd exposes `ProvisionDatabase`, `WakeDatabase`, `ParkDatabase`,
`TouchDatabase`, `DatabaseAdmin`, `DeleteDatabase` and `GetDatabase`.
Provision, delete, admin and get accept only apid's client certificate;
wake, park and touch also accept pgproxyd. A `[database_scheduler]` table in
schedd's config enables the scheduler and lists the database nodes.

## Phasing

1. Walking skeleton on one internal database node: create, connect through
   the proxy, idle park, wake on query, delete.
2. Durability: WAL shipping, base backups, restore into a new database.
3. Metering and guardrails.
4. Density and wake: spares, admission ceiling, wake-storm queueing, classes.
5. Operations: volume moves, node evacuation, node-loss restore, majors 14–18.
6. Qualification: security review, `QualifyProvider` pass, then default for
   new databases and a migration path off Neon.

Development may use a nested-virtualization VM; acceptance runs only on
native x86_64 KVM database nodes.

## Consequences

- Gregale operates stateful infrastructure for the first time: backups,
  restore drills and node-loss runbooks become ship-blocking.
- A database is pinned to the node holding its volume until moved.
- Self-hosting only beats Neon above roughly 300 databases on a two-node
  fleet; until then Neon stays cheaper in raw infrastructure.

## Rejected alternatives

- Shared Postgres server or per-tenant processes on a shared kernel: rejected
  by the owner for tenant isolation.
- gVisor: poor fit for Postgres's fork-per-connection and fsync-heavy I/O.
- Separating storage from compute (Neon-style pageserver): the best long-term
  economics, but out of reach today; the volume abstraction keeps it possible.
- Memory snapshots for wake: RNG replay, volume consistency and disk cost.
