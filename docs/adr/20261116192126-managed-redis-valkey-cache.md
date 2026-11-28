# ADR-20261116192126: Managed Redis-compatible cache on compute nodes, with scale to zero

- **Status:** proposed. Owner decisions, 2026-11-16: self-host **Valkey**,
  **cache-only** durability, **Hobby and up**; then "Redis should work on
  compute nodes, max 512mb (can change later), and how the scale to zero
  would work?"; then data must be kept when resize, upgrade or restart
  happens. Nothing is built yet.
- **Date:** 2026-11-16
- **Owner:** apid, schedd, vmmd and gatewayd-internal maintainers
- **Builds on:** [ADR-576](576-private-tcp-service-addressing.md) (private
  TCP service addresses), [ADR-1134](1134-wake-ahead-learns-private-tcp.md)
  (wake-ahead over private TCP), ADR-155 bindings, the plan entitlements of
  [ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md).
- **Leaves unchanged:** the stateless-only rule
  ([ADR-047](047-stateless-runtime-advisory.md)). Customers still cannot
  deploy `redis`/`valkey` images as apps (`stateless_only_violation`). A
  managed cache runs a platform-built image with platform-owned config.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Customers ask for Redis and there is none.** `docs/storage.md` sends them
  to Upstash. The microservices-demo run (2026-11-15) could not run its
  `cartservice`: Compose `redis` image services are skipped and the
  `REDIS_ADDR=redis:6379` import is dropped as a service address
  (`projectsetup.EnvSkipServiceAddress`).
- **Apps cannot hold it themselves.** An instance's memory is per-replica,
  `/tmp` is a tmpfs and there are no durable volumes.
- **Compute nodes already do almost everything a cache needs.** One
  Firecracker VM per workload, park to a memory snapshot and restore on
  demand, and since ADR-576 `<name>.svc.gregale:<port>` addresses for raw
  TCP. Their proxy authorizes the caller, wakes a parked target and holds the
  session until it is up.
- **What is missing is idleness.** vmmd counts an open stream as activity
  (ADR-576), and Redis clients keep pooled connections open and `PING` them.
  A cache behind today's TCP proxy would park only after the plan's session
  idle timeout cuts every connection, and the clients would see the cut.
- **Licensing.** Redis 8 is RSALv2/SSPLv1/AGPLv3. Valkey is BSD-3-Clause,
  speaks RESP2/RESP3, and every Redis client works with it unchanged.

## Decision

A managed cache is a **platform-owned, single-instance app on compute
nodes** running Valkey, reached at a private service address. It scales to
zero by parking to a memory snapshot, so **its data survives park and wake**.
It is still cache-only: no AOF, no replica, no backup. Data is lost when the
snapshot is lost or the cache restarts.

### Resource, API and CLI

- `Cache` is an account resource (organization-owned as in
  ADR-202610171906): name, size, eviction policy, idle policy, state
  (`provisioning`, `running`, `parked`, `restarting`, `suspended`,
  `deleting`).
- API `/v1/redis/*`: create, list, get, resize, update, restart, flush,
  delete, bindings and `info` (used memory, keys, hit/miss, evictions,
  connections, ops/s, last park and wake).
- CLI `gregale redis create|list|info|resize|update|restart|delete|bind|unbind|connect`.
- Behind the `managed_redis` capability flag until the GA gate.
- Internally apid creates a hidden app (`kind=cache`) per cache, with
  `min_instances=0`, `max_instances=1` and a platform image. Customers see
  only the cache: it is not listed, deployed, scaled or exec'd as an app.
  That gives it scheduling, placement, park/wake, billing, usage and
  deletion from the app machinery with no new node role.

### Size: at most 512 MB

- `api.ManagedRedisMaxMemoryMB = 512`, one constant, so raising it later is
  a one-line change plus a qualification run. Sizes: 32, 64, 128, 256, 512 MB.
- The size is Valkey's `maxmemory`. The VM gets size + `api.CacheVMOverheadMB`
  (guest kernel, guest-init, Valkey buffers and fragmentation; measured in
  slice 1, expected ~48 MB). That VM RAM is what admission charges and what
  is billed, like any app.
- Memory is a hard cap: Valkey evicts by policy (default `allkeys-lru`; also
  `allkeys-lfu`, `volatile-lru`, `volatile-lfu`, `volatile-ttl`,
  `noeviction`, which refuses writes with `OOM`).

### Valkey in the VM

- Platform image: a Valkey release plus guest-init's cache boot mode. Fixed
  config: `save ""`, `appendonly no`, `maxmemory` and policy from the
  resource, `protected-mode no` on the guest interface only, `requirepass`
  set to a per-cache internal secret that never leaves the platform.
- Commands blocked for clients: `CONFIG`, `DEBUG`, `MODULE`, `SHUTDOWN`,
  `SAVE`, `BGSAVE`, `BGREWRITEAOF`, `REPLICAOF`/`SLAVEOF`, `FAILOVER`,
  `CLUSTER`, `ACL`, `MONITOR`. `FLUSHALL`, Lua (`EVAL`, Functions), pub/sub,
  streams and transactions are allowed: they act only inside the tenant's VM.
- **Never more than one replica, never a clone.** Two instances would be two
  different caches, and restoring one snapshot twice would duplicate
  Valkey's random state (the reseed concern of
  [ADR-022](022-vsock-resume-hook.md)). The cache app is excluded from scale-out,
  warm pools and snapshot sharing.

### Addressing and auth

- The cache gets an ADR-576 service address. Authorized apps in the account
  connect to `redis://<name>.svc.gregale:6379`. The ADR-576 authorizer
  (account, declared bindings, allowed callers, previews) decides who may
  connect.
- **No Valkey password** (owner, 2026-11-16, replacing proxy-side `AUTH`).
  The cache has no public address and is reachable only through the service
  TCP proxy, whose authorizer is the gate. A password would add a secret to
  app env without moving that boundary. Delivering one to compute nodes for
  proxy-side `AUTH` would put more secret material on compute, against the
  compute-keys work. So `REDIS_URL=redis://<name>.svc.gregale:6379` is the
  whole binding, and `host:port` clients work unchanged.
- Compose: each consumer key of a provided `redis` service is rewritten to
  the cache in the shape it had. A URL stays a URL (database index and query
  kept, credentials dropped), `host:port` becomes
  `<cache>.svc.gregale:6379`, and a bare host becomes the cache host. No
  forwarder inside the app VM is needed.
- Public access from outside Gregale (`redis-cli` on a laptop) is a later
  slice through raw TCP ingress (ADR-183) with TLS and a per-cache token.
  `gregale redis connect` goes through the authenticated CLI tunnel instead.

### Scale to zero

The service TCP proxy becomes **RESP-aware for `kind=cache` targets**. It
parses the request stream (framing only, no values kept) and does three
things.

**1. It decides when the cache is idle.** A cache is idle when, for its idle
timeout, no session has sent a *data command* and no session is in a
*pinned state*:

- Not data commands (they never keep a cache awake): `PING`, `ECHO`,
  `HELLO`, `AUTH`, `SELECT`, `CLIENT SETNAME|SETINFO|GETNAME|ID`, `QUIT`,
  `RESET`, `INFO`, `COMMAND`, `READONLY`/`READWRITE`.
- Pinned states (keep it awake while held): `SUBSCRIBE`/`PSUBSCRIBE`/
  `SSUBSCRIBE`, a blocking command in flight (`BLPOP`, `BRPOP`, `BLMOVE`,
  `BZPOPMIN`, `XREAD … BLOCK`, `WAIT`, …), an open `MULTI` or `WATCH`,
  `CLIENT TRACKING on`.
- The proxy reports activity to vmmd instead of vmmd treating every open
  stream as activity. For cache instances, vmmd's idle reaper uses only
  that signal.
- Idle timeout: default 15 minutes; settable from 5 minutes to 24 hours, or
  `never` (always on, billed as such). Plan idle timeouts for apps do not
  apply.

**2. It parks without dropping clients.** At idle the cache parks like an
app: Valkey pauses, Firecracker writes the memory snapshot, the VM stops.
Every client session to the proxy **stays open**. Only the proxy's vsock
streams to the guest close. The proxy keeps, per session, the state it must
replay: protocol (`HELLO 2|3`), selected DB, client name/info. Sessions in a
pinned state block the park, so nothing else is lost.

**3. It wakes on the next real command.** When a held session sends a
command, the proxy wakes the cache through the ADR-196 waker (trigger
`service.mesh`, coalesced with other wakers), re-dials the guest, replays
`AUTH` and the session state, then forwards the buffered command. The
client sees one slow reply, not a reset. A new connection to a parked cache
wakes it the same way, as ADR-576 already does. Wake-ahead (ADR-1134) learns
"app → cache" edges, so a cold app restores its cache alongside itself.

What survives:

| Event | Data | Client connections |
| --- | --- | --- |
| Idle park, then wake | kept (memory snapshot) | kept (held by proxy) |
| Caller app parks | kept | the caller's own sockets drop with it, as today |
| Restart | kept (snapshot restore, below) | kept |
| Resize, Valkey or guest upgrade, node drain | kept (live handoff, below) | kept |
| Crash inside the VM (guest panic, OOM kill) | **lost**; cold boot | reset |
| Node loss while running | **lost**; re-placed empty in < 60 s | reset |
| Node loss while parked | kept if the park snapshot is stored off-node like app snapshots, else lost | reset |
| Snapshot evicted or expired | **lost**; next wake is a cold boot | kept |

- **Snapshot lifetime.** A parked cache's snapshot is kept for 7 days without
  a wake. After that it is dropped and the next wake cold-boots empty. That
  bounds disk for abandoned caches. `gregale redis info` shows `data:
  dropped after 7 days parked`.
- **Clock and TTLs.** The guest wall clock is corrected on restore as for
  apps. Valkey expires keys by wall clock, so keys whose TTL passed while
  parked are expired on access and by the active expire cycle right after
  wake. Parking does not stretch TTLs.
- **Wake time.** Same restore path as apps. A 512 MB cache restores lazily,
  so the first commands may fault pages in. Target, measured in slice 1:
  p95 first reply after wake under 500 ms at 64 MB and under 1.5 s at 512 MB.

### Keeping data through restart, resize and upgrade

Planned operations never cold-boot a cache that holds data. Each operation
**fails safe**: if a step fails or times out, it is abandoned and the old VM
keeps serving, so nothing is lost. `--flush` on restart or resize is the
only way to ask for an empty cache.

**Restart (same image, same size): snapshot and restore.** This is a park
and wake on purpose. The proxy holds the sessions, vmmd writes the memory
snapshot, stops the VM and restores it into a fresh VM (same node, or another
for a drain). The pause is the park plus the restore, about 1 s at 512 MB.
It fixes a wedged guest or host process without touching Valkey's data.

**Resize, Valkey or guest upgrade, node drain: live handoff by replication.**
A snapshot cannot change the VM's RAM or the Valkey binary, so these copy the
data into a new VM:

1. schedd admits VM B (new size or image, any compute node) next to the
   running VM A. While both run, only A is billed.
2. B replicates from A (`REPLICAOF` through a vmmd vsock relay, as native
   Postgres replication does in ADR-1027). Sync is diskless, because the
   guest has no disk.
3. When B's replication offset is within 1 MB of A's, the proxy holds new
   commands for the cache, sends `CLIENT PAUSE WRITE` to A, and waits for
   B's offset to equal A's.
4. B is promoted (`REPLICAOF NO ONE`). The proxy re-dials every session to
   B and replays its state, as on wake (protocol, DB, name, and subscribed
   channels and patterns). Then it releases the held commands. The target
   write pause is p95 under 100 ms.
5. A stops. The service address now routes to B.

How the switch treats sessions in a pinned state:

- **Blocking command in flight.** It cannot complete on A while writes are
  paused. The proxy unblocks it on A, drops that reply, and re-issues it on
  B with the remaining timeout.
- **Open `WATCH`/`MULTI`.** The proxy waits up to 5 s for `EXEC`. After that,
  `EXEC` returns the nil "watched key changed" reply, which clients already
  retry.
- **Client tracking.** The proxy sends a flush-all invalidation and
  re-enables tracking on B.

**Memory during the handoff.** A full sync forks Valkey inside A. Copy-on-write
can need up to the written share of the dataset again. For the handoff,
admission charges A's node an extra `min(used_memory, size) / 2`, and vmmd
raises A's cgroup limit by the same amount. This is the most likely failure,
and its outcome is the safe one: if A comes near the raised limit, the
handoff is cancelled before A is at risk, then retried at a quieter time,
with an `info` warning to the customer.

**Resize down.** If `used_memory` exceeds the new size, the API answers
`409 cache_too_full_to_shrink`, unless `--evict` is given. In that case
`maxmemory` is lowered on A first, Valkey evicts by its policy, and the
handoff follows.

**Version direction.** Replication from an older Valkey to a newer one is
supported. Rolling back to an older Valkey can fail if the RDB format
changed. That rollback is refused, and the cache stays on the newer
version.

**Upgrades in the fleet.** A new Valkey minor or guest-init makes caches
`stale`. A running stale cache is handed off with no data lost in the
account's maintenance window, or at once for a security fix. A parked stale
cache is restored from its snapshot when it wakes, then handed off in the
background. Target: patched Valkey across the fleet within 72 h of an
upstream security release, with no cache emptied by it.

Node failure and crashes inside the VM still lose a running cache's data.
This ADR keeps the owner's cache-only choice for those.

### Plans and billing

| Plan | Caches | Max size per cache |
| --- | ---: | ---: |
| Free | — | — |
| Hobby | 1 | 512 MB |
| Pro | 3 | 512 MB |
| Scale | 10 | 512 MB |

- Free answers `403 managed_redis_not_in_plan` (as ADR-20261110184700 for
  Postgres). A Compose `redis` on Free is not provisioned, and the deploy
  names the plan as the reason.
- **Running** caches are billed like apps: VM RAM per running second against
  the plan's compute allowance and overage. **Parked** caches cost snapshot
  storage only, counted in the existing snapshot storage allowance. No new
  meter.
- Counts are provisional, to be checked against the COGS model.
- An account that falls below its plan has its extra caches stopped
  (snapshot dropped) and deleted after 30 days through the delete grace
  period. CPU-abuse rules apply to cache VMs as to apps.

### Compose and `gregale dev`

- A Compose service whose image is `redis`, `valkey/valkey`,
  `redis/redis-stack-server` or `bitnami/redis` becomes a managed cache
  named after the service, sized from `--maxmemory` in its `command:` if
  present (capped at 512 MB), else 128 MB. Sibling services reach it by the
  Compose name.
- `gregale dev --redis` runs a local Valkey container. `--service-override-file`
  already accepts `REDIS_URL`.

## Phasing

1. **Image and VM.** Valkey image and cache boot mode. On metal: measure
   overhead MB, cold boot and park/restore at 32–512 MB, and check that keys,
   TTLs and Valkey's state are right after restore.
2. **RESP-aware proxy.** Data-command classification, pinned states,
   activity reporting to vmmd, held sessions with state replay, proxy-side
   `AUTH`. Unit tests on a recorded corpus from go-redis, redis-py, ioredis,
   Jedis/Lettuce and Sidekiq.
3. **apid and CLI.** `/v1/redis/*`, hidden `kind=cache` app, bindings,
   entitlements, 7-day snapshot lifetime, past-plan lifecycle,
   `gregale redis …`, flag `managed_redis` (dark).
4. **Compose.** Provisioning from Compose, sibling-name resolution, and the
   microservices-demo `cartservice` unchanged as the acceptance test.
5. **Handoff.** Restart through snapshot restore, live replication handoff
   for resize, upgrade and drain, and fail-safe abort. On metal, a write
   loop (go-redis, ioredis) runs through 100 handoffs at 512 MB with zero
   lost acknowledged writes, and the pause is measured. This must pass
   before GA.
6. **GA gate.** `docs/redis.md` and the `docs/storage.md` table, capability
   row, runbook (node loss, security roll), a journey in the full prod run
   covering park → held session → wake, then owner approval to release the
   flag.

## Implementation status

**Slice 1 (control plane), built dark behind the `managed-redis` capability
(preview).** It differs from the design above in these interim ways, each
closed by a later slice:

- **The cache app is visible.** It is an ordinary internal app named after
  the cache, and it counts towards the plan's app limit. It is not yet
  hidden. imaged refuses any image on it other than `api.ManagedValkeyImage`.
- **No password** (see "Addressing and auth"). A binding writes
  `redis://<name>.svc.gregale:6379`. An earlier build derived a per-cache
  password from apid's host HMAC key; it was removed with this decision.
- **Hobby's largest cache is 448 MB, not 512.** Hobby's largest instance is
  512 MB, and the VM needs 64 MB on top of `maxmemory`. Pro and Scale get
  512 MB. `api.ManagedValkeyMaxMemoryFor` derives this from plan RAM.
- **Server commands are blocked with `rename-command`,** not an ACL user,
  because the server is configured on the command line only.
- **Idle (slice 2, built).** The service TCP proxy runs a RESP-aware session
  for cache targets (`pkg/gateway/redis_session.go`). After 30 s with no
  data command, no outstanding reply and no pinned state, it closes the
  upstream leg, and vmmd's idle timeout then parks the cache. It keeps the
  client connection open and answers `PING`, `QUIT` and `RESET` itself. It
  re-attaches on the next command, replaying `HELLO`, `AUTH`, `SELECT` and
  `CLIENT SETNAME` (`AUTH` only if the client sent one).
- **Compose (slice 3, built).** `redis`/`valkey` services in a project deploy
  become caches named `<project>-<service>` (other environments add the
  environment and a hash), when the `managed-redis` capability is on for the
  account and the plan has room. A key that is already set keeps its value.
  The deploy plan lists each provision or the reason there is none.
- **Restart, resize and upgrade (slice 4, code written, not yet run on metal).**
  - `POST /v1/redis/{name}/restart` is schedd's ordinary restart, which
    snapshots the running VM and wakes from the snapshot. `fresh` is never
    used for a cache.
  - `PATCH /v1/redis/{name}` (size, eviction policy; shrinking needs
    `evict`) and `POST /v1/redis/{name}/upgrade` deploy
    `api.ManagedValkeyHandoffCommand`. Within a 15-minute window from
    creation, the new VM starts with TCP closed, replicates from the cache's
    own service address (which reaches the running VM, because the new
    deployment has no traffic), pauses writes there (`CLIENT PAUSE WRITE`,
    at most 300 s), waits for its offset to match, promotes itself, then opens
    port 6379. Readiness passes, the ordinary rollout moves traffic, and the
    old VM stops. Client sessions re-attach to the new VM on their next
    command. Any failure exits before the port opens and lifts the pause, so
    the old VM keeps serving. After the window, a cold boot of that
    deployment starts empty, like any cache cold boot.
  - This replaces the vsock replication relay above. Replication goes
    through the cache's own service address instead. The proxy passes a
    cache's calls to itself raw, because replication bytes are not commands.
    For every customer session it refuses `CONFIG`, `REPLICAOF`, `SLAVEOF`,
    `SYNC`, `PSYNC`, `REPLCONF` and the renamed commands. Valkey keeps these,
    because the handoff needs them.
  - Verified on metal by `TestManagedRedisMetal`, run
    redis-20261118T234819:
    - the service authorizer admits a cache calling its own address;
    - the rollout runs the new VM next to the old one under the cache's
      `max_instances: 1`;
    - `CONFIG SET port` opens TCP on a running Valkey 8.1.4.
  - Still open before release: the 100-handoff no-lost-writes test from the
    phasing list.
  - Metal runs added three requirements:
    - A resize first wakes the cache and waits up to 60 s for a running
      instance, while the RAM still matches. A parked cache's snapshot
      cannot restore once the app's RAM changes, so the source would
      cold-boot empty. If it does not wake in time, the call returns 503
      `cache_waking` and the cache is unchanged.
    - The handoff replicates from the cache's service IP, not its DNS name,
      and the cache app gets the plan's whole startup window.
    - A cache deployment counts as a raw TCP listener. Readiness is TCP
      accept, and there is no plan-default HTTP liveness probe, which had
      killed a woken cache about 50 s in. schedd recognises a cache by its
      image (`api.ManagedValkeyImage`), so the wake path needs no lookup.
- **Park keeps a cache's memory (slice 5, an exception to the snapshot
  rule).** The platform's snapshots are deployment cache, not the memory of
  the last run. Park reuses a healthy init capture, and a VM that served
  requests is discarded for a clean recapture (ADR-20261115113000).
  - Metal run redis-20261118T060901 showed what this does to a cache:
    SET, park, then a wake from the primed init capture, and GET returned
    nil.
  - For a managed cache only, schedd's park now always captures the
    running VM (`keepsMemoryAtPark`). It never reuses a capture and never
    queues a clean recapture.
  - imaged publishes that capture in place of the deployment's live row
    (`isManagedCacheDeployment`). For any other app it keeps the older row
    and drops the new capture.
  - Wake restores the newest live row, as before.
  - The old row is retired in the same transaction that publishes the new
    one (`state.WithReplacedSnapshot`), so a wake always finds a row.
    Retiring the old row in a separate statement left a window, and a wake
    120 ms after park cold-booted the cache empty (run
    redis-20261118T230904).
  - Clean recapture exists so one request's state cannot reach later
    requests. That concern does not apply here: the cache's memory is the
    customer's own data, and it is restored only into the same cache.
- **A handoff never goes live without its snapshot.** An ordinary
  deployment whose prime capture fails goes live without a snapshot, and its
  first request cold boots (deploy-path review P13). A handoff deployment
  cannot do that. Its prime VM holds the copied data, and a cold boot runs the
  copy again from the cache's own address, which after cutover is the new VM
  itself, so port 6379 never opens. In the soak run redissoak-20261121T120457
  the cache was down, and its data lost, after one failed capture.
  - schedd now fails such a prime instead (`isManagedCacheHandoff`). The
    rollout aborts and the running VM keeps serving with its data.
  - Its writes stay paused until the handoff's `CLIENT PAUSE` expires, at most
    300 s. They are delayed, not lost.
- **A handoff cuts over at once, never by canary.** The safe-by-default
  release policy (ADR-1446) gives a production deploy of an app that already
  serves a 4-step canary. On prod (2026-11-23, first run with
  `service_tcp` on) a resize's handoff deployment got one, so the old VM
  kept serving for 7m40s beside the new one. That outlasts the 300 s write
  pause, so the old VM could acknowledge writes that were dropped with it. A
  canary or 5xx rollback has nothing to offer a cache anyway: the old VM's
  copy goes stale as soon as the handoff starts. `release.ApplyDefault` now
  leaves every deploy of a cache app immediate (ADR-1446 amendment of
  2026-11-23), and the prod journey fails a resize whose handoff takes over
  3 minutes.
- **Unchanged:** the stateful-image deny-list. `valkey` is not on it today,
  and adding it would break customers who deploy Valkey themselves. Owner
  decision 2026-11-25: it stays off the list; managed Redis is the easy path,
  not the only one.
- **Valkey upgrades (2026-11-25):** a cache deployment may run any image in
  `api.ManagedValkeyImages()`; imaged refuses any other image on a cache app.
  schedd gives the raw TCP probes to a deployment that runs one of them with
  the command apid gives a cache (`api.IsManagedValkeyCommand`). A customer's
  own app on the official Valkey image keeps its own probes. A new Valkey image
  ships in two releases. The first adds it to the list. The second makes it
  `api.ManagedValkeyImage`, which apid deploys. A schedd that did not know
  the image would give the cache the HTTP liveness probe, which kills a cache
  about 50 s after it starts, so apid must never deploy an image the previous
  release does not accept. Existing caches keep their image until their
  next handoff moves them: `gregale redis update`, a resize and an
  eviction-policy change all deploy `api.ManagedValkeyImage`, so a resize
  also upgrades the cache. An image leaves the list
  only once no cache deployment runs it. A cache whose image left the list
  would get the HTTP liveness probe on its next wake and be killed. Before
  removing an image, this query on the control-plane database must return no
  row for it:
  `SELECT d.image_digest, count(*) FROM managed_valkey_caches c JOIN apps a ON a.id = c.app_id JOIN deployments d ON d.app_id = c.app_id WHERE a.status <> 'deleted' AND d.status IN ('imaging', 'live') GROUP BY 1;`
  (A deleted cache app can keep a `live` deployment row, but it never wakes.)
  First use: 8.1.4 → 8.1.12 (9 live caches on 8.1.4 on 2026-11-25).
  Step 1 (#8091) added 8.1.12 to the list; step 2 makes it the image apid
  deploys and keeps 8.1.4 accepted for the caches that still run it.
  Checked before step 2 (2026-11-26): `TestManagedRedisMetal` passes on
  8.1.12 (create, park and wake, restart, resize handoff; native run
  redis-20261126T072526, leakcheck clean). The real handoff script also copied
  an 8.1.4 cache (apid's server command, 5,000 keys, a writer running) into
  8.1.12 in containers: the new VM promoted itself, opened 6379 after 5 s and
  held every acknowledged write (127 of 127).
  Rolled out 2026-11-27: prod (rc.382+) created a new cache on 8.1.12, and
  `gregale redis update` moved the two remaining 8.1.4 caches in about 45 s
  each, keeping their data. With no 8.1.4 cache left outside deleted apps,
  8.1.4 left the list.
- **Cross-account isolation verified on prod (2026-11-25):** an app in
  another account cannot reach a cache. Service addresses are indexes within
  each account, so the proxy resolves a foreign cache's address in the
  caller's own account and refuses it (`unknown_service`). DNS gives a
  foreign cache's name and a name that does not exist the same answer, so it
  does not reveal which caches exist. The authorizer requires the same owner
  (`servicebinding.NewAuthorizer`) before any cache grant applies.

## Rejected alternatives

- **Database nodes (first draft of this ADR).** Owner chose compute nodes.
  They already have scale to zero, placement, billing and private TCP, and
  a cache needs no volume.
- **Close connections at park and let clients reconnect.** Simpler, but
  many clients surface the first failed command as an error (Jedis without
  pool validation, redis-py without `retry_on_error`). Holding sessions in
  the proxy costs one per-session state record.
- **Treat any byte as activity (today's ADR-576 rule).** A pooled client's
  `PING`s would keep every cache awake forever, so it would never scale to
  zero.
- **Cold-boot on resize, upgrade or restart.** Rejected by the owner
  (2026-11-16): planned operations must keep data.
- **Resize by virtio-mem only.** It would avoid a copy for size changes, but
  it is still a proposal (ADR-20261027090015 §7) and cannot change the
  Valkey binary. The handoff covers every case with one mechanism. virtio-mem
  can later make growth copy-free.
- **Move data by `SCAN` + `DUMP`/`RESTORE` from the proxy.** No fork, but
  writes during the copy need dual-writes and ordering rules. Replication
  already solves that.
- **Cold-boot on wake (no memory snapshot).** Wake would empty the cache
  every time, so an idle cache would be worthless.
- **Provider adapter (Upstash), native KV over HTTP, shared multi-tenant
  Valkey.** Rejected earlier for latency and margin, incompatibility, and
  cross-tenant blast radius.
- **Redis 8, Dragonfly, KeyDB.** Licences (RSAL/SSPL/AGPL, BSL) or
  maintenance. Valkey is BSD.

## Open questions

- Product name: the CLI noun `redis` and "Redis-compatible" wording are
  descriptive use. Legal should confirm, or rename the noun to `kv`/`cache`.
- Plan cache counts and the 7-day parked snapshot lifetime are provisional.
- Whether park snapshots of caches are stored off-node (survive node loss)
  depends on the snapshot store in use when this ships.
