# ADR-20261116192126: Managed Redis-compatible cache on compute nodes, with scale to zero

- **Status:** proposed. Owner decisions, 2026-11-16: self-host **Valkey**,
  **cache-only** durability, **Hobby and up**; then "Redis should work on
  compute nodes, max 512mb (can change later), and how the scale to zero
  would work?". Nothing is built yet.
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
- The service TCP proxy, which already terminates each session, **sends
  `AUTH` to Valkey on the caller's behalf**. Apps need no password; a
  client's own `AUTH`/`HELLO … AUTH` is accepted and replaced. So
  `REDIS_URL=redis://<name>.svc.gregale:6379` is the whole binding, and the
  internal secret is never in app env.
- Compose: `REDIS_ADDR=redis:6379` resolves through the existing project
  sibling-name import (`host:port` of a sibling service) once the `redis`
  service exists as a cache. No forwarder inside the app VM is needed.
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
| Resize, Valkey upgrade, restart | **lost** (cold boot) | reset; clients reconnect |
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

### Upgrades

A new Valkey minor or guest-init makes caches `stale`. Parked caches pick
it up at their next wake **only if** their snapshot is gone. Otherwise
restoring the old snapshot keeps the old image, and the upgrade waits for
the next cold boot. Running stale caches restart (empty) only in the
account's maintenance window, at most weekly. A security fix may restart at
once, announced as a `redis.restarted` event. Target: patched Valkey across
the fleet within 72 h of an upstream security release.

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
5. **GA gate.** `docs/redis.md` and the `docs/storage.md` table, capability
   row, runbook (node loss, security roll), a journey in the full prod run
   covering park → held session → wake, then owner approval to release the
   flag.

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
