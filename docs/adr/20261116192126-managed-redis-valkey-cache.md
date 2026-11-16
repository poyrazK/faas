# ADR-20261116192126: Managed Redis-compatible cache on self-hosted Valkey

- **Status:** proposed. Owner decisions, 2026-11-16 ("How can we provide
  redis to our users?"): self-host Valkey, **cache-only** durability,
  Hobby and up. Nothing is built yet.
- **Date:** 2026-11-16
- **Owner:** apid, schedd and database-node maintainers
- **Amends:**
  - [ADR-1027](1027-native-managed-postgres-on-firecracker-database-nodes.md):
    database nodes also run cache VMs, and the one public listener on a
    database node also serves the Redis protocol.
  - The stateless-only rule (ADR-066 as ADR-1027 amends it;
    [ADR-047](047-stateless-runtime-advisory.md)) is unchanged: customers still
    cannot deploy `redis`/`valkey` images as apps (`stateless_only_violation`).
    Cache VMs are a platform-operated workload like database VMs.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Customers ask for Redis and there is none.** `docs/storage.md` sends them
  to Upstash. The microservices-demo run (2026-11-15) could not run its
  `cartservice` because Compose `redis` image services are skipped, and the
  `REDIS_ADDR=redis:6379` import is dropped as a service address
  (`projectsetup.EnvSkipServiceAddress`). Sessions, rate limits, caches and
  job locks are what most web stacks keep in Redis.
- **Apps cannot hold it themselves.** Instances park and wake, `/tmp` is a
  tmpfs, there are no durable volumes, and the stateful base-image deny-list
  rejects `redis:7`. A Redis inside an app image is per-instance and lost at
  park.
- **The platform already has the pieces.** ADR-1027 built database nodes:
  one Firecracker microVM per tenant, vsock-only guests, `vmmd` VMs that
  outlive vmmd restarts, a schedd scheduler (`pkg/dbsched`) with node
  admission and wake storms handled, and `pgproxyd` as the TLS front door.
  Managed PostgreSQL (ADR-155, GA ADR-20261031091607) built the account
  resource, bindings into the encrypted app-secret store, plan entitlements,
  metering and the past-plan lifecycle.
- **Licensing.** Redis 8 is RSALv2/SSPLv1/AGPLv3. Valkey (Linux Foundation
  fork of Redis 7.2) is BSD-3-Clause and speaks the same RESP2/RESP3
  protocol, so every Redis client library and `redis-cli` work unchanged.

## Decision

Offer an account-scoped, **Redis-compatible cache** backed by **Valkey**,
running as **one Firecracker microVM per cache on database nodes**. It is
**cache-only**: data lives in memory and is lost on restart, node failure,
release roll or plan suspension. There is no persistence tier.

### Resource and API

- `Cache` is an account resource (organization-owned like managed Postgres,
  ADR-202610171906), with a name, region, memory size, eviction policy and
  state (`provisioning`, `running`, `restarting`, `suspended`, `deleting`).
- API `/v1/redis/*`: create, list, get, resize, update eviction policy,
  restart, delete, rotate credentials, bindings, and `info` (used memory,
  keys, hit/miss, evictions, connections, ops/s).
- CLI `gregale redis create|list|info|resize|delete|bind|unbind|connect|rotate`.
  `gregale redis connect` opens `redis-cli` (or prints the URL) with a
  short-lived credential.
- Bindings follow ADR-155: a binding names the app, environment scope and
  env key (default `REDIS_URL`, value `rediss://default:<token>@<id>.redis.<domain>:6379`).
  The credential lives only in the encrypted secret store; catalog rows hold
  an opaque reference.
- Behind the `managed_redis` capability flag until the GA gate below.

### Cache VM

- New guest boot mode in `guest-init`: start `valkey-server` directly,
  listen on a unix socket, bridge one vsock port to it. **No network
  interface, no drive1 volume.** drive0 is a shared read-only base holding
  the Valkey binary (one base per Valkey minor, as one per PG major).
- Valkey config is fixed by the platform: `save ""`, `appendonly no`,
  `maxmemory` = the cache's size, `maxmemory-policy` from the resource
  (default `allkeys-lru`; also `allkeys-lfu`, `volatile-lru`,
  `volatile-lfu`, `volatile-ttl`, `noeviction`), `databases 16`.
- VM RAM = `maxmemory` + fixed overhead (`api.CacheVMOverheadMB`, measured
  in slice 1; target ≤ 40 MB for kernel, guest-init and Valkey's
  fragmentation headroom). The cgroup keeps the §11 cap (RAM + 8 MB); there
  is no writeback burst because there is no volume.
- Customer ACL user `default` gets `+@all` minus `@admin`, `CONFIG`,
  `DEBUG`, `MODULE`, `SHUTDOWN`, `SAVE`, `BGSAVE`, `BGREWRITEAOF`,
  `REPLICAOF`/`SLAVEOF`, `FAILOVER`, `CLUSTER`, `MONITOR` and `ACL`.
  `FLUSHALL`, Lua scripts (`EVAL`, Functions), pub/sub, streams and
  transactions are allowed: they act only inside the tenant's VM.
- **Never memory-snapshotted, never parked for idleness.** Redis clients hold
  pooled connections and ping, so idleness is not observable, and a cache
  emptied after an idle timeout would surprise. A cache runs while its
  account is in good standing. That is affordable because caches are small
  and the VM has no disk.

### Scheduling

- schedd gains cache VMs in `pkg/dbsched` (same `database_nodes`
  inventory, a `cache_vms` table, the same node-row-locked admission). Placement
  charges RAM only; a node's `cache_ram_ceiling_mb` bounds cache VMs so they
  cannot starve database wakes. Boot slots are shared with databases.
- vmmd gains `StartCache` / `StopCache` / `CacheControl`, with the same
  jail record and adoption on vmmd restart (ADR-1027 "vmmd restarts keep
  databases running"), so routine vmmd releases do not empty caches.
- **Node failure:** schedd re-places every cache from a dead node on another
  node within its usual node-loss detection, empty, same hostname. Target:
  serving again in under 60 s.
- **Valkey minor or guest-init change:** caches turn `stale` as databases
  do. A stale cache restarts (empties) only inside the account's
  maintenance window, at most once a week unless a security fix needs it,
  and the restart is posted as a `redis.restarted` event and in
  `gregale redis info`. A security release may restart immediately; the
  incident runbook names that.

### Connection proxy

`pgproxyd` becomes the database node's protocol-aware front door for both
protocols (one public listener per node role stays true). On the Redis port
(6379, TLS 1.3 only) it:

1. routes by SNI `<cache>.redis.<domain>` (all mainstream clients send SNI
   with `rediss://`);
2. reads the client's first command and requires `AUTH` or `HELLO … AUTH`;
   it verifies the token against a stored SHA-256 of a 256-bit random token
   (not a password, so no slow hash is needed), and refuses everything else
   before touching the guest;
3. splices the session to the guest over vsock, having authenticated to
   Valkey with the guest's internal password (never leaves the platform).

So rotation and revocation are proxy-only; the guest's credential never
changes. The proxy enforces per-cache connection caps and per-source
connection-rate limits, and counts bytes. `CLIENT LIST`/`CLIENT KILL`
inside the guest see only the proxy's connections; that is acceptable.

### Apps that speak plain `host:6379`

Many stacks (the microservices-demo `cartservice`, Sidekiq/Celery defaults)
use `REDIS_ADDR=redis:6379` with no TLS and no password. For a bound cache,
the app VM's outbound path (`outboundd` / the guest TCP bridge) offers a
**plaintext loopback endpoint**: the binding may set a host alias
(`redis`, the Compose service name) that resolves inside the app VM to a
local forwarder which opens TLS to the proxy and sends `AUTH` with the bound
token before splicing. The app needs no code change; the credential never
enters app env when only the alias is used.

### Compose and `gregale dev`

- A Compose service whose image is `redis`, `valkey/valkey`,
  `redis/redis-stack-server` or `bitnami/redis` is provisioned as a managed
  cache on Hobby and up (as Compose `postgres` is), sized from
  `--maxmemory` in its `command:` if present, else the plan default. Every
  service referencing it by name gets the plaintext alias above, and
  `REDIS_URL` if it reads that key. On Free, the deploy names the plan as the
  reason (as for Postgres).
- `gregale dev --redis` starts a local Valkey container or uses the remote
  cache through `gregale redis connect`; `--service-override-file` already
  accepts `REDIS_URL`.

### Plans and metering

Provisional numbers, to be confirmed against the COGS model (ADR-1430):

| Plan | Caches | Pooled memory | Max per cache | Connections per cache |
| --- | ---: | ---: | ---: | ---: |
| Free | — | — | — | — |
| Hobby | 1 | 50 MB | 50 MB | 100 |
| Pro | 3 | 512 MB | 256 MB | 500 |
| Scale | 10 | 4 GB | 2 GB | 2,000 |

- Free gets `403 managed_redis_not_in_plan`, matching
  ADR-20261110184700.
- Memory is a hard cap, not an overage: Valkey evicts by policy (or refuses
  writes with `OOM` under `noeviction`). No overage billing in v1.
- Meter `memory_byte_seconds` (allocated `maxmemory` per running second,
  like app RAM billing) and proxy `egress_bytes` for traffic to clients
  outside Gregale; traffic from Gregale apps is not egress.
- An account that falls below its plan has its caches **stopped at once**
  (nothing to keep) and their records deleted after 30 days, reusing the
  delete grace period. Free and unverified accounts never reach database
  nodes; abuse rules (CPU mining, ADR on CPU abuse) apply to cache VMs too.

### Security notes

- A Valkey/Lua remote-code bug (2025 had several, e.g. Lua use-after-free)
  reaches only the tenant's own VM: no NIC, jailer uid, seccomp, cgroup.
  The stale-roll path above is the patch path; target: patched Valkey on
  every node within 72 h of an upstream security release.
- Public reachability is the same as managed Postgres: TLS-only, auth
  before splice, rate-limited. An `--allow-from` IP list per cache is a
  follow-up.

## Phasing

1. **Guest and vmmd.** Valkey base image, cache boot mode, `StartCache`/
   `StopCache`, adoption on vmmd restart; metal test measures overhead MB,
   cold boot time and density per node.
2. **schedd and proxy.** `cache_vms`, placement/admission, node-loss
   re-placement, stale roll in maintenance windows; RESP auth-before-splice
   in `pgproxyd`.
3. **apid and CLI.** `/v1/redis/*`, catalog, bindings, plan entitlements,
   metering, past-plan lifecycle, `gregale redis …`, flag `managed_redis`
   (dark).
4. **Compose and plaintext alias.** Compose provisioning, outbound
   forwarder, `gregale dev --redis`, microservices-demo `cartservice`
   unchanged as the acceptance test.
5. **GA gate.** Docs (`docs/redis.md`, `docs/storage.md` table), capability
   row, runbook (node loss, security roll), journey in the full prod run,
   owner approval to release the flag.

Depends on database nodes being in production (ADR-1027 is still behind its
qualification gate; production managed Postgres runs on Neon). Slices 1–3
can land and be qualified on staging metal before that.

## Rejected alternatives

- **Provider adapter (Upstash/Redis Cloud).** Fastest, but cross-provider
  latency (~34 ms per round trip measured to Neon) defeats a cache, margins
  are thin, and data sits with a subprocessor. Owner chose self-host.
- **Gregale-native KV over HTTP.** Not Redis-compatible; existing apps and
  Compose stacks would not run unchanged.
- **Persistent tier (AOF + replica + backups).** Owner chose cache-only.
  Customers who need durable state use managed Postgres or object storage.
  A persistent tier would need drive1 volumes and a standby, as ADR-1027
  does for Postgres; it can be added later behind the same API.
- **Shared multi-tenant Valkey with ACL users per tenant.** Cheaper per
  cache, but one tenant's `KEYS *`, big Lua script or memory pressure hits
  every tenant, and one Valkey bug crosses tenants. ADR-1027's no shared
  processes rule holds.
- **Redis 8, Dragonfly, KeyDB.** Redis 8's licences and Dragonfly's BSL
  restrict hosting; KeyDB is barely maintained. Valkey is BSD and is what
  the large clouds now run.
- **Idle park.** See "Cache VM": idleness is not observable through pooled
  connections, and a silently emptied cache is a worse surprise than the
  small always-on cost.

## Open questions

- Product name: the CLI noun `redis` and "Redis-compatible" wording are
  descriptive use; legal should confirm, or rename the noun to `kv`/`cache`.
- Plan numbers above are provisional.
- Whether Hobby caches share database nodes with paying Postgres or get
  their own pool.
