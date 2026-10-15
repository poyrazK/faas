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
`<database>-pooler.db.<domain>` is the same database through a transaction
pool (see "Transaction pooling").

### Durability, metering and security

- WAL is shipped over vsock to the node and on to object storage; base
  backups and point-in-time restore into a new database follow (ADR-155,
  ADR-677 restore proofs). Standard databases accept up to the archive
  interval (60 s) of loss on node failure; a production class adds a standby
  VM on a second node.
- Metering bills allocation, not sampled consumption, like app billing
  (plan RAM per running second): a running VM's vCPUs to
  `compute_unit_seconds`, the provisioned volume to `storage_byte_seconds`,
  archive bytes to `history_byte_seconds` and proxy bytes to
  `egress_bytes`, so the existing usage ledger and guardrails apply
  unchanged. See "Usage metering" below.
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

### Wake admission and wake storms

- **Boot slots.** Each database node has `boot_slots`
  (`database_nodes.boot_slots`; schedd `[[database_scheduler.nodes]]
  boot_slots`, default 4, roughly its physical cores). `AdmitWake` refuses a
  wake while that many of the node's databases are `waking`
  (`ErrBootSlotsBusy`), in the same node-locked transaction as the RAM
  ceiling, so the cap holds across schedd replicas. A storm of wakes then
  boots in waves instead of starving every boot of CPU. Provisioning and
  restore boots take no slot.
- **Queueing.** A wake the node cannot admit yet (RAM ceiling or boot
  slots) waits in schedd for up to `wake_queue_wait` (30 s), retrying with
  jittered backoff from 20 ms to 500 ms. Waiters retry independently, so
  admission is not first-come-first-served. Past the wait the wake fails
  `ResourceExhausted` and pgproxyd answers the client `57P03`; pgproxyd's
  own wake timeout (1 min) bounds the whole wait.
- **Idle eviction.** Over the RAM ceiling, the wake first parks the node's
  longest-idle scale-to-zero database untouched for `evict_idle_after`
  (2 min; negative disables it). pgproxyd touches a database every 30 s
  while a session is open, so this parks only databases nobody is connected
  to: the park the idle sweep would make later, made early. Always-on and
  resize-pending databases are never evicted. Neither is one whose lock is
  held (a backup, a resize, another wake), and each candidate is checked
  again under its lock.
- A wake that leaves `waking` (running or failed) and a park signal the
  node's queued wakes in the same process, which retry at once; the backoff
  covers other replicas.
- Metal (`TestMetalWakeStorm`, six 384 MiB databases on the shared 2-vCPU
  nested test host; compare within a run). Woken at once, all six were up
  in 14.8 s, every wake taking ~14.6 s. With two boot slots, never more
  than two booted at once, and all six were up in 9.7 s, median wake
  7.0 s. Before the in-process signal, polling alone left idle gaps
  between waves: 13.6 s against 10.3 s unbounded in that run. Over the
  RAM ceiling, a wake parked the database idle for ten minutes and was up
  in 1.7 s.

### Proxy surface

`pgproxyd` (`pkg/pgproxy`, `cmd/pgproxyd`) accepts PostgreSQL over TLS only
(SSLRequest or PostgreSQL 17 direct TLS), routes by SNI
`<database-id>.<host_suffix>` or an `endpoint=<id>` startup option, and runs
SCRAM-SHA-256 with the client against the role's verifier from
`database_role_verifiers` before anything is woken. An unknown database or
role gets a deterministic decoy verifier, so it fails exactly like a wrong
password (`28P01`) and wakes nothing. After the client proves its key, the
proxy wakes the database through schedd (`WakeDatabase`, which also returns
the node's vmmd target), opens a `DatabaseSession` stream through vmmd to
the VM's data port, and logs in to PostgreSQL with the proven ClientKey
(SCRAM pass-through). Passwords never reach the proxy or the control plane:
the driver hashes each role password into a verifier, sets that verifier in
PostgreSQL through `admin_sql` and stores it for the proxy.

vmmd's database lifecycle RPCs accept only schedd's certificate and
`DatabaseSession` only pgproxyd's, so the proxy can reach a database's data
port but never its control port.

pgproxyd caches verifiers briefly. A re-issued password has a new salt, so
a stale cached verifier cannot be retried within the handshake; the proxy
drops the cached entry after any failed login and the next attempt reads
the current verifier.

### Transaction pooling (phase 6)

A native database allows 25 sessions. A FaaS app's instances each keep a
client pool, so on Neon runtime credentials use its pooled endpoint
(pgbouncer, transaction mode). Native offers the same:

- `<database-id>-pooler.<suffix>` (covered by the wildcard certificate;
  `options=endpoint=<id>-pooler` without SNI) authenticates the client
  exactly as the direct endpoint does, before anything wakes. It then
  never gives the client a PostgreSQL session of its own.
- **Pools.** A pool is one role's logins to one database with one verifier
  (a rotated password starts a new pool). It keeps the ClientKey a client
  proved, to log further server connections in; the key is forgotten
  once the pool has neither clients nor servers.
- **Borrowing.** A client borrows a server from its first message until
  PostgreSQL reports the session idle (`ReadyForQuery 'I'`) with no answer
  still owed, so a transaction keeps its server. A client that leaves
  mid-transaction gets its server closed, which rolls the transaction
  back, rather than returned.
- **Prepared statements.** Named protocol-level prepared statements (pgx
  caches them; psycopg prepares after five uses) are renamed by content
  (`gregale_<hash>`), prepared again on whichever server runs them, and
  answered in order through a queue of expected responses, as pgbouncer
  1.21+ does. A client's Close only forgets its name. A server holding over
  1000 statements is closed when released.
- **Limits.** At most `pool_max_servers` (default 10, at most 20) servers
  per database. A role waiting at the cap reclaims another role's idle
  server. A transaction waits up to `pool_wait` (30 s), then the client gets
  53300. Servers idle for `pool_server_idle` (1 min) close, so a database
  nobody uses can park; open servers touch it and report pooled egress.
- **Not shared:** session state (`SET`, `LISTEN`, advisory locks, SQL
  `PREPARE`, unnamed statements across a Sync) and startup parameters
  other than the role, database and drivers' cosmetic ones (which must
  match server defaults). Others are refused with a pointer to the direct
  endpoint. Cancel requests work on both endpoints (see Query
  cancellation).
- The native driver returns both endpoints; credential delivery gives
  runtime, read-only and Data API credentials the pooled one and
  migrations the direct one, as on Neon. `PooledConnections` is now true.
- **Tests.** Against PostgreSQL: 30 pgx clients × 20 transactions over at
  most 3 servers, transactions never moved between servers, rollback on
  abandon, errors in extended batches, COPY in and out, multi-statement
  queries, cap sharing and the 53300 timeout, idle closing, refusals.
  Metal (through vmmd to a VM): 40 pooled clients ran 800 queries in
  0.4 s, while 28 of 40 direct sessions were refused at the database's
  limit.

### Query cancellation

A client cancels a running query on a new connection that carries the key
PostgreSQL sent at login. pgproxyd serves these on both endpoints.

- **Keys.** Every client gets a key of pgproxyd's own in place of
  PostgreSQL's: a random 32-bit secret and a process id whose top byte is
  the issuing instance's `instance_id`. pgproxyd keeps the key until the
  client leaves.
  - On the direct endpoint, the key maps to the session's backend: the
    node's vmmd target and the key PostgreSQL sent.
  - On the pooled endpoint, it maps to the server the client holds for its
    current transaction, if any.
- **Serving a cancel.** Most drivers send the cancel in plaintext; libpq 17
  and later, and pgx for an encrypted session, send it after TLS. Both are
  accepted. The cancel carries no credentials. pgproxyd opens a session
  stream through vmmd, sends PostgreSQL the backend's own cancel and waits
  until PostgreSQL closes it, by which time the backend is signalled. It
  never wakes a database. The client's connection is then closed, as
  PostgreSQL closes it.
- **Pooled safety.** A pooled client returns its server only after any
  cancel in flight for it has been delivered. A cancel therefore never
  reaches another client's transaction. A cancel that reaches an idle
  backend is ignored by PostgreSQL.
- **Several instances.** The app route rotates over pgproxyd upstreams, so
  a cancel can reach another database node's pgproxyd. The key's top byte
  names the issuing instance. Its peers forward the cancel to it over the
  overlay (`cancel_peers`, by id) with a private request code. A forwarded
  cancel is only ever handled locally, so a wrong peer list cannot make it
  loop. Unknown keys and unknown instances are dropped.
- **Operations.** pgproxyd's `instance_id` and `[cancel_peers]` are set
  through Ansible (`pgproxyd_instance_id`, `pgproxyd_cancel_peers`);
  `pgproxyd_cancel_requests_total{result}` counts ok, forwarded, unknown
  and failed cancels.
- **Evidence.**
  - Against PostgreSQL: TLS and plaintext cancels on both endpoints, a
    forward between two instances (and a re-forward dropped), and the
    sparing of other clients. With one pooled server shared by two
    clients, the idle client's key and an unknown key cancel nothing.
  - Metal, through vmmd into a database VM: the cancel ends the query
    15 ms (direct) and 11 ms (pooled) after it is sent, and the session
    goes on.

### App route

Apps reach their databases without an egress port on any plan. Neon needed
`egress-ports add 5432`, which only Pro and Scale may declare (ADR-361).
Database traffic to Gregale's own proxy is a platform hop, like ADR-576's
service addresses, so it is not tenant egress. This amends ADR-361 in the
same way ADR-576 did.

```
guest ──connect 198.18.0.1:5432──► netns forward: accept (ahead of every ADR-361/373 rule)
      ──masquerade──► host nat prerouting: dnat to <bridge>:10083
      ──► gatewayd-internal forwarder (pkg/dbroute)
          SO_ORIGINAL_DST must be 198.18.0.1:5432; per-source and per-node session caps
      ──overlay──► any pgproxyd ──► schedd wake ──► vmmd DatabaseSession ──► VM
```

- **Address.** `api.DatabaseRouteAddress` is `198.18.0.1`. It sits in the
  RFC 2544 block, in the half ADR-576 does not use, and is the same on every
  node, so an address cached inside a snapshot stays valid after a restore
  elsewhere. Only TCP 5432 is admitted. The host drops everything else
  addressed to it, so nothing sent there ever leaves the node.
- **DNS.** The guest resolver answers `<database-id>.<host_suffix>` (one
  label under the suffix) with the address for 60 s, and AAAA with nothing.
  The connection string is the same one external clients use, so TLS
  `verify-full` against pgproxyd's wildcard certificate and SNI routing work
  unchanged. Deeper names and the suffix itself are still forwarded upstream.
- **Forwarder.** It relays bytes only. TLS, SNI routing and SCRAM stay end
  to end between the client and pgproxyd, so the forwarder holds no
  credentials and cannot read a session. Any pgproxyd serves any database,
  so the forwarder tries its configured upstreams in rotation and fails
  over on a dial error. Caps: 256 concurrent sessions per instance and 8,192
  per node (`pkg/api/limits.go`). If one direction ends and the other
  stalls, both are closed after 10 s.
- **Why not allow 5432 to the database nodes' public addresses.** With a
  tenant egress gateway (ADR-372) that traffic would leave through the
  WireGuard gateway and come back. Every database connection would then
  depend on the gateway, pay two extra hops, and carry the gateway's
  address. The overlay keeps it on the platform network.
- **Rollout.** There are three switches, enabled together per node:
  - vmmd `[compute_node] database_route_enabled`: netns admission and the
    runtime host NAT;
  - the nftables role's `faas_database_route_enabled`: the boot-time ruleset,
    kept byte-identical by `make egress-check`;
  - gatewayd-internal `database_route_listen`, `database_route_upstreams`
    and `database_route_host_suffix`.

  New namespaces carry the admission. A prepared namespace from before the
  switch no longer matches the requested config, so it is never reused.
  Running instances pick the admission up on their next wake. Until then a
  guest's connection fails closed: the resolver already answers with the
  route, but the old namespace drops it.
- **Database nodes.** pgproxyd must accept connections on the overlay from
  compute nodes. Its default `listen_addr` (`:5432`) already does; the
  database node's host firewall must allow the overlay to reach 5432.

Evidence:
- **Unit tests:** netns rule order (the route precedes the fan-out, DNS-gate,
  allowlist and port-policy rules), the host policy and its safety panics,
  Go/Jinja parity (`database-route` rows in the cross-check), the forwarder
  (half-close, destination check, failover, caps), the resolver, and
  gatewayd/vmmd config.
- **Metal, netns:** `TestMetalDatabaseRouteReachesForwarderWithOriginalDestination`
  shows a Free-plan, DNS-gated guest reaching the forwarder with the right
  original destination. Other ports, UDP, the bridge port itself, and
  namespaces without the admission are dropped.
- **Metal, driver:** `TestMetalDriverEndToEnd` connects through the
  forwarder to a parked database in 1.07 s, against 1.01 s direct.

### Deployment

Database nodes are an auxiliary host group, like the ADR-372 egress
gateway: inventory group `database_nodes`, playbook
`deploy/ansible/database_node.yml`, `make bootstrap-database-nodes`. They
are not a fourth manifest box role yet.

- **pgproxyd** is an `OptionalRegistry` unit (`pkg/daemonunitspec`,
  generated into `roles/pgproxyd_service/files`). It is not a core daemon:
  the control-plane and legacy trees skip it.
  - It runs as its own `faas-pgproxyd` user with an empty capability set and
    the platform's sandbox. Its secrets are owner-only files in
    `/etc/faas/pgproxyd/` (public TLS key, decoy key, database URL), and it
    refuses group- or world-readable ones.
  - It is `Type=notify` with a 60 s watchdog. `127.0.0.1:9109` serves
    `/healthz`, `/readyz` (control-plane database ping, schedd connection,
    listener) and `/metrics` (`pgproxyd_sessions_total{result}`,
    `pgproxyd_active_sessions`, `pgproxyd_wake_seconds`).
- **Database access.** pgproxyd reads the control-plane database as
  `faas_pgproxyd`, which may only `SELECT database_role_verifiers`. It never
  uses the fleet-wide `faas` role: it is the one daemon that faces the
  internet. The playbook creates the role on the control plane. The postgres
  role's `faas_postgres_pgproxyd_cidrs` admits it in pg_hba.
- **mTLS.** `pkg/pki` gains a `database` box role. Its leaves are vmmd's,
  under the node identity `<id>.faas`, plus `pgproxyd/{schedd,vmmd}-client`
  with CN `pgproxyd.faas`. Operators issue them with
  `gregalectl pki issue-bundle --box-role database --cn <id>`.
  - schedd and vmmd allowlist `pgproxyd.faas` at the handshake.
  - schedd's database scheduler verifies database-node vmmd certificates
    against its configured node ids. It does not use the `compute_nodes`
    registry, which never lists database nodes.
- **Firewall** (`database_node` role): only TCP 5432 is public. The overlay
  is trusted; SSH is admitted only from listed ranges; forwarding is
  dropped, because database VMs have no network.

- **vmmd in database mode.** With box role `database`, vmmd branches before
  any compute setup into a small path:
  - It never registers in `compute_nodes`, so schedd cannot place an app VM
    on a database node.
  - It builds no tenant bridge, netns or snapshot state.
  - It serves only the six database RPCs; an interceptor refuses every other
    method with `Unimplemented`. mTLS admits `schedd.faas` and
    `pgproxyd.faas` only.
  - It owns the whole jail uid range and its own jail base and cgroup
    (`faas.slice/faas-db.slice`, a declared slice with a hard memory fence).
  - `/srv/db/volumes` is writable through a unit drop-in.
- **Base drives.** `[database_vms.images]` maps a major to a PostgreSQL image
  pinned to its linux/amd64 manifest digest; tags are refused. vmmd builds
  each base on the node, in the background, at
  `<base_dir>/postgres-<major>-<hash(image, guest-init)>.ext4`. A release
  with a new guest-init therefore rebuilds it, while running VMs keep the
  base bind-mounted into their jail. Old bases are pruned. Systemd is
  notified once RPCs are served; `/readyz` waits for the bases.
- **Ansible.** `database_node.yml` installs the signed release (cosign
  verified against the release workflow identity), stages the mTLS bundle
  and firewall, runs the `firecracker` role with the release's signed guest
  kernel, then `database_vmmd`, then `pgproxyd_service`.

Metal: `TestMetalDatabaseNodeMode` runs vmmd in database mode. It served
RPCs immediately, built the PostgreSQL 17 base from the pinned image in
12.2 s, refused a compute RPC, and ran a database from create to delete.

**Manifest role.** Database hosts are fleet hosts with role `database`,
each with a `database` block of scheduler facts (`admission_ceiling_mb`,
`volume_capacity_bytes`, optional `boot_slots` and `postgres_majors`). A
fleet with any database host needs a `managed_postgres` section
(`host_suffix`, `shared_archive`, `app_route`). `gregalectl manifest
ansible` derives the rest from the hosts' addresses:
- **Inventory.** The `database_nodes` group.
- **Each database node:** `database_vmmd_overlay_ip`, the vmmd port, the
  overlay CIDR, pgproxyd's host suffix and schedd target, and its cancel
  `instance_id` (1… in manifest order) with the other nodes as
  `cancel_peers`.
- **Control plane:** `faas_schedd_database_scheduler` (rendered to
  `/etc/faas/schedd-database.toml`; schedd reads it through
  `FAAS_SCHEDD_DATABASE_CONFIG`, and refuses the table in both files),
  and `faas_postgres_pgproxyd_cidrs`.
- **Compute nodes, with `app_route`:** the nftables and vmmd switches
  (`FAAS_VMMD_DATABASE_ROUTE_ENABLED`) and gatewayd-internal's forwarder
  on port 10083 of its service-proxy address, with every pgproxyd as an
  upstream.

A database node has no shared role identity in the private hosts map:
schedd binds its vmmd certificate to `<name>.faas`. A fleet without
database hosts renders exactly as before.

pgproxyd's DSN is derived too: `postgres://faas_pgproxyd:…@<control
plane>:5432/faas`, with the password taken from the Vault variable
`faas_pgproxyd_postgres_password`, which `database_node.yml` also uses
to create the role, so the two cannot drift.

Still manual: pgproxyd's public `*.<suffix>` certificate (files), the
`faas_pgproxyd_postgres_password` Vault value, and each node's PKI bundle.

### Durability: WAL archiving, base backups, point-in-time restore

Database VMs have no network, so WAL leaves and returns over vsock, and
storage is owned by an unprivileged daemon on the database node:

```
guest PostgreSQL --archive_command--> guest-init archive-wal --vsock 5434-->
  vmmd relay (stamps the database id it booted) --unix socket-->
  dbbackupd: fsync to the node-local archive, then upload to object storage
```

- **Identity comes from the host.** vmmd prefixes every relayed stream with
  the database id of the VM it started, plus the restore source when it
  started one. A guest cannot name another database's archive.
- **WAL archiving.** PostgreSQL runs with `archive_mode = on` and
  `archive_timeout = 60s`. `archive_command` is guest-init's `archive-wal`.
  It reports success only after dbbackupd has fsynced the segment on the
  node, so PostgreSQL keeps (and retries) anything not yet durable.
  dbbackupd uploads to object storage afterwards. The upload lag is the
  off-node recovery point, and it is exported as a metric.
- **Base backups.** guest-init's `basebackup` control op runs
  `pg_basebackup -Ft -X none` against the local socket and streams the tar
  to dbbackupd over the same vsock path, which stores it zstd-compressed
  with a manifest parsed from its `backup_label`. schedd takes the first
  one while provisioning, and its backup sweep takes another for any
  running database whose last backup (`database_vms.last_backup_at`) is
  older than a day. A parked database is not woken for a backup: its WAL
  is archived, and the next wake backs it up.
- **Point-in-time restore** always creates a new database, never an
  overwrite (the portable contract). It can take longer than an RPC, so
  `RestoreDatabase` only places it, and schedd restores it in the
  background:
  - Placement is pinned to the source's node, where its base backups are.
    The row records the lineage (`restore_source`, `restore_target`),
    which Inspect reports back as `RestoreLineage`.
  - The job holds a lease in `database_vms.restore_lease_until` (the
    database clock's) and renews it. Any schedd replica resumes a restore
    whose lease lapsed, so a schedd restart does not strand it. Deleting a
    restoring database cancels the job first.
  - Recovery to a time stops only at a commit after it, and PostgreSQL
    fails the recovery when the archive ends first. So the job first wakes
    the source and runs its `flush_wal` op: wait until the guest clock
    passes the target, commit a transaction that takes an xid, switch WAL,
    and wait until that segment is archived.
  - The restore VM boots with `gregale.restore_target` on its kernel line
    and the relay's restore source. guest-init pulls the newest base backup
    whose stop time (from the archived `.backup` history file) is at or
    before the target, extracts it (regular files and directories only,
    never outside the data directory), and recovers with
    `restore_command = archive-wal get` and `recovery_target_time`, then
    promotes. It reports ready only after `pg_is_in_recovery()` is false.
    A boot interrupted before extraction finished starts it over; one
    interrupted later resumes recovery.
  - The restored database archives to its own archive on its new timeline.
    The job takes its first base backup, stops it and parks it
    bootstrapped; later boots are ordinary.
  - The restore carries the source's roles with their verifiers, but
    pgproxyd authenticates against verifiers published per database id, so
    the source's logins do not reach it (`restore_credentials_isolated`).
- **vmmd stays a relay.** It holds no storage credentials and parses no
  backup data. dbbackupd holds the object-storage credentials, the catalog
  and retention.
- **dbbackupd** (`cmd/dbbackupd`, role `dbbackupd_service`) runs on every
  database node as its own user. It serves vmmd's relays on
  `/run/faas-dbbackupd/dbbackupd.sock`, fsyncs to `/srv/db/archive`, and
  uploads WAL and base backups (data before manifest) to an S3-compatible
  bucket under a prefix.
  `dbarchive_pending_oldest_seconds` is the off-node recovery point.
  Enabling it renders `archive_socket` into vmmd.toml, which turns
  archiving on for every database VM booted afterwards.
- Metal (`TestMetalPointInTimeRestore`, nested test node): base backup
  1.8 s, source flush 0.3 s, restore to ready 3.7 s, stops 0.1–0.6 s. The
  restore holds exactly the commits before the target, takes writes, and
  boots normally afterwards.
- **Retention.** Each archive must restore to any instant in
  `max(restore window, min_retention)` (dbbackupd's `min_retention`,
  default 24 h). The window reaches dbbackupd in the relay header, which
  vmmd fills from `StartDatabase`, so the guest cannot change it; dbbackupd
  records it per database. Hourly, for each database:
  - The anchor is the newest base backup whose stop time is at or before
    the period's start. It and every newer backup stay, and so does all WAL
    from its start segment on, compared by segment number across
    timelines. Older backups, segments, partial segments and backup
    history files go. Timeline history files stay.
  - Removal deletes the bucket copy first, so a bucket outage leaves the
    local file for the next pass.
  - With no anchor yet (a database younger than its period, or history
    files not archived), nothing goes.
  - schedd refuses a restore to a point older than the source's window,
    since it may be pruned. Without a window, older points are best effort.
- **Deletion.** Deleting a database deletes its archive: schedd's delete
  asks vmmd for the volume with `archive = true`, and vmmd first sends
  dbbackupd an admin stream (`Header.Admin`, op `drop`) that removes every
  object under the database's bucket prefix and the local directory, then
  deletes the volume. The relay never sets `Admin`, so a guest cannot drop
  an archive. If dbbackupd is unreachable the volume stays and schedd's
  retried delete finishes both.
- **Reads from the bucket.** A restore reads the source's archive locally
  and falls back to the bucket for anything missing, so it works on a node
  that never held the source:
  - The catalog merges local manifests with the bucket's (a bucket manifest
    counts only next to its data). Stop times come from history files read
    the same way.
  - WAL is fetched into an already unlinked temporary file (the protocol
    needs its size first). A base backup streams from the bucket through the
    decoder, never staged, since it can be as large as the volume.
  - Nothing read from the bucket is kept. The only local archives are those
    of databases the node runs, which is what uploads and pruning walk; a
    node restoring someone else's database never prunes it.
  - Metal (`TestMetalPointInTimeRestore`): after the source's local archive
    is deleted, a runtime backed by an empty store restores it from the
    bucket to the same rows.
- **Restore onto another node.** With `shared_archive` set in schedd's
  `[database_scheduler]` (every node's dbbackupd uploads to the same
  bucket and prefix), a restore leaves the source's node when that node is
  full, draining or unavailable. Without it, restores stay on the source's
  node and fail while it is unavailable.
  - A reachable source is flushed as usual, so the restore is exact on any
    node.
  - When the operator marks the source's node `unavailable` (node loss),
    nothing can flush it. The restore boots with
    `gregale.restore_until_end`: if PostgreSQL ends the targeted recovery
    because the archive runs out first, guest-init restarts it once
    without the target, and it recovers to the end of the archive and
    promotes. The loss is bounded by the archive's lag: archive_timeout
    (60 s) plus the upload interval.
  - The guest reports its last replayed commit
    (`pg_last_xact_replay_timestamp`), which schedd records as
    `database_vms.restore_recovered_to` and returns on `GetDatabase`. The
    lineage keeps the requested target, so the portable contract is
    unchanged; the recovered point is operator evidence of the actual
    recovery point.
  - Metal (`TestMetalRestoreOntoAnotherNodeAfterNodeLoss`): node A's
    database is parked and uploaded, A is marked unavailable and its
    archive disk deleted, and the scheduler restores onto node B from the
    bucket in 13 s, to the last commit before the target.
- **Deleting on a lost node.** schedd deletes a database whose node is
  marked `unavailable` without calling that node: its VM and volume went
  with it. With a shared archive any other node drops the bucket copy
  (`DeleteDatabaseVolume` with `archive_only`); with no node left the
  delete fails and stays `deleting` until one returns. Without a shared
  archive nothing else reaches it, and it goes when the node returns.
- **Orphans.** If a lost node comes back, its volumes of databases deleted
  meanwhile have no record. A record always exists before its volume and
  outlives it, so schedd's maintenance sweep (with the backup sweep, every
  10 minutes) lists each node's volumes (`ListDatabaseVolumes`) and
  removes any without a record, stopping its VM first and dropping its
  archive with it. Metal: after a node-loss restore, the original is
  deleted while its node is lost, then the node returns and its volume is
  collected.
- **Pruning a lost node's databases.** Each node's dbbackupd prunes from
  its own disk, so a lost node's databases would keep their bucket archive
  forever. With a shared archive, schedd's maintenance sweep asks a live
  node (`PruneDatabaseArchive`, an admin `prune` stream to its dbbackupd)
  to prune each such database in the bucket, at most hourly, with the
  database's window and that node's `min_retention`. The catalog and the
  files come from the bucket listing. The archive no longer grows, so it
  shrinks to its newest base backup and the WAL after it, which still
  restores to its end. Metal: the lost database's bucket archive is pruned
  before the node-loss restore reads it.

### Driver

`pkg/managedpostgres/firecracker` is the `managedpostgres.Provider` for this
backend (driver name `firecracker`, registered in apid). It reaches schedd
with apid's client certificate and writes verifiers through `pkg/dbcreds`.

- **Provision** asks schedd for a VM of the class's fixed shape (development
  1 vCPU/512 MiB, burstable 2 vCPU/1 GiB), then runs an init batch through
  `DatabaseAdmin`: the `gregale_schema_owner` role, the customer database it
  owns, and PUBLIC locked out of CONNECT/CREATE/TEMPORARY. Inspect derives
  the class back from the VM shape.
- **Credentials** follow ADR-461: one role per binding identity and access
  level (`gregale_rt_`/`gregale_ro_`/`gregale_mig_` + a hash). The driver
  generates the password, sets only its verifier inside the VM and stores the
  same verifier for the proxy; the password exists only in the returned
  `CredentialMaterial`. Migration roles act as the schema owner; runtime and
  read-only roles end every batch with an assertion that they cannot create
  objects (`P0001` fails the batch). Revoke deletes the verifier first, so
  the proxy refuses the role before the VM is woken to disable it.
- **Admission** is strict because the service fails a database whose
  observed spec differs from the requested one. The driver refuses an
  unbounded volume and other regions with `ErrUnsupported`, and a restore
  window unless the backend's settings carry `max_restore_window_seconds`
  (set only where dbbackupd archives). The window round-trips through
  `database_vms.restore_window_seconds`.
- **Restore** calls `RestoreDatabase` and reports the database pending
  until schedd parks it. A resource id that is not a UUID (qualification
  names restores `qualification-restore-<hash>`) maps to a name-based UUID,
  so retries reach the same VM.
- **Always-on** (`scale_to_zero=false`) is a `database_vms.always_on` flag:
  - Idle parking skips the database.
  - schedd's sweep also wakes always-on databases it finds parked. It
    probes the running ones, and when vmmd has no VM for a running record
    (a node reboot: `FailedPrecondition`, recorded as `vm_lost`) it reboots
    it. An unreachable node is left alone rather than restarting a database
    that may be healthy.
  - `Update` switches a database between the two policies in place
    (`SetDatabaseAlwaysOn`), so `ScaleToZeroUpdate` is true. Turning
    scale-to-zero back on restarts the idle clock.
  - The observation carries `DataResourceID` (the database id: the volume
    only this id names), which the service requires for compute-policy
    changes.
  - Metal: `TestMetalAlwaysOnKeepWarmAndLostVM` checks the database is never
    idle-parked, and that a VM stopped behind the scheduler's back is
    detected and rebooted in 1.4 s.

- **Deterministic credentials.** The contract makes issuance idempotent:
  re-issuing an identity must return the same login, and qualification
  checks it.
  - The password and the SCRAM salt are keyed HMACs of (database, identity,
    access) under a driver key (`secret_env["credential-key"]`, at least 32
    bytes, sealed in apid's env). A re-issue is therefore byte-identical:
    the same password and the same verifier, so pgproxyd's cached verifier
    never goes stale.
  - Rotation is a new identity key, hence a new role. No password is ever
    stored. Compromising the key compromises every derived password, so it
    is sealed like the other provider secrets.
  - Every role carries the marker comment
    `gregale:credential:v1:<scope>:<access>`. The scope is shared by all
    roles of one database. The Data API starter's migrations find their
    `data_api` login from their migration login's marker, as on Neon.
- **Data API credentials (ADR-650).** A backend with `data_api_enabled`
  issues `data_api` logins and advertises them. The login is
  `gregale_api_<hash>`:
  - It holds DML on schema `api` and nothing else. `api` belongs to the
    schema owner and is created on first issue; a foreign owner is
    refused. Default privileges extend the DML to future api tables and
    sequences.
  - Its one setting is `statement_timeout=15000` (the Data API timeout);
    the starter refuses a login with others. It has no memberships.
  - The batch's closing assertion fails the issue (`ErrConflict`) if the
    role could create objects, reach rows outside `api`, own anything or
    run a SECURITY DEFINER function in `api` or `public`.
  - Its binding gets the pooled endpoint. The Data API sets its JWT
    claims per transaction, so the claims never reach another client's
    transaction on a shared server connection.

  Neon's SQL contract check (`VerifyDataAPICredentialProbe`) now lives in
  `managedpostgres`, and both backends' qualification uses it.

  Evidence:
  - PostgreSQL 16: the role model test checks the marker, the single
    setting, no memberships and the refusal when public rows are readable.
    `TestQualifyProviderPasses` passes all 28 checks.
  - Metal: `TestMetalQualifyProvider` passes the same 28 checks through
    pgproxyd on database VMs (15.5 s).
  - Metal: `TestMetalDataAPIThroughThePooler` follows the starter's order
    (migrations create `api`, then the login is issued). Two pooled
    clients ran 100 transactions in 351 ms. Neither saw the other's claims
    at transaction start, RLS held, and pooled sessions run with the 15 s
    timeout.
  - A transaction-local claim leaves the setting `''`, not NULL, on that
    server session. `''::jsonb` fails, so clients must set claims on every
    request, as PostgREST does. On Neon's pooler it is the same.
- **Qualification.** The driver implements all four probers that
  `QualifyProvider` requires of this capability set.
  - Probe connections go through pgproxyd with issued credentials, as an
    app's would.
  - `ProbeScaleToZero` parks the database through schedd and measures the
    wake on the next connection.
  - `ProbeCredentialPrivileges`, `ProbeReadOnlyCredentials` and
    `ProbeDataAPICredentials` follow the shared evidence flow: runtime
    confinement, row-level security, migrations as the schema owner,
    revocation ending sessions and logins, and data surviving rotation.
  - `ProbeComputePolicy` switches the database to always-on and back.
  - Fixtures are dropped as the superuser through `DatabaseAdmin`.
  - `PrepareRestore`, `VerifyRestore` and `CleanupRestore` write a marker
    into a schema-owned fixture, pick a point a whole second after it, and
    overwrite it after the point; the restore must hold the first marker.
    `VerifyRestoreCredentialIsolation` points the source's login at the
    restore and requires pgproxyd to reject it.
  - `TestQualifyProviderPasses` runs all checks (28 with the Data API
    on) against PostgreSQL 16;
    `TestMetalQualifyProvider` runs them against real database VMs, and
    `TestMetalQualifyProviderWithRestore` adds the restore checks.

### Resize

- **Class and storage** change in place through `Update`; the major,
  region and restore window are fixed. Storage only grows: shrinking ext4
  under a database is not supported. `ClassResize` is advertised when the
  backend has more than one class, and `ProbeComputeResize` qualifies it
  (to the other class and back, data, credentials and replay preserved).
- **schedd `ResizeDatabase`** records the new shape if the node fits it
  (free volume capacity, and RAM while the VM is active) and marks it
  `resize_pending`, moving the storage ledger to the new size in the same
  transaction. Firecracker hotplugs neither vCPUs, memory nor disk, so a
  running database is parked cleanly, its volume grown, and woken on the
  new shape: a few seconds of downtime. A parked database takes the shape
  at its next wake.
- **vmmd `GrowDatabaseVolume`** extends the stopped volume's file, then
  checks and grows its ext4 filesystem offline with the host's e2fsprogs
  (`e2fsck -f -p`, `resize2fs`). It reads the superblock first, so a
  class-only resize skips the check and a retry after a crash between the
  two steps finishes the job.
- The driver reports the database pending while `resize_pending` is set,
  and the service's reconciler repeats `Update`, which resumes it.
- Metal (`TestMetalResize`): a running database resized from 1 vCPU/384
  MiB/1 GiB to 2 vCPU/1 GiB/2 GiB is back in 1.5 s with `shared_buffers`
  88 MB -> 245 MB, its data intact and its filesystem 2 GiB; resized back
  while parked, it boots small at its next wake. `QualifyProvider` passes
  27 checks, including `compute_resize_probe` and `storage_resize_probe`.
- **Customers grow storage through the resize journal** that already
  carries class and idle-policy changes (ADR-623/624), not a parallel flow:
  one fenced operation per database, so a storage change serializes with
  every other catalogue mutation and recovers like them.
  `managed_postgres_resizes.target_storage_limit_bytes` (migration
  `20261013192154603`) is null for no change; a CHECK requires it to exceed
  a positive source limit and forbids it on an idle-policy change, and the
  receipt trigger merges it into the target spec it compares with the
  database row at completion. `ResizeOperation.TargetStorageLimitBytes`
  carries it, `TargetSpec` applies it and replay compares it.
- The API request takes `service_class` and `storage_limit_bytes`, both
  optional but not both absent; an unchanged limit counts as absent. apid
  (`pkg/apid/managedpg`) admits the class against the plan and the limit
  against the plan's per-database allowance, the latter as a limit problem
  with `limit`/`observed`; the service rejects a shrink and checks the
  backend ceiling. A storage target needs the backend's `StorageResize`
  capability, and anything but a storage-only change still needs
  `ClassResize`. The view reports `from_`/`target_storage_limit_bytes`, and
  the CLI takes `--storage-bytes`.
- **Qualification**: a backend advertising `StorageResize` must pass
  `storage_resize_probe` (`ProbeStorageResize`: grow by 1 GiB, identity,
  data, credentials and replay preserved, the new limit observed). It runs
  after every other probe, since storage cannot shrink back to the
  qualification spec; the report's `storage_resize` must match the backend
  for readiness. The native driver advertises it; Neon does not.
- Metal (`TestMetalStorageResizeThroughService`, through the service's
  journal and reconciler): a running database grown 1 -> 2 GiB parks, grows
  and wakes in 9.3 s on a loaded host; parked and grown 2 -> 3 GiB, it
  finishes in 65 ms and stays parked, waking on the 3 GiB filesystem at its
  next query (4.3 s). The superblock matches each limit, the seeded rows
  and the existing login survive, and a shrink is refused.
  `TestMetalQualifyProvider` passes all 27 checks.

### Moves and node evacuation

A database moves to another node by copying its parked volume, not by
restoring it: a restore-based move would promote onto a new timeline, and a
move aborted after promotion would leave that timeline in the archive for
later recoveries to follow past the source's newer commits. A byte copy
keeps the timeline, so the archive simply continues from the new node, and
an abort only has to discard the copy.

- **Copy.** vmmd's `ExportDatabaseVolume` streams a stopped volume's data
  extents (holes and all-zero chunks skipped, zstd on the wire) and
  `ImportDatabaseVolume` writes them to a temporary file on the target.
  Both hash every extent's offset, length and bytes, then the size; the
  target installs the volume only on a matching digest. schedd relays one
  stream into the other, so no node-to-node identity is needed; the bytes
  cross the control plane, acceptable for an operation this rare. A volume
  in transfer cannot boot.
- **Archive handoff.** Before the switch, `SyncDatabaseArchive` makes the
  old node's dbbackupd ship every archive file it still holds to the shared
  bucket (admin op `sync`). After it, `DeleteDatabaseVolume.release_archive`
  deletes the old volume and drops that node's archive copy (admin op
  `release`, which ships anything left first); the bucket keeps everything.
  Moves therefore need `shared_archive`.
- **schedd.** `Move` parks a running database, marks it `moving` with
  `move_node` and a lease (`BeginMove` picks the active node with the most
  free volume capacity unless one is named, under that node's lock),
  discards any copy a failed earlier move left there, copies, syncs, and
  switches `node_id` (`FinishMove`, fenced by the lease). A moving volume
  counts against both nodes' capacity, and a moving database cannot wake,
  park, resize or be deleted. Any failure before the switch aborts the
  move: the database stays parked on its node with `move_failed`, its
  volume there untouched, and the copy is discarded. A running or
  always-on database is woken on its new node, so clients see one stop and
  boot plus the copy. A replica that dies mid-move leaves its lease to
  lapse, and `SettleMoves` (at startup and in the idle sweep) aborts the
  move. `CollectOrphans` releases a moved database's leftover volume, or a
  failed move's copy, without touching the bucket.
- **Evacuation.** With `[database_scheduler] evacuate_draining`, the backup
  sweep moves databases off draining nodes one at a time: parked ones, and
  always-on ones, which never park. A running scale-to-zero database moves
  once it parks, so its clients see no extra downtime.
- Metal (`TestMetalMoveDatabase`, two in-process database nodes sharing a
  file bucket): a running database with 50,000 rows moved in 4.6 s (park,
  1 GiB sparse volume copy, archive handoff, wake), its rows intact on the
  new node and the old node's volume and archive copy gone. A restore to
  a point after a write on the new node held all 50,001 rows: the archive
  continues across the move.
- **Known gaps.** The history meter samples each node's local archive, so
  a moved database's pre-move history, now only in the bucket, stops being
  metered. Moves have no operator RPC yet; only evacuation drives them.
- The database-node method allowlist had missed four RPCs added after it
  (`ListDatabaseVolumes`, `GrowDatabaseVolume`, `PruneDatabaseArchive`,
  `DatabaseArchiveBytes`); a test now derives it from the service
  descriptor.

### PostgreSQL majors 14–18

Every major from 14 to 18 runs the same way; guest-init already takes the
major as a parameter (`/usr/lib/postgresql/<major>/bin`), and every setting
and flag it uses exists from 14 on.

- **Pins.** `dbvm.PostgresImages` pins the official Debian bookworm image of
  each major to its linux/amd64 manifest; the `database_vmmd` role's
  `database_vmmd_postgres_images` default carries the same pins, and a test
  keeps them equal. vmmd builds every pinned major's base on the node.
- **Placement.** A database node lists the majors it has bases for
  (`database_nodes.postgres_majors`, schedd `[[database_scheduler.nodes]]
  postgres_majors`; empty means any). Placement, moves and restores pick
  only nodes offering the database's major, so a fleet can add a major one
  node at a time. A major no node offers fails placement with no capacity.
- **Driver.** The native driver advertises 14–18 by default;
  `postgres_majors` in its settings narrows the list. Major upgrades of an
  existing database (pg_upgrade) are not supported yet: a database keeps
  the major it was created with.
- Metal (`TestMetalPostgresMajors`): each base built from its pinned image
  with this release's guest-init in 13–20 s; each major provisioned (with
  its first base backup), took writes and restored to a point in time
  between two commits, the restore holding only the earlier one.
  Provision took 9.5–15.5 s for 14–17 and 27.7 s for 18; the restores
  9.2–13.5 s.

### Spares

A wake is a cold boot. Of a ~930 ms wake to ready, PostgreSQL itself is
under 100 ms; the rest is the jail, Firecracker, the guest kernel and
guest-init. A spare has done all of that before anyone asks.

- **Spare boot.** vmmd boots a spare as a database VM of a given shape
  (major, vCPUs, memory) with `gregale.spare=1` on its command line and a
  1 MiB placeholder behind its volume drive. The placeholder lives in the
  spare's own directory under the volume root (`.spares/<id>`), which is
  bind-mounted into the jail as `spare/`. guest-init does everything but
  touch the volume and waits in `StateSpare`.
- **Adoption.** A start of a database whose shape has a waiting spare (and
  that is not a restore boot) adopts it. The database's volume is
  hard-linked into the spare's directory, which is visible in the jail
  because the directory itself was bound before boot, so no mount has to
  reach into jailer's mount namespace. It is chowned to the spare's jail
  uid, and Firecracker's `PATCH /drives/volume` points the drive (and its
  rate limiter) at the link. The VM is re-keyed to the database, the
  archive relay stamps the database from then on, and guest-init's
  `adopt` op drops the cached placeholder blocks (`BLKFLSBUF`; the kernel
  read them when it probed the drive), mounts the volume and starts
  PostgreSQL, or reports `needs_bootstrap` for a fresh volume. Any failure
  destroys the spare, and the start cold-boots instead.
- **One tenant, once.** A spare is adopted by one database and destroyed
  when that database parks, exactly like a cold-booted VM; its directory,
  holding only links, goes with it. No memory, RNG state or files pass
  between tenants. vmmd tops the pools up every 10 s and right after each
  adoption, stops waiting spares at shutdown, and `Recover` clears spare
  directories after a crash.
- **Configuration.** `[[database_vms.spares]]` (Ansible
  `database_vmmd_spares`, empty by default) lists the pools. Spares use
  node memory schedd does not see, so a node's `admission_ceiling_mb` must
  leave room for them.
- Metal (`TestMetalSpareAdoption`, PostgreSQL 17, 1 vCPU / 384 MiB): a
  cold wake to ready 930 ms; booting a spare 662 ms ahead of time; a wake
  into the spare to ready 382 ms. The data survived adoption, writes made
  in the spare reached the volume, and after the park the pool refilled
  and a second wake adopted again.

### Security review (phase 6)

The review took the threat model above at its word: a guest may be
compromised, and pgproxyd faces the internet. It asked what a hostile guest
can make the host do, whether customer SQL can escalate, and whether the
tenant seams hold.

**Fixed:**

1. *Archive disk exhaustion.* A guest could archive any number of WAL files
   (each capped at 1 GiB) and stream a base backup without end, past its
   volume's rate limiter, filling the archive disk every database on the
   node shares. vmmd now stamps `quota_bytes` on every relayed stream: 10x
   the volume size by default (`archive_quota_factor`; negative disables).
   dbbackupd refuses WAL and backup bytes past it with `quota_exceeded`. A
   refused archive_command leaves the WAL on the database's own volume.
2. *vmmd memory.* The host read a guest's control reply with an unbounded
   `ReadBytes`, so a reply without a newline grew vmmd's memory until the
   request's deadline (an hour for a base backup), against the root
   daemon serving every tenant. Replies are now read through a 64 KiB
   limit (`dbguest.MaxResponseBytes`).
3. *Console growth.* A guest writing to its console without end grew
   `console.log` in the jail directory. The log now keeps only its latest
   4 MiB.
4. *Archive stream fan-out.* A guest could open unlimited archive streams,
   each costing vmmd and dbbackupd a connection. A VM gets 4 at a time;
   PostgreSQL uses one or two.
5. *pgproxyd pre-auth limits.* The public listener had no cap, and every
   unauthenticated client costs a TLS handshake and a verifier lookup in
   the control-plane database. It now caps open connections (10,000),
   in-flight handshakes (512) and in-flight handshakes per source (32),
   closing clients past a cap at once.

**Reviewed and holding:**

- The archive relay stamps the database (and restore source) on every
  guest stream; a guest cannot name another database's archive. Admin ops
  need a header only vmmd writes, on a socket only vmmd's user can open
  (0600 in a 0700 directory).
- Customer roles are `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION
  NOBYPASSRLS`, so the pre-16 CREATEROLE route to `pg_execute_server_program`
  does not exist. Admin SQL interpolates only names matching
  `^gregale_(rt|ro|mig)_[0-9a-f]{40}$` and SCRAM verifiers matching their
  format; no customer string reaches it. Passwords are 256-bit HMAC
  derivations, so online guessing is moot.
- Database RPCs accept only `schedd.faas` (and `pgproxyd.faas` for sessions)
  and, on a database node, nothing but database RPCs.
- Spares run no tenant code before adoption and serve one tenant once; a
  spare's directory holds only its placeholder and links. Volume transfers
  install only on a matching digest. Volume ids are validated before any
  path is built.

**Out of scope here:** app VMs also write an unbounded console file under
`/var/log/faas`, a pre-existing compute-node issue.

### Qualification and the default backend (phase 6)

- `managed-postgres-qualify` knows the native driver. `--check-config`,
  `--verify` and the rollout check describe the backend from its
  configuration (`firecracker.DescribeFactory`): real capabilities and
  fingerprint, without schedd, the control-plane database or the credential
  key; anything that would reach a database fails `Unavailable`. A live run
  builds the real provider and needs
  `FAAS_MANAGED_POSTGRES_QUALIFY_NATIVE_DATABASE_URL` (the control-plane
  database pgproxyd reads role verifiers from) plus the credential key;
  without the URL it refuses before touching anything. The durable
  lifecycle injects the same lost-acknowledgement faults into native
  `Provision` and `IssueCredentials` as into Neon's.
- Fixed on the way: the driver read its key from
  `secret_env.credential_key`, a name the registry's key rule
  (`[a-z][a-z0-9-]*`) rejects, so no native backend configuration could
  load. It is `credential-key`.
- Making native the default is configuration, not code: point the region's
  `defaults` entry at the native backend, keep the Neon backend configured
  so existing databases keep resolving, and approve the native backend with
  its own qualification artifact (approvals bind to the default backend's
  fingerprint). The gate has one default per region and canary accounts
  only filter who may provision, so the switch moves every new database in
  the region at once.
- Scale is entitled to `production`, which here means a synchronous
  standby on a second node (below). With it the preflight reports a native
  default ready; a backend without it would report
  `plan_entitlement_unsupported`. Production was never offered as a larger
  shape without the standby: that would quietly give Scale databases up to
  60 s of loss on node failure.

### Production class: synchronous standby (phase 6)

A production database (2 vCPU, 2 GiB) runs a hot standby with its own
volume on a second database node, with the database's own id. While the
standby is synchronous every commit waits until the standby has flushed
it, so a node loss loses no acknowledged commit.

**Guest.** `gregale.standby=1` boots the volume as a hot standby. An empty
volume (or one whose copy never finished: no `standby-seeded` marker)
first copies the primary with `pg_basebackup -X stream` through the
replication relay, holding the standby's slot. The standby streams on
slot `gregale_standby` (bounded by `max_slot_wal_keep_size`, a quarter of
the volume) and falls back to the archive. A hot standby reports `ready`
in `postmaster.pid`; guest-init reports it as state `standby`, and it
serves no client sessions. `OpPromote` promotes onto a new timeline, so a
promoted standby never writes archive files under the primary's names.
`gregale.promote=1` promotes at boot (a failover while parked).

**Replication auth needs no secret.** A primary's guest-init serves vsock
port 5435 and hands each stream to a bridge process (guest-init re-executed)
running as `nobody`. `pg_ident` maps `nobody` to `gregale_replication`
(LOGIN REPLICATION) on `local replication` lines only, so it reaches
physical replication and nothing else. Tenant sessions arrive through the
root-owned data bridge, which the map does not name.

**Synchronous mode is host-driven and never persists across a boot.**
`OpReplication` sets `off` (no slot), `async` (role and slot) or `sync`
(`synchronous_standby_names = gregale_standby`) and reports
streaming/synchronous/lag. Every primary boot and every promotion resets it
to asynchronous before reporting ready.

**Between nodes.** The standby's vmmd relays each stream as a
`DatabaseReplication` RPC to the primary's vmmd. It dials as itself with
the new node-identity leaf `vmmd/database-peer-client` (`<id>.faas`) and
requires the server to be the primary's node. The primary's vmmd admits a
stream only from the node schedd named as that database's standby: the
TLS verifier admits only current standby nodes, and the RPC binds the
caller to the database. `off` and `StopDatabase` forget the standby. A
standby whose relay cannot dial the primary falls back to the archive.
Nodes run standbys only with `database_vmmd_standbys` on, once their
bundle carries the leaf.

**schedd.** `database_vms` gains `replicated`, `standby_node`,
`standby_running`, `standby_synced` and `promote_pending`. Placement gives
a replicated database a standby node besides its own (else no capacity).
Its volume counts there always, and its memory while the standby runs:
`ClaimStandby` checks that node's ceiling. `standby_synced` is the only
thing a failover trusts:

- It is set only after the primary reports the standby synchronous.
- It is cleared before a primary may commit without its standby. A wake
  clears it in the same transaction that admits the wake (primaries boot
  asynchronous). Before the monitor turns a primary asynchronous, the
  record goes first.
- A clean park stops the primary, then the standby, so a synchronous
  standby stays synchronized while parked.

Wakes never wait for the standby. It attaches in the background: async
on the primary, then stop any leftover standby, create and grow the
volume, boot (and seed) the standby, then sync, then record.
`RunStandbyLoop` (2 s) attaches missing standbys and records syncs. It
drops a standby that stops keeping up or whose node drains or is lost,
record first, then async (or `off`, losing the slot, when the node goes).
A standby's loss therefore holds commits for at most one check. Parked
databases' standbys move off draining or lost nodes. Delete discards the
standby volume; the orphan sweep leaves it alone.

**Failover.** When the operator marks a node unavailable,
`SettleLostNodes` fails a database over if its standby is synchronized
and the standby's node is up:

1. Fence the old primary (stop it, if its node still answers).
2. Promote a running standby now, or mark a parked database
   `promote_pending`; its next boot (`StartDatabase promote`) promotes the
   standby volume on a new timeline.
3. Switch the record to the standby's node (`last_error = failed_over`).

An isolated old primary that is not fenced cannot commit: its synchronous
standby no longer acknowledges. A database whose standby is not
synchronized is parked as before (`node_lost`) and restored from the
archive if its node does not return. After a failover the monitor places
and seeds a new standby elsewhere.

**Automatic failover.** The standby monitor fails over without the
operator too. Its probe of a running replicated database's primary (3 s
timeout) may get no answer from the node (gRPC unavailable or deadline;
a missing VM on a node that answers does not count). If that lasts for
`auto_failover_after` (default 30 s, negative turns it off) and the
standby is synchronized, schedd probes the standby. Only if the standby
answers and is replaying does it run the same fence-promote-switch.

- If schedd cannot reach the standby's node either, it is more likely cut
  off itself, and it does nothing.
- Any answer from the node restarts its clock.
- If only the link from schedd to the old primary is down, the old primary
  can no longer commit (its synchronous standby is gone). The orphan sweep
  stops it and frees its volume once its node answers again.
- PostgreSQL's synchronous replication caveat applies. A client that
  cancels its wait (`statement_timeout`, a cancel request) gets a warning
  and a commit made only locally. Such a commit on a partitioned old
  primary is lost with it.

Metal (`TestMetalAutomaticFailoverOnASilentPrimaryNode`): the primary
node's vmmd stopped answering while its VM ran on. The database failed
over 3.5 s later (threshold 3 s) with all 5,000 acknowledged rows. The
stale primary stopped and its volume went once the node answered again.

**Parked databases fail over automatically too.** Nothing runs on a parked
primary's node, so the monitor probes the node itself: once per check, a
`ListVolumes` call (3 s timeout) to every node holding parked primaries
with a synchronized standby, all at once. The rule is the running one: no
answer for `auto_failover_after`, and the standby's node answers.
The database is then switched to its standby's node, parked with its
promotion pending; its next wake promotes the standby volume there.

Two fixes keep the old node's stale volume from coming back:
- **Standby placement avoids silent nodes.** A new standby is never
  placed on a node the monitor finds silent. The orphan sweep clears that
  mark once the node lists its volumes again.
- **A fresh standby starts clean.** A standby placed on a newly chosen
  node first discards any volume of the database there. That is always
  stale, for example the old primary's after a failover, and it is never
  seeded from.

Metal (`TestMetalAutomaticFailoverOfAParkedDatabase`): a parked
database's node went silent. It failed over 3.0 s later (threshold 3 s).
The promotion wake on the standby's node took 2.0 s and kept all 5,000
rows, and the old volume went once the node answered again.

Metal (`TestMetalProductionClassFailover`), two nodes through vmmd's
handlers:

- Two replicated databases, one running and one parked, each had a
  synchronous standby 5.5 s after the wake.
- The running primary was killed with no shutdown and its node marked
  lost. Both databases failed over (5.8 s) with every acknowledged commit,
  writing on timeline 2.
- The parked database's promotion boot took 2.3 s.
- In the guest and runtime test the seed took 4 s, a seeded standby
  resumes in 2.2 s, and a promotion takes 0.5 s.

**Changing class to or from production.** A resize carries whether the
database keeps a standby (`ResizeDatabaseRequest.replicated`; unset keeps
it). The class change is therefore one schedd step, and the record never
shows a shape that matches no class.

- **Joining production.** The resize places the standby node as placement
  does: another active node offering the major, with room for the
  volume. Without one it is refused (`ErrNoCapacity`) and nothing changes.
  A running database restarts on the new shape, and its standby copies the
  primary and turns synchronous in the background. A parked one gets its
  standby at its next wake.
- **Leaving production.** The standby is retired before the record
  changes:
  1. a running database stops cleanly, primary first, so the standby
     holds every commit to the end;
  2. the standby is marked not synchronized, and its VM stops;
  3. its volume is discarded. A standby node that is unavailable or gone
     leaves the volume to the orphan sweep.
  4. The record turns replication off and notes that the primary still
     holds the standby's replication slot (`drop_standby_slot`).

  Left alone, that slot would keep WAL up to a quarter of the volume. The
  standby monitor drops it once the primary runs (replication `off`, which
  drops only an inactive slot), and clears the note when the primary
  reports none. Joining production again before then reuses the slot.
- **Driver.** `Update` now moves between any classes; it refused moves
  into and out of production before.
- **Growing a production database's storage** is checked against the
  standby's node too, which holds a second copy of the volume. Without
  room there the resize is refused (`ErrNoCapacity`) and nothing changes.
  The standby's volume grows at its next attach. A standby node that
  drains or is unavailable is not checked, since its standby moves to a
  node with room. The Postgres store locks both nodes in one order.
- **Evidence.** Both stores' contract test covers:
  - placing the standby and counting its volume on its node;
  - refusing to drop replication while the standby runs;
  - the owed slot.

  Scheduler tests cover the ordering (primary, then standby, then volume)
  and leaving production while parked. On metal (two nodes, vmmd's gRPC
  handlers), joining production took 7.6 s: restart, standby copy,
  synchronous. A commit then waited for the standby. Leaving took 3.2 s:
  restart, standby VM and volume gone. Once the monitor ran, the primary
  had no replication slot, and all 2,000 rows survived.

Not yet done:

- **After every wake the primary runs asynchronous until its standby
  syncs**, typically a few seconds. A node loss in that window falls back
  to the archive (up to 60 s of loss), as for other classes.
- **Several schedd replicas.** A standby claim that never synchronized is
  reclaimed by another replica only after 15 minutes, since that replica
  may still be seeding it.

### Import from another PostgreSQL (migration off Neon, step 1)

A native database can copy another PostgreSQL database into itself: the
data path of a migration off Neon. The copy runs on the database node, in
the target's own VM, with binaries of its major. Bulk data never crosses
the control plane.

- **Guest.** `OpImport` starts a background copy into an empty database:
  1. Check that the source major is at most the target's.
  2. Check that every source extension, less the skipped ones, is
     available and *trusted*, which is what the schema owner can create
     anyway. `pg_stat_statements`, for example, is refused until skipped.
  3. Check that the target holds no user objects.
  4. `pg_dump -Fc --no-owner --no-privileges --no-publications
     --no-subscriptions` of the source.
  5. `pg_restore --single-transaction --role=<schema owner>` of a TOC with
     plpgsql and the skipped extensions removed, then `ANALYZE`.

  Everything restored belongs to the schema owner, so native credentials
  reach it like anything a migration created. `OpImportStatus` reports
  none/running/done/failed with a stable code (`source_unreachable`,
  `source_newer`, `extension_unavailable`, `target_not_empty`,
  `import_failed`). The source password reaches the tools only through
  `PGPASSWORD`.
- **The guest never reaches the network.** Its tools connect to a socket
  guest-init relays to host vsock port 5436. vmmd's import relay (dbvm)
  dials only the endpoint the control plane named. It resolves the name
  once and accepts only public addresses (no private, loopback,
  link-local, overlay `100.64/10`, service `198.18/15` or other
  special-purpose ranges). It asks for TLS with PostgreSQL's SSLRequest,
  verifies the certificate for the host name against the host's roots,
  and then splices. A source without TLS or with the wrong certificate is
  refused. The guest needs no CA store and never learns the endpoint.
- **schedd and the driver.** `ImportDatabase` / `DatabaseImportStatus`
  (apid only) wake the database and forward to vmmd, which keeps the
  endpoint and passes the login to the guest. Idle parking and eviction
  leave a database alone while its guest reports an import running. A VM
  that stops mid-import loses the import, and the target is empty again
  (one transaction), so the import simply starts over.
  `managedpostgres.Importer` (`Import`, `ImportStatus`) is the portable
  surface; the native driver copies into its customer database as
  `gregale_schema_owner`.
- **Metal.** A source VM behind a TLS-terminating shim (dbvm) was copied
  with 20,000 rows, a function, a view, a second schema, a table comment
  and the sequence position, all owned by the schema owner, in 0.6 s. The
  untrusted extension failed the import until it was skipped. Through
  schedd and vmmd's gRPC handlers, 50,000 rows took 0.6 s.

Not yet done:

- **The migration itself.** Provision a native target for a Neon
  database, issue a Neon migration credential, import, then move the
  app's bindings. The binding move is the restore cutover's activation
  (ADR-464..469: credential staging, verification and the admission, task
  and boot fences exist; writer draining and binding publication do not),
  which both restores and migrations need.
- **Writes during the copy are not captured.** A consistent copy needs the
  app's writers drained first (the same cutover fence), or accepts the
  dump's snapshot. There is no logical-replication catch-up.
- **Space.** The dump lands on the target volume, so the volume needs room
  for the compressed dump beside the data.
- **Grants.** Objects outside schema `public` get no runtime grants, as for
  any native schema.

### Environment-clone snapshots (ADR-590, step 1)

ADR-590's environment clones capture a checkpoint of each source database
while its writers are held, then fork it. Native databases implement the
capture and the fork (`SnapshotProvider`, `SnapshotRestoreProvider`,
`SnapshotRestoreDeletionProvider`) without copying anything at capture
time.

- **A snapshot is an archive hold.** dbbackupd keeps named holds per
  database (admin ops `hold`, `get_hold`, `unhold`). A hold records its
  point and creation time. Pruning moves the retention period's start
  back to the earliest held point, so the base backup and WAL that reach
  it stay. With a bucket, holds live there only, so every pruner sees
  them: the node, the next node after a move, and a node pruning a lost
  node's archive. Without a bucket they sit beside the node's archive.
  Dropping a database's archive drops its holds.
- **Capture.** schedd's `CaptureDatabaseSnapshot` (apid only) does three
  things:
  1. Checks the point. It must lie between the database's creation and
     now, at least 10 minutes inside the archive's retention
     (max(window, 1 h)), so a concurrent prune cannot pass it.
  2. Places the hold through the database's node, or any live node with
     a shared archive.
  3. Makes the archive reach the point: the database, woken if parked,
     commits past it and ships that WAL (as a restore's source flush
     does).

  A retry with the same name and point returns the snapshot, even after
  the point has aged. Another point under the name is a conflict. A
  database on a lost node cannot be captured.
- **Restore.** `RestoreDatabase` takes the snapshot's name. With it, a
  point outside the source's restore window is accepted while the hold
  pins exactly that point. Otherwise it is the ordinary point-in-time
  restore into a new database.
- **Driver.** The snapshot id is `<source id>/<hold name>`. The hold name
  is derived from the owning resource id, so a lost capture reply is
  found again. Snapshots report no expiry, which the clone receipts
  require, and `RetainSnapshot` changes nothing because a hold already
  lasts until it is deleted.

  The fork is the database the target owner's id names, with the
  source's spec. It is `Restored` once recovered and backed up; a failed
  restore is a conflict. Its deletion is one deterministic operation,
  schedd's synchronous delete. Deletion first checks that the database
  is the one the receipts recorded: same lineage and same creation time.
  `DatabaseRuntime.created_at` now carries that time.
- **Cost.** A held point keeps older base backups and WAL, metered as the
  source's history bytes until the snapshot is deleted.
- **Metal.** `TestMetalSnapshotRestoreOutlivesRetention` runs through the
  driver, schedd's and vmmd's gRPC handlers and dbbackupd. A commit lands
  before the point and one after it. Capture takes 317 ms (hold and WAL
  flush). A newer base backup and a prune whose retention starts after
  it keep an older backup for the point. The snapshot's restore recovers
  only the earlier commit in 7.0 s. After the fork and the snapshot are
  deleted, the next prune leaves only the newest backup.
  `TestMetalQualifyProviderWithRestore`, `TestMetalSchedulerLifecycle` and
  `TestMetalRestoreOntoAnotherNodeAfterNodeLoss` still pass.

Not yet done: the clone pipeline's other provider surfaces for native
databases (copy readers and targets over the restored database, and
checkpoint connections and maintenance on the source). Until they exist,
a clone of a native database stops after the fork.

### Usage metering

schedd keeps a usage ledger per database (`database_usage`) that outlives
the database, so the collector can read its last windows after deletion and
re-read recent ones for corrections with the same answer. Rows are
intervals at a constant per-second rate, or hourly counts:

| Meter | Ledger | Written when |
| --- | --- | --- |
| `active_seconds`, `compute_unit_seconds` | `compute`: vCPUs | opened on entering waking, closed on leaving waking/running/parking, in the same transaction as the state change |
| `storage_byte_seconds` | `storage`: provisioned volume bytes | opened at placement, closed at deletion |
| `history_byte_seconds` | `history`: archive bytes | sampled from each node's dbbackupd (`DatabaseArchiveBytes`, admin `size`) by the 10-minute maintenance sweep; a new size closes the interval and opens another |
| `egress_bytes` | `egress`: bytes per hour | pgproxyd counts bytes sent to clients and reports them on its 30 s touches and at session end (`TouchDatabase.egress_bytes`), at most once |
| `compute_unit_seconds` (standby) | `standby_compute`: the standby's vCPUs | opened when a production database's standby VM is claimed, closed when it stops, is promoted or the database is deleted |
| `storage_byte_seconds` (standby) | `standby_storage`: volume bytes | open while the database has a standby node, at the volume's current size; closed when replication turns off, on failover and at deletion |

- A window's usage is the overlap of the rows with it (`DatabaseUsage`, apid
  only); a database with no ledger is `NotFound`. Provisioning and restore
  VM time is not billed, since it is not the customer's compute.
- **The standby counts.** A production database's standby holds real
  resources: a second VM and a second copy of the volume. Following the
  rule of plan limits rather than prices, it adds no meter or price. Its
  vCPU-seconds count into `compute_unit_seconds` and its volume into
  `storage_byte_seconds`, so the account's usage ceilings see what the
  database holds. `active_seconds` stays the primary's. Every store write
  that changes the standby re-syncs those intervals in the same
  transaction (`getSyncingStandby`).
- A restore is its own database with its own ledger
  (`RestoreUsageIsolated`, only where point-in-time restore is on).
- When the operator marks a node unavailable, schedd's 30-second sweep
  parks the records of its active databases (`node_lost`), which closes
  their compute intervals; otherwise an always-on database would bill
  compute forever. Their wakes fail until the node returns. They keep
  their last history sample until deleted.
- The ledger keeps closed rows 90 days.
- Metal: `TestMetalDriverEndToEnd` reads all five meters after a
  provisioned, queried database, and again after its deletion.

Backend configuration (`FAAS_MANAGED_POSTGRES_CONFIG`):

```json
{"id": "native-eu", "driver": "firecracker", "region": "eu-central", "namespace": "native",
 "settings": {"host_suffix": "db.gregale.dev", "schedd_target": "unix:///run/faas/schedd.sock",
              "max_restore_window_seconds": "604800"},
 "secret_env": {"credential-key": "FAAS_MANAGED_POSTGRES_NATIVE_CREDENTIAL_KEY"}}
```

## Phasing

1. Walking skeleton on one internal database node: create, connect through
   the proxy, idle park, wake on query, delete.
2. Durability: WAL shipping, base backups, restore into a new database.
3. Metering and guardrails.
4. Density and wake: spares, admission ceiling, wake-storm queueing, classes.
5. Operations: volume moves, node evacuation, node-loss restore, majors 14–18.
6. Qualification: security review, `QualifyProvider` pass, the production
   class (synchronous standby on a second node, done), then default for
   new databases and a migration path off Neon (the import is done; the
   binding cutover is not).

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
