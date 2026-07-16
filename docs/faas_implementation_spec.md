# One-Box FaaS — Implementation Whitepaper

**Version 1.0 · July 2026 · Confidential, internal**
**Audience:** engineers and coding agents building the platform. This document is the buildable spec; treat it as the source of truth for architecture decisions. Business rationale lives in the founding whitepaper (`faas_founding_whitepaper.pdf`); financial numbers live in `ex44_faas_financial_model.xlsx`. Where this document and the spreadsheet disagree on a business number, the spreadsheet wins.

**How to use this document (agents):** every component in §4 is independently implementable against the interfaces defined here. Milestones in §14 are ordered and each has executable acceptance criteria — do not start milestone N+1 before N's criteria pass. Record any deviation from this spec as a new ADR in §3's format.

---

## 1. Inherited constraints (the physics)

These numbers come from the financial model and are **not negotiable at implementation time** — code must enforce them, telemetry must verify them.

| Constraint | Value | Enforced by |
|---|---|---|
| Host: Hetzner EX44 | i5-13500 (20 threads), 64 GB DDR4, 2×512 GB NVMe RAID-1, 1 Gbit | — |
| Host OS RAM reserve | 2 GB | budget table §13 |
| Control-plane RAM reserve | 6 GB | budget table §13, systemd slices |
| Tenant RAM budget | 56 GB | `schedd` admission |
| RAM utilization ceiling | 85 % of tenant budget (47.6 GB) | `schedd` headroom guard |
| Per-VM overhead budget | 8 MB (VMM + jailer + TAP slack) | `schedd` accounting |
| CPU overcommit | 8× (160 vCPU slots) | `schedd` admission |
| Disk reserve (OS, kernels, base images, logs) | 60 GB | LVM layout §8 |
| Snapshot budget | 452 GB | `imaged` GC + quotas |
| Fleet average snapshot size target | 130 MB | telemetry alert §12; quotas §8 |
| Plan quotas (deployed / concurrent / MB RAM / GB-h) | Free 1/1/128/5 · Hobby 5/2/256/50 · Pro 25/5/512/250 · Scale 100/20/1024/1500 | `apid` validation, `schedd` admission, `meterd` quotas |
| Expected resident concurrency (planning) | 0.02 / 0.15 / 0.60 / 3.00 | telemetry comparison only |
| Overage meter | €0.01 per GB-RAM-hour | `meterd` → Stripe |
| Build cost envelope | builds fit inside control-plane + headroom, never tenant RAM | `builderd` admission §9 |

**Three numbers the business is fragile to** (founding doc §5): fleet snapshot size, resident concurrency per customer, churn. The first two are produced by this system — §12 makes them first-class metrics from day one.

---

## 2. Architecture overview

One physical host runs everything. Every box below is a systemd unit; every arrow is either HTTP over localhost, gRPC over a unix socket, or a Postgres row.

```
                        ┌───────────────────────────────── EX44 ─────────────────────────────────┐
                        │                                                                        │
  client ── TLS ──►  gatewayd ──── route lookup (cache→PG) ────┐                                 │
  *.apps.DOMAIN         │                                      │                                 │
  custom domains        │ wake needed?                         ▼                                 │
                        ├────────► schedd ─────────► vmmd ── jailer ── firecracker ── microVM    │
                        │           │  admission,     │        (one process per running VM)      │
                        │           │  reaper,        │                                          │
                        │           │  eviction       │  netns + TAP per instance (§7)           │
                        │           ▼                 ▼                                          │
                        │        postgres ◄──── snapshot/restore on NVMe (§8)                    │
                        │           ▲                                                            │
   deploy (API/CLI) ──► apid ───────┤                                                            │
                        │           │                                                            │
                        └─► builderd ── ephemeral builder microVM (Railpack/BuildKit inside)     │
                                    │                                                            │
                                 imaged ── OCI ➜ ext4 rootfs + guest-init ➜ boot ➜ snapshot      │
                                    │                                                            │
                        meterd ── 1 s samples ➜ minute rows ➜ GB-h ➜ Stripe usage records        │
                        └────────────────────────────────────────────────────────────────────────┘
   off-box: object storage (build cache, cold snapshots, backups) · Storage Box (PG WAL + nightly) · Stripe · DNS
```

**Request path (hot):** TLS → `gatewayd` → routing cache hit → proxy to instance IP:8080 → response. Budget: < 2 ms added latency.

**Request path (cold wake):** `gatewayd` sees app has no running instance → holds the request → asks `schedd` → admission check (RAM headroom, plan concurrency) → `vmmd` restores snapshot into fresh netns/TAP → guest resumes (app already initialized in snapshot memory) → readiness ping → proxy. Budget: p50 ≤ 350 ms, p95 ≤ 800 ms first byte (§6.3).

**Deploy path:** `apid` accepts source (≤ 100 MB) or OCI reference → `builderd` runs the build in an ephemeral builder microVM → OCI image → `imaged` converts it to a per-app **app layer** over a shared read-only base (two-drive scheme, §4.6) + injects `guest-init` → boots once, waits ready, pauses, snapshots → app state = `PARKED`. First deploy of an app is also its first snapshot.

**Two workload models, one data plane (ADR-003):** an *App* is any HTTP server listening on `:8080` in its microVM. A *Function* is an App whose rootfs we generate from a platform runner image (node22 / python312) wrapping the customer's handler file behind the same `:8080` contract. Functions get zero new infrastructure: same lifecycle, same snapshots, same metering, same routing. Cron triggers are synthetic requests fired by `schedd`.

---

## 3. Decision record

Format for future ADRs: `ADR-NNN · title · status · decision · consequences`. These ten are **accepted** and locked for v1.

| ADR | Decision | Why | Rejected alternatives |
|---|---|---|---|
| 001 | Control plane in **Go**, monorepo, static binaries | firecracker-go-sdk is first-party and actively maintained (Go ≥ 1.23); single-binary deploys; agents generate/test Go well | Rust (slower iteration, no first-party SDK), Node/Python (RAM cost on a budgeted box) |
| 002 | **Builds on the EX44** (option B), governed | Founder decision; €0 extra; one machine | Off-box builder VM (revisit at Gate B if build queue p95 > 60 s) |
| 003 | **Builds run inside ephemeral builder microVMs**, not host containers | Untrusted `npm install` gets the same VM-grade isolation as untrusted runtime code; RAM cap is the VM boundary (exact, unbreachable); reuses vmmd primitives; kills rootless-runc attack surface on the host | Rootless BuildKit directly on host (weaker isolation, cgroup escapes are kernel bugs away), host docker (unacceptable) |
| 004 | Zero-config engine: **Railpack** (BuildKit-based, Go); **Dockerfile** escape hatch; **pre-built OCI** accepted | Railpack is Nixpacks' successor (Nixpacks in maintenance mode), produces far smaller images — directly protects the 130 MB fleet snapshot target | Nixpacks (larger images), CNB Buildpacks (multi-GB builder images don't fit our RAM/disk budget) |
| 005 | Park/wake via **Firecracker snapshot–restore**, file-backed memory; **cold boot from rootfs must always work** as fallback | Restore ≈ 150–300 ms with app already warm; snapshots are version-coupled to Firecracker, so they are cache, not truth | Cold boot always (1–3 s wakes), CRIU (fragile), keeping VMs resident (destroys the economics) |
| 006 | **Postgres 16** single node, one database, `sqlc`-generated queries | One state store for routes, apps, builds, usage; WAL-shipped to Storage Box | SQLite (concurrent writers: meterd + apid + schedd), etcd (nothing to consense on one box) |
| 007 | Edge: **gatewayd is our own Go binary** using CertMagic; wildcard `*.apps.DOMAIN` via DNS-01; custom domains via on-demand HTTP-01 | Wake-blocking (hold request during restore) is core product logic — we own it; CertMagic is Caddy's battle-tested TLS core as a library | Stock Caddy/Traefik + sidecar wake logic (two hops, split brain), nginx+lua (unmaintainable) |
| 008 | Host: **Ubuntu 24.04 LTS, cgroups v2 only**, KVM, systemd slices | Firecracker snapshot restore documented slow on cgroups v1; v2 is mandatory | Debian (fine too — pick one and stop) |
| 009 | Per-instance **network namespace with identical inner TAP** (`tap0`, guest 10.0.0.2/30) | Snapshots bake device topology + guest IP; identical-netns trick lets one snapshot restore as N concurrent instances; host side NATs per-instance | Per-instance IP baked in snapshot (breaks concurrency > 1), vsock-only (no inbound HTTP) |
| 010 | Billing: **Stripe subscriptions + metered overage item**; usage pushed hourly | Matches financial model (2.9 % + €0.30 assumption); dunning states in §10 | Homegrown invoicing (no), Paddle/LemonSqueezy (MoR fees eat the margin math — revisit only if VAT pain demands) |

---

## 4. Component specifications

Each component: single Go binary, own systemd unit, structured logs (JSON, `slog`), Prometheus `/metrics`, config via one TOML file + env overrides. All inter-component APIs are gRPC over unix sockets in `/run/faas/` except gatewayd→apps (plain HTTP).

### 4.1 `gatewayd` — edge proxy

**Owns:** TLS termination, routing, wake-blocking, request accounting, per-tenant rate limits.

- Listeners: `:443` (HTTPS, HTTP/1.1 + h2), `:80` (redirect + ACME HTTP-01).
- TLS: CertMagic. Wildcard cert for `*.apps.DOMAIN` via DNS-01 (Hetzner DNS API token). Custom domains (Pro+): on-demand HTTP-01 with an allowlist check against `custom_domains` table before issuance (prevents cert-mint abuse).
- Routing: hostname → `app_id` via in-memory cache (LRU, 10k entries) backed by Postgres `LISTEN app_routes_changed`. Cache miss = one indexed PG lookup.
- Wake-blocking: if app has no `RUNNING` instance, enqueue request (per-app queue, cap 512 requests / 30 s TTL, then `503 + Retry-After`), call `schedd.EnsureInstance(app_id)`, stream queued requests once readiness passes.
- Records `last_request_at[instance]` (in-memory, flushed to PG every 15 s) — this drives idle parking.
- Rate limits (token bucket, per app): Free 5 rps burst 20; Hobby 20 rps burst 100; Pro 100 rps burst 500; Scale 500 rps burst 2000. Over-limit → `429`.
- Request/response size caps: 25 MB body either direction. Timeouts: 60 s upstream response start, 300 s total.
- Emits: `gateway_requests_total{app,code}`, `gateway_wake_latency_seconds` (histogram), `gateway_queue_depth`.

### 4.2 `apid` — control API

**Owns:** the public REST API, auth, validation, and being the *only* writer to customer-intent tables.

- Auth: API keys (`fp_live_…`, SHA-256 stored), per-user; sessions for the dashboard later. Every key scoped to an account.
- Resources (all JSON; full endpoint table in Appendix A): accounts, apps, deployments, builds, instances (read-only), usage, plans, custom_domains.
- Deploy inputs (three, ADR-004): `POST /v1/apps/{app}/deployments` with (a) tarball upload `source` (≤ 100 MB Free/Hobby, ≤ 250 MB Pro/Scale — reject larger with `413` and a docs link), (b) `dockerfile: true` flag if tarball root has one, or (c) `image: registry.DOMAIN/...@sha256:...` reference.
- Function deploys: `type: function`, `runtime: node22 | python312`, tarball contains `handler.{js,py}` (+ optional `package.json` / `requirements.txt`). apid rewrites this into an App deployment using the runner scaffold (§4.9) and the same pipeline runs.
- Validation enforces plan quotas *before* work happens: deployed-sandbox count, RAM size ≤ plan cap, concurrency setting ≤ plan cap.
- Idempotency: `Idempotency-Key` header on all POSTs, stored 24 h.
- Never talks to vmmd/builderd directly — writes rows, notifies via `pg_notify`; owners poll/listen.

### 4.3 `schedd` — scheduler and lifecycle owner

**Owns:** the instance state machine (§6), admission control, idle reaper, eviction, cron.

- **Admission (wake or build):** grant iff
  `resident_ram_mb + request_mb + 8 ≤ 0.85 × 56_000` **and** plan concurrent count not exceeded **and** vCPU slots (160) not exhausted. Builds request from the same guard but are also capped by the build semaphore (§9). Denial → gateway serves `503 capacity` (alert fires long before customers see this; see §12).
- **Idle reaper:** every 10 s, park instances with `now − last_request_at > idle_timeout(plan)`. Defaults: Free 30 s, Hobby 60 s, Pro 300 s, Scale 600 s (app-configurable down to 10 s, not above plan default × 2).
- **Eviction (RAM pressure > 80 % of the 85 % target):** park instances LRU by last request; never evict an instance younger than 30 s; Scale plan evicted last.
- **Free-tier disk reaper:** free apps with zero requests for 14 days → snapshot + rootfs moved to object storage, state `EVICTED_COLD` (redeploy = one click, re-flatten from stored image). This is the founding doc's ceiling-protection policy (§9.7 there).
- **Cron:** `crons` table; fire = synthetic `POST` through gatewayd (so metering/limits apply identically).
- Single process, single writer to `instances` — no distributed locking on one box. Multi-node later = shard apps by node, one schedd per node, `apid` routes writes (interface kept narrow deliberately: `EnsureInstance`, `Park`, `Evict`).

### 4.4 `vmmd` — microVM supervisor

**Owns:** everything that touches `/usr/bin/firecracker` and the jailer. The only root-privileged component (CAP_NET_ADMIN + file ownership); drops per-VM work to the jailer immediately.

- One firecracker process per instance, always via **jailer**: unique uid/gid per instance (range 20000–29999, recycled), chroot `/srv/fc/jail/{instance}`, cgroup v2 scope `faas-tenant.slice/vm-{instance}.scope` with `memory.max = plan_mb + 8 MB`, `cpu.weight` by plan, pids ≤ 64.
- API: gRPC `CreateFromSnapshot(app, instance)`, `CreateColdBoot(app, instance)`, `Pause+Snapshot(instance)`, `Destroy(instance)`, `Stats()`.
- Snapshot create: pause VM → `PUT /snapshot/create` (full; memory file + vmstate) → fsync → destroy VM → record `snapshot_bytes`. Diff snapshots: not v1.
- Restore: create netns + TAP (§7) → jailer spawn → `PUT /snapshot/load` (`mem_backend: File`) → resume → guest agent re-seeds entropy + steps clock (§4.8) → readiness.
- Boot config (cold path): kernel 6.1 LTS from Firecracker CI artifacts, `console=off quiet`, **two virtio-blk drives** (drive0 shared base rootfs read-only; drive1 app layer — §4.6), one virtio-net, `mem_size_mib = plan`, `vcpu_count` = 2 (Scale: 4), MMDS off, balloon off (v1), entropy: virtio-rng.
- **Firecracker version pinning:** snapshots are only guaranteed to load on the Firecracker version that made them. `snapshots.fc_version` column; on FC upgrade, mark all snapshots stale — apps lazily re-snapshot via cold boot on next wake (this is why ADR-005 requires cold boot to always work).

### 4.5 `builderd` — build orchestrator

**Owns:** the build queue and ephemeral builder microVMs. Full pipeline in §9.

- Builder VM: 2 vCPU, **2048 MB**, 8 GB scratch ext4 (thrown away), 4 GB per-app cache volume (kept, quota'd), rootfs = our `builder-base` image containing BuildKit (rootless inside the VM — inside a VM it may as well be root), Railpack, git, and the OCI exporter. No inbound network; outbound via the build egress policy (§7).
- Semaphore: **1 guaranteed slot; a 2nd opportunistic slot** granted only when tenant resident RAM < 60 % of target (schedd admission). Queue is FIFO per account with global fairness (no account holds both slots).
- Timeouts: 10 min build, 15 min end-to-end. On timeout/OOM (VM hits its own wall — host unaffected): kill VM, mark build `failed(reason)`, requeue once if `oom` and slot was opportunistic.
- Source in: scratch disk pre-loaded with the tarball. Image out: OCI layout written to the cache volume, hash-addressed; host copies it out after VM exit (no live channel needed — keeps the surface tiny).

### 4.6 `imaged` — image and snapshot service

**Owns:** OCI → bootable rootfs conversion, base images, kernels, snapshot GC.

- **Two-drive scheme (protects the 130 MB fleet target):** drive0 = shared, read-only, content-addressed **base rootfs** (`base-minimal`, `runner-node22`, `runner-python312` — counted once, in the 60 GB reserve); drive1 = per-app **app layer** ext4 containing only the OCI layers above the base (deps + code + `/etc/faas/app.json`). guest-init assembles them with overlayfs at boot. A flattened single-drive rootfs would duplicate ~150+ MB of base per app and silently destroy the financial model's disk math — do not "simplify" to it.
- App-layer build: diff OCI layers above the matched base → `mkfs.ext4 -d <dir> layer.ext4 <padded size>`, ≤ plan app-layer cap: Free 256 MB, Hobby 512 MB, Pro 1 GB, Scale 2 GB. Content over cap fails the deploy with a clear error naming the cap and observed size. `guest-init` is injected into the app layer.
- Base images: `runner-node22`, `runner-python312`, `builder-base` — built in CI from Dockerfiles in `images/`, content-addressed, staged to `/srv/fc/base/`.
- Snapshot GC: keep current + previous deployment's snapshots per app; delete orphans nightly; enforce the 452 GB budget with account-level fairness (biggest-over-quota first). Emits `snapshot_fleet_avg_mb` and `snapshot_fleet_p95_mb` — **the** business metrics.

### 4.7 `meterd` — metering and billing

**Owns:** usage truth. Sampling → aggregation → quota state → Stripe.

- Sample loop (1 s): for each RUNNING instance read cgroup `memory.current` (host truth, includes VMM) → accumulate `mb_seconds`. Flush per-minute rows: `usage_minutes(instance, app, account, minute, mb_seconds, requests)`.
- GB-RAM-hour = `Σ mb_seconds / 1024 / 3600`, computed on **plan RAM size + 8 MB overhead**, not sampled RSS, for billing (predictable for customers; matches the financial model's math). Samples are kept for capacity telemetry.
- Quota ladder per account per month: 0–100 % of included GB-h: nothing; 100 %: email; Free tier at 100 %: hard stop (park, don't wake, `402` page); paid tiers: overage accrues at €0.01/GB-h, pushed to Stripe as usage records hourly.
- Stripe objects: Product per plan; monthly Price; one metered Price (`gb_ram_hour`); customer + subscription per account; webhooks consumed: `invoice.paid`, `invoice.payment_failed`, `customer.subscription.updated/deleted`.
- Dunning: `payment_failed` → account `past_due` (apps run, deploys blocked) → 7 days → `suspended` (apps parked, wake returns `402` page) → 21 days → `deleted_pending` (30-day snapshot retention, then GC). All transitions emailed.

### 4.8 `guest-init` — PID 1 inside every microVM

Tiny static Go binary (< 5 MB), injected by imaged.

Boot path: mount `proc`/`sys`/`tmp` → bring up `eth0` (always 10.0.0.2/30, gw 10.0.0.1 — identical in every VM, ADR-009) → apply `/etc/faas/app.json` env → exec app as uid 1000 (`app`) → supervise (restart ≤ 3, then exit VM).
Readiness: TCP accept on `:8080` (or optional `GET /healthz` if declared).
Resume path (post-restore, triggered by host signal via vsock): re-seed `/dev/urandom` from virtio-rng, step clock via `ptp_kvm`/chrony `makestep` (restored guests wake with stale clocks and duplicate RNG state otherwise — both are known snapshot-restore hazards), then re-arm readiness.

### 4.9 Function runners

`runner-node22`, `runner-python312`: a 15-line HTTP host on `:8080` that loads the customer handler and adapts request/response.

Contract (identical across languages): handler receives `{method, path, headers, query, body_b64}`; returns `{status, headers, body_b64}` or a plain body. Node: `export default async function handler(req)`. Python: `def handler(request) -> Response | dict | str`.
Streaming, websockets: not in v1 for functions (fine for Apps — gatewayd proxies them transparently).
Adding a runtime = adding one runner image + one detection rule. Target: launch with these two; Go runner at M7 if demanded.

---

## 5. Data model (Postgres, authoritative excerpt)

`sqlc` against this schema; migrations via `goose`, numbered, never edited after merge.

```sql
create table accounts (
  id uuid primary key default gen_random_uuid(),
  email citext unique not null,
  plan text not null default 'free',            -- free|hobby|pro|scale
  status text not null default 'active',        -- active|past_due|suspended|deleted_pending
  stripe_customer_id text unique,
  created_at timestamptz not null default now()
);

create table api_keys (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id),
  key_sha256 bytea unique not null,
  label text, last_used_at timestamptz,
  created_at timestamptz not null default now()
);

create table apps (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id),
  slug text unique not null,                    -- {slug}.apps.DOMAIN
  type text not null default 'app',             -- app|function
  runtime text,                                 -- node22|python312 when function
  ram_mb int not null,                          -- ≤ plan cap
  idle_timeout_s int,
  max_concurrency int not null default 1,       -- ≤ plan cap
  status text not null default 'active',        -- active|evicted_cold|deleted
  created_at timestamptz not null default now()
);

create table deployments (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  build_id uuid,                                 -- null when image: deploy
  image_digest text not null,
  rootfs_path text, rootfs_bytes bigint,
  status text not null,                         -- pending|building|imaging|snapshotting|live|failed|superseded
  error text,
  created_at timestamptz not null default now()
);

create table builds (
  id uuid primary key default gen_random_uuid(),
  deployment_id uuid not null references deployments(id),
  kind text not null,                           -- railpack|dockerfile
  source_bytes bigint not null,
  status text not null,                         -- queued|running|succeeded|failed
  failure_class text,                           -- oom|timeout|user_error|infra
  log_path text, started_at timestamptz, finished_at timestamptz
);

create table snapshots (
  id uuid primary key default gen_random_uuid(),
  deployment_id uuid not null references deployments(id),
  fc_version text not null,
  mem_bytes bigint not null, disk_bytes bigint not null,
  path text not null, stale bool not null default false,
  created_at timestamptz not null default now()
);

create table instances (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  deployment_id uuid not null references deployments(id),
  state text not null,                          -- §6 state machine, owned by schedd
  netns text, guest_uid int, host_ip inet,
  ram_mb int not null,
  started_at timestamptz, last_request_at timestamptz, parked_at timestamptz
);

create table usage_minutes (
  account_id uuid not null, app_id uuid not null, instance_id uuid not null,
  minute timestamptz not null,
  mb_seconds bigint not null, requests int not null default 0,
  primary key (instance_id, minute)
) partition by range (minute);                  -- monthly partitions, drop after 13 months

create table custom_domains (
  domain citext primary key, app_id uuid not null references apps(id),
  verified_at timestamptz
);

create table crons (
  id uuid primary key, app_id uuid not null references apps(id),
  schedule text not null, path text not null default '/', enabled bool not null default true
);

create table events (                            -- audit log, append-only
  id bigint generated always as identity primary key,
  at timestamptz not null default now(),
  actor text not null, kind text not null, subject uuid, data jsonb
);
```

Conventions: every state column has a CHECK constraint; every table with `account_id` gets a composite index leading with it; all money math in integer cents/millicents — never floats.

---

## 6. Instance lifecycle

### 6.1 State machine (owner: schedd; single writer)

```
                       deploy pipeline (§9)
  (new deployment) ──────────────────────────► PARKED
                                                 │  wake (request │ cron)
                                                 ▼
                    ┌──────────── WAKING ───────────────┐
                    │   restore ok        restore fail   │
                    ▼                                    ▼
                 RUNNING ◄──── readiness ──── COLD_BOOTING (fallback, marks snapshot stale)
                    │
     idle timeout / eviction / deploy superseded
                    ▼
               SNAPSHOTTING ──► PARKED
                    │ snapshot fail (disk?)
                    ▼
                 STOPPED (cold; next wake = COLD_BOOTING)     FAILED (crash-loop ≥3: park + notify)
```

Timers: WAKING ≤ 5 s then fallback to cold boot; COLD_BOOTING ≤ 30 s then FAILED; SNAPSHOTTING ≤ 20 s then STOPPED. Every transition is an `events` row.

### 6.2 Invariants (test these, they are the product)

1. At most `max_concurrency(plan)` instances of one app in {WAKING, COLD_BOOTING, RUNNING}.
2. Σ (ram_mb + 8) over all instances in {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING} ≤ 47,600 MB.
3. An app always has either a live snapshot or a rootfs it can cold boot — never neither.
4. A parked app consumes zero resident RAM (verify: cgroup gone).
5. Two concurrent instances restored from one snapshot never share an IP, netns, jail uid, or RNG stream.

### 6.3 Wake latency budget (p50 targets)

| Step | ms |
|---|---|
| gatewayd route + queue + schedd admission | 5 |
| netns + TAP + jailer spawn | 30 |
| snapshot load (file-backed, NVMe) + resume | 150–250 |
| guest resume hook (entropy, clock) + readiness | 40 |
| proxy first byte | 5 |
| **Total p50 / p95 target** | **≤ 350 / ≤ 800** |

Measured end-to-end as `gateway_wake_latency_seconds`. Regression gate in CI-on-metal (§14).

---

## 7. Networking

- Public: gatewayd binds :80/:443 on the host IP. Nothing else listens publicly. SSH on a non-standard port, key-only, fail2ban.
- Per instance: netns `fc-{instance}`; inside it `tap0` ↔ firecracker; guest always `10.0.0.2/30`, host side `10.0.0.1` (ADR-009 — identical inner world so any snapshot restores anywhere). A veth pair `ve-{instance}` bridges the netns to `br-tenants`; the veth's host address `10.100.x.y/16` is the instance's routable identity; nftables DNATs `host_ip:ephemeral → 10.0.0.2:8080` within the netns.
- Egress (tenant): default-allow TCP 80/443/53 + UDP 53; **deny 25, 465, 587** (spam = Hetzner abuse desk = existential, founding doc R6); deny RFC1918 + link-local + metadata ranges (no lateral movement into the control plane); per-instance conntrack cap 4,096; egress bandwidth per plan via `tc`: 10 / 25 / 100 / 250 Mbit.
- Egress (builder VMs): allow 443/80/53 to package registries only via a squid allowlist in v1.1; v1 = same as tenant policy. Deny everything inbound always.
- DNS names: `{slug}.apps.DOMAIN` wildcard A record → host IP. Custom domains: customer CNAMEs to `edge.DOMAIN`, apid verifies via TXT `_faas-verify.{domain}` before gatewayd will mint a cert.

---

## 8. Storage layout

```
/srv/fc/
  base/                       kernels (vmlinux-6.1.x), shared ro base rootfs + builder images (content-addressed, in 60 GB reserve)
  apps/{app}/layer-{deploy}.ext4        per-app app layer (drive1)
  snaps/{app}/{deploy}/{mem,vmstate,disk}
  jail/{instance}/            jailer chroots (tmpfs-backed, empty when idle)
  cache/build/{app}/          per-app build cache volume, 4 GB quota, LRU
```

LVM on the RAID-1 pair: `lv-system` 60 GB (the reserve, includes `/srv/fc/base`), `lv-fc` the rest (≈ 452 GB) for app layers + snapshots. XFS with project quotas per app directory enforcing app-layer caps (§4.6) plus snapshot bytes. Parked footprint per sandbox = mem file + vmstate + app layer; the fleet average of that sum is the 130 MB business target. `imaged` refuses new snapshots when `lv-fc` > 90 % and pages the operator at 80 % (§12).

Off-box: object storage bucket (build caches evictable, cold-evicted free-tier images, weekly snapshot archive); Storage Box (PG WAL streaming via `pgbackrest`, nightly base backup, `/srv/fc/base` mirror). **Restore drill is a milestone acceptance test (§14 M8), not a document.**

---

## 9. Build pipeline (decision B, hardened)

Phases, all rows on `builds`/`deployments`:

1. **Accept** (apid): tarball ≤ cap; reject > 10k files or symlink escapes; store to spool.
2. **Queue** (builderd): FIFO + fairness; position surfaced in API (`queued_ahead`).
3. **Plan**: if `Dockerfile` present and plan ≥ Hobby → dockerfile kind; else Railpack detect (node/python first-class at launch; its other providers best-effort). Detection failure → actionable error ("no lockfile found — supported: …").
4. **Build** (inside builder microVM): scratch disk gets source + per-app cache volume mounted; Railpack or `buildctl` runs with `--frontend dockerfile` for kind=dockerfile; output = OCI layout on cache volume. VM killed on 10-min timeout. Host copies OCI out after exit.
5. **Image** (imaged): diff against base → app-layer ext4 within plan cap → inject guest-init (§4.6).
6. **Prime snapshot**: cold-boot once (readiness gate) → pause → snapshot → destroy → `PARKED`, deployment `live`, previous deployment `superseded` (kept for one-click rollback; its snapshot GC'd on the next successful deploy).
7. **Failure taxonomy** → `failure_class`: `user_error` (their code/config, full log shown), `oom` (VM hit 2 GB — message suggests smaller deps or Pro), `timeout`, `infra` (ours — auto-requeue once, alert).

Concurrency and RAM interaction (the R1 discipline, mechanized): builder VMs are admitted through the same headroom guard as tenant wakes, from the *headroom side* of the ledger — 1 guaranteed slot budgeted permanently in §13; the opportunistic 2nd slot exists only when tenant residency < 60 %. Builds can therefore never push tenant admission into refusal: tenants evict builds, never vice versa.

---

## 10. Metering and billing detail

- Unit: GB-RAM-hour, billed on provisioned `ram_mb + 8` per running second (§4.7). Definition published verbatim in docs — no surprise-RSS billing.
- Included quotas per plan per calendar month (UTC): 5 / 50 / 250 / 1,500 GB-h. Overage €0.01/GB-h, metered in millicents, Stripe usage records hourly, idempotent by `(subscription_item, hour)`.
- Requests are counted but not billed (v1). Egress not billed (1 Gbit flat).
- Plan changes: upgrade immediate + prorated by Stripe; downgrade at period end; quota checks (deployed count, RAM sizes) run pre-downgrade and block with a task list ("delete 3 apps or reduce RAM…").
- The `usage` API (`GET /v1/usage?month=`) returns exactly what the invoice will say — same query, same code path.

---

## 11. Security hardening checklist (ship-blocking)

**Host:** cgroups v2 unified only; kernel ≥ 6.8 HWE; `kernel.unprivileged_userns_clone=0` (nothing on the host needs it — builds are in VMs); auditd on execve in control-plane slices; unattended-upgrades security-only with reboot window Sun 04:00 UTC; nftables default-drop inbound.
**Jailer/VM:** unique uid/gid per instance; chroot; seccomp default filter (Firecracker's); `--daemonize` off, supervised by vmmd; no shared directories with guests — block devices only; virtio-rng always attached.
**Snapshot uniqueness:** resume hook re-seeds guest entropy + steps clock (§4.8); TLS session keys, UUID generators inside customer apps are their concern *after* our entropy re-seed is proven (test: two instances from one snapshot must produce different `/proc/sys/kernel/random/uuid` immediately post-resume).
**Control plane:** apid input validation is the trust boundary — fuzz it; API keys hashed; rate limit auth failures (10/min/IP); Postgres on unix socket only; secrets in `/etc/faas/secrets/` root:root 0400, never in env of tenant-reachable processes.
**Patch policy:** Firecracker/kernel CVE affecting guest isolation = same-day; everything else = weekly window. Subscribe to firecracker-microvm security advisories; drill the FC-upgrade-invalidates-snapshots path (it's routine, not an incident — ADR-005).
**Abuse:** signup requires email verification + one of (card, GitHub account ≥ 30 days); crypto-mining heuristic = sustained cpu.stat throttled + 100 % for > 15 min on Free/Hobby → auto-park + review queue; AUP bans mining, scanning, spam relaying.

---

## 12. Observability and SLOs

Prometheus (node_exporter + per-daemon `/metrics`) → Grafana Cloud free tier; alerting via Grafana → email + Pushover.

**The dashboard row that mirrors the financial model (check weekly, feed the sheet monthly):**

| Metric | Plan value | Alert |
|---|---|---|
| `snapshot_fleet_avg_mb` / `p95` | 130 / — | avg > 160 warn, > 200 page |
| resident GB per paying customer | 0.305 (=312 MB) | > 0.45 warn |
| `resident_ram_pct_of_target` | ≤ 100 % | > 80 % warn, > 92 % page |
| `lv_fc_used_pct` | — | > 80 % warn, > 90 % page |
| build queue wait p95 | < 60 s | > 300 s warn |
| `gateway_wake_latency_seconds` p95 | ≤ 0.8 s | > 1.5 s warn |
| cold-boot fallback rate | < 2 % of wakes | > 10 % warn (snapshot rot) |

**SLOs (public, on the status page):** API availability 99.5 % monthly; wake p95 < 1 s; build success (non-`user_error`) 99 %. Error budgets, not promises — one box (until Gate A) is stated honestly on the status page.

Logs: journald → Loki free tier; tenant app stdout/stderr ring-buffered per instance (10 MB), surfaced via `GET /v1/apps/{app}/logs` (tail + follow).

---

## 13. RAM budget ledger (enforced as systemd slices)

| Slice | Budget | Contents |
|---|---|---|
| `system.slice` | 2,048 MB | OS, sshd, journald, node_exporter, chrony |
| `faas-cp.slice` | 6,144 MB | postgres 1,536 · gatewayd 512 · apid 256 · schedd 128 · vmmd 256 · builderd 128 · meterd 256 · imaged 512 (spikes during flatten) · loki/promtail agents 256 · slack 2,304 → **1 guaranteed builder VM (2,048 + 8) lives here** |
| `faas-tenant.slice` | 57,344 MB (`memory.max`, hard fence) | tenant microVMs; **schedd admits only to 47,600 MB** (85 % of the model's 56 GB budget) |
| headroom (inside tenant slice, above admission line) | ≈ 8.4 GB | spike absorption; opportunistic 2nd builder VM may borrow ≤ 2 GB of it only below 60 % tenant residency |

`memory.max` on each slice makes the ledger real: a control-plane leak OOMs the control plane, never tenants — and vice versa.

---

## 14. Delivery plan (for agents; sequential, each gate = passing acceptance tests)

Conventions for all milestones: Go ≥ 1.23; integration tests that need KVM are tagged `//go:build metal` and run on the dev EX44 (or any nested-KVM runner) via `make test-metal`; unit tests must pass with `make test` on any machine.

| M | Scope | Acceptance (executable) |
|---|---|---|
| **M0** | Repo scaffold, CI, host bootstrap (ansible: LVM, slices, nftables, cgroups v2 verify) | `make bootstrap` idempotent on fresh Ubuntu 24.04; `test-metal` runs a hello firecracker VM from CI kernel + busybox rootfs |
| **M1** | vmmd: jailer lifecycle, netns/TAP factory, cold boot, destroy | boot 50 VMs (128 MB) concurrently; invariant 6.2-5 checks pass; teardown leaks zero netns/taps/uids (`make leakcheck`) |
| **M2** | imaged: OCI→app-layer ext4 + guest-init; base/runner images | convert a hello app over `runner-node22` base; two-drive VM boots via overlayfs, serves :8080 in < 3 s cold; app layer < 50 MB |
| **M3** | Snapshots: pause/snapshot/restore; resume hooks | park→wake p50 < 350 ms over 100 cycles; two concurrent restores pass the uniqueness test (§11); FC-version-stale → cold-boot fallback works |
| **M4** | gatewayd + schedd: routing, wake-blocking, idle reaper, admission | `curl` to a parked app returns 200 with wake; 1,000 rps to hot app adds < 2 ms p50; RAM admission refuses correctly at synthetic 85 % |
| **M5** | apid + Postgres + deploy pipeline with **prebuilt images only**; CLI (`faas deploy --image`) | end-to-end: `faas deploy` → parked → first request wakes; quotas enforced (plan matrix table-test) |
| **M6** | builderd + Railpack/Dockerfile in builder VMs | `faas deploy` a bare Node and Python repo (no config) → live; OOM bomb build kills only its VM (tenant latency unaffected — measured); cache makes 2nd build ≥ 2× faster |
| **M7** | meterd + Stripe: usage, quotas, overage, dunning; functions runtime (runner-node22, runner-python312); cron | invoice shadow equals hand-computed GB-h for a scripted 24 h scenario (< 0.1 % delta); function hello-world p95 wake < 1 s; Free-tier hard stop verified |
| **M7.5** | Git-deploy + thin dashboard (see `ux_spec.md` §5, §4; ADR-011/012): `githubd`/module, GitHub App, OAuth + repo picker, apps/usage/billing dashboard | push to `main` auto-deploys via the normal pipeline; commit status written back; dashboard connect-repo → live URL end-to-end; least-privilege scopes verified |
| **M8** | Hardening + ops: §11 checklist, backups + **timed restore drill**, status page, docs site, Gate-A runbook (2nd box active-passive); UX: cold-wake transparency surfaces (`ux_spec.md` §6), account export/delete (G6) | restore drill: PG + one app back serving on a clean VM < 30 min, documented as executed; security checklist signed off item-by-item; SLO dashboard live; first-time user reaches live URL < 5 min via CLI **and** GitHub connect |

Post-M8 = private beta (founding doc roadmap M2–M3: hand-held first ten customers).

## 15. Repository layout and conventions

```
faas/
  cmd/{apid,gatewayd,schedd,vmmd,builderd,imaged,meterd,faas}/   one main.go each (faas = CLI)
  pkg/{api,state,fcvm,netns,oci,rootfs,meter,stripex,wire}/
  guest/{init,runners/node22,runners/python312}/
  images/                      Dockerfiles for base/runner/builder images
  deploy/{ansible,systemd,nftables}/
  migrations/
  docs/adr/ADR-011+…           this file's §3 seeds 001–010
  Makefile                     bootstrap · test · test-metal · leakcheck · lint
```

Conventions (agents: treat as lint): errors wrapped with `%w` + operation context; no global state except wiring; table-driven tests; every gRPC/REST handler ≤ 50 lines (extract); every quota/limit in `pkg/api/limits.go` as one table — **never** a literal at point of use; feature flags via config, not branches older than one milestone. PRs small enough to review in ten minutes; every PR names the milestone and, if it touches architecture, an ADR.

## 16. Open questions (decide before the milestone that needs them)

| Question | Needed by | Current lean |
|---|---|---|
| Custom domains at launch or Gate B? | M4 (gatewayd) | Ship mechanism, gate behind Pro flag |
| Log retention & pricing (10 MB ring enough?) | M7 | Ring only in v1; object-storage archive as Pro add-on later |
| Postgres for customers (managed PG as a product)? | post-GA | No — stay a compute company until Gate C |
| Regional expansion = FSN + HEL pair at Gate A? | Gate A | Yes, matches founding doc R3 |
| Windows of scheduled maintenance vs live-migrate? | Gate A | Maintenance windows; snapshots make drain cheap |
| WebSocket/streaming for functions | M7 | Apps only in v1 |

---

## 17. Known gaps register (v1.0 review — resolve each with an ADR before the milestone named)

| # | Gap | Resolution lean | Decide by |
|---|---|---|---|
| G1 | **Registry unspecced** — §4.2 accepts `image: registry.DOMAIN/...` but no registry component exists | v1: accept **public registries only** (Docker Hub, ghcr.io), digest-pinned, pulled by imaged through the build egress policy. Own registry (CNCF `distribution` behind gatewayd) only if private images become a paid ask | M5 |
| G2 | **Customer secrets** — apps need env secrets; no encryption/injection/redaction design | `faas secrets set KEY=…` → sealed with a host age key, stored encrypted in PG, injected into `/etc/faas/app.json` env at boot only, values redacted from build/app logs by pattern; never in snapshots of *other* deployments | M5 (real apps blocked without it) |
| G3 | **Dashboard/web UI** — spec is API+CLI only | **RESOLVED (ADR-011, `ux_spec.md` §4/§11):** CLI-first, but a *thin* server-rendered dashboard (apid, Go templates + HTMX) ships **at launch** because GitHub connect-repo needs an OAuth callback + repo picker. Scope kept minimal: auth, GitHub connect, apps/logs, usage/billing, account | M7.5 (was post-M8) |
| G8 | **GitHub push-to-deploy** — chosen at launch; no component exists | **RESOLVED (ADR-012, `ux_spec.md` §5):** `githubd` (or apid module) — push-webhook receiver on `gatewayd /webhooks/github` (signature-verified), Checks-API status writer, per-repo install-token cache, least-privilege scopes. PR-preview envs deferred to v1.1 | M7.5 |
| G4 | **Transactional email provider** — verification, dunning, quota mails reference email; no provider | Resend or Postmark free/cheap tier (the €3/mo domain line in the model already budgets this); templates in repo, sent by apid via one `pkg/mail` interface | M7 (dunning needs it) |
| G5 | **CLI auth UX** — `faas login` undefined | v1: browser-paste flow (dashboard shows key, CLI stores in OS keychain). OAuth device flow later | M5 |
| G6 | **GDPR self-serve** — export + delete endpoints absent (policy exists in founding doc, mechanics don't) | `GET /v1/account/export` (JSON bundle) and `DELETE /v1/account` (dunning-style staged deletion, 30-day grace); DPA template in docs site | M8 |
| G7 | **Long-lived connections vs idle reaper** — an app holding one websocket never parks | Rule: open connections count as activity (reaper checks conntrack for the instance); document that persistent connections bill as resident GB-h — the meter already handles it correctly | M4 (one line in schedd) |

---

## Appendix A — API surface (v1)

```
POST   /v1/apps                          create (slug, type, runtime?, ram_mb, …)
GET    /v1/apps · GET/PATCH/DELETE /v1/apps/{app}
POST   /v1/apps/{app}/deployments        source tarball | {image}
GET    /v1/deployments/{id}              status incl. build log stream (SSE)
POST   /v1/apps/{app}/rollback
GET    /v1/apps/{app}/logs?follow=1
GET    /v1/apps/{app}/instances          read-only
POST   /v1/apps/{app}/park · /wake       manual overrides
GET    /v1/usage?month=YYYY-MM
POST   /v1/domains · DELETE /v1/domains/{domain}
POST   /v1/crons · PATCH/DELETE /v1/crons/{id}
GET    /v1/account · PATCH /v1/account/plan
POST   /v1/keys · DELETE /v1/keys/{id}
```

Errors: RFC 7807 problem+json, stable `code` strings, every limit error includes the limit, the observed value, and a docs URL.

## Appendix B — Reference configs

Jailer invocation (per instance):

```
jailer --id {instance} --uid {uid} --gid {gid} \
  --chroot-base-dir /srv/fc/jail --netns /run/netns/fc-{instance} \
  --cgroup-version 2 --parent-cgroup faas-tenant.slice \
  -- firecracker --api-sock api.sock --config-file vmconfig.json
```

Firecracker machine config (cold boot):

```json
{ "boot-source": { "kernel_image_path": "vmlinux-6.1", "boot_args": "reboot=k panic=1 pci=off quiet init=/sbin/init" },
  "drives": [
    { "drive_id": "base",  "path_on_host": "/srv/fc/base/runner-node22.ext4", "is_root_device": true,  "is_read_only": true },
    { "drive_id": "layer", "path_on_host": "layer.ext4", "is_root_device": false, "is_read_only": false } ],
  "machine-config": { "vcpu_count": 2, "mem_size_mib": 256, "smt": false },
  "network-interfaces": [ { "iface_id": "eth0", "host_dev_name": "tap0" } ],
  "entropy": {} }
```

nftables tenant egress (excerpt):

```
chain tenant_egress {
  ct state established,related accept
  ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } drop
  tcp dport { 25, 465, 587 } drop
  tcp dport { 80, 443, 53 } accept
  udp dport 53 accept
  drop
}
```

## Appendix C — External references

Firecracker snapshot support and versioning: github.com/firecracker-microvm/firecracker `docs/snapshotting/` (cgroups v2 restore-latency note; snapshot/version coupling; `mem_backend` File/Uffd). firecracker-go-sdk: github.com/firecracker-microvm/firecracker-go-sdk (active, Go ≥ 1.23). Railpack: github.com/railwayapp/railpack and blog.railway.com/p/introducing-railpack (Nixpacks in maintenance mode; image-size reductions). BuildKit: github.com/moby/buildkit. CertMagic: github.com/caddyserver/certmagic. Snapshot-uniqueness hazard: arxiv.org/abs/2102.12892. Marc Brooker, “Seven Years of Firecracker” (2025): brooker.co.za.

## Appendix D — Validation plan (how this document earns certainty)

Every row is an experiment with a pre-committed pass threshold. Run V1–V5 on a single rented EX44 (**total budget: one month's rent**) before M1 work begins; failures change plan quotas and §1 constraints, so they are cheapest to absorb now. Results get recorded next to the row; a failed threshold triggers an ADR, not a shrug.

| # | Assumption at risk | Experiment | Pass threshold | When |
|---|---|---|---|---|
| V1 | 130 MB avg snapshot (C-grade) | Deploy 10 representative apps (Express, Next.js, Flask, FastAPI+pandas, Go static, …); park; measure mem+vmstate+app-layer per plan | Plan-weighted avg ≤ 130 MB, p95 ≤ 300 MB | pre-M1 |
| V2 | Wake p50 ≤ 350 ms | 100 park→wake cycles per app class on NVMe, file-backed restore | p50 ≤ 350 ms, p95 ≤ 800 ms | pre-M1 |
| V3 | 8 MB per-VM overhead | Boot 120 × 128 MB VMs; host RSS delta ÷ 120 | ≤ 8 MB incl. TAP/jailer | pre-M1 |
| V4 | Density / CPU overcommit 8× | 120 resident VMs + synthetic load on 20; measure p95 latency degradation | < 20 % degradation | pre-M1 |
| V5 | 2 GB builder VM suffices | Build top-20 OSS starter repos (Node/Python) under the cap | ≥ 90 % succeed without OOM | pre-M6 |
| V6 | Restore uniqueness + clock | Two instances from one snapshot; compare RNG streams, clock skew post-resume | Distinct entropy, skew < 50 ms | M3 gate |
| V7 | Resident concurrency 0.02/0.15/0.6/3 (C-grade) | Beta cohort telemetry (`resident GB per customer`, §12) | Within 1.5× of plan by 20 customers | beta |
| V8 | 5 % churn, 55/40/5 mix, 4 free riders (C-grade) | Cohort curves from first 90 days; monthly sheet re-run with observed values | Documented monthly; sheet updated | beta+ |
| V9 | Payment path & fees (2.9 % + €0.30) | Confirm Stripe availability for the incorporation country; else price MoR alternative into the sheet | Fee delta reflected in model before launch | pre-incorporation |
| V10 | Server quote €44 + €39 | Re-quote EX44 at order time (post-June-2026 price adjustment) | Within §9.9 sensitivity range (€39–50) | at order |

Standing rules: (1) no number graduates from "assumption" to "fact" without a row here; (2) the §6.2 invariants are enforced as property-based tests, not prose; (3) each ADR gets one adversarial review pass before acceptance.

*End of spec. Deviations require an ADR. Keep the three fragile numbers on the dashboard.*
