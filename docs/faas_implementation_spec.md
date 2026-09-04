# Gregale — Implementation Whitepaper

**Version 1.0 · July 2026 · Confidential, internal**
**Audience:** engineers and coding agents building the platform. This document is the buildable spec; treat it as the source of truth for architecture decisions. Business rationale lives in the founding whitepaper (`faas_founding_whitepaper.pdf`); financial numbers live in the financial model spreadsheet. Where this document and the spreadsheet disagree on a business number, the spreadsheet wins.

Gregale deploys on any bare-metal x86_64 host with `/dev/kvm`, ≥64 GB RAM, NVMe storage, and a 1 Gbit uplink. The reference deployment today is a single physical node (originally a Hetzner EX44: i5-13500 / 64 GB DDR4 / 2×512 GB NVMe RAID-1) — those values populate §1's host sizing row. The architecture itself targets multi-host scale-out (see §2 and `docs/scale_out_and_workload_classes.md`); the EX44 row is a reference, not a hard requirement.

**How to use this document (agents):** every component in §4 is independently implementable against the interfaces defined here. Milestones in §14 are ordered and each has executable acceptance criteria — do not start milestone N+1 before N's criteria pass. Record any deviation from this spec as a new ADR in §3's format.

---

## 1. Inherited constraints (the physics)

These numbers come from the financial model and are **not negotiable at implementation time** — code must enforce them, telemetry must verify them.

| Constraint | Value | Enforced by |
|---|---|---|
| Reference host (Hetzner EX44 — sizing template, not a requirement) | i5-13500 (20 threads), 64 GB DDR4, 2×512 GB NVMe RAID-1, 1 Gbit | — |
| Host OS RAM reserve | 2 GB | budget table §13 |
| Control-plane RAM reserve | 6 GB | budget table §13, systemd slices |
| Tenant RAM budget | 56 GB | `schedd` admission |
| RAM utilization ceiling | 85 % of tenant budget (47.6 GB) | `schedd` headroom guard |
| Per-VM overhead budget | 8 MB (VMM + jailer + TAP slack) | `schedd` accounting |
| CPU overcommit | 8× (160 vCPU slots) | `schedd` admission |
| Disk reserve (OS, kernels, base images, logs) | 60 GB | LVM layout §8 |
| Snapshot budget | 452 GB | `imaged` GC + quotas |
| Fleet average snapshot size target | 130 MB | telemetry alert §12; quotas §8 |
| Plan quotas (deployed / per-app concurrent / MB RAM / GB-h) | Free 1/1/128/5 · Hobby 5/2/256/50 · Pro 25/5/512/250 · Scale 100/20/1024/1500 | `apid` validation, `schedd` admission, `meterd` quotas |
| Plan per-VM concurrency bound (`concurrency_per_vm`) | Free 1 · Hobby 5 · Pro 25 · Scale 80 | `apid` validation on `GET /v1/apps/{slug}`; CLI `gregale app <slug> --concurrency`; **listener-level only** — handler-process concurrency is the customer's responsibility (§4.9.1, issue #559) |
| Per-deployment `require_authn` opt-in (issue #560) | Free no · Hobby no · Pro yes · Scale yes | `apid` PATCH validator via `Plan.RequireAuthnAllowed()`; column default `false` so every existing customer is unaffected; `gatewayd-internal` enforces after Host→app resolution and before the wake gate, cross-account tokens receive 403 |
| Spend cap (`accounts.overage_cap_cents`, issue #561) | Free 0 (no overage) · Hobby NULL · Pro NULL · Scale NULL; customer-mutable via `POST /v1/account/overage-cap` and the dashboard `Spend cap` form | storage layer #279 (`migrations/00054_account_credits.sql`); enforcement at `schedd.Engine.admitGate` via `pkg/sched/OverageChecker` (5 s TTL cache, fail-open); wire surface `CodeAdmissionRefused` (HTTP 402); meterd's quota tick is the unchanged advisory-skip signal (#279 PR-A) |
| Capacity-pressure rebalancer (Tier A9 / ADR-087) | `PressureAtCapacityThresholdPerMin=5`, `PressureReassessmentIntervalSeconds=30`, `PressureMigrationPolicy="migrate_after_2"` (closed set ∈ {skip_live, migrate_after_1, migrate_after_2}); env-overridable via `FAAS_PRESSURE_*`; surface lives in `pkg/api/limits.go` and `pkg/sched/pressure_aggregator.go` + `pressure_rebalancer.go` | `schedd` engine-side `IncAtCapacity` at every `WakeResult{AtCapacity:true}` return; aggregator + watcher poll for sustained apps; `Engine.RebalancePressuredApps` reassigns to peer with headroom; `app_changed{pressure_rebalanced}` notify on the rebalance — see ADR-087 |
| Expected resident concurrency (planning) | 0.02 / 0.15 / 0.60 / 3.00 | telemetry comparison only |
| Overage meter | €0.01 per GB-RAM-hour | `meterd` → Stripe |
| Build cost envelope | builds fit inside control-plane + headroom, never tenant RAM | `builderd` admission §9 |

**Three numbers the business is fragile to** (founding doc §5): fleet snapshot size, resident concurrency per customer, churn. The first two are produced by this system — §12 makes them first-class metrics from day one.

---

## 2. Architecture overview

One control-plane node runs everything today; the architecture below extends to N nodes (Tier A, §2.1). Every box is a systemd unit; every arrow is either HTTP over localhost, gRPC over a unix socket, or a Postgres row.

```
                        ┌────────────────────── control-plane node (today: Hetzner EX44) ─────────────────────┐
                        │                                                                              │
  client ── TLS ──►  gatewayd-public ──► gatewayd-internal ──── route lookup (cache→PG) ────┐         │
  *.apps.gregale.dev         │                                      │                                       │
  custom domains        │ wake needed?                         ▼                                       │
                        ├────────► schedd ─────────► vmmd ── jailer ── firecracker ── microVM          │
                        │           │  admission,     │        (one process per running VM)            │
                        │           │  reaper,        │                                                  │
                        │           │  eviction       │  netns + TAP per instance (§7)                   │
                        │           ▼                 ▼                                                  │
                        │        postgres ◄──── snapshot/restore on NVMe (§8)                            │
                        │           ▲                                                                  │
   deploy (API/CLI) ──► apid ───────┤                                                                  │
                        │           │                                                                  │
                        └─► builderd ── ephemeral builder microVM (Railpack/BuildKit inside)           │
                                    │                                                                  │
                                 imaged ── OCI ➜ ext4 rootfs + guest-init ➜ boot ➜ snapshot            │
                                    │                                                                  │
                        meterd ── 1 s samples ➜ minute rows ➜ GB-h ➜ Stripe usage records              │
                        └──────────────────────────────────────────────────────────────────────────────┘
   off-node: object storage (build cache, cold snapshots, backups) · off-host PG WAL destination · Stripe · DNS
```

**Request path (hot):** TLS → `gatewayd-public` → `gatewayd-internal` → routing cache hit → proxy to instance IP:8080 → response. Budget: < 2 ms added latency.

**Request path (cold wake):** `gatewayd-public` accepts TLS, hands to `gatewayd-internal` which sees app has no running instance → holds the request → asks `schedd` → admission check (RAM headroom, plan concurrency) → `vmmd` restores snapshot into fresh netns/TAP → guest resumes (app already initialized in snapshot memory) → readiness ping → proxy. Budget: p50 ≤ 350 ms, p95 ≤ 800 ms first byte (§6.3).

**Deploy path:** `apid` accepts source (≤ 100 MB) or OCI reference → `builderd` runs the build in an ephemeral builder microVM → OCI image → `imaged` converts it to a per-app **app layer** over a shared read-only base (two-drive scheme, §4.6) + injects `guest-init` → boots once, waits ready, pauses, snapshots → app state = `PARKED`. First deploy of an app is also its first snapshot.

**Two workload models, one data plane (ADR-003):** an *App* is any HTTP server listening on `:8080` in its microVM. A *Function* is an App whose rootfs we generate from a platform runner image (node22 / node24 / python312 / python313 / go124 / go124-alpine) wrapping the customer's handler file behind the same `:8080` contract. Functions get zero new infrastructure: same lifecycle, same snapshots, same metering, same routing. Cron triggers are synthetic requests fired by `schedd`.

---

## 3. Decision record

Format for future ADRs: `ADR-NNN · title · status · decision · consequences`. These ten are **accepted** and locked for v1.

| ADR | Decision | Why | Rejected alternatives |
|---|---|---|---|
| 001 | Control plane in **Go**, monorepo, static binaries | firecracker-go-sdk is first-party and actively maintained (Go ≥ 1.23); single-binary deploys; agents generate/test Go well | Rust (slower iteration, no first-party SDK), Node/Python (RAM cost on a budgeted box) |
| 002 | **Builds on the control-plane nodes** (option B), governed | Founder decision; €0 extra; reuse existing capacity | Off-host builder VM (revisit at Gate B if build queue p95 > 60 s) |
| 003 | **Builds run inside ephemeral builder microVMs**, not host containers | Untrusted `npm install` gets the same VM-grade isolation as untrusted runtime code; RAM cap is the VM boundary (exact, unbreachable); reuses vmmd primitives; kills rootless-runc attack surface on the host | Rootless BuildKit directly on host (weaker isolation, cgroup escapes are kernel bugs away), host docker (unacceptable) |
| 004 | Zero-config engine: **Railpack** (BuildKit-based, Go); **Dockerfile** escape hatch; **pre-built OCI** accepted | Railpack is Nixpacks' successor (Nixpacks in maintenance mode), produces far smaller images — directly protects the 130 MB fleet snapshot target | Nixpacks (larger images), CNB Buildpacks (multi-GB builder images don't fit our RAM/disk budget) |
| 005 | Park/wake via **Firecracker snapshot–restore**, file-backed memory; **cold boot from rootfs must always work** as fallback | Restore ≈ 150–300 ms with app already warm; snapshots are version-coupled to Firecracker, so they are cache, not truth | Cold boot always (1–3 s wakes), CRIU (fragile), keeping VMs resident (destroys the economics) |
| 006 | **Postgres 16** single primary + streaming replicas (future), one database, `sqlc`-generated queries | One state store for routes, apps, builds, usage; WAL-shipped off-host | SQLite (concurrent writers: meterd + apid + schedd), etcd (nothing to consense on a single node) |
| 007 | Edge: **`gatewayd-public` (TLS) and `gatewayd-internal` (routing/wake/proxy) are our own Go binaries** using CertMagic; wildcard `*.apps.gregale.dev` via DNS-01; custom domains via on-demand HTTP-01 | Wake-blocking (hold request during restore) is core product logic — we own it; CertMagic is Caddy's battle-tested TLS core as a library | Stock Caddy/Traefik + sidecar wake logic (two hops, split brain), nginx+lua (unmaintainable) |
| 008 | Host: **Ubuntu 24.04 LTS, cgroups v2 only**, KVM, systemd slices | Firecracker snapshot restore documented slow on cgroups v1; v2 is mandatory | Debian (fine too — pick one and stop) |
| 009 | Per-instance **network namespace with identical inner TAP** (`tap0`, guest 10.0.0.2/30) | Snapshots bake device topology + guest IP; identical-netns trick lets one snapshot restore as N concurrent instances; host side NATs per-instance | Per-instance IP baked in snapshot (breaks concurrency > 1), vsock-only (no inbound HTTP) |
| 010 | Billing: **Paddle Billing v2 subscriptions + metered overage item**; usage pushed hourly via `billing.Provider` interface (ADR-025 + ADR-032 v2) | Paddle's MoR handles VAT globally so the launch customer base isn't gated on a single EU entity; the adapter interface (`pkg/billing/Provider`) is provider-neutral so a future Stripe / LemonSqueezy plugin is additive. Dunning states in §10 | Homegrown invoicing (no), Stripe (deferred to v1.1; the legacy apid Stripe surface is still bootable from `FAAS_BILLING_PROVIDER=stripe` for a node-level opt-out) |

---

## 4. Component specifications

Each component: single Go binary, own systemd unit, structured logs (JSON, `slog`), Prometheus `/metrics`, config via one TOML file + env overrides. All inter-component APIs are gRPC over unix sockets in `/run/faas/` except gatewayd-internal→apps (plain HTTP). Verbosity is operator-controlled via the `FAAS_LOG_LEVEL` env var (one of `debug`, `info`, `warn`, `error`, case-insensitive); a `SIGHUP` re-reads it at runtime (issue #518 PR-A). Default is `info`; an unrecognised value falls back to `info` and logs a one-shot warn — daemons never refuse to start on log misconfiguration.

### 4.1 `gatewayd-public` ⏵ `gatewayd-internal` — edge proxy

As of Tier A7 (ADR-070), this section describes the two split daemons. Pre-Tier-A7 readers should consult the monolithic `gatewayd` (then a single daemon) content in the original commit.

**Owns:** TLS termination (gatewayd-public), routing + wake-blocking + request accounting + per-tenant rate limits (gatewayd-internal).

- Listeners: `:443` (HTTPS, HTTP/1.1 + h2), `:80` (redirect + ACME HTTP-01). Bound by `gatewayd-public` only; `gatewayd-internal` is reached only via the unix socket on the node.
- TLS: CertMagic (owned by `gatewayd-public`). Wildcard cert for `*.apps.gregale.dev` via DNS-01 (provider-pluggable; the reference deploy uses Hetzner DNS). Custom domains (Pro+): on-demand HTTP-01 with an allowlist check against `custom_domains` table before issuance (prevents cert-mint abuse).
- Routing: hostname → `app_id` via in-memory cache (LRU, 10k entries) backed by Postgres `LISTEN app_routes_changed` (owned by `gatewayd-internal`). Cache miss = one indexed PG lookup.
- Wake-blocking: if app has no `RUNNING` instance, `gatewayd-internal` enqueues the request (per-app queue, cap 512 requests / 30 s TTL, then `503 + Retry-After`), calls `schedd.EnsureInstance(app_id)`, streams queued requests once readiness passes.
- **Fan-out across `max_concurrency` (issue #168):** the routing cache is a per-app set of `Target{NodeID, InstanceID, WakeID}` (size ≤ plan's effective `max_concurrency`), picked via atomic round-robin so the hot path is allocation-free. `Backend.Admit(ctx, app_id, max_concurrency)` is the scale-out admission primitive; it atomically checks `HealthyCount < max_concurrency` before the gRPC round-trip so concurrent callers cannot collectively over-admit past the cap. At-capacity refusals surface as a typed `atCapacity=true` result (no gRPC status); `gatewayd-internal` treats them as a benign no-op when it already has ≥1 cached target. On every proxied request the handler stamps `x-faas-instance` with the picked `InstanceID`, overwriting any inbound header (trust model). Per-instance `last_request_at` is keyed by `instance_id` directly — the addr→instance resolver hop is gone.
- **Rate limits (ADR-040 / issue #292):** `gatewayd-internal` runs two token buckets per request in series, both before the wake gate so abuse doesn't burn the schedd gRPC admission queue. The **per-account** bucket (`RateLimitPerAccountRPM` — Free 50/min, Hobby 200/min, Pro 1000/min, Scale 5000/min; key = `apps.account_id` joined in `pgRouter.toApp`) runs first and bounds the botnet signature of attacks spread across one customer's apps. The **per-app** bucket (`RateLimitRPS`/`RateLimitBurst` — existing) runs second as the inner cap. Each 429 carries `Retry-After: 1` and `x-faas-rate-limit-scope: {account,app}` so observability tooling can split the two populations. The new counter is `gateway_per_account_rate_limited_total{account_id, plan}`, pre-instantiated under `__other__` to keep the §12 panel non-dark; alert is `FaasPerAccountRateLimitSpike` (> 100/min fleet, warn).
- **Per-deployment `require_authn` opt-in (issue #560):** an app may opt into per-deployment authentication by setting `apps.require_authn=true` (PATCH via `UpdateAppRequest`; Pro/Scale only). When set, the routing layer in `gatewayd-internal` requires a valid `Authorization: Bearer <token>` header on every incoming request — tokens are account-scoped API keys (`fp_live_…`, SHA-256 verified through `pkg/auth.Middleware.RequireSession`). The check runs **after** Host→app resolution (so the app's `require_authn` flag is known) and **before** the wake gate / forwarder (so unauthenticated traffic cannot trigger cold-boot on a token-gated app). Cross-account tokens — token valid for *any* account other than the app's owning account — receive 403 `insufficient_scope` and never reach the wake path. Audit rows: `app.authn_required` on the PATCH that flips the flag on, `app.authn_disabled` on the true→false transition, and `instances.authn_missing` / `instances.authn_invalid` / `instances.authn_scope` per denied request. The default is `false`; every existing customer is unaffected.
- **Per-app wire-protocol selector (issue #67 / ADR-124, hardened ADR-127):** an app may pick the wire protocol its customer-facing edge speaks via `apps.app_protocol` (closed-set `{http1, http2, grpc}`; migration `00382_apps_app_protocol.sql` + `apps_app_protocol_chk` CHECK). Default is `http1` (universal — every pre-ADR-124 app keeps its current behaviour); `http1` and `http2` are permitted on every plan; `grpc` is gated to Hobby+/Pro/Scale and rejected on Free with 403 `plan_app_protocol_grpc_not_allowed`. Invalid values (anything outside the closed set) are rejected at `apid` with 400 `app_protocol_invalid` — the column-level CHECK is the belt-and-braces backstop, not the primary validator. `apid` writes via `CreateAppRequest.AppProtocol` (closed-set pointer; apid-floor coerces empty→`"http1"` so legacy callers stay green) and `UpdateAppRequest.AppProtocol` (same Set\*/optional-pointer convention as every other field). On the read side, `gatewayd-internal` plumbs `state.App.AppProtocol` through `pgRouter.toApp` onto `gateway.App.AppProtocol`, and the handler stamps `x-faas-protocol: <http1|http2|grpc>` on every inbound request at the same site `x-faas-stream` and `x-faas-upgrade` are stamped today (`pkg/gateway/handler.go` ~5131 streaming, ~5261 buffered). The forwarder reads the header at `pkg/gateway/forwardproxy.go::fwdStreamOnceWithEvents` and emits a `slog.Debug` framing-selection line so operators with debug logging on can correlate per-app protocol choice with bridge-side framing behaviour. **Wire-shape is now in fleet (ADR-126, PR #1050).** The per-app framing switch reaches the guest's `:8080` as native H2 frames (for `app_protocol=http2`) or native gRPC trailers (for `app_protocol=grpc`) via the bridge-side H2C terminator (`cmd/vmmd-stream-bridge/h2c_terminator.go::handleH2CStream`); `app_protocol=http1` rides the legacy `handleH1Stream` path verbatim (zero behaviour change). Two rollback switches (`FAAS_BRIDGE_PROTOCOL=h1` surgical, `FAAS_STREAM_BRIDGE_VERSION=v1` wholesale) and the runbook at `docs/ops/h2c-rollback.md` are the operator's levers. **Hardening follow-on (ADR-127, G19.1).** The four production-readiness gaps surfaced by the post-#1050 audit are addressed: snapshot invalidation on `app_protocol ∈ {http2, grpc}` adoption (`FAAS_BASE_IMAGE_VERSION` + `MarkAllSnapshotsStaleByAppProtocol` + F3 sweep in `pkg/imaged/loop.go::runFCSweep`); listener + transport security pins (`MaxConcurrentStreams=100`, `MaxReadFrameSize=1 MiB`, `MaxHeaderListSize=1 MiB`, `ReadIdleTimeout`, `PingTimeout`, `WriteByteTimeout`, `defer recover()` + `defer transport.CloseIdleConnections()` on `handleH2CStream`); observability (`vmmd_bridge_framing_total{app_protocol, bridge_protocol, framing}` counter pre-instantiated across the closed cross-product + `slog.Info` framing-selection line on every request + `bridge-protection` Grafana dashboard + `FaasBridgeFramingMismatch` / `FaasBridgeRollbackStuck` Prometheus alerts); and these docs (spec §4.1 + STATUS M8.6 + `docs/ops/h2c-rollout.md` + `docs/ops/h2c-rollback.md`). Layer 12 (metal acceptance: 5 unconditional `t.Skip`s in `cmd/e2e/bridge_h2c_terminator_metal_test.go`) is **deliberately out of scope** and lands in two follow-on issues (G19.2 un-stub the tests; G19.3 add `deploy/ansible/roles/metal-h2c-acceptance/`).
- **Spend cap → workload refuse (issue #561):** a customer may set `accounts.overage_cap_cents` (storage layer #279, `migrations/00054_account_credits.sql`) to a monthly ceiling on overage spending. The schedd enforces the gate at `Engine.admitGate` via the `pkg/sched/OverageChecker` seam (5 s TTL cache, fail-open on transient PG errors; meterd's quota loop is the safety net for sustained outages). When the cap is reached, the wake path lifts to `CodeAdmissionRefused` (`pkg/api/errors.go`) — HTTP 402 on the gateway edge, `FailedPrecondition` on the gRPC ingress. Existing live instances are **not** auto-parked by the cap alone; parking is the M7 free-stop path (`pkg/meter/quota.go::EnforceQuota` case "stop"). Customers manage the cap via `POST /v1/account/overage-cap` (account-self-scoped) or the dashboard `Spend cap` form (CSRF-envelope POST `/dashboard/raise-overage-cap`); the API surface carries the precise current `cap_cents` / `current_overage_cents` in the RFC 7807 body so a script can compute "how much to raise" without parsing prose. Audit row `overage.cap_reached` is emitted the first time each (account, UTC day) the gate refuses; UTC-day dedupe chosen over the issue body's calendar-month wording to prevent audit-table flooding.
- Records `last_request_at[instance]` (in-memory, flushed to PG every 15 s) — this drives idle parking.
- Rate limits (token bucket, per app): Free 5 rps burst 20; Hobby 20 rps burst 100; Pro 100 rps burst 500; Scale 500 rps burst 2000. Over-limit → `429`.
- Request/response size caps (per-plan, ADR-047):
  - **Default (Free-tier path):** 25 MB body either direction; 60 s upstream response start, 300 s total. This is the legacy buffered path; it serves all Free-tier apps (and Hobby+ apps that have not opted in to streaming via the per-app `streaming_enabled` flag, with an operator opt-in via `FAAS_GATEWAY_STREAMING`). The cap is enforced by `pkg/gateway.capWriter` (issue #995 / ADR-121): inbound body via `http.MaxBytesReader` (existing); outbound body via `capWriter` on the buffered reverse-proxy dispatch site. On cap-exceeded, `gatewayd-internal` emits a 413 `response_too_large` problem+json (RFC 7807) when the cap trips before the upstream's headers reach the wire; mid-body trips surface as a hardened connection reset (stdlib's `WriteHeader(413)` silently no-ops after the upstream proxy has written 200 — see ADR-121 §2 for the architectural constraint and R2 for the recorder/test surface).
  - **Streaming path (Hobby+ only, ADR-047):** 100 MB body cap; 900 s response deadline. The `gatewayd-internal` handler takes the streaming path iff `FAAS_GATEWAY_STREAMING` is set (operator opt-in) AND the app's `streaming_enabled` flag is true AND the inbound request did not opt out via `Accept: application/json`. The handler wraps `w` with a per-flush `onFlush` callback (`statusRecorder.doFlush`) that attributes egress bytes (per-instance, per-minute) on every `Write`+`Flush` boundary, plus a residual capture on `finalFlush`. The cap is enforced by `pkg/gateway.capWriter`; on cap-exceeded `gatewayd-internal` emits a 413 `streaming_not_available` problem+json (RFC 7807) instead of stdlib's 502.
  - **Warn-on-approach (issue #995 / ADR-121):** both the buffered and streaming `capWriter` paths emit `gateway_response_body_warn_total{app_id, bucket}` (`bucket ∈ {near_threshold, exceeded}`, threshold crossings at 80% / 95% / 100% of the per-plan cap). The counter is the scrape-friendly signal; a once-per-process `slog.Warn` is emitted for the first hit per `(app_id, bucket)` so a runaway app doesn't flood the log stream. See §17 for the dashboard chip.
  - **Per-request opt-out:** `Accept: application/json` flips a single request to the buffered path. The customer's per-app flag stays unchanged so flipping the flag on later is a config change, not a per-request decision.
  - **Plan gate:** `apid` rejects `streaming_enabled=true` on Free apps with `CodePlanStreamingNotAllowed` (per pkg/api/errors.go) at deploy time. The runtime fallback log `streamingFallbackLog` fires only when `!streaming && plan == Free && SSE` — the operator-toggled `FAAS_GATEWAY_STREAMING` is the operator-side lever, not a per-app misconfiguration.
- **Multi-node forwarding (ADR-028 + ADR-047):** When schedd has placed an instance on a remote compute_node, `gatewayd-internal` dials the per-node vmmd over the overlay (Tailscale or Wireguard; see §10 below) and bridges the HTTP bytes via vmmd's bidi `ForwardHTTPStream` RPC. The cache layer is `NodeClientCache`: one `*grpc.ClientConn` per `compute_node.id`, evicted on every `compute_node_changed` pg_notify. Hop-by-hop headers (RFC 7230 §6.1) are stripped before the bridge. The streaming envelope is 100 MB body / 900 s response deadline (the per-plan caps in §4.1 apply on `gatewayd-internal` via `capWriter`; the vmmd bridge runs the streaming cap uniformly). The local-node path (default single-node deploy) skips the bridge and uses the existing direct reverse-proxy path. The legacy unary `ForwardHTTP` RPC was removed in PR-D — `ForwardHTTPStream` is the only bridge today.
- Emits: `gateway_requests_total{app,code}`, `gateway_wake_latency_seconds` (histogram), `gateway_queue_depth`, `gateway_response_bytes_total{app,plan}` (per-flush delta + residual capture), `gateway_stream_flushes_total{app,plan}` (per-`doFlush` increment), `gateway_stream_active{app,plan}` (gauge — Inc on `setupStreamingWriter`, Dec on handler defer; buffered-path requests never touch the gauge). All emitted by `gatewayd-internal`. See §12 for the dashboard table.

#### 4.1.1 Platform path reservations

`gatewayd-public` is the **only public listener on the box**; it terminates TLS and hands every request to `gatewayd-internal`, which checks `isApidPath` (`cmd/gatewayd-internal/proxy.go:202-228`) before falling through to the host-routed wake/proxy path. The matcher is the canonical reservation list — customer apps **cannot** expose routes under any of these prefixes, and the spec §4.1.1 enumerates them so the customer-facing docs can mirror the platform's own contract.

| Reserved path                          | Owning handler (apid)                                     | Why reserved                                     |
|----------------------------------------|------------------------------------------------------------|--------------------------------------------------|
| `/v1` and `/v1/` subtree               | REST API (§4.2) — apps, deployments, domains, crons, …     | Permanent API surface; ADR-011 single-listener    |
| `/dashboard` and `/dashboard/` subtree | Dashboard (M7.5, ADR-011)                                 | Customer-facing dashboard, session middleware     |
| `/oauth/...` subtree (NOT bare `/oauth`) | OAuth callbacks (`/oauth/callback` mounted today)        | Subtree-only; bare `/oauth` deliberately 404s    |
| `/login`, `/login/`, `/login/...`      | Magic-link + session login                                 | Auth flows                                       |
| `/signup`, `/signup/...`               | Account creation                                           | Auth flows                                       |
| `/login/forgot`, `/login/forgot/...`   | Password reset request                                     | Auth flows                                       |
| `/auth/verify`, `/auth/verify/...`     | Magic-link consume (legacy)                                | Auth flows                                       |
| `/auth/reset`, `/auth/reset/...`       | Password reset completion (issue #165 / PR #180)          | Auth flows                                       |
| `/logout`, `/logout/`, `/logout/...`   | Session logout                                             | Auth flows                                       |
| `/status`, `/status/`, `/status/...`   | Public status page (§12)                                   | Spec §12 panel surface                           |
| `/healthz`                             | Loopback infra probe (CD health check)                     | Required by `deploy/digitalocean/bootstrap.sh` (no longer in tree; canonical path was `deploy/controlplane/bootstrap.sh`, RETIRED 2026-08-15 by issue #911 / PR-1; v2 path is `make bootstrap` + `gregalectl manifest {validate,render}` + `gregalectl release install`) and `cd-digitalocean.yml` post-deploy smoke |
| `/cli-auth`                            | Device-code approval page (§2.2)                           | CLI pairing                                      |
| `/v1/apps/{slug}/logs`                 | Carve-out — `gatewayd-internal`'s own `AppLogsHandler` (issue #254 / Move 4 PR-2) | Customer log stream routed via gatewayd-internal→schedd  |

**Anchor discipline.** Every anchored root matches exact + `/` subtree via `hasApidPrefix` (cmd/gatewayd-internal/proxy.go:171-176). A bare `HasPrefix(prefix)` would also match `prefix + arbitrary junk` (e.g. `/v1.zip`, `/loginfoo`) and silently steal customer-app paths — review finding #6 from the dashboard era. Bare `HasPrefix` is therefore deliberately avoided; only `/oauth/` is subtree-form because the only mounted route is `/oauth/callback`.

**Customer-facing implication.** Apps must pick a different prefix for their own routes (e.g. `/api/`, `/v2/`). `/v1.zip` is **not** reserved — only `/v1` and `/v1/...` — so customers who want to expose a single-character-shorter alternative can use `/v1.<service>` or similar. The reservation table is enforced by `isApidPath` at request time; `gatewayd-internal` returns a 404 to any path the customer tries to expose that conflicts.

**Drift protection.** The `TestApidPathReservations_Documented` test in `cmd/gatewayd-internal/proxy_test.go` reads this section and asserts every `apidRoot*` constant in `cmd/gatewayd-internal/proxy.go:233-246` appears verbatim — the spec is documentation that must match the matcher, but the matcher is the source of truth. If a future change adds a new `apidRoot*` constant, this section must be updated in the same PR; CI fails the merge otherwise.

#### 4.1.2 Edge-rule hot-path ordering

The ten edge-rule kinds (`route`, `rewrite`, `redirect`, `headers`, `cors`, `jwt`, `ip`, `validate`, `limit`, `cache`; see ADR-091 / issue #561 / Cloudflare Ruleset Engine; `validate` added in PR-B / D20.6; `limit` added in D24; `cache` added in ADR-122) run in a fixed pipeline inside `gatewayd-internal`'s `ServeHTTP`. The ordering is deterministic and the rationale for each position is load-bearing — moving any kind up or down the stack creates a documented regression (browsers reject preflights that 3xx; JWT-failed traffic must not wake a microVM; malformed-body traffic must not wake a microVM; oversize-body traffic must not wake a microVM; an authed response must never be served a different principal's cached body).

```
matchAndSubstituteRoute          # §4.1.2.1 — kind=route (PR 3)
  → Backend.Lookup                # §4.1.2.2 — scheduler cache+PG (skipped on kind=route hit; goto haveApp)
  → matchAndApplyRedirect         # §4.1.2.3 — kind=redirect (PR 4): 3xx short-circuits BEFORE rewrite so a rewritten path doesn't accidentally shadow a redirect
  → matchAndApplyRewrite          # §4.1.2.4 — kind=rewrite  (PR 4): mutates r.URL.Path so downstream gates (CORS/headers/JWT/IP) match on the rewritten path
  → applyEdgeRuleHeaders          # §4.1.2.5 — kind=headers  (PR 4): response-side ops installed on `statusRecorder` BEFORE any 4xx is written
  → applyEdgeRuleCORS             # §4.1.2.6 — kind=cors     (PR 5): OPTIONS preflight short-circuits with 204 BEFORE redirect would have 3xx'd the preflight away
  → applyEdgeRuleJWT              # §4.1.2.7 — kind=jwt      (PR 5): AFTER rewrite/headers, BEFORE require_authn, so a JWT-failed request never reaches the per-deployment auth chain or wakes the VM
  → applyEdgeRuleIP               # §4.1.2.8 — kind=ip       (PR 5): AFTER jwt (cheap deny before authenticated-path DB), BEFORE Backend.Pick, so an IP-denied request never pays a cold-wake cost
  → applyEdgeRuleLimit            # §4.1.2.13 — kind=limit (D24): AFTER IP, BEFORE the global 25 MiB reader and BEFORE require_authn, so an oversize request is rejected with 413 + Content-Length fast-path deny BEFORE any body bytes are buffered or any auth/wake work runs
  → applyEdgeRuleValidate         # §4.1.2.12 — kind=validate (PR-B / D20.6): AFTER IP + limit (cheap body-shape deny on unverified source), BEFORE require_authn, so a malformed-body request is rejected with 422 without consuming auth, allocating quota, or waking a microVM
  → enforceRequireAuthn           # §4.1.2.9 — per-deployment require_authn gate (issue #560)
  → enforcePublicAuth             # §4.1.2.10 — per-app public_auth gate (issue #477)
  → applyEdgeRuleCache_serve      # §4.1.2.15 — kind=cache (ADR-122): serve path (hit/miss/bypass) AFTER both auth gates, BEFORE wake gate, so a hit avoids the wake cost entirely
  → Backend.Pick                  # §4.1.2.11 — scheduler wake/gate (any earlier 4xx already short-circuited)
  → proxy leg
```

**Architectural note on the kind=route exception (load-bearing, ADR-091 D4):** of the nine kinds, only `kind=route` runs *before* `Backend.Lookup` — at `ServeHTTP` line 2255-2257, a `kind=route` hit substitutes `app` and `goto haveApp` so downstream RequireAuthn / PublicAuth / wake gate / proxy all see the *target* app's context (auth remains per-app). The other eight kinds run at `haveApp` (line 2267 onward), AFTER `Backend.Lookup`. Reordering `kind=route` to run AFTER Lookup would defeat the substitution; reordering any of the other eight to run BEFORE Lookup would mean they fire on an empty `App{}` and silently fall through the same-account guard — both are regressions.

**Hard guarantees** the ordering pins:

- **CORS precedes redirect (§4.1.2.6 > §4.1.2.3 if a 3xx came first).** A preflight that gets a 3xx is a browser-side failure (browsers don't follow 3xx for preflights). The actual production ordering has redirect at §4.1.2.3 and CORS at §4.1.2.6 — redirect runs first because it's cheaper to 3xx a known-redirect URL than to evaluate a CORS preflight; the CORS preflight path is only reached when no `kind=redirect` rule matches the host/path, which is the common case for an app that *does* serve CORS. The defensive ordering is documented in `pkg/gateway/handler.go:2277-2283`.
- **JWT precedes `enforceRequireAuthn` (§4.1.2.7 < §4.1.2.9).** The edge-rule JWT gate fires on the inbound token before the per-deployment bearer-token gate; otherwise a JWT-failed request would still pay the per-deployment auth DB lookup.
- **CORS / JWT / IP all precede `Backend.Pick` (§4.1.2.6, §4.1.2.7, §4.1.2.8 < §4.1.2.11).** A rejected request never pays the cold-wake cost. ADR-091 D4 codifies this; this spec section is the implementation contract.
- **Validate precedes `enforceRequireAuthn` (§4.1.2.12 < §4.1.2.9) AND `Backend.Pick` (§4.1.2.12 < §4.1.2.11).** A malformed-body request is rejected with 422 (`request_validation_failed`, see §11 problem+json) and never wakes a microVM. This is the §4.7 deny-before-cost posture extended to body-shape: the wake quota cannot be exhausted by 4xx traffic. The plan calls out a follow-on in PR-D for the `DeployWake` bitmask so the post-wake path (snapshot restore) is exercised.
- **Limit precedes Backend.Pick (§4.1.2.13 < §4.1.2.11) AND `enforceRequireAuthn` (§4.1.2.13 < §4.1.2.9).** An oversize request is rejected with 413 (`request_too_large`, see §11 problem+json) before any auth/wake work runs, AND the Content-Length fast path denies before the global 25 MiB reader wraps `r.Body` — the "never buffer an oversize body" property is a guarantee, not a hope. The placement is load-bearing: `TestApplyEdgeRuleLimit_ContentLengthFastPath_DenyBeforeBackendPick` is the regression pin (`fakeBackend.Pick` is never called for an oversize request).
- **Same-account assertion at every kind** (ADR-091 D5). A cross-account `kind=X` rule silently falls through (audit + `outcome=blocked` metric + no enforcement).

##### Defense-in-depth guards (spec-level decisions, NOT deviations)

These are guards the apid validator enforces on rule creation; the gateway stamper trusts the validated input. Reversing any of them requires a new ADR (ADR-091 D10 / D11 / D12).

- **CORS `*` + credentials footgun (ADR-091 D12).** `AllowOrigins: ["*"]` combined with `AllowCredentials: true` is rejected at create-time by `pkg/api/dto.go::EdgeRuleCORSAction.Validate`. Browsers reject the combination on the wire (RFC 6454 §7); cheaper to surface a 422 than to ship a rule that silently fails.
- **HS\* dropped from JWT algorithm vocabulary (ADR-091 D11).** Only the closed set `{RS256, RS384, RS512, ES256, ES384, ES512}` is accepted. HS\* over JWKS would mean a symmetric key served from a public endpoint. If a future customer needs HS\*, that's a `secret_ref` action shape and a new ADR.
- **JWKS URL network-position guard (ADR-091 D10).** `EdgeRuleJWTAction.Validate` rejects JWKS URLs starting with `https://localhost`, `https://127.*`, `https://10.*`, `https://192.168.*`, `https://169.254.*`, `https://[::1]`, `https://[fc*::*`, `https://[fd*::*`. The `https://` prefix is a separate requirement. §11's egress posture already forbids the same ranges at the host firewall; this is the application-layer equivalent.

##### Cross-references

- **ADR-091** (authoritative sub-decision history; D4 codifies the ordering table; D17–D20 codify the rollout-closer tests/docs).
- **§11** (egress posture that bounds the JWKS URL network-position validator).
- **§4.7** (per-account rate-limit posture that re-confirms the deny-before-cost guarantee).
- **ADR-070** (Tier A7 edge split — `gatewayd-public` and `gatewayd-internal` on opposite sides of a unix socket; the architecture that makes `pkg/gateway/internal_proxy.go:286` the load-bearing single-trusted-XFF source for §4.1.2.8).
- **ADR-100** + §4.1.2.14 (tenant surfaces; the multi-tenant hostname routing layer that sits *below* the §4.1.2 edge-rule pipeline, in `pgRouter.ResolveHost`).

##### §4.1.2.12 — kind=validate (PR-B / D20.6)

The `kind=validate` shape is the only edge-rule kind that reads the request body. Customers ship a JSON Schema 2020-12 document inline in the rule's `action.jsonb` (`max_edge_rule_validate_schema_bytes = 64 KiB`); the gateway compiles the schema on first sight, keys it by SHA-256 of the body, and consults it on every match.

- **422 surface.** Schema mismatch returns 422 with `code = "request_validation_failed"` and a `problem+json` `errors[]` of `FieldError{field, expected, got}` per `pkg/api/errors.go:1602`. The shape is RFC 7807-compatible. Body cap exceeded (per-rule `max_body_bytes` or the platform cap, whichever is lower) returns 413 `request_too_large`; Content-Type outside the rule's allowlist returns 415 `unsupported_media_type`. Audit events: `edge_rule.validate_matched`, `edge_rule.validate_failed`, `edge_rule.validate_blocked`, `edge_rule.validate_unsupported_media_type`. Metric: `gateway_edge_rule_match_total{kind="validate", outcome=…}` pre-instantiated at line 528-535 (PR-B extension).
- **Cap chain.** Per-rule `max_body_bytes ∈ [0, api.MaxRequestBodyBytes]` is layered on top of the global `http.MaxBytesReader` installed in `ServeHTTP` (handler.go). A 0 `max_body_bytes` inherits the platform cap. The schema cap (`max_edge_rule_validate_schema_bytes = 64 KiB`) is enforced at apid-Validate time (dto.go:EdgeRuleValidateAction.Validate) AND re-checked at compile time (`pkg/edgevalidate.Compile`) — defence-in-depth per §11.
- **Streaming posture.** Per-rule `apply_while_streaming: false` (default) means the rule is skipped when `isUpgradeRequest(r)` is true. SSE-style apps opt the rule in per rule. The opt-out mirrors §4.1's `Accept: application/json` opt-out so an SSE-enabled app keeps validation off until the customer opts the rule in. The CLI emits a warning when `streaming_enabled=true` is detected for the same app so the surprise is documented.
- **External-`$ref` strip.** apid-Validate strips external `$ref` / `$id` URLs at create-time (the regex at `dto.go::edgeRuleValidateRefURLPattern`); `pkg/edgevalidate.Compile` re-strips at compile time on the gateway side. Internal JSON Pointers (`#/definitions/Foo`) currently trip the apid-side regex too (an unanchored `$ref|id` alternation; a future PR will tighten this), but the gateway-side regex is the authoritative gate on the hot path. A runtime `$ref` URL in a stored rule emits 502 `bad_gateway` from the gateway compile path (handler.go applyEdgeRuleValidate line ~1649) — a deploy-time bug-class alarm.
- **Body-restore.** `applyEdgeRuleValidate` is the first production hot-path code to **buffer and re-install** `r.Body` for downstream consumption (`io.NopCloser(bytes.NewReader(buf))` at handler.go line ~1636). The proxy leg reads `r.Body` via `pkg/gateway/forwardproxy.go:319-339`. A regression in the restore shape surfaces as a 502 from the proxy leg (ctxReader EOF mid-body) and is covered by the PR-C e2e happy+reject path.

##### §4.1.2.13 — kind=limit (ADR-091 D24)

The `kind=limit` shape is the standalone per-route body-size primitive — the load-bearing primitive for the "POST /upload ≤ 5 MB, POST /users ≤ 1 MB, POST /webhooks ≤ 2 MB" case where the customer does not want to ship a JSON Schema. The applier is the only kind (besides validate's inner-reader wrapper) that wraps `r.Body`, but unlike validate it does not buffer-and-restore — the per-rule cap is installed as an `http.MaxBytesReader` around the existing `r.Body`, and the cap is enforced on the next read (the proxy leg, the validate applier further down).

- **413 surface.** Cap exceeded returns 413 with `code = "request_too_large"` and a `problem+json` body carrying the rule ID + cap. The shape is RFC 7807-compatible. Audit events: `edge_rule.limit_matched`, `edge_rule.limit_rejected`, `edge_rule.limit_blocked`. Metric: `gateway_edge_rule_match_total{kind="limit", outcome=…}` + `gateway_edge_rule_apply_total{kind="limit", result=…}` (the apply axis carries the "blocked" cross-account case as `success` so the §12 dashboard chip doesn't falsely flag a defense-in-depth no-op as a wire error — same posture as validate's cross-account path).
- **Cap chain.** Per-rule `max_body_bytes ∈ (0, api.MaxRequestBodyBytes]` is layered on top of the global `http.MaxBytesReader` installed in `ServeHTTP` (handler.go). The §4.1.2.13 placement (between `applyEdgeRuleIP` and the global reader) means the per-rule cap is the OUTER reader on the in-limit path; the global reader layers inside as the backstop for requests that don't match any limit rule. Nesting two `MaxBytesReader`s is safe — the inner reader only ever tightens.
- **Content-Length fast path (load-bearing).** When the inbound request advertises a body larger than the cap via `Content-Length`, the applier writes 413 immediately, without reading a single body byte. A bare `http.MaxBytesReader` only trips when something reads the body, and at this hot-path slot nothing reads it until the proxy leg — so without the fast path, a 30 MB POST against a 5 MB rule would buffer 30 MB into memory before tripping. The fast path can only ever produce a **false-positive** 413 (a client that lied high); a lying-low client cannot bypass because the inner `MaxBytesReader` still trips on the proxy leg's first read. Stated explicitly in ADR-091 D24 §4.
- **First-match-wins (not smallest-cap-wins).** Consistent with every other kind; priority is the customer's declared tiebreak. A future "tightest cap wins" optimization would couple two rules semantically — the customer could end up with one rule accidentally swallowing another's intent. Rejected in D24 §rejected-alternatives.
- **Streaming posture.** The rule carries an optional `max_body_bytes_streaming ≤ 100 MiB` (matching `pkg/api.RawStreamMaxRequestBytes` for ADR-080 raw-bridge parity). **Runtime enforcement ships alongside the field.** The applier at the §4.1.2.13 slot consults `streamingFor(h, r, app)` — the 4-conjunct detection formula (`h.streamingEnabled && app.StreamingEnabled && !isAcceptJSON(Accept) && !isUpgradeRequest(r)`) — and picks the cap per request: streaming requests use `max_body_bytes_streaming` (clamped to `api.RawStreamMaxRequestBytes`); buffered requests use `max_body_bytes` (clamped to `api.MaxRequestBodyBytes`); streaming cap == 0 falls back to the buffered cap (safe degradation, never widens). The 413 detail message suffixes the cap kind — `(buffered cap)` or `(streaming cap)` — so a customer can bisect which cap fired without consulting logs. The audit payload (`edge_rule.limit_rejected` + `edge_rule.limit_matched`) carries an additive `cap_kind` field for the same purpose on the operator side. See ADR-091 D24 §6 for the rationale (per-cap-kind clamp, DTO `s ≥ b` invariant trust, deferred-to-runtime risks rejected).
- **Relationship to `kind=validate.max_body_bytes`.** Kept, NOT deprecated. The semantic split is: validate's `max_body_bytes` is the "cap the body I'm about to schema-check" knob; limit's `max_body_bytes` is the standalone gate. The two paths share the same underlying primitive (per-rule `MaxBytesReader`) but surface different control surfaces — validate couples the cap with schema checking, limit couples the cap with nothing else. A customer who declared a `kind=validate.max_body_bytes` rule today can migrate to `kind=limit` in a follow-up (no schema is required for the limit path).

##### §4.1.2.14 — tenant surfaces (ADR-100 / issue #879)

The `tenant_surfaces` shape is the multi-tenant hostname routing primitive introduced for the SaaS case (one Gregale account, many end-customer hostnames, one shared cert). It is **not** an edge-rule kind — it sits one layer *below* the §4.1.2 ordering, in `pgRouter.ResolveHost` (`cmd/gatewayd-internal/backend.go:30-87`), where the host-to-app lookup is the first resolution step. The ordering at that layer is load-bearing:

1. `slugFor(host)` — production subdomain (`{slug}.apps.gregale.dev`).
2. `SurfaceByHostname(host)` — new branch (verified row only); consults `tenant_hostnames` keyed by `hostname citext PK`, joins to `tenant_surfaces` to read the bound `app_id`. Unverified rows fall through.
3. `DomainByName(host)` — legacy single-app custom domain (`migrations/00001_init.sql:106`).
4. Preview parser — `pr-{N}.{slug}.apps.gregale.dev` (ADR-095 / PR-B #872).

The order guarantees **surface routing never shadows a production subdomain** and **a single-app-owner custom domain never collides with a verified surface hostname**. The reverse order would either risk `customer-a.com` claiming the slug=production path via a surface, or a surface silently shadowing a customer's existing `custom_domains` row. The D4 codification in ADR-100 is the load-bearing contract.

- **Cert engine.** A surface mints one cert per `cert_kind` value. `per_host_san` (v1 default) bundles up to 100 hostnames as Subject Alternative Names against one LE order; `per_host` (fallback) mints one cert per hostname; `shared_wildcard` (deferred follow-up ADR) is reserved in the schema but the `CertIssuer` fails closed with `ErrUnsupportedCertKind` until the DNS-01 solver ships. The `CertIssuer.RequestCertForSurface(ctx, surfaceID)` re-mint is synchronous on every surface mutation (create, hostname add, hostname remove) — human-paced re-mint frequency, bounded by surface evolution. The allowlist (`pkg/gateway/allowlist.go:90-156`) gains a surface branch in `OnDemandLookup` that fails closed if the surface row is unverified; the on-demand `OnDemandAllowlist` signature stays binary in v1 because the per-mutation re-mint path handles the SAN group out-of-band.
- **Cert health surface.** `CertExpiry(ctx, surfaceID)` reads the on-disk `<StorageDir>/certificates/<issuerKey>/<primary>/<primary>.crt` and returns the not-after timestamp. PR-C wires this into the `TenantSurfaceResponse` DTO as `cert_expires_at` (the issue body's adjacent gap — `CustomDomainResponse` is extended in the same PR to expose `tls_state` + `cert_expires_at` for parity). The existing `gateway_tls_cert_expiry_seconds{hostname, kind}` Prometheus series picks up the SAN cert under its primary name naturally; no new metric is required.
- **Quota.** `TenantSurfacesPerAccount` (Free 0 / Hobby 1 / Pro 5 / Scale 25), `TenantHostnamesPerSurface` (10/50/250/1000), and `TenantSurfacesAllowed` bool (false on Free, true on Hobby+). All three live in `pkg/api/limits.go`; `pkg/api/limits_test.go` table-driven coverage. **No inline numbers anywhere** (CLAUDE.md mandate). RFC 7807 stable codes: `CodeTenantSurfaceQuotaReached`, `CodeTenantHostnameQuotaReached`, `CodeTenantSurfaceCertKindInvalid`, `CodeTenantHostnameAlreadyClaimed`.
- **Routing layer shape.** `pkg/gateway/surface_parser.go` mirrors `pkg/gateway/preview_parser.go:39-87` (PR-B #872). Pure parser, no I/O, no globals, no logger. Lives in `pkg/gateway` (not `cmd/gatewayd-internal`) so the package stays free of `pkg/state`. The `Backend.Lookup` signature is **not** widened; surface context is plumbed via `App.Scope="surface:<surface_id>"` (the ADR-095 PR-B widener, reused).
- **Hot-path latency.** The surface branch fails closed on a DB miss (`pgtest-pool-exec-vs-queryrow-for-selects` precedent). The PgPool statement is keyed on `tenant_hostnames.hostname` (citext PK) so the lookup is an index hit, not a scan. The `gateway_wake_latency_seconds` histogram is unaffected — the surface lookup adds one extra round-trip per cold-wake, similar to the `custom_domains` legacy path. The e2e pins p50 < 5 ms additional on the surface lookup against the dev cluster.
- **PR-preview coexistence.** The `pr-{N}.{slug}.apps.gregale.dev` shape (ADR-095 PR-B, `pkg/gateway/preview_parser.go`) is a *fourth* lookup branch in `pgRouter.ResolveHost`, AFTER `SurfaceByHostname`. Ordering matters: a SaaS customer's preview app should not be visible to a surface-bound FQDN, and a SaaS customer's surface should not be visible as a preview. Both branches are gated by `*(host)` shape-match before any DB call, so the common case (`{slug}.apps.gregale.dev` or `api.customer-a.com`) hits only one branch.
- **Cluster shape.** PR-0 (docs + fence + limits + CLI typo fix) / PR-A (schema + state + cert engine) / PR-B (parser + routing) / PR-C (HTTP API + CLI + E2E + adjacent fixes). Feature-flagged `FAAS_TENANT_SURFACES_ENABLED` (default OFF through v1.10). Full cluster outline at `docs/adr/100-pr-cluster-outline.md`.

**Cross-references.** ADR-100 (the source of truth for D1..D5 and the cert engine). ADR-028 line 182-184 (the deferral this amendment reverses). ADR-095 (PR-preview routing; the shape-matcher precedent). ADR-024 (CertMagic cut-over; the on-demand+SAN-aggregate model). ADR-070 (the gatewayd-public / gatewayd-internal split that makes this layer readable). GitHub issue #879.

##### §4.1.2.15 — streaming discoverability (ADR-102)

The streaming response path (ADR-047, ADR-028 amendment, ADR-080) is fully wired end-to-end, but **the absence of an outward-facing signal** that a response streamed vs buffered was a customer-facing gap — a customer who paid for `streaming_enabled=true` and got buffered behavior had no way to self-diagnose. ADR-102 closes five gaps in one branch (D1..D8 below) without introducing a new edge-rule kind or a new migration.

- **D1. `Streaming-Status` response header — unconditional.** Stamped at handler.go:~4026 on every response that reaches the proxy leg, regardless of whether the path is streaming or buffered. Closed enum (6 variants: `streaming`, `accept-json-downgrade`, `flag-disabled`, `plan-disallows`, `operator-disabled`, `upgrade-bypass`). Constants in `pkg/api/limits.go`. The header is the canonical customer-facing signal; it is the load-bearing property that closes the G2 silent-buffered-fallback trap.
- **D2. `decideStreaming` helper.** Replaces the legacy `streamingFor` bool at handler.go:2282. Returns a `streamingDecision{Status, Cap, CapKind}` plus the legacy `isStreaming bool` (preserved so `applyEdgeRuleLimit`'s signature doesn't change in this PR). Five conjuncts evaluated in precedence order; the first failure wins. See ADR-102 §architecture for the full tree.
- **D3. Accept: application/json hard-flip + advisory header.** Pre-D3 the gateway downgraded Accept:application/json to buffered (the §4.7 opt-out the customer-facing docs never mentioned). Post-D3 the request streams regardless of Accept when `app.streaming_enabled=true && plan.AllowedStreaming()`. The `accept-json-downgrade` enum variant survives one release cycle so pinned-SDK customers can grep for it; the variant + the `Streaming-Status-Accept-Hint: would-buffer-pre-D3` advisory header both retire in ADR-102-followup ~30 days post-merge.
- **D4. Per-endpoint RESPONSE cap.** Mirrors the §4.1.2.13 per-rule REQUEST cap. A kind=limit rule with `max_body_bytes_streaming > 0` overrides the plan-level streaming cap (`pkg/api/Plan.MaxResponseBodyBytes()`) on the response side. The DTO `s ≥ b` invariant (`pkg/api/dto.go:4188`) is enforced for the REQUEST cap; the same invariant applies for the RESPONSE cap per cmd-side compileLimitRules + the runtime mirror clamp. `CapKind` label is `"plan"` or `"endpoint-rule"`; stamped on the audit payload + the SDK probe response.
- **D5. CreateApp 403 (Free + flag).** `cmd/apid/handlers.go::buildApp` rejects `streaming_enabled=true` on Free plans with 403 `CodePlanStreamingNotAllowed` — mirroring the existing UpdateApp gate at `cmd/apid/handlers_ext.go:245-252`. Defense-in-depth mirror in `decideStreaming` (plan-disallows branch) catches any pre-D5 Free + flag row that survives the data-tier migration. Postgres CHECK constraint ships in the ADR-102-followup PR (NOT VALID + VALIDATE idiom per migration 00155 precedent) after telemetry confirms zero Free + flag rows in production.
- **D6. SDK + CLI probe.** `GET /v1/apps/{slug}/streaming-cap` returns `AppStreamingStatus{AppID, Status, EffectiveCap, PlanCap, FlagEnabled, PlanAllowed, CapKind}`. Auth chain: read-only, no MFA, primary caller is an API key with `ScopesReadSurface`. IDOR-safe via loadApp (cross-account slug → 404). The apid-side mirror of `decideStreaming` evaluates the four conjuncts it can read from the apid cache (per-app flag, plan tier, upgrade header, Accept header); the per-edge-rule cap override is gatewayd-side state and is **not** in the probe's response — `CapKind="plan"` is the only value the probe returns this PR. CLI mirror: `gregale apps streaming-cap <slug>` and `gregale app <slug> streaming-cap`. SDK mirrors: `pkg/api.GetAppStreamingStatus` (hand-written Go), auto-regenerated Node + Python via `make sdk-gen-twice`.
- **D7. CORS expose-headers.** `corsDefaultOps` (handler.go:1593) gains `Access-Control-Expose-Headers: Streaming-Status, Streaming-Status-Accept-Hint` so uncredentialed CORS clients (the default `kind=cors` path) can read the custom response headers. Customer-authored `kind=cors` rules with their own `ExposeHeaders` take precedence per the existing precedence rule.
- **D8. Test matrix + 3 metal e2e cases.** `TestStreamingStatusMatrix` (handler_test.go) — 7-row table-driven coverage of all six enum variants + the endpoint-rule override. `TestStreamingStatusHeader_StampUnconditional` — static structural pin that the unconditional stamp line exists in handler.go (a code-reviewer-readable property, dodges the streaming-writer recursion panic on no-upstream tests). The pre-existing 4-conjunct test `TestApplyEdgeRuleLimit_StreamingFor_FourConjuncts` is reframed for D3 (Accept:application/json streams post-D3; the test asserts `want: true` for that row). Three metal e2e cases in `cmd/e2e/streaming_metal_test.go`: streaming happy-path, endpoint-rule cap, 413 from real cap trip. The other four variants (Accept opt-out, Upgrade, Free+flag 403, Free-flag) are unit-test-only per the metal-flake budget.

**Deferred work.** (1) The Postgres CHECK constraint (`apps_streaming_enabled_plan_check`) ships in the ADR-102-followup PR after production telemetry confirms zero Free + flag rows. (2) The `accept-json-downgrade` enum variant + the `Streaming-Status-Accept-Hint` advisory header retire ~30 days post-merge. (3) The `EffectiveCap` value reflects only the plan-level cap in this PR; the per-edge-rule override resolution lives in gatewayd-side state and is not part of the apid cache. A future ADR may wire the apid→gatewayd-internal control-listener hop for live cap resolution (the routes handler at `cmd/apid/handlers_routes.go:68-163` is the precedent).

**Cross-references.** ADR-047 (the streaming response path this amendment exposes). ADR-091 D24 §6 (the per-cap-kind REQUEST cap that D4 mirrors on the response side). ADR-080 (the raw-bytes upgrade bridge that powers the `upgrade-bypass` enum). ADR-070 (the gatewayd-public / gatewayd-internal split — the probe endpoint lives on apid because the auth chain lives there). The deferred CHECK ships in ADR-102-followup per the ADR-102 §deferred-work block.

#### 4.1.2.6a CORS ergonomics (ADR-091 D20–D25)

Three ergonomic layers extend the §4.1.2.6 `kind=cors` surface; none
of them are spec deviations, only widenerings of the allowlist
grammar and customer-facing ergonomics. The matcher (`matchOrigin`
in `pkg/gateway/handler.go`) is the only origin-algebra seam;
everything else routes through it.

1. **Subdomain / port wildcard grammar (D20).** `AllowOrigins`
   accepts `https://*.example.com`, `https://localhost:*`, and
   `https://api.example.com:*` in addition to literal origins and
   the bare `*`. The grammar is enforced by `api.CorsOriginPattern`
   at create-time; the gateway hot path runs the same predicates
   in `matchOrigin` (defence in depth).
2. **Per-app default CORS (D21, D22).** `apps.cors_default_enabled`
   and `apps.cors_default_origins` (migration 00224) give a single
   opt-in a soft CORS stamp without the customer configuring an
   edge rule. The default runs INSIDE `applyEdgeRuleCORS`,
   immediately after the existing `MatchCORS` miss path, so
   pipeline order stays `kind=cors` rule → per-app default → JWT →
   IP. The OPTIONS short-circuit is SKIPPED on the default path —
   the customer's backend remains authoritative for the preflight
   answer; the gateway only stamps response headers.
3. **Typed SDK helper + CLI subcommand (D23, D24).**
   `pkg/api.CreateCORSEdgeRule` packs the `EdgeRuleCORSAction`
   JSON and pins `kind="cors"` so callers don't have to assemble
   the action blob themselves. `gregale cors allow|ls|rm|show` is
   a thin shim over the helper for the common
   "configure-cors-and-stop-thinking-about-it" crowd that
   motivated the original ticket. Node + Python SDKs pick up the
   same shape via `make sdk-gen` (the kebab POST is the source of
   truth; no hand-written kebab method on the SDK side).

Precedence rule (load-bearing, codifies the contract customers see):
an explicit `kind=cors` rule wins on its match_host + match_path
+ match_methods. The per-app default applies only on a `MatchCORS`
miss — never stacked with an explicit rule, never overrides one.
The `*`+credentials footgun guard (D12) is unchanged; only the bare
`*` entry trips it. A subdomain-wildcard entry expands to a concrete
origin at request time, so browsers permit credentials for it.

#### §4.1.2.0 — apps.maintenance_mode (per-app coarse gate)

The per-app coarse maintenance primitive. A single boolean (`apps.maintenance_mode boolean NOT NULL DEFAULT false`) flips the entire app into maintenance: every request to that app — regardless of host, path, or method — returns 503 with `Retry-After` and `Problem.code = "app_maintenance_mode"`. The flag ships via PATCH `/v1/apps/{slug}` (`MaintenanceMode *bool`) and is gated only on Plan (Free-tier-allowed, no `IsPaidOnly()`). It's the "roll the whole app into maintenance" knob that previously required either a code deploy or a per-rule blanket of `kind=maintenance` rules.

- **503 surface.** Flipped app returns 503 with `code = "app_maintenance_mode"`, `problem+json` body carrying the slug in `Problem.detail` (per-tenant visibility so a customer reading the error on the wire can see WHICH app is in maintenance), and `Retry-After: <seconds>` set via the same `api.WithHeader` helper every other 503 in the platform uses. The default Retry-After is 60 s (`api.EdgeRuleMaintenanceRetryAfterSeconds`); the value is shared with `kind=maintenance` so a customer can reason about both primitives with a single knob. Audit event: `app.maintenance_mode_match`; metric: `gateway_app_maintenance_total{plan=…}` pre-instantiated at the closed `{free, hobby, pro, scale}` plan set.
- **Coarse-before-fine ordering (§4.1.2.0 < §4.1.2.14).** When `apps.maintenance_mode=true` AND a matching `kind=maintenance` rule are both live, the coarse gate fires FIRST and the customer sees `code = "app_maintenance_mode"`, NOT `code = "edge_rule_maintenance"`. The reason: a flipped boolean is the customer saying "the whole app is down" — surfacing a fine-grained rule's Problem.code + Message + Retry-After in that case would imply the fine-grained rule is what took the app down, which is misleading. Pin: `TestAppsMaintenanceMode_E2E_CoarseGateBeatsEdgeRule` (cmd/e2e/apps_maintenance_mode_e2e_test.go) asserts the coarse code AND asserts the fine rule's `Message` does NOT leak into `Problem.detail`.
- **Cache invalidation.** Flips propagate via a new `apps_maintenance_mode_notify` AFTER UPDATE trigger (migration `00237`) that emits `pg_notify('app_changed', NEW.id::text)` ONLY when `maintenance_mode IS DISTINCT FROM old.maintenance_mode` (low-volume, not on every app update). The gatewayd listener (`cmd/gatewayd-internal/run.go`) drops ONLY that app's entry from the apps LRU (`Backend.ResetApp(appID)` at `pkg/gateway/pgbackend.go:1025`); it does NOT do a wholesale `FlushRoutes`, because flipping one app's maintenance_mode shouldn't evict every other app's cache. Pin: `TestHandleInvalidation` at `cmd/gatewayd-internal/backend_test.go` asserts the `NotifyAppChanged` arm uses `ResetApp` (not `FlushRoutes`), and `TestHandleInvalidation_TerminalStatesEvict` confirms the cross-account fall-through is NOT triggered by a maintenance flip.
- **Free-tier-allowed.** No `IsPaidOnly()` — Free-tier customers get the same primitive. The cost is one boolean per app and one cache entry, both negligible. Cost-control gate (D4) was the design rationale for keeping Free-tier cost reasonable; maintenance mode INCREASES cost-effectiveness by letting Free-tier customers avoid wake costs during outages.
- **Plan-gated limit.** No per-app counter for v1 (D20.10 if customers ask). The flag is not quota-gated — only plan-gated (Free-tier-allowed).
- **Relationship to `kind=maintenance`.** The coarse gate (§4.1.2.0) and the fine-grained rule (§4.1.2.14) are complementary, not alternatives. The coarse gate is the "roll the whole app into maintenance" knob; the fine-grained rule is the "roll this specific (host, path, methods) into maintenance" knob. They share the same Retry-After default + cap (60 s default, 24 h hard cap), the same Problem envelope, and the same audit/metric surface — so a customer can mix-and-match without learning two APIs.

#### §4.1.2.14 — kind=maintenance (per-rule fine-grained gate)

The per-rule fine-grained maintenance primitive. A customer marks a specific `(match_host, match_path, match_methods)` tuple as returning `503 + Retry-After` — the "roll THIS route into maintenance, not the whole app" knob. Closes the customer-facing gap that previously required a code deploy (today, putting `POST /payments` into maintenance while keeping `GET /payments` working means writing a handler that returns 503, which is a deploy). The primitive is a new edge-rule kind (`maintenance`), joining the existing closed vocabulary of `route, rewrite, redirect, headers, cors, jwt, ip, validate, limit` (10 kinds total).

- **503 surface.** Matching rule returns 503 with `code = "edge_rule_maintenance"`, `problem+json` body carrying the rule's `Message` in `Problem.detail` (≤ 512 B; the apid validator enforces the cap), and `Retry-After: <seconds>` set via `api.WithHeader`. The per-rule `retry_after_seconds` is clamped to the platform default (`api.EdgeRuleMaintenanceRetryAfterSeconds = 60`) on `<= 0`, and to the platform cap (`api.MaxEdgeRuleMaintenanceRetryAfterSeconds = 86400` — 24 h) on `> cap`. Both clamps are enforced at apid-Validate time AND re-checked at cmd-side compile (`compileMaintenanceRules` in `cmd/gatewayd-internal/edge_rules.go`) — defence-in-depth per §11. Audit events: `edge_rule.maintenance_matched`, `edge_rule.maintenance_blocked`; metric: `gateway_edge_rule_match_total{kind="maintenance", outcome=…}` pre-instantiated at the same closed `{match, miss, blocked}` outcome set as every other kind.
- **Placement (§4.1.2.14, immediately after the §4.1.2.0 coarse gate).** A matching `kind=maintenance` rule fires BEFORE redirect/rewrite/headers/CORS/JWT/IP/limit/validate/auth/wake — same deny-before-cost posture as ADR-091 D4 codifies for every other cost-control kind. The customer never pays a cold-boot cost for a route that's in maintenance. Pin: `TestEdgeRulesMaintenance_E2E_MatchReturns503` (cmd/e2e/edge_rules_maintenance_e2e_test.go) walks the load-bearing 503 + Retry-After + Content-Type + Problem.code + Problem.detail contract end-to-end.
- **Methods filter.** The rule carries `match_methods: []string` (the standard edge-rule matcher); a rule with `match_methods: [POST]` does NOT shoot down a GET. Pin: same `TestEdgeRulesMaintenance_E2E_MatchReturns503` asserts the GET case falls through to `Backend.Pick` (404, no real impl). The "roll ONLY POST /payments into maintenance while GET /payments keeps working" use case — the load-bearing reason this primitive exists — is the load-bearing test case.
- **Same-account assertion (ADR-091 D5).** A cross-account `kind=maintenance` rule silently falls through (audit + `outcome=blocked` metric + no enforcement), same as every other kind. Pin: `TestEdgeRulesMaintenance_E2E_CrossAccountFallsThrough` (cmd/e2e/edge_rules_maintenance_e2e_test.go) seeds a rule owned by account B applied to a host in account A, asserts the request reaches `Backend.Pick` (404, not 503), and confirms the audit row is `maintenance_blocked` not `maintenance_matched`.
- **Free-tier-allowed.** Same rationale as §4.1.2.0. No `IsPaidOnly()`.
- **Per-rule quota.** Counts against the existing `EdgeRulesPerApp` quota (5/25/100/500 by plan). No new per-rule counter for v1 — D20.10 if customers ask.
- **TTL / auto-disable.** Out of scope for v1; deferred to D20.7. Customers today re-PATCH the rule to clear `maintenance` (or DELETE it) when the outage ends.
- **Default Retry-After.** `api.EdgeRuleMaintenanceRetryAfterSeconds = 60` (env-overridable via `FAAS_EDGE_RULE_MAINTENANCE_RETRY_AFTER_SECONDS`). A customer who doesn't set `retry_after_seconds` in the rule body gets the 60 s default — same posture as every other default in the platform.
- **Cap.** `api.MaxEdgeRuleMaintenanceRetryAfterSeconds = 86400` (24 h). A PATCH with `retry_after_seconds > 86400` returns 422 `request_validation_failed` from the apid validator BEFORE the rule reaches Postgres. The cap is hard (no per-rule override).

#### §4.1.2.15 — kind=throttle (per-route token-bucket cap, ADR-091 D20.5 amendment, issue #881)

The per-route rate-limiting primitive. A customer tightens the per-route rps/burst below their plan's `plan.RateLimitRPS` — the canonical use case is "I want my JWKS-protected endpoint capped at 100 RPS even though my plan allows 500." The primitive is a new edge-rule kind (`throttle`), joining the existing closed vocabulary of `route, rewrite, redirect, headers, cors, jwt, ip, validate, limit, maintenance, geo` (12 kinds total).

- **Sub-plan ceiling only.** A throttle rule is STRICTLY a tightening primitive. The apid validator (`pkg/api/dto.go::EdgeRuleThrottleAction.Validate`) rejects `requests_per_second > plan.RateLimitRPS` and `burst > plan.RateLimitBurst` with 422 `request_validation_failed` BEFORE the rule reaches Postgres. The gateway compiler (`compileThrottleRules` in `cmd/gatewayd-internal/edge_rules.go`) defensively re-clamps + `slog.Warn` at load time — same defence-in-depth posture as `compileLimitRules` for kind=limit. A customer cannot raise their plan limit by registering a throttle rule.
- **Bucket key: `appID + "\x00" + ruleID`.** Rule-scoped. Cardinality is bounded by *configured rules*, not by traffic, so the limiter's map stays bounded even with N=many rules. Per-IP sub-keying is deliberately out of v1 — multiplying bucket cardinality by attacker-controlled IP count against a memory-bounded limiter is a memory-exhaustion vector on the wake path. If a future ADR introduces per-IP limit, it needs its own bounded design (ADR-093-style cap + `__ip_other__` overflow that still consumes the parent rule bucket).
- **LRU eviction (prerequisite).** The existing `pkg/gateway/ratelimit.go::Limiter` has no eviction. The throttle bucket-multiplies the cardinality. New `NewLimiterWithLRU(cap)` constructor + **full-bucket-only invariant** — only buckets with `tokens >= burst` are evictable; partially-drained buckets are pinned to prevent a forced-eviction bypass (an attacker that can force a partially-drained bucket's eviction could reset their own bucket). Cap = `pkg/gateway.EdgeRuleCacheCap` (10,000) for consistency with the existing per-rule cache.
- **Hot-path placement (§4.1.2.15, immediately after `kind=limit`).** `applyEdgeRuleThrottle` runs AFTER `applyEdgeRuleLimit`, BEFORE `applyEdgeRuleValidate`, BEFORE the global `MaxBytesReader` and the wake gate. Rationale: requests rejected by jwt/ip/geo/limit must not consume a route token; the throttle (O(1) map op) must run before `validate` allocates and parses a body; and throttled traffic never wakes a VM. The placement is the cost-control slot (D4) — same posture as every other cost-control kind.
- **429 surface.** Mirrors the existing inline shape at `pkg/gateway/handler.go:3524`: `Retry-After: 1`, `x-faas-rate-limit-scope: route` (new scope value), `X-RouteRateLimit-{Limit,Remaining,Reset}` via `writeRouteRateLimitHeaders`, and `api.WriteProblem(NewProblem(429, "rate_limited", …))`. The `x-faas-rate-limit-scope` header distinguishes route-scoped 429s from the existing app-scoped + account-scoped ones.
- **Per-rule quota.** New `Limits.EdgeRulesThrottlePerApp` (Free=1 / Hobby=5 / Pro=25 / Scale=100) — same Free-allowed + per-kind cap precedent as `kind=geo`. Enforced in `pkg/state/pgstore.go::CreateEdgeRuleIfUnderQuota` AND mirrored in `pkg/state/memstore.go` (the classic omission is the in-memory mirror).
- **Validator posture.** `pkg/api.EdgeRuleThrottleAction.Validate(ctx ThrottleValidationContext)` takes a per-plan ceiling bag (`PlanMaxRPS`, `PlanMaxBurst`). The CLI invokes with `ThrottleValidationContext{}` (zero context → zero ceiling → skipped ceiling check) — the CLI is HTTP-only per the ADR-097 invariant and doesn't pull plan rows directly; the apid sub-plan validator against `acct.Plan` is the authoritative gate. Both 0-rps and 0-burst are rejected as 422 (the "0-rps rule is a silent no-op AND would create a permanently unevictable bucket" failure mode).
- **Phase 1 recommender (PR-A, ships alone).** `GET /v1/apps/{slug}/throttle-suggestions?range=` returns `suggested_rps = clamp(ceil(observed_rps * 2), 1, plan.RateLimitRPS)` per route, with `__route_other__` excluded from the suggestions and the count surfaced as `routes_collapsed`. New `pkg/promql.QueryGrouped` seam + `pkg/throttlerec` package. The `sources/promql.QueryBuckets` hardcodes the `app` label and `le` inner; `QueryGrouped` takes the outer/inner labels as parameters so the new recommender can group by `route` without breaking the existing invariant.
- **Per-consumer/API-key keying remains OUT of scope** (Phase 3 — needs the ADR-040 policy question settled: opaque X-API-Key header vs JWT claim). The ADR-091 D20.5 fence names per-rule rate limit and was deferred to a new ADR — this amendment closes that fence in place, matching the D20.6 / D24 precedent.

**§4.1.2.15 `kind=cache` — Edge response cache (ADR-122).** A bounded, in-process, per-`gatewayd-internal` response cache for safe GET / HEAD responses. The primitive is a new edge-rule kind (`cache`), joining the existing closed vocabulary of `route, rewrite, redirect, headers, cors, jwt, ip, validate, limit, throttle, maintenance, geo, budget` (14 kinds total). Allow safe caching for selected GET routes — `GET /catalog/*`, cache 60 s, vary by Authorization + Accept-Language, serve stale 5 min on failure, surface cache-hit rate + saved compute cost.

- **Hard bypass on authed requests (the load-bearing safety property).** A request with `Authorization` header or a session cookie MUST bypass the cache entirely — never stored, never served. This is correct-by-construction: an unauthed populate cannot leak to an authed request because authed responses are never stored in the first place. The `vary_on` closed vocabulary is `Accept-Language | Accept-Encoding | query` — `Authorization` is deliberately NOT a vary dimension; it is a hard bypass. Per-principal caching is a follow-on ADR (deferred, ADR-122 §Out of scope).
- **Cacheability predicate (deny-by-default).** Store only when ALL hold: `method ∈ {GET, HEAD}`; request has no `Authorization` header AND no session cookie; response status ∈ {200, 203, 300, 301, 308, 404, 410}; response has no `Set-Cookie`; response lacks `Cache-Control: no-store|private`; body ≤ per-entry cap (`ResponseCachePerEntryMaxBytes = 1 MiB`); no concurrent in-flight write to the same key. A `Cache-Control: no-store` / `private` from the origin is an absolute veto — the app always wins over a platform-level rule.
- **Cache key.** `appID | ruleID | method | normalizedPath | sha256(sorted(vary_on header values)) | sortedQuery`. NOT bound to `deploymentID` in v1 — deploy invalidation is wired via `NotifyAppChanged → InvalidateByApp` (the `cacheRuleSnapshot.DeploymentID` slot is reserved for a follow-on). `appID` isolation prevents cross-tenant leakage when two apps share a path glob (e2e unit pin: `TestCacheSecurity_6_AppIsolation`).
- **Store: in-process per `gatewayd-internal`.** `sync.RWMutex` + `container/list` LRU + byte ceiling, mirroring `pkg/gateway/public_auth_cache.go`. Default size `DefaultResponseCacheMaxBytes = 32 MiB` (tunable via env). NO persistence, NO cross-node sharing, NO bodies at rest. On restart the cache is cold; on `NotifyAppChanged` the entries for that app are dropped; on `NotifyEdgeRuleChanged` the entire cache is wholesale `Reset()`-ed.
- **Serve stale on failure (ADR-122 §Decision).** Retain entries past `max_age` for `stale_if_error` (capped at 5 min at the apid validator; a rule with `stale_if_error == 0` disables stale-on-error). Serve stale ONLY on a genuine origin failure (`gate.Wait` wake failure or upstream 5xx/timeout), never on a cache miss. Stamps `Warning: 110 - "Response is Stale"` per RFC 7234 §5.5.2 and a platform-internal `X-From-Cache: stale` debug surface. `outcome="stale_if_error_served"` is its own metric label so stale hits are never conflated with fresh hits in the hit-rate number (the dashboard surfaces stale serves separately so operators see when their cache is being relied on as a fallback).
- **Placement (§4.1.2.15, immediately AFTER §4.1.2.10 `enforcePublicAuth` and BEFORE §4.1.2.11 `Backend.Pick`).** A hit returns without calling `h.gate.Wait`, so no VM wakes and no `gb_ram_hour` accrues. A `public_auth`-gated app's request is rejected with 401 by `enforcePublicAuth` BEFORE the cache applier is consulted — placing BEFORE auth would silently bypass the gate with a stale 200. CORS preflight (OPTIONS) is short-circuited at §4.1.2.6, so preflight responses are NEVER cached (an OPTIONS cached against the wrong `Origin` is a real CORS bypass).
- **Saved-cost surface (telemetry-only, NOT a new SKU).** `gateway_response_cache_total{outcome}` closed at `{hit, miss, bypass_authed, bypass_uncacheable, stale_if_error_served, store_skipped}` (pre-instantiated); `gateway_response_cache_wakes_avoided_total{app_id}` increments ONLY when a hit lands on an app with zero healthy instances (`h.backend.HealthyCount(appID) == 0`), i.e. a hit that genuinely displaced a cold boot — a hit against an already-warm app saves latency but no compute, and must NOT be counted as savings. `gateway_response_cache_bytes` / `gateway_response_cache_entries` are store occupancy gauges. Saved cost = `wakes_avoided × (plan_ram_mb + 8) × billed_seconds`, computed in the reporting layer from existing `pkg/api/limits.go` plan RAM. No new billing table; `gb_ram_hour` SKU unchanged (mirrors `usage_minutes.tx_bytes` per §1061).
- **Closed-vocabulary discipline.** `vary_on ∈ {Accept-Language, Accept-Encoding, query}` (3 values), `methods ∈ {GET, HEAD}` (2 values); both enforced at apid-Validate. Apid validates per-plan count limits (`EdgeRulesCachePerApp`: Free=0, Hobby=1, Pro=5, Scale=20). The runtime posture reflects `kind=cors` and `kind=throttle`: NOT paid-only, gated by per-app count instead. Per-kind quota branch in `pkg/state/pgstore.go::CreateEdgeRuleIfUnderQuota` (lock + count, mirrored in `memstore.go`). The 14-kind closed vocabulary is enforced by the `edge_rules_kind_check` Postgres CHECK plus `state.EdgeRuleKind.IsValid()` — adding a 15th requires a new migration that re-emits the full prior enum on Down.
- **Stateless-contract deviation (recorded explicitly).** This is the first customer-visible exception to the "platform is stateless by contract" posture (`docs/storage.md:35`); the deviation is bounded by opt-in (per-rule), default-off (no rule = no cache), in-memory only (no persistence), and never authoritative (a miss always falls through to the wake path per ADR-005). Per CLAUDE.md this required the ADR rather than a quiet feature.
- **Audit + metric names.** Audit events: `edge_rule.cache_matched`, `edge_rule.cache_missed`, `edge_rule.cache_stale_served`. Metric: `gateway_edge_rule_match_total{kind="cache", outcome=…}` pre-instantiated at the same closed `{match, miss}` outcome set as every other kind; the dedicated `gateway_response_cache_total{outcome}` carries the per-applier posture above. Runbook: `FaasResponseCache`.

#### §4.1.2.16 — `public_auth_mode='members_only'` (ADR-123, issue #477 §6 / #4)

The `members_only` value of `apps.public_auth_mode` (the sixth in the closed enum `open` / `bearer` / `basic` / `ip_allowlist` / `internal_only` / `members_only`) is the per-app **org-membership** ingress gate. A request to a `members_only` app must carry a valid `faas_sid` session cookie whose principal is an **active member** of `apps.org_id`. The cookie side is validated upstream by `pkg/auth/middleware.RequireSession` (AEAD-decrypt + live-row cross-check + binding-hash verify — the IAM-6 trust chain); the gate only adds the **membership** predicate on top. Reuses the existing IAM-6 cookie layer end-to-end — no new cookie, no new token, no new dashboard auth wiring.

- **Trust chain.** `RequireSession` runs BEFORE the gate (it's in the public-daemon hop's middleware chain), so by the time the gate sees the request the cookie envelope is already verified. A stolen / forged / revoked / binding-mismatch cookie is auto-revoked at `middleware.go:648-741` and the request 401s before reaching the gate — the gate never sees those failure modes except as a residual "no principal on `r.Context()`" branch. This is deliberate: a second-cookie-validation pass at the gate would be a fingerprint oracle for "this cookie passed authn but failed authz" vs "this cookie was always going to fail authn".
- **Placement.** Sits in the `ServeHTTP` chain at `pkg/gateway/handler.go::applyIngressMembersOnly`, immediately after `applyIngressInternalSvc` (ADR-119) and before `applyEdgeRuleIP` — same posture as its two siblings. A denied request never wakes a Firecracker microVM. Membership-lookup SQL is O(log n) on the `(org_id, account_id)` PK index of `org_memberships` (migration 00099, the personal-org + shared-orgs split).
- **Membership predicate.** `pkg/authz.IsOrgMember(ctx, pool, app.OrgID, principalID)` runs `SELECT EXISTS(SELECT 1 FROM org_memberships WHERE org_id = $1 AND account_id = $2 AND removed_at IS NULL)`. Returns `(true, nil)` for an active member, `(false, nil)` for a removed member or no row, `(false, ErrMembershipLookup)` for any DB error. The gate pivots on `errors.Is(err, authz.ErrMembershipLookup)` to surface a controlled 401 + audit `reason='lookup_error'` rather than a 500 from the unwrapped pgx error — fail-closed posture mirrors ADR-119 round-2 at `cmd/schedd/internal_svc_minter.go:6`.
- **401 vs 403 split.** No-cookie / revoked / binding-mismatch / `lookup_error` (cookie authn failed) → **401 Unauthorized** with `code = "unauthorized"`. Valid cookie but not a member of `apps.org_id` (authn passed, authz denied) → **403 Forbidden** with `code = "forbidden"` + `detail = "this account is not an active member of the organization that owns this app"`. The split is what lets the dashboard render two distinct CTAs: "you forgot to log in" (401) vs "you are logged in but not a member of this org" (403).
- **Plan tier (Hobby+).** Free PATCHes to `mode='members_only'` are rejected with **402** `plan_public_auth_members_only_not_allowed` (mirror of `bearer`'s 402 — see `pkg/api/limits.go::Limits.PublicAuthMembersOnlyAllowed`). Hobby / Pro / Scale accept. The 422-supersedes-402 invariant from ADR-079 (`validation_failed` for an unknown mode value) still holds — the closed-enum check fires first, so a Free customer who PATCHes `mode='bogus'` gets 422, not 402. Hobby already unlocks the `OrgMembersMax` ladder via ADR-061 line 174; `members_only` on a Hobby personal-org app is functionally `bearer` with the same account, so the Hobby+ tier matches `bearer` deliberately.
- **`X-Active-Org` header independence.** The gate does NOT consult `X-Active-Org` / `?org=` (the headers `pkg/authz.LoadOrgWithResolver` uses to stamp `principal.Membership`). A request that hits the `members_only` gate cannot game the membership check by sending a forged `X-Active-Org=<target-org>` — the gate looks up directly against `apps.org_id`. This is the load-bearing difference from the IAM-6 cookie layer (ADR-061) which DOES use those headers for the per-request active-org stamp.
- **SynthServer mirror.** Cron traffic carries no human session, so the synth-side `applyIngressMembersOnly` denies every cron wake to a `members_only` app. Wired at all three call sites (`pkg/gateway/synth.go:handleSynthesize`, `handleInvocationDispatch`, `handleInvocationDispatchBatch`). The wake never fires. Customers who want cron-receivable member-gated apps must use `open` or `internal_only`. Mirrors the design gap surfaced during the ADR-119 PR-A build (`schedd` cron bypasses `Handler.ServeHTTP` entirely via `pkg/gateway/synth.go:handleSynthesize`; the gate must cover both surfaces).
- **Audit redaction.** The cookie envelope / session id MUST NEVER appear in an audit payload (`CLAUDE.md` §Conventions: "Never log secret values; env secrets are sealed at rest"). The cookie-side resolver returns only the `account_id` (a UUID, not a cookie value); only that + `app_id` + `from_host` + a short reason enum (`no_cookie` / `expired_session` / `revoked_session` / `binding_mismatch` / `not_member` / `removed_member` / `lookup_error`) flow into the audit row. Pin test: `TestApplyIngressMembersOnly_AuditDoesNotEchoCookie` substring-checks that `faas_sid=<value>` never appears in the audit JSON.
- **Operator misconfig → 500.** A `members_only` app row with `apps.org_id = ''` (pre-00099 migration drift) or a `members_only` row on a daemon that hasn't wired `WithMembersOnlyChecker` + `WithMembersOnlyPrincipalExtractor` returns 500 `internal` with `code = "internal"`. The gate refuses to silently pass — same loud-posture as `applyIngressIPAllowlist`'s empty-CIDR branch and `applyIngressInternalSvc`'s no-verifier branch.
- **Audit + metric names.** Audit events: `edge_rule.ingress_members_blocked{reason=…}` (denial, closed reason set above), `edge_rule.ingress_members_forged` (defense-in-depth trust-chain break — future hook; ADR-118 line 226-238 precedent). Metric: `gateway_edge_rule_match_total{kind="ingress_members", outcome=…}` pre-instantiated at the same closed `{match, miss, blocked, failed}` outcome set as every other kind; the dedicated counter is named in `pkg/gateway/metrics.go:1051` (closed set: `ingress_ip` / `ingress_internal` / `ingress_geo` / `ingress_throttle` / `ingress_members`). `gateway_edge_rule_apply_total{kind="ingress_members", outcome=…}` mirrors. Runbook: `FaasIngressMembers`.
- **Migration.** `migrations/00378_apps_public_auth_members_only.sql` widens the `apps_public_auth_mode_chk` CHECK constraint by DROP-then-ADD (per the `trigger-replay-safety-drop-before-create` precedent at `scripts/ci/check_migration_slots.sh`). No new column; the policy lives on the closed enum. The 7-scenario test at `migrations/00378_apps_public_auth_members_only_test.go` pins the round-trip + the DOWN-grade narrows.
- **Cross-references.** ADR-061 (IAM-6 foundation), ADR-079 (closed-enum precedent), ADR-118 (`ip_allowlist` sibling — the closest per-app gate precedent), ADR-119 (`internal_only` sibling — the closest in trust-model terms), ADR-123 (this ADR). §4.1.2.10 (the per-app public-auth gate slot this widens), §4.1.2.7 + §4.1.2.8 (the closed-vocabulary precedent for the per-kind quota branch in `pkg/state/pgstore.go::CreateEdgeRuleIfUnderQuota`).

### 4.2 `apid` — control API

**Owns:** the public REST API, auth, validation, and being the *only* writer to customer-intent tables.

- Auth: API keys (`fp_live_…`, SHA-256 stored), per-user; sessions for the dashboard later. Every key scoped to an account. **Tenancy** lands in IAM-6 (ADR-061, issue #190); the tenant is the `orgs` row, APIs are path-scoped under `/v1/orgs/{slug}/...`, and cookie sessions stay account-bound. See ADR-061 for the wire-shape contract.
- Resources (all JSON; full endpoint table in Appendix A): accounts, apps, deployments, builds, instances (read-only), usage, plans, custom_domains.
- Deploy inputs (three, ADR-004): `POST /v1/apps/{app}/deployments` with (a) tarball upload `source` (≤ 100 MB Free/Hobby, ≤ 250 MB Pro/Scale — reject larger with `413` and a docs link), (b) `dockerfile: true` flag if tarball root has one, or (c) `image: registry.gregale.dev/...@sha256:...` reference.
- Function deploys: `type: function`, `runtime: node22 | node24 | python312 | python313 | go124 | go124-alpine`, tarball contains `handler.{js,py}` (+ optional `package.json` / `requirements.txt`). apid rewrites this into an App deployment using the runner scaffold (§4.9) and the same pipeline runs.
- Validation enforces plan quotas *before* work happens: deployed-sandbox count, RAM size ≤ plan cap, concurrency setting ≤ plan cap.
- Idempotency: `Idempotency-Key` header on all POSTs, stored 24 h.
- Never talks to vmmd/builderd directly — writes rows, notifies via `pg_notify`; owners poll/listen.
- **Multi-workload repo decomposition (ADR-050) + blast-radius preview (ADR-124):** `POST /v1/projects/scan` (dry-run) and `POST /v1/projects` (apply) handle N-workload tarballs as a single transaction. The `PlanResponse` carries a partition so a single commit's blast radius is visible pre-apply — `will_deploy[]`, `unaffected[]`, `skipped[]`, and `removed[]` are the four sets, matched by `(RootDir, Name)` against the account's existing apps. CLI: `gregale scan --show-affected`, `gregale deploy --exclude=a,b`. Dashboard: `/dashboard/projects/{slug}/preview` (form + apply). Match key mirrors `pkg/reposcan.Workload.Key()` / `pkg/reconcile.diff.workloadDiff` so the wire and the apply engine agree byte-for-byte.

### 4.3 `schedd` — scheduler and lifecycle owner

**Owns:** the instance state machine (§6), admission control, idle reaper, eviction, cron.

- **Admission (wake or build):** grant iff
  `resident_ram_mb + request_mb + 8 ≤ 0.85 × 56_000` **and** plan concurrent count not exceeded **and** vCPU slots (160) not exhausted. Builds request from the same guard but are also capped by the build semaphore (§9). Denial → `gatewayd-internal` serves `503 capacity` (alert fires long before customers see this; see §12).
- **`AdmitInstance` RPC (issue #168):** `gatewayd-internal` can ask schedd to admit ONE additional instance for an app, bypassing the Phase-1 fast-path shortcut. The cap is enforced atomically by the same ledger as `Wake`; the response carries an `at_capacity` typed result so the gateway can distinguish "we refused because you're already at cap" (no FAILED row written) from a real failure (RAM headroom, chooser, store). The gRPC surface is additive — see ADR-018 update.
- **Idle reaper:** every 10 s, park instances with `now − last_request_at > idle_timeout(plan)`. Defaults: Free 30 s, Hobby 60 s, Pro 300 s, Scale 600 s (app-configurable down to 10 s, not above plan default × 2).
- **Aggressive reaper (ADR-038, issue #171):** every 10 s, *alongside* the idle reaper, a fast-cooldown path parks the surplus above `max(min_instances, desired + 1)` where `desired = ceil(windowed_rps / autoscale_target_rps)` from a per-app 5 × 1 s rolling RPS mirror (`pkg/sched/recentload/`). The `+1` is a hysteresis buffer so a brief RPS wobble doesn't wake-then-park. Scope: apps with `max_concurrency > 1` and `autoscale_target_rps > 0`; single-instance apps and apps without an autoscale target stay on the idle reaper only. Per-tick cap of 8 parks per app to prevent a single tick from blocking the reaper for `8 × ~150 ms ≈ 1.2 s` during a sudden scale-down storm. Default ON; `FAAS_REAPER_AGGRESSIVE=false` (schedd.toml `reaper_aggressive = false`) disables in-place — the mirror, metric, and audit row stay live so diagnosis continues. G7 (OpenConns > 0) and `MinInstanceAge` (30 s) protections still apply. Acceptance: 5-instance burst → 0 rps parks back to ≤ `min_instances + 1` within 30 s (three 10 s ticks). Audit row `events.kind='reaper_scale_down'` is emitted once per app per tick that parks ≥ 1 instance with `{app, desired, parked[], reason: "traffic_dropped", now}` in `data jsonb`.
- **Eviction (RAM pressure > 80 % of the 85 % target):** park instances LRU by last request; never evict an instance younger than 30 s; Scale plan evicted last.
- **Free-tier disk reaper:** free apps with zero requests for 14 days → snapshot + rootfs moved to object storage, state `EVICTED_COLD` (redeploy = one click, re-flatten from stored image). This is the founding doc's ceiling-protection policy (§9.7 there).
- **Cron:** `crons` table; fire = synthetic `POST` through `gatewayd-internal` (so metering/limits apply identically). Per-app and per-account caps are enforced in `apid`'s `createCron` under an apps `FOR UPDATE` row lock (mirrors `CreateAppIfUnderQuota`); Free plan is gated to 402 `plan_crons_not_allowed` because the per-app cap is 0. See `pkg/api/limits.go::CronLimitPerApp` / `CronLimitPerAccount`.
- Single process, single writer to `instances` per node — no distributed locking required. Multi-node today: each node has its own schedd writing the rows it owns; `apid` routes writes (interface kept narrow deliberately: `EnsureInstance`, `Park`, `Evict`).
- Autoscale target tiering (RPS Pro+, CPU Pro+, plan Hobby→Pro re-tier applied 2026-07-28) — see [ADR-037 §Reconciliation note + §Amendment](adr/037-reactive-scaleup-trigger.md).

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

- **Two-drive scheme (protects the 130 MB fleet target):** drive0 = shared, read-only, content-addressed **base rootfs** (`base-minimal`, `runner-node22`, `runner-node24`, `runner-python312`, `runner-python313`, `runner-go124`, `runner-go124-alpine` — counted once, in the 60 GB reserve); drive1 = per-app **app layer** ext4 containing only the OCI layers above the base (deps + code + `/etc/faas/app.json`). guest-init assembles them with overlayfs at boot. A flattened single-drive rootfs would duplicate ~150+ MB of base per app and silently destroy the financial model's disk math — do not "simplify" to it.
- App-layer build: diff OCI layers above the matched base → `mkfs.ext4 -d <dir> layer.ext4 <padded size>`, ≤ plan app-layer cap: Free 256 MB, Hobby 512 MB, Pro 1 GB, Scale 2 GB. Content over cap fails the deploy with a clear error naming the cap and observed size. `guest-init` is injected into the app layer.
- Base images: `runner-node22`, `runner-node24`, `runner-python312`, `runner-python313`, `runner-go124`, `runner-go124-alpine`, `builder-base` — built in CI from Dockerfiles in `images/`, content-addressed, and auto-staged to `/srv/fc/base/` by `imaged` (`pkg/imaged/base_stage.go::EnsureRuntimeBase` for the selected function runtime; the builder base is staged at startup). Runtime image contents are contract-smoke-tested in CI, including architecture, interpreter, shell, and staged `/sbin/init` checks. Operator-side digest pin per runtime via `FAAS_DEPLOY_BASE_REF_<RUNTIME>` env var; no manual ext4 placement is supported.
- Snapshot GC: keep current + previous deployment's snapshots per app; delete orphans nightly; enforce the 452 GB budget with account-level fairness (biggest-over-quota first). Emits `snapshot_fleet_avg_mb` and `snapshot_fleet_p95_mb` — **the** business metrics.
- **Post-build image-layer secret scan (ADR-101, PR-A of the secret-scan cluster):** between `SetDeploymentRootfs` and the `pending → snapshotting` transition, `imaged` re-stages the per-app ext4 via `stageScanExt4` (same helper the grype path uses) and walks the resulting filesystem with `pkg/secretscan.IsTextFile` + `pkg/secretscan.ScanFile`. Same engine the apid source-tree scanner (`cmd/apid/secretscan.go::scanExtractedTreeSecrets`, secret-scan v2 / PR #873) uses — same patterns, same providers, same Severity table. Differs in posture: **loud-fail**. A pattern-level finding calls `markDeployFailed` with the `errImageSecretDetected` sentinel, stamps the audit row via `state.Store.UpsertDeploymentSecretFindings` (reuses `deployments.secret_findings` + `secret_scanned_at` from migration 00264; status value `complete` on a clean walk, `complete_with_redactions` on a hit), and short-circuits the `pending → snapshotting` transition. `error_code = 'image_secret_detected'` on the free-text `deployments.error_code` column (no CHECK widening needed). Function deploys are out of scope (already scanned at apid source-tree time). Each sidecar ext4 gets the same walk with `layer = "sidecar-<slug>"` so a finding is attributable. Drill-down surface: `GET /v1/deployments/{id}/secret-scan` mirrors `/scan` (404 on IDOR + scan-pending); `DeploymentResponse.SecretScan` mirrors `DeploymentResponse.Scan`. CLI: `gregale deployment <id> --show-secret-scan` flag. Closes the build-step adversary pivot (`ENV SECRET=...` in a Dockerfile, `--build-arg SECRET=...` to BuildKit, `COPY .env /app/.env` in a build step) that v2 source-tree scanning couldn't reach — v2 PR #873 covered the source-tree upload path; PR-A covers the post-build image path.

### 4.7 `meterd` — metering and billing

**Owns:** usage truth. Sampling → aggregation → quota state → Paddle (production billing provider; ADR-032 v2).

- Sample loop (1 s): for each RUNNING instance read cgroup `memory.current` (host truth, includes VMM) → accumulate `mb_seconds`. Flush per-minute rows: `usage_minutes(account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes)`. `cpu_usec` is the cumulative host-cgroup CPU-µs delta (ADR-039, additive on `(instance_id, minute)`); `tx_bytes` is the cumulative HTTP response bytes the gateway forwarded for this instance in this minute (ADR-046, additive); `net_tx_bytes` is the cumulative byte delta on root-side `vethHost.rx_bytes` for this instance in this minute (ADR-046, additive). The first three columns are billable-floor or telemetry; `tx_bytes` and `net_tx_bytes` are telemetry only — no provider push in this PR. On the streaming path (ADR-047), `tx_bytes` is attributed per-flush via `statusRecorder.doFlush`'s `onFlush` callback (the residual after the last flush boundary is captured at `finalFlush` so a response that ends mid-chunk is not silently lost).
- GB-RAM-hour = `Σ mb_seconds / 1024 / 3600`, computed on **plan RAM size + 8 MB overhead**, not sampled RSS, for billing (predictable for customers; matches the financial model's math). Samples are kept for capacity telemetry.
- Quota ladder per account per month: 0–100 % of included GB-h: nothing; 100 %: email; Free tier at 100 %: hard stop (park, don't wake, `402` page); paid tiers: overage accrues at €0.01/GB-h, pushed to Paddle as usage records hourly via the adapter interface.
- Paddle objects: Product per plan; monthly Price; one metered Price (`gb_ram_hour`); customer + subscription per account; webhooks consumed: `transaction.paid`, `transaction.payment_failed`, `subscription.created`, `subscription.updated`, `subscription.canceled`.
- Dunning: `payment_failed` → account `past_due` (apps run, deploys blocked) → 7 days → `suspended` (apps parked, wake returns `402` page) → 21 days → `deleted_pending` (30-day snapshot retention, then GC). All transitions emailed.

#### 4.7.1 Operator credits + per-account overage cap (issue #279)

Two operator-side addenda on top of the §4.7 quota ladder:

- **`POST /v1/admin/accounts/{id}/credits`** (admin scope + `FAAS_ADMIN_EMAILS` allowlist, idempotent via the `Idempotency-Key` header) inserts one row in `account_credits` and one immutable row in `credit_ledger`, then emits a `credit.issued` audit event. Reason is `3..500` chars (CHECK at the column + handler 400). Migration `00054_account_credits.sql` adds both tables with `ON DELETE CASCADE` on `account_id` so GDPR delete is a single transaction (slot walked past 00050 → 00051 → 00053 → 00054 to dodge collisions with PR #325 / #322 / #341 / #340; memory: `migration-slot-collisions-across-prs.md`). Credits are surfaced in `usageSummary` as `credits_remaining_cents` but are **not** applied at the per-minute derivation.
- **`POST /v1/invoices/{id}/consume-credits`** (issue #279 PR-C; admin scope + `FAAS_ADMIN_EMAILS` allowlist + MFA-gated, idempotent via the `Idempotency-Key` header) is the operator trigger for the credit consumption reducer. The reducer reads `usage_minutes.mb_seconds` for the invoice's billing period `[period_start, period_end)`, converts to integer cents via `floor(mb_seconds × 100 / 3600)` at `OverageMillicentsPerGBHour` (€0.01/GB-h, floored; same formula as `pgstore.CurrentMonthOverageCents`), and drains the account's active credits FIFO (`created_at ASC`) against the overage. `cents_remaining` never goes negative: each credit decrement is `UPDATE … WHERE cents_remaining >= $amt RETURNING cents_remaining`, conditional inside a single PgStore transaction. The `consume_credits` loop is the same reducer that a future `meterd` cron and the PR-B `UpsertInvoice` webhook Tx both call — the HTTP endpoint is just one trigger. Idempotent under webhook redelivery via the `UNIQUE (provider_invoice_id, credit_id) WHERE provider_invoice_id IS NOT NULL` partial index on `credit_ledger` (migration `00058_credit_consumption.sql`); a second reducer call for the same `provider_invoice_id` returns `already_consumed_for_invoice=true` with the same `consumed_cents` and writes no new ledger rows. Migration `00058` adds the `provider_invoice_id text` column and the partial unique index; issuance rows (today's only writer) leave `provider_invoice_id` NULL so the partial constraint does not apply.
- **`accounts.overage_cap_cents`** (nullable `bigint`, optional per-account ceiling; zero is a valid cap) is read by `meterd`'s quota tick once per account per cycle. The tick computes the current-month derived overage (`SUM(usage_minutes.mb_seconds) since date_trunc('month', now()) at UTC`) and skips the overage-row insert when `month_cents >= cap_cents`. The Free hard-stop / paid warning ladder is unchanged — the cap is layered on top of in-budget usage. Per-hit increments of `meterd_billing_cap_exceeded_total{plan=…}` (cardinality 4) provide the alert signal. **Race scope**: meterd is the sole writer to `usage_minutes` today (spec §6.1), so the cap check + overage-row insert is serialised by the single-writer invariant. A future meterd-replica deploy would need a per-account `SELECT … FOR UPDATE` on `accounts` around the cap check + insert decision — that locking is the obvious follow-up; until then, the worst-case is one minute's overage-row insertion past the cap.
- **Provider refund seam**: `billing.Provider.Refund(ctx, chargeID, amountCents)` is on the interface. The operator route `POST /v1/admin/accounts/{id}/refunds` currently supports Polar order IDs, binds the provider order to a paid local invoice, requires MFA for session principals plus the admin allowlist, and forwards the explicit `Idempotency-Key` through the provider context. Stripe and Paddle retain their adapter implementations for compatibility deployments; a provider without `CapRefund` returns 501. Provider refund webhooks remain observational and emit a `refund.processed` audit row after verification.

#### 4.7.2 Post-response tail metering (issue #667 / ADR-078 — informational only)

The `ctx.waitUntil(promise)` primitive (§4.9.2, ADR-078) lets a
handler register background work that runs AFTER the response
flushes. The wake stays `StateRunning` for the duration of the
tail drain, metered via the existing `mb_seconds` mechanism. The
**tail drain itself** is metered as `tail_seconds` — and is
**informational only**. This sub-section pins the load-bearing
invariant up front: `tail_seconds` does NOT enter
`Math.GBHours`, `Provider.PushUsageRecord`, or any billing path.

**Runner-side tail host (issue #667 follow-up, ADR-078 amendment).**
The runner process (long-lived HTTP host inside the VM, parent of
the handler subprocess) reads the JSONL pipe at
`envelope.TailPipePath`, spawns a `sync.WaitGroup` of per-task
goroutines each wrapped in
`context.WithTimeout(env.WaitUntilSec * time.Second)`, and on each
terminal outcome (completed / failed / timeout / panicked) writes
a line to the unix-domain proxy at
`/run/guest-init/tail-events.sock`. The proxy frames the 16-byte
0x04 DGRAM and ships it via vsock to `VMADDR_CID_HOST:1027`.
The response envelope is written to stdout AFTER the tail host's
`Drain()` returns — `signal.SignalReady(...)` fires after the tail
drain (not on the first non-5xx response as the original ADR
described; see ADR-078 §"Amendment — framework_ready timing").
The 5 s `snapshotAndPark` watchdog is the hard ceiling: if the
tail host hangs for any reason, the park gate fires
`tail_failed{reason=forced_at_park}` and force-parks the wake.

- **Schema:** `usage_minutes.tail_seconds bigint NOT NULL DEFAULT 0`
  (migration `00151_wait_until_tail.sql`). Additive merge via the
  extended `state.Store.AppendUsage` signature — the new
  `tailSeconds int64` parameter follows the same shape as
  `cpu_usec` / `tx_bytes` / `net_tx_bytes` (first-write-wins for
  the billable columns, additive for the sampled columns).
- **Source:** `pkg/fcvm.Manager.ReadAndResetTailSeconds` reads the
  per-instance accumulator atomically each `SampleAndRoll` tick
  (the swap-and-reset shape is identical to `ReadAndResetEgress`
  on the same struct). The accum is fed by
  `Manager.MarkInstanceTailTerminal`, which converts the 0x04
  DGRAM's `elapsed_ms` to seconds and adds it to the running
  per-instance total.
- **Pusher:** `pkg/meter/pusher.go::PushHour` reads
  `state.Usage.MBSeconds` only and forwards the integer to
  `Provider.PushUsageRecord(ctx, acct, hour, mbSeconds)`. The
  pusher does NOT inspect `TailSeconds`. The permanent guard test
  `pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds`
  pins this contract — removing it requires removing ADR-078
  §"Tail is informational" (a new ADR).
- **Dashboards:** the §12 tail-watchdog panel reads
  `vmmd_guest_tail_seconds{plan, runtime, outcome}` (60 series),
  `vmmd_guest_tail_failed_total{plan, reason}` (16 series), and
  `vmmd_tail_cap_reached_total{plan}` (4 series) — all closed-set
  labels pre-instantiated at boot. The customer-facing billing
  dashboard is unchanged: `tail_seconds` is not displayed in the
  customer's invoice, and the financial model (`§1` of the
  spreadsheet) does not include it as a line item.
- **Reversing the invariant** is a deliberate, ADR-bounded
  decision. Any change that adds `tail_seconds` to the billing
  pipeline MUST remove the guard test, update ADR-078 §"Tail is
  informational", and update the financial model. The shape of
  those three touch-points together is the load-bearing piece —
  removing any one of them in isolation fails CI.

### 4.8 `guest-init` — PID 1 inside every microVM

Tiny static Go binary (< 5 MB), injected by imaged.

Boot path: mount `proc`/`sys`/`tmp` → bring up `eth0` (always 10.0.0.2/30, gw 10.0.0.1 — identical in every VM, ADR-009) → apply `/etc/faas/app.json` env → exec app as uid 1000 (`app`) → supervise (restart ≤ 3, then exit VM).
Readiness: TCP accept on `:8080` (or optional `GET /healthz` if declared).
Resume path (post-restore, triggered by host signal via vsock): re-seed `/dev/urandom` from virtio-rng, step clock via `ptp_kvm`/chrony `makestep` (restored guests wake with stale clocks and duplicate RNG state otherwise — both are known snapshot-restore hazards), then re-arm readiness.

### 4.9 Function runners

`runner-node22`, `runner-node24`, `runner-python312`, `runner-python313`, `runner-go124`, `runner-go124-alpine`: a 15-line HTTP host on `:8080` that loads the customer handler and adapts request/response.

Contract (identical across languages): handler receives `{method, path, headers, query, body_b64}`; returns `{status, headers, body_b64}` or a plain body. Node: `export default async function handler(req)`. Python: `def handler(request) -> Response | dict | str`.
Streaming, websockets: not in v1 for functions (fine for Apps — `gatewayd-internal` proxies them transparently).
Adding a runtime is a 7-layer procedure (migrations, schema, apid handler whitelist, openapi enums, runner shim, imaged handler surfaces, base Dockerfile + auto-stage wiring) — see ADR-052 for the canonical touch-list. The worked example for `node24` and `python313` is Tier 1 PR 1 + PR 2.

#### 4.9.1 Per-VM concurrency model (issue #559)

The platform advertises a per-VM concurrency bound (the `concurrency_per_vm` field on `GET /v1/apps/{slug}`, mirrored in `pkg/api/limits.go::ConcurrencyPerVMBound`): **Free 1, Hobby 5, Pro 25, Scale 80**. This is an upper bound the *platform* publishes for a single VM and is **distinct from** the per-app instance cap (`max_concurrency`, spec §6.2-1) — a customer's per-app instance cap is the count of live VMs the schedd will admit, not the request fan-out one VM serves.

The runner's HTTP listener (`http.ListenAndServe` on `:8080`) dispatches each accepted connection on its own goroutine, so the bound is reachable at the listener layer for any runner — Go's `net/http` does not serialize. Whether the customer's **handler process** achieves the bound depends on the runtime:

- **Node.js (single-event-loop)** — a Node handler achieves the bound; the event loop serves concurrent requests within one process.
- **Python asyncio** — an `async def handler` achieves the bound.
- **Go `net/http`** — a Go handler achieves the bound.
- **Synchronous subprocess-per-request** (e.g. a Python `read-stdin → write-stdout` script) — does NOT achieve the bound; one subprocess handles one request at a time regardless of the listener's goroutine count.

All five current runners spawn one subprocess per request via `cmd.Run()` in the runner's `invokeHandler`. The platform's `concurrency_per_vm` bound is the listener's goroutine fan-out, not a single process's request queue — so a customer's runner/process choice bounds the effective concurrency below `concurrency_per_vm`, not at it. A future runner that pre-warms a long-lived interpreter pool could close the gap; today's runners do not.

The runner-level concurrency claim is pinned by `guest/runners/internal/runnerparity/concurrency_test.go::TestRunner_HandleIsConcurrent` — 20 parallel GETs against a slow handler complete in ~1× handler-sleep, not 20× (the serialized floor). A regression to single-threaded accept would surface there.

#### 4.9.2 Post-response tail primitive — `ctx.waitUntil(promise)` (issue #667 / ADR-078)

The handler may register background work that runs AFTER the
response has flushed to the gateway. The wake stays
`StateRunning` (and therefore metered via the existing
`mb_seconds` mechanism) until every registered task completes or
a per-plan timeout fires. Mirrors Cloudflare Workers'
[`ctx.waitUntil`](https://developers.cloudflare.com/workers/runtime-apis/context/#waituntil),
Vercel Edge's `waitUntil`, and AWS Lambda's `lambda.runtimeContext
.waitUntil` — the single most-asked-for primitive on the function
surface.

- **Envelope extension:** `{wait_until_sec int, tail_pipe_path string}`
  on the request envelope (every runner reads it; default 0/empty
  = no tail). The handler subprocess appends one JSONL line per
  `waitUntil(promise)` registration to `tail_pipe_path`; the
  runner's tail host reads the pipe and starts a `sync.WaitGroup`
  of per-task `context.WithTimeout(wait_until_sec)` goroutines.
- **Per-plan timeout matrix (`pkg/api/limits.go`):** Free 5 s,
  Hobby 15 s, Pro 30 s, Scale 60 s (`TailTimeoutS`). The
  `Plan.TailTimeoutSeconds()` accessor clamps non-zero values to
  the structural floor (`TailTimeoutFloorSeconds = 5`).
- **Structural cap:** `TailCapMax = 16` (uniform across plans,
  hard-coded). The per-plan `ConcurrentTailsPerInstance` controls
  how aggressive the cap is across concurrent requests: Free 4,
  Hobby 16, Pro 64, Scale 256. A customer hitting
  `ConcurrentTailsPerInstance + 1` increments
  `vmmd_tail_cap_reached_total{plan=…}` (cardinality 4).
- **Wire (runner → guest-init):** 16-byte DGRAM on vsock port
  1027 lead byte `0x04` — `[type 0x04][outcome uint8][reserved 6B
  zero][elapsed_ms BE uint64]`. Outcome ∈ `{1 completed, 2 failed,
  3 timeout}`. Instance identity resolved from the DGRAM peer CID
  on the guest-init receiver, NOT carried in the wire.
- **Host fan-in:** guest-init forwards verbatim to vmmd on port
  1026 via the existing `SendStatelessAdvisory` path
  (`pkg/fcvm/manager.go:183`). vmmd hands the DGRAM to schedd via
  `Manager.MarkInstanceTailTerminal(ctx, instanceID, outcome,
  elapsedMs)` which decrements `instances.tail_count` and stamps
  `usage_minutes.tail_seconds += (elapsed_ms + 999) / 1000`.
- **Reaper gate (issue #667 / ADR-078 §"Reaper gate"):** an
  instance with `tail_count > 0` is NOT idle-eligible and NOT
  aggressive-eligible (mirrors the G7 `OpenConns > 0` precedent in
  `pkg/sched/reaper.go`). RAM-pressure evictions
  (`SelectEvictions`) are unchanged per the same precedent —
  tearing down a tailing instance is fine, the 5 s watchdog in
  `snapshotAndPark` is the safety valve.
- **Park watchdog:** `ParkTailDrainTimeoutSeconds = 5` in
  `pkg/sched/engine.go::snapshotAndPark`. On watchdog fire,
  force-park with audit reason `tail_count_force_park` and emit
  `wake.tail_failed{reason=forced_at_park}` for any unfinished
  tails.
- **Billing:** the tail drain itself is metered as
  `usage_minutes.tail_seconds` — **informational only** (see
  §4.7.2). Customers pay exactly the plan RAM × resident-seconds
  the synchronous request envelope covered; tail drains are a
  free operational primitive.

---

### 4.10 Triggers and event-source mappings (issue #757 / ADR-100)

The unified Trigger primitive replaces six unrelated invocation surfaces with one resource + one batch envelope + one FSM.

#### Resource model

One row per customer surface in the `triggers` table (migrations/00267_triggers.sql). The discriminator is `kind`:

| Kind | Backend | Library / surface |
|------|---------|-------------------|
| `cron` | robfig/cron/v3 schedule parser | pre-existing `crons` table; this kind pins the row via `cron_id` |
| `kafka` | consumer group | segmentio/kafka-go (commit #10) |
| `nats` | JetStream durable consumer | nats.go/jetstream (commit #9) |
| `redis_streams` | XReadGroup + XAck | redis/go-redis/v9 (commit #11) |
| `sqs_compat` | long-poll HTTP queue | stdlib net/http (commit #12) |
| `queue` | in-platform unified queue | pgxpool + `invocations` rows where source IN ('queue','delayed_task') (commit #8) |

#### Per-kind config schema

Stored as `config jsonb` on the trigger row. Decoded lazily per kind via `pkg/sched/poller_*.go::decodeXConfig`:

- `kafka`:        `{"brokers":[...], "topic":"...", "group":"..."}`
- `nats`:         `{"url":"nats://...", "stream":"...", "subject":"...", "durable":"..."}`
- `redis_streams`: `{"addr":"redis:6379", "stream":"...", "group":"..."}`
- `sqs_compat`:   `{"queue_url":"http://...", "long_poll_secs":20}`
- `queue`:        `{"mode":"queue|delayed_task"}` (source discriminator)

#### Batch semantics

Every non-cron kind shares the same envelope:

- `batch_size_max`   1..5000 (per-plan cap; Free 0, Hobby 50, Pro 500, Scale 5000)
- `batch_window_ms`  10..600000 (per-plan cap; Free 0, Hobby 30000, Pro 300000, Scale 300000)
- `max_attempts`     1..25 (per-plan cap; Free 0, Hobby 3, Pro 10, Scale 25)
- 6MB payload cap (Lambda's hard cap; fixed)

A batch closes on ANY of: `len == batch_size_max`, `now >= window_deadline`, `Σ(payload_bytes) >= 6MB`.

#### Wire envelope

`POST /v1/invocations:dispatch_batch` on the gateway synth server. The function receives:

```
POST /_triggers/<kind>/<trigger_slug>
x-faas-trigger-id:   <uuid>
x-faas-trigger-kind: <kind>
x-faas-batch-size:   <N>
body_b64: <JSON-array-of-records-b64>
```

Response shape (stolen verbatim from AWS Lambda):

```json
{"batchItemFailures":[{"itemIdentifier":"..."}]}
```

Empty / missing ⇒ full success. `pkg/gateway/synth.go::parseBatchFailures` decodes the response.

#### Per-record FSM

```
pending ── claim ─▶ claimed ── succeeded
                            ── retry ─▶ retry (next_fire_at=future)
                                          └▶ attempts >= max ─▶ dead_letter
                            ── poison_record ─▶ dead_letter
```

The `trigger_records` table (migrations/00267_triggers.sql) is the ledger; the `trigger_dead_letter` table is the terminal failure store. `ClaimTriggerRecords` uses `FOR UPDATE SKIP LOCKED` so two schedd instances racing on the same trigger each get a disjoint slice.

#### Plan caps

`pkg/api/limits.go::Limits` (commit #4):

| Cap | Free | Hobby | Pro | Scale |
|-----|------|-------|-----|-------|
| TriggersAllowed | false | true | true | true |
| TriggerLimitPerApp | 0 | 2 | 10 | 50 |
| TriggerLimitPerAccount | 0 | 10 | 50 | 200 |
| TriggerBatchSizeMax | 0 | 50 | 500 | 5000 |
| TriggerBatchWindowMaxSec | 0 | 30 | 300 | 300 |
| TriggerMaxAttemptsMax | 0 | 3 | 10 | 25 |
| TriggerRecordsPerSecondPerApp | 0 | 100 | 1000 | 10000 |

#### Audit + wire events

Audit kinds (commit #15):

- `trigger.fired`         per-record: broker delivered + dispatched
- `trigger.fired.batch`   per-batch: aggregated outcome counts
- `trigger.retry`         per-record: state → retry, next_fire_at
- `trigger.dlq`           per-record: state → dead_letter

pg_notify channels (commit #16):

- `NotifyTriggerReady`    schedd wakeup (every broker-delivered record)
- `NotifyTriggerChanged`  apid → schedd + dashboard SSE (CRUD + pause/resume)

#### Broker adapter behavior

One file per broker (`pkg/sched/poller_*.go`). Each implements:

```go
type triggerSource interface {
    Kind() string
    Poll(ctx, t sqlc.Trigger) PollResult
    Ack(ctx, t, ids) error
    Nack(ctx, t, ids, reason) error
    Close() error
}
```

Ack semantics per broker: queue → no-op (rows already in `invocations`); kafka → `CommitMessages`; nats → `Msg.Ack()`; redis → `XAck`; sqs → `POST .../delete`.

Nack semantics: queue → no-op; kafka → `SetOffset` rewind; nats → `NakWithDelay(2s)`; redis → `XClaim` after 30s idle; sqs → `POST .../release`. `poison_record` becomes a broker-side drop on every broker.

#### Failure routing

| Reason | Source | Routing |
|--------|--------|---------|
| `rate_limited` | wake rate-limit gate deny | dead_letter, drop |
| `poison_record` | malformed function response | dead_letter, manual_retry |
| `max_attempts` | attempts reached per-trigger cap | dead_letter, customer_dlq |
| `broker_error` | gateway transport failure | retry → max → dead_letter |
| `payload_too_large` | record exceeds 6MB | dead_letter, drop |
| `plan_quota` | per-app / per-account cap reached | dead_letter, drop |
| `customer_disabled` | pause / delete mid-dispatch | dead_letter, drop |

#### Migration safety

- `00267_triggers.sql` is a single transaction; `invocations.source` widening uses `DROP CONSTRAINT … ADD CONSTRAINT` (PG15 has no `CREATE TRIGGER IF NOT EXISTS`, per the trigger-replay-safety precedent).
- The pg_notify trigger `trg_notify_trigger_ready` fires AFTER INSERT ON trigger_records.
- Cross-PR slot precheck before PR creation per `migration-gates-collision-and-replay.md`.

---

## 5. Data model (Postgres, authoritative excerpt)

`sqlc` against this schema; migrations via `goose`, append-only and never edited
after merge. Versions 1–590 are frozen legacy sequence; ADR-142 uses UTC
timestamp IDs for all new migrations and applies missing post-cutover IDs by
ledger membership.

```sql
create table accounts (
  id uuid primary key default gen_random_uuid(),
  email citext unique not null,
  plan text not null default 'free',            -- free|hobby|pro|scale
  status text not null default 'active',        -- active|past_due|suspended|deleted_pending
  provider_customer_id text unique,
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
  slug text unique not null,                    -- {slug}.apps.gregale.dev
  type text not null default 'app',             -- app|function
  runtime text,                                 -- node22|node24|python312|python313|go124|go124-alpine when function
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

### 5.1 Audit event taxonomy (`events.kind`)

The `events` table (DDL above) backs two emitters: **schedd** (state-transition events from `pkg/sched/engine.go`) and **apid** (the IAM-4 surface documented in [`docs/adr/035-auth-audit-events.md`](../adr/035-auth-audit-events.md)). The kind namespace is `<daemon>.<family>.<verb>`; per-daemon prefixes are bounded by ADR-035's audit-tagging convention so a dashboard filter never has to special-case.

The list below covers **all** customer-facing kinds as of PR #291. Prior kinds (`auth.*`, `account.*`, `key.*`, `secret.*`) are defined in ADR-035 and are the source of truth — duplicated here for self-containment.

| Kind | Emitted from | Data payload |
|---|---|---|
| `auth.login`, `auth.logout`, `auth.login_failed` | apid `handlers_auth.go` | per ADR-035 |
| `key.created`, `key.deleted` | apid `handlers_ext.go::createKey`, `deleteKey`, `exchangeCliAuthCode` | `{key_id, scopes}` |
| `secret.set`, `secret.deleted` | apid `handlers_secrets.go` | per ADR-035 |
| `account.plan_changed`, `account.deletion_scheduled`, `account.deletion_restored` | apid `handlers_account.go`, `handlers_ext.go::changePlan` | `{from, to}` / `{via}` |
| `app.created` | apid `handlers.go::createApp` | `{app_id, slug, type, runtime, ram_mb, max_concurrency}` |
| `app.deployed` | apid `handlers.go::createDeployment` | `{app_id, deployment_id, ref, supersedes}` — `supersedes=""` on the first deploy |
| `app.updated` | apid `handlers_ext.go::updateApp` | `{app_id, slug, old, new}` — `old`/`new` only carry the fields the caller actually patched (PR #340 per-field capture invariant) |
| `app.deleted` | apid `handlers_ext.go::deleteApp` | `{app_id, slug}` — soft delete; snapshot GC follows per §9 |
| `app.rolled_back` | apid `handlers_ext.go::rollbackApp` | `{app_id, from: deployment_id, to: deployment_id}` |
| `domain.added` | apid `handlers_ext.go::createDomain` | `{app_id, domain}` — `domain` is the canonical lowercased form stored on the row |
| `domain.removed` | apid `handlers_ext.go::deleteDomain` | `{app_id, domain}` |
| `cron.created` | apid `handlers_ext.go::createCron` | `{cron_id, app_id, schedule, path, enabled}` — only emitted on the success path; the PR #340 plan-tier gate (402) suppresses this row for Free accounts |
| `cron.updated` | apid `handlers_ext.go::updateCron` | `{cron_id, app_id, old, new}` |
| `cron.deleted` | apid `handlers_ext.go::deleteCron` | `{cron_id, app_id}` |
| `cron.fired` | schedd `pkg/sched/loop.go::dispatchOneCron` (issue #291 follow-up) | `{cron_id, app_id, schedule, path, fired_at, last_fired_at_before, status, invocation_id, instance_id}` — emitted after `MarkCronFired` regardless of synthetic Invoke outcome; `status="ok"` carries non-empty `invocation_id`+`instance_id`, `status="err"` carries empty strings (JSON shape stable so dashboards can filter without nil checks). `last_fired_at_before` is omitted on the first fire (no prior fire exists) so the key reads as `null` rather than the misleading `0001-01-01T00:00:00Z`; from fire #2 onward the key is always present. Subject is the owning account id (per-account filter grain).
| `webhook.replay_rejected` | `gatewayd-public` `audit.go`, apid `audit.go` | `{provider, delivery_id}` — emitted on the replay path (`delivery_id` re-appears within the 5-minute TTL window). Subject: customer account id from the apid Stripe/Paddle paths; `nil` from the `gatewayd-public` GitHub path (no account id at the edge). ADR-042.

**Failure semantics (ADR-035).** Audit emission is best-effort across **both** daemons. An `AppendEvent` failure logs Warn, increments the per-daemon counter (`apid_audit_write_failures_total` for apid, `schedd_audit_write_failures_total` for schedd — both helpers live in `pkg/audit.Ops`), and returns. The mutation that produced the audit emit is **never** rolled back (read the audit row as observation, not source of truth). The customer-facing list endpoint `GET /v1/audit-events?kind_prefix=<family>.` filters server-side by `events.kind` prefix; `kind_prefix=app.` returns only `app.*` rows, `kind_prefix=cron.` only `cron.*` rows including the new `cron.fired` kind, etc.

**4xx invariant.** Failed (4xx) mutations do NOT emit — the audit row is a success signal. PR #340 introduced a 402 plan-tier gate on `POST /v1/crons`; that gate fires before `s.store.CreateCronIfUnderQuota`, so a Free customer hitting POST /v1/crons gets a 402 and the `cron.created` row is never written. `cmd/apid/handlers_audit_test.go::TestAuditEvents_CronCreatedFreeReturns402DoesNotEmit` pins this invariant end-to-end.

#### 5.7 Audit events — refunds + credits (issue #279)

Two new event kinds land in the existing append-only `events` table:

- **`credit.issued`** — emitted by `apid` on `POST /v1/admin/accounts/{id}/credits`. `subject` = the beneficiary account id (the account receiving the credit). `actor` = fixed `"apid"` (cmd/apid/audit.go:24); the operator's account id rides in `data["actor"]`. `data` payload: `{credit_id, cents, actor, reason}` where `reason` is passed through `logsanitize.Field` before emission (codeql-go/log-injection sanitiser precedent, PR #119 / 969ba0a).
- **`credit.consumed`** — emitted by `apid` on `POST /v1/invoices/{id}/consume-credits` (issue #279 PR-C); future callers (`meterd` cron, PR-B `UpsertInvoice` webhook Tx) reuse the same reducer and stamp the same audit kind with their own `actor` string. `subject` = the beneficiary account id (the account whose credits were drained). `actor` = `"apid"` for the admin endpoint today. `data` payload: `{credit_id, delta_cents, invoice_id, provider_invoice_id, period_end, total_consumed_cents_for_invoice, remaining_credits_cents}`. One row per drained credit (FIFO order). Idempotent under webhook redelivery via the partial unique index on `credit_ledger (provider_invoice_id, credit_id)` — a second reducer call writes no new ledger rows and no new audit rows; the response returns `already_consumed_for_invoice=true` instead.
- **`refund.processed`** — emitted by `apid` on a verified `charge.refunded` webhook (HMAC-verified via `billing.Provider.VerifyWebhook`). `subject` = the customer account the refund applies to (Stripe `customer` → `accounts.ProviderCustomerID`). `data` payload: `{actor, actor_email, provider_refund_id, charge_id, amount_cents, currency}`. Idempotent under webhook redelivery: duplicate `charge.refunded` events produce additional `refund.processed` audit rows but never double-refund (Stripe is the source of truth on the payment-method side).

Both rows are observational — the actual state changes (Stripe `Refund`, `account_credits` insert, `credit_ledger` insert) are the source of truth.

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
     liveness probe N consecutive failures (issue #554)
                    ▼
               SNAPSHOTTING ──► PARKED
                    │ snapshot fail (disk?)
                    ▼
                 STOPPED (cold; next wake = COLD_BOOTING)     FAILED (crash-loop ≥3: park + notify)
```

**Liveness probe (issue #554 / ADR-079):** the `RUNNING →
STOPPED (reason='liveness_failed')` edge is added by the
issue #554 path. `cmd/vmmd` polls the guest via vsock 1028
STREAM (host→guest); on N consecutive non-2xx (or timeout /
conn-refused) responses `Engine.DestroyForLivenessFailure`
fires. The destroy eagerly marks the deployment's latest
snapshot stale so the next Wake cold-boots from rootfs per
ADR-005 — a wedged snapshot is never restored. After 3
restarts in 300 s the parent app flips to
`apps.status='evicted_cold'` (`Engine.ParkDeployment` with
audit kind `instances.parked_liveness_exhausted`). Per-plan
defaults: Hobby/Pro/Scale → period 5 s, consecutive 3,
cooldown 60 s, max restarts 3 in window 300 s. Free is gated
off (LivenessAllowed() returns false).

Timers: WAKING ≤ 5 s then fallback to cold boot; COLD_BOOTING ≤ 30 s then FAILED; SNAPSHOTTING ≤ 20 s then STOPPED. Every transition is an `events` row.

**Compute-node heartbeat (ADR-028):** schedd pings every active `compute_node` on a 30 s tick via `pkg/sched.Heartbeat`. The goroutine dials each row's `target_url` (Tailscale/Wireguard overlay in production; unix:///run/faas/vmmd.sock for default-local) and stamps `last_heartbeat_at = now()` on success. A row whose `last_heartbeat_at` ages past 90 s gets `active=false` via `SetComputeNodeActive`. The pg_notify `compute_node_changed` (migration 00026) fires on the UPDATE so `gatewayd-internal`'s `NodeClientCache` evicts the cached conn without polling. Re-activation is automatic on the next successful ping. Direction was chosen to invert vmmd-pushes: schedd is the admission authority and shouldn't trust inbound traffic from a box it may have already drained; outbound probing means schedd detects failure on its own clock.

### 6.2 Invariants (test these, they are the product)

1. At most `max_concurrency(plan)` instances of one app in {WAKING, COLD_BOOTING, RUNNING}. **Issue #168:** `gatewayd-internal` routes requests across the entire live set via atomic round-robin so a fan-out burst actually distributes load; `Backend.Admit` atomically refuses over-cap callers under the same `tgtMu` lock that mutates the cache.
2. Σ (ram_mb + 8) over all instances in {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING} ≤ 47,600 MB.
3. An app always has either a live snapshot or a rootfs it can cold boot — never neither.
4. A parked app consumes zero resident RAM (verify: cgroup gone).
5. Two concurrent instances restored from one snapshot never share an IP, netns, jail uid, or RNG stream. **Issue #168:** `gatewayd-internal` picks per-instance `x-faas-instance` (overwriting inbound) so the per-node vmmd forwarder attributes every byte to the correct microVM even when multiple restored siblings share one compute_node.

### 6.3 Wake latency budget (p50 targets)

| Step | ms |
|---|---|
| gatewayd-internal route + queue + schedd admission | 5 |
| netns + TAP + jailer spawn | 30 |
| snapshot load (file-backed, NVMe) + resume | 150–250 |
| guest resume hook (entropy, clock) + readiness | 40 |
| proxy first byte | 5 |
| **Total p50 / p95 target** | **≤ 350 / ≤ 800** |

Measured end-to-end as `gateway_wake_latency_seconds`. Regression gate in CI-on-metal (§14).

The schedd-side wake path is decomposed into three `schedd_wake_rpc_duration_seconds{app, phase}` histograms (ADR-097, P1B) so operators can attribute a p95 regression to a specific phase without re-running the wake under a profiler:

| schedd-side phase | Bucket range | What it covers |
|---|---|---|
| `admit_to_rpc` | 0.01–5 s | gRPC handler → `Engine.admitGate` → `NodeLedger.Admit` → placement → `vmmd` RPC start. Lock + admission + ledger + placement. |
| `rpc_call` | 0.01–5 s | vmmd `CreateFromSnapshot` / `CreateColdBoot` round trip. Cross-process boundary, the only phase that crosses a node-local socket. |
| `rpc_to_running` | 0.01–5 s | RPC return → `e.transition(ctx, ..., state.StateRunning)`. Boot-input re-read + `SetInstanceRuntime` + audit emit. |

`wake_id` is attached as a `prometheus.Exemplar` on every observation so an operator can join the histogram to `gateway_wake_latency_seconds` on the gateway side and to the `events` table — no `wake_id` label is added to the histogram (cardinality blow-up). Bucket set is spec §6.3 verbatim plus a 0.01 s low-end bucket for `admit_to_rpc`. ADR-097.

### 6.4 Failure-mode catalogue (ADR-025 v1.1, ADR-028 v1.1, ADR-029 v1.1)

This subsection is the cross-reference page for the three v1.1 ADRs. Steady-state behaviour is in §6.1–§6.3; this section maps the edge cases an operator will see in multi-node (ADR-025) routing. The intent is *one* page an on-call can scan when paged — not a redesign.

| Failure | Detection | Behaviour today | Reference |
|---|---|---|---|
| `default-local` row hard-deleted via admin | `DELETE /v1/compute-nodes/default-local?hard=1` returns 409 `default_local_protected` | Refused at the handler; migration 00024's backfill references still hold | ADR-029 v1.1 |
| Operator UPSERTs typo'd `target_url` | Next `POST /v1/apps/{slug}/logs` 502 (no gRPC client resolves); `pg_notify compute_node_changed` evicts the cached conn | Loud-fail at the next wake; admin dashboard or `apid_logs_emitted_total` reveals the misconfig | ADR-029 v1.1 |
| Remote vmmd `Heartbeat` stalls > 90 s | schedd's `pkg/sched.Heartbeat` watchdog flips `active=false`; `compute_node_changed` fires | Placement skips the node; `gatewayd-internal` evicts the conn; in-flight wake falls back to a different node via `ChoosePlacement` | ADR-028 v1.1, ADR-025 v1.1 |
| gatewayd-internal↔vmmd overlay partition (Tailscale/Wireguard) | `Heartbeat` misses; same as above | Acceptable v1.0 tail; tighter alerting via `gateway_wake_latency_seconds` + `wake_locality_host_total` | ADR-028 v1.1 |
| Per-instance bridge (socat-equivalent) fails | vmmd logs raw stderr; bridge script's `ip netns exec` returns non-zero | Inbound request 502; trace ends at vmmd (no client-side leak of internal paths) | ADR-028 v1.1 |
| `pkg/wire.DialContext` returns `codes.Unavailable` (TLS handshake fail) | mTLS handshake refuses; admission rejects | Schedd admission fails closed — no instance restored; customer sees a 503 from `POST /v1/apps/{slug}/wake` | ADR-025 v1.1 |
| `pkg/storage.PrefixRouter` chooses wrong backend (misconfigured `storage.local_prefix`) | `imaged` `publishBaseExt4` writes to local disk instead of OCI | Loud-fail on first wake of a remote node (the OCI pull returns 404); surfaced via `pkg/storage/router.go` resolution log | ADR-025 v1.1 |
| Schedd picks a drained node (race vs watchdog) | `ChoosePlacement` ranks by `HeadroomMB`; a node whose `active=false` has 0 headroom | Picker ranks it last; if no other node fits, request 503 | ADR-025 v1.1, ADR-028 v1.1 |
| Concurrent Admin-allowlist rotation (`FAAS_ADMIN_EMAILS` change) | apid reads at startup; in-process only | Empty allowlist ⇒ all admin routes 403 `admin_required`. No reload path in v1.0. | ADR-029 v1.1 |
| `pkg/gateway.TargetSet` admits an instance whose `NodeID == ""` (regression guard) | `targetSet.Add` returns error; pgbackend tests cover | Refused at the atomic update; `gatewayd-internal` logs the rejection | ADR-028 v1.1, §6.2 |
| WakeResponse wire shape reverts (someone re-adds `.addr`) | `pkg/scheddgrpc` proto compile fails; `grep -rn "\.addr" pkg/ cmd/` sweep | Proto compilation is the regression gate; pre-#199 clients fail at unmarshal | ADR-025 v1.1, ADR-028 v1.1, issue #168 |
| `migration 00024` `default-local` `47600` literal backfilled changed | Admission ceiling would change | Anti-goal: do not touch the literal. Re-backfill requires a new ADR | ADR-025 v1.1 |
| VM wedged (busy-loop / leaked FD / deadlocked runner), liveness probe fails N consecutive | `RUNNING → STOPPED` | 3 consecutive non-2xx / timeout / conn-refused on vsock 1028 STREAM → `Engine.DestroyForLivenessFailure` eagerly marks snapshot stale → next Wake cold-boots (per ADR-005). 3 destroys in 300 s → `Engine.ParkDeployment` flips parent app to `apps.status='evicted_cold'` + audit kind `instances.parked_liveness_exhausted`. Idle timer resets on destroy. | ADR-079, issue #554 |
| App did not bind to `$PORT` (no listener on readiness probe) | `pkg/fcvm/vmm.go::waitReady` detects `ECONNREFUSED` over the readiness window | RFC 7807 `app_not_listening` 422 stamped via `SetDeploymentFailedEx`; CLI renders the 5-line shape from `pkg/whycopy` row `app_not_listening` (hint/why/fix/relevant_logs) | error-explanations cluster, amendment 1 |
| App bound to `127.0.0.1` (loopback) instead of `0.0.0.0` | `pkg/fcvm/vmm.go::waitCharacterization` reads `listening_addrs` from the WakeCharacterizationReport | RFC 7807 `app_loopback_bound` 422 — the per-VM bridge forwards to `10.0.0.2` (ADR-009), so loopback-only binds never receive gateway traffic. CLI hints to bind `0.0.0.0` | error-explanations cluster, amendment 1 |
| Binary tarball targets a non-linux/amd64 architecture | `pkg/builderd/vm_metal.go::classifyBuildFailure` detects `ENOEXEC` from the kernel + `pkg` discriminator | RFC 7807 `app_arch_mismatch` 422 with `pkg` (npm/pip/go/cargo/etc.) on the wire. CLI recommends `GOOS=linux GOARCH=amd64 go build` for Go, `cargo build --target x86_64-unknown-linux-gnu` for Rust | error-explanations cluster, amendment 1 |
| Source references `$ENV_VAR` not declared in the app's env config | `pkg/reposcan/scan.go` + `cmd/gregale/commands_doctor.go::doctorCheckEnvRequired` preflight | RFC 7807 `env_var_missing` 422 — the runtime would crash on first access; preflight surfaces the source-side signal via `gregale doctor` so the customer fixes it before deploy | error-explanations cluster, amendment 1 |
| `/healthz` returns 401 (or 403) | `cmd/vmmd/liveness_recv.go` discriminates 401/403 from the generic `liveness_non_200` path | RFC 7807 `app_healthz_unauthorized` 422 — after 3 consecutive 401s, the engine marks the deployment failed because we can't distinguish "the app is up but the healthz path is gated" from "the app is down". CLI hints to expose `/healthz` without auth or use `healthcheck_path` to point at a public route | error-explanations cluster, amendment 1 |
| Container OOM (cgroup `memory.events` OOM kill on the workload) | `guest/init/cgroup_partition_linux.go` listens on `cgroup.events`; vsock msg_type=3 surfaces the kill | RFC 7807 `app_runtime_oom` 422 with the plan's RAM cap in the prose. CLI hints to upgrade plan or trim in-memory state | error-explanations cluster, amendment 1 |
| Dependency install step (npm/pip/go mod/cargo) exited non-zero | `pkg/builderd/vm_metal.go::classifyBuildFailure` extended with the `pkg` discriminator | RFC 7807 `dep_install_failed` 422 with `pkg` (npm/pip/go/cargo) on the wire. CLI recommends `npm install` locally to reproduce | error-explanations cluster, amendment 1 |
| App boot timeout (readiness probe waited the full `startup_timeout_s` and `/healthz` never returned 200) | `pkg/sched/engine.go::KillStuck` carries `StuckReason` into the typed Problem | RFC 7807 `app_startup_timeout` 422 distinct from idle timeout (which parks). CLI hints to increase `startup_timeout_s` or defer boot work until after the `/healthz` listener is up | error-explanations cluster, amendment 1 |
| Tarball or base image is a stateful shape (Dockerfile `VOLUME`, top-level `data/` / `db/` / `var/`, or a stateful base image) | `pkg/oci` + G13 stateless-only gate | RFC 7807 `stateless_only_violation` 422 (already shipped pre-cluster). Now flows through the same 5-line renderer from `pkg/whycopy` row `stateless_only_violation` so the prose is uniform with the new codes | error-explanations cluster, amendment 1 |

**Operator runbook pointers:**

- Per-component verification scripts live in `docs/runbooks/multi-host-rollout.md` (Phase D of the Tier 2 plan, issue #297 — **TBD**, not yet written) and `docs/runbooks/gate-a.md` (G.1, Gate-A active-passive adoption).
- Admin surface row-by-row CRUD: `apid GET/POST/DELETE /v1/compute-nodes` (ADR-029 v1.1).
- Cross-box gRPC dial: `pkg/wire.DialContext` (ADR-025 axis 1).

### 6.4.1 Explanation catalog (error-explanations cluster, amendment 1)

The 9 new rows above all flow through a single static catalog at
`pkg/whycopy/` so the customer-facing prose is reviewable in one
place, table-driven tested, and tripwire-protected. Each row
maps one RFC 7807 stable `Code…` to:

- **Title** — overrides `Problem.Title` when non-empty
- **Hint** — single short next-action line (≤200 bytes)
- **Why** — cause with the observed value templated in (≤512 bytes)
- **Fix** — prescriptive remediation, 1-3 lines (bullets separated by `\n`)
- **DocsURL** — overrides `Problem.DocsURL` when set
- **Observed** — optional per-code renderer that templates the observed value into Why/Fix (e.g. `app_not_listening` lifts the actual port the probe dialed)

The catalog is the single source of truth for customer-facing
prose. Detection sites call `whycopy.Decorate(p, code, observed)`
after the constructor so the wire `Problem` carries the full
Hint/Why/Fix/RelevantLogs block on every code path.

**Persistence:** the same prose is stamped onto the
`deployments` row alongside `error_code` (`migrations/00290`,
4 new columns: `error_hint`, `error_why`, `error_fix`,
`error_relevant_logs jsonb`). Post-mortem retrieval via
`gregale inspect <slug> --errors` lifts the persisted prose
without re-running the build.

**CLI surfaces:**

- `cmd/gregale/commands.go::renderAPIError` renders the
  5-line shape (Title / Detail / Hint / Why / Fix / RelevantLogs
  / DocsURL). Legacy 3-line shape preserved when the cluster
  didn't stamp any of the new fields.
- `cmd/gregale/commands_doctor.go` — `gregale doctor [path]`
  customer preflight that scans the cwd for the source-side
  failure modes (loopback-bind, env-var, arch, stateless shape).
- `cmd/gregale/pack.go::runPackPreflight` — warn-only preflight
  run during `gregale deploy`; surfaces PORT unset + loopback
  bind + arch mismatch hints after the deploy summary.
- `cmd/gregale/commands2.go::runLogs --explain` — 4-line
  summary on stream end (last error, level counts, top patterns).
- `cmd/gregale/commands_inspect_errors.go` — `gregale inspect
  <slug> --errors` post-mortem leaf that lifts the persisted
  prose from the latest failed deployment.

**Tripwires:**

- `cmd/gregale/lint_tripwires_test.go::TestEveryCodeHasWhycopyEntry` —
  every `Code…` in `pkg/api/errors.go` must have a matching row
  in `pkg/whycopy`. Build fails on missing rows.
- `pkg/whycopy/whycopy_test.go::TestDecorate_AllCodesHaveProse` —
  every catalog row must have non-empty Title/Hint/Fix (and
  Hint ≤200 bytes, Why ≤512 bytes).
- `cmd/gregale/lint_tripwires_test.go::TestLintTripwire_NoGlyphLiteralOutsideOutput` —
  every customer-facing glyph (`✓ ✗ → ! — 💡 ┌─ │ └─`) is
  centralised in `cmd/gregale/output.go`; no other file may
  use them verbatim.
- `cmd/gregale/lint_tripwires_test.go::TestLintTripwire_NoLiteralDocsDomainEverywhere` —
  every docs URL routes through `wire.DocsHost`.

### 6.4.2 Explanation surfaces (error-explanations cluster, amendment 2 — Cluster A)

Amendment 1 (§6.4.1) closed detection → wire → DB → CLI render. Amendment 2
closes two remaining seams so the cluster is end-to-end visible:

1. **Dashboard rendering of the persisted prose.** The deployment-detail
   page (`pkg/dashboard/templates/deployment_detail.html`) now renders
   `ErrorCode / ErrorHint / ErrorWhy / ErrorFix / ErrorRelevantLogs` as
   a conditional `.error-explanation` section. The section is gated on
   `{{ if .Data.Deployment.ErrorCode }}` so legacy pre-amendment-1 rows
   render unchanged (no `.error-explanation` element when the code is
   empty). The 5 fields are projected from the `state.Deployment` row
   via `cmd/apid/handlers_dashboard.go::dashboardDeploymentItem`. Test
   fixtures: `pkg/dashboard/dashboard_test.go::TestRender_DeploymentDetail_StatelessViolation`
   (asserts the 5 prose blocks render) and `…_LegacyRowRenders` (asserts
   the conditional section is absent on legacy rows).
2. **Pre-upload doctor gate.** `gregale deploy --doctor-strict` runs
   `runDoctorChecks(cwd)` BEFORE any HTTP / pack. Error-class findings
   (currently `stateless_only_violation` via the top-level `data/`
   discriminator) exit 1 with the doctor report printed to stderr; the
   upload never starts, so a customer who runs the flag from CI gets
   the same prose locally that the server would have 422'd on. Warnings
   render + continue (mirrors the standalone `gregale doctor` semantics).
   Scope: `--doctor-strict` (not `--strict` — already taken by the
   `--diff` deploy-diff cluster). Flag name is guarded by
   `cmd/gregale/lint_tripwires_test.go::TestLintTripwire_DoctorStrictMutex`,
   which fails on any new unscoped `--strict` Bool/String declaration
   outside the two documented call sites.

`app_healthz_unauthorized` forward-compat (amendment 2, sub-task 1):
the guest-init → vmmd probe wire now carries a `WWWAuthenticate` field
on `livenessResp` / `livenessResponseBody` (omitempty JSON). Today's
discriminator is closed-set (any 401/403 → `livenessOutcomeUnauthorized`),
but the new field lets a future platform-side probe auth PR read the
realm without another wire-shape bump. Closed-set comment drift fix
on `pkg/fcvm/metrics.go:324` and `cmd/vmmd/liveness_recv.go:67-87`
adds the 6th outcome to the listed set. New unit tests in
`cmd/vmmd/liveness_recv_test.go` cover `livenessOutcomeUnauthorized`
+ `livenessOutcomeUnauthorized` reset-on-success + the 403 arm.

### 6.4.3 Runtime OOM detection (error-explanations cluster, amendment 3 — Cluster C)

Amendment 1 (§6.4.1) shipped the catalog + DB schema + CLI renderer.
Amendment 2 (§6.4.2, Cluster A) closed the dashboard and `--doctor-strict`
seams. **Amendment 3 (Cluster C) wires the detection seam** for
`app_runtime_oom` — the only gap remaining in the audit.

**Detection locus.** Inside the guest, on the per-workload cgroup v2
leaf (`main-app` leaf, `partitionInto`'s write at
`guest/init/cgroup_partition_linux.go:135`). The host-side per-VM scope
(`vmmd/writePlanCgroup`) sees only the firecracker process; the host
*cannot* see per-PID OOM kills inside the VM. The only detection
surface is the guest's cgroup v2 memory controller on the workload leaf.

**Wire envelope** — added to the existing closed-set dispatcher on
framework_ready port 1027 DGRAM:

```
[1B type=0x05][json:{"peak_mb":N,"plan_mb":N}]
```

The body is a UTF-8 JSON envelope mirroring the host-side
`workloadOOMWire` (cmd/vmmd/framework_ready_recv.go). Body cap:
`VsockWorkloadOOMMaxBody = 256` bytes (the payload is < 32 bytes;
256 is a future-proof margin mirrored on the guest side at
`workloadOOMEmitMaxBody`).

The host resolves instance identity from the DGRAM peer CID (same join
as types 0x01..0x04). The new closed-set member is 0x05, owned by the
existing dispatcher — no new vsock port, no new listener. Future event
classes (0x06+) require a byte + a switch case + a test in lockstep;
the tripwire is `cmd/vmmd/framework_ready_recv.go::TestParseFramework
ReadyDatagram_TypeClosedSet`.

**Producer chain** (end-to-end):

```
in-VM workload PIDs (cgroup v2 leaf, memory.max = plan + 8 MB)
   ↓ poll(2) on leaf's cgroup.events + memory.events oom_kill delta
guest/init::WatchOOM(ctx, leaf, planMB, emit, log)         ← NEW
   ↓ samples memory.events.high (or memory.current fallback)
guest-init::EmitWorkloadOOM(ctx, peakMB, planMB)           ← NEW (vsock DGRAM 0x05)
cmd/vmmd::framework_ready_recv.go::dispatchWorkloadOOM
   ↓
pkg/fcvm::Manager.ReportWorkloadOOM(instanceID, peakMB, planMB)  ← NEW
   ↓ via WithWorkloadOOMSink (mirrors WithLivenessSink)
cmd/vmmd/main.go closure → cli.ReportWorkloadOOM gRPC
   ↓
pkg/scheddgrpc/server.go::ReportWorkloadOOM                 ← NEW (RPC + handler)
   ↓
pkg/sched/engine.go::DestroyForWorkloadOOMFailure(ctx, instanceID, peakMB, planMB)  ← NEW
   ↓
whycopy.Decorate(problem, CodeAppRuntimeOOM, struct{PeakMB, PlanMB int}{...})
   ↓
store.SetDeploymentFailedEx(deploymentID, CodeAppRuntimeOOM, ...problem.Hint,
                            problem.why, problem.Fix, nil)
   ↓
dashboard .error-explanation  +  CLI inspect --errors  +  gregale doctor
```

**Whycopy Observed** — the `CodeAppRuntimeOOM` catalog row ships an
`Observed func(observed any) (why, fix string)` closure that templates
the (peakMB, planMB) tuple into the customer-facing prose:

- **Why:** "the cgroup memory controller killed the process at
  `<peak>` MB (plan cap `<plan>` MB + 8 MB overhead); the kernel
  OOM-killer fired inside the microVM"
- **Fix:** "• upgrade from a `<plan>` MB plan to a plan with ≥
  `<peak+8>` MB of RAM\n• trim in-memory state (caches, buffers,
  large request bodies held in memory)"

The static `Why` / `Fix` strings remain the fallback when `Decorate`
is called with `observed=nil` (no templating — the existing 6 customer
fixtures that never set the observed value still see the right prose).

The cross-reference line "if this is a build step, see
/errors/build/limits#memory instead" was dropped — the build-OOM path
is `CodeBuildOOM`, not `CodeAppRuntimeOOM`; the cross-reference was
misleading because the two constructors are distinct.

**Tripwires** stay green: `TestParseFrameworkReadyDatagram_TypeClosedSet`
(closed-set guard), `TestEveryCodeHasWhycopyEntry` (Observed closure
presence), `TestObservedRendering` (template parity). The audit moves
from 9/9 covering-of-named-codes → 9/9 with end-to-end detection
wired.

**Metal acceptance gate** — `cmd/vmmd/workload_oom_metal_test.go`:
boots a Hobby-plan guest, places a workload in the per-VM cgroup leaf
with `memory.max = 256 MiB`, runs
`python3 -c 'x=[]; [x.append(bytearray(1024*1024)) for _ in range(384)]'`
to force OOM, asserts the deployment row is stamped `app_runtime_oom`
with the templated peak MB ≈ 384 + plan cap 256 within the tripwire
budget. Runs on `make test-metal` (x86_64) or `make metal-lima`
(M3+ Mac).

### 6.7 Durable async-job contract (ADR-134, ADR-135)

Every durable background job in Gregale — async invocations,
delayed tasks, queues, cron, trigger records — runs against the same
ten semantic guarantees. Producers migrate onto the contract
gradually; the two schedd drains (`pkg/sched/drain.go` for
`invocations`, `pkg/sched/dispatch_triggers.go` for `trigger_records`)
already share the `pkg/dispatch.Job` interface.

| # | Capability | Where it lives | Plan cap |
|---|---|---|---|
| 1 | Idempotency keys | `cmd/apid/server.go` idempotency middleware on every async route | n/a |
| 2 | Durable leases | `pkg/lease.Manager` (CAS-with-token, `UPDATE ... WHERE lease_token=$N AND lease_expires_at<now()`) | n/a |
| 3 | Explicit retry policy | per-row `retry_policy JSONB` on `invocations` + `trigger_records` | `MaxQueueAttempts` (plan-wide fallback) |
| 4 | Exponential backoff with jitter | `pkg/dispatch.RetryPolicy.Backoff(attempt)` — same curve as the trigger drain's `computeRetryBackoff` | derived |
| 5 | Cancellation | `state.CancelInvocation` + `state.CancelTriggerRecord` | n/a |
| 6 | Deadline / timeout | per-row `deadline_at TIMESTAMPTZ` on both tables; reaper drops breaches to `state='dead_letter'` | `MaxAsyncInvocationDeadlineSeconds` |
| 7 | DLQ replay | `state.RetryQueueDeadLetter` (same row → `state='pending'`), `state.RetryTriggerRecordByOperator` (new row tagged `Source=TriggerRecordReplay`) | n/a |
| 8 | Per-account concurrency | counter-table pattern: `account_async_quota` with `current_inflight` atomic-increment under `current_inflight < max_inflight` cap-check | `MaxAsyncInvocationsPerAccount` |
| 9 | Result retention | per-row `result_retention_until` + `pkg/sched/retention_invocations.go` (60s tick) / `retention_triggers.go` (5-min tick) | `MaxAsyncResultRetentionSeconds` |
| 10 | Shared dispatch contract | `pkg/dispatch.Job` interface — both drains consume | n/a |

**Why counter-table not live-count:** the per-account cap must be
advisory across producers (async + queue + delayed + cron all share
the same `current_inflight` counter). Live-count would require `SELECT
COUNT(*)` per claim with an unlocked window — fine at low QPS, broken
at the §12 200-invocations/sec/tenant target. Counter-table is one
`UPDATE ... WHERE current_inflight < max_inflight RETURNING` row
update per claim, atomic, no TOCTOU.

**Why a wrapper type for `state.Invocation` to satisfy `dispatch.Job`:**
Go forbids a field and a method of the same name on a struct, and
`Job.ID()`, `Job.AppID()`, `Job.AccountID()` collide with the
`Invocation` row's primary-key columns. `state.InvocationJobAdapter`
embeds `Invocation` and proxies the three accessors; the other six
methods are inherited via embedding. Compile-time check lives at
`pkg/sched/drain_compile_test.go::TestDispatch_ContractCompiles`.

**Historical migration slot dance (pre-ADR-142):** PR-B claims slots 517-519; PR-C claims 520-522;
PR-E claims 523-524. PR-A + PR-D ship with no migration (pure refactors).
Real migrations land at 518, 519, 521, 522, 524; the `00XXX_reserve_slot.sql`
fences are placeholders so a sibling PR branching from main does not
accidentally land a real migration at one of those slots.

**Two drains, one contract — NOT merging them.** The unified `invocations`
drain (async / queue / delayed / cron share one row type) is migrated
to `dispatch.Job` end-to-end. The `trigger_records` drain keeps its
own FSM but now consumes the same `dispatch.RetryPolicy`,
`dispatch.Lease`, `dispatch.DeadlinePolicy` types — see PR-C for the
trigger-side wiring.

**Spec compliance:** every `Limits` field added in ADR-134 lives in
`pkg/api/limits.go` and is mirrored in `limits_test.go`'s 4-row parity
table (Free / Hobby / Pro / Scale) — no inline limits anywhere in the
codebase. Test `TestPlanLimitsMatchSpec` is the gate; it fails CI if
any row drifts.

**§17 gap closure:** G-Async-Retention and G-Account-Concurrency close
on this PR landing.

---

## 7. Networking

- Public: `gatewayd-public` binds :80/:443 on the host IP. Nothing else listens publicly. SSH on a non-standard port, key-only, fail2ban.
- Per instance: netns `fc-{instance}`; inside it `tap0` ↔ firecracker; guest always `10.0.0.2/30`, host side `10.0.0.1` (ADR-009 — identical inner world so any snapshot restores anywhere). A veth pair `ve-{instance}` bridges the netns to `br-tenants`; the veth's host address `10.100.x.y/16` is the instance's routable identity; nftables DNATs `host_ip:ephemeral → 10.0.0.2:8080` within the netns.
- **Cross-box overlay (ADR-028):** `gatewayd-internal` reaches remote vmmd hosts via a Tailscale (default) or Wireguard (operator) overlay. The dial leg is plain TCP through the overlay interface (Tailscale: `tailscale0`; Wireguard: operator-named). vmmd's gRPC server binds the overlay port (default 50051) and refuses to serve the public listener. Operators provision via `deploy/ansible/roles/overlay/`. Authkey / peer list management is operator-owned; the role consumes vaulted secrets and renders systemd units.
- Egress (tenant): default-allow TCP 80/443/53 + UDP 53; **deny 25, 465, 587** (spam = hosting abuse desk = existential, founding doc R6); deny RFC1918 + link-local + metadata ranges (no lateral movement into the control plane); per-instance conntrack cap 4,096; egress bandwidth per plan via `tc`: 10 / 25 / 100 / 250 Mbit.
- **Per-instance egress metering (ADR-046, telemetry seam only):** vmmd samples the kernel byte counter at `/sys/class/net/<vethHost>/statistics/rx_bytes` for every RUNNING instance (root-side `vethHost`, since customer egress traverses `tap0 → vethPeer → vethHost` and lands as RX on the host side); cumulative readings are converted to regression-safe deltas in `pkg/fcvm/netstats.Cache` and exposed through `vmmd.Stats` → `schedd.ListInstanceStats` → `meterd.Sampler.SampleAndRoll`. The gateway additionally records HTTP response body bytes via `pkg/gateway/handler.go:statusRecorder.Bytes`. Both accumulate additively in `usage_minutes.tx_bytes` (gateway) and `usage_minutes.net_tx_bytes` (vmmd). **No billing change:** Stripe/Paddle push shapes remain `gb_ram_hour`; per-plan shaping is unchanged.
- **Per-app egress IP allowlist (ADR-031 + ADR-033, M8 tier-2):** operators may pin `apps.egress_allowlist cidr[]` on a v4 or v6 CIDR list (Pro ≤16 entries combined, Scale ≤64 combined; Free/Hobby gate). Empty list = current default-allow behaviour preserved. Non-empty list emits one rule per non-empty family inside the per-netns forward chains — `iifname "tap0" ip daddr { v4 CIDRs… } accept` on `ip faas forward` and/or `iifname "tap0" ip6 daddr { v6 CIDRs… } accept` on `ip6 faas forward` — each placed **after** its chain's lateral-movement deny + SMTP drops so deny > allow on overlap and **before** the chain's default policy so unlisted destinations drop. Live instances keep their old ruleset until the next wake (same contract as `RAMMB` / `MaxConcurrency`). Non-`/0` contract held by the DB trigger `apps_egress_allowlist_cidr` (migration 00033); the apid + vmmd wire layers are defence-in-depth.
- **Per-app static outbound IP (ADR-119, single-node v1, BYOIP):** a Scale customer may pin `apps.static_egress_ip INET` to a single customer-supplied IPv4 so every outbound packet from that app exits with the customer's IP (B2B allowlisting: partner APIs, managed Postgres, payment-processor IP lists). v1 is single-node: the IP is aliased on the local bridge (`br-tenants`) and the per-netns `postrouting` chain emits a sibling MASQUERADE rule — `oifname <VethPeer> ip saddr 10.0.0.2 snat to <CustomerIP>` — placed AFTER the default MASQUERADE so the SNAT-to-customer overrides the host-identity rewrite. The deny set (RFC1918, CGN 100.64/10, link-local 169.254/16, multicast 224/4, loopback, IPv6) is enforced at the apid handler AND at vmmd's TOML bundle loader AND at the bridge alias allocator — three layers of defence-in-depth so an operator typo can't pin a reserved IP. Per-app quota = 1 (Scale-only via `limits.StaticEgressIPsPerApp`); a partial unique index `apps_static_egress_ip_key` defends against cross-app IP collision at the DB layer (two apps sharing the same IP would alias-conflict on the bridge). The drift path mirrors `egress_allowlist`: schedd's `egress_drift` subscriber fires `vmmdpb.UpdateStaticEgressIP` on every `app_changed` pg_notify carrying the column; live instances keep their old ruleset until the next wake (same contract as the egress allowlist). Out of scope for v1 — explicitly deferred: multi-host placement pin (anycast/floating-IP), IPv6, platform-owned IP pool, Paddle/Stripe add-on billing.
- Egress (builder VMs): allow 443/80/53 to package registries only via a squid allowlist in v1.1; v1 = same as tenant policy. Deny everything inbound always.
- DNS names: `{slug}.apps.gregale.dev` wildcard A record → host IP. Custom domains: customer CNAMEs to `edge.gregale.dev`, apid verifies via TXT `_faas-verify.{domain}` before `gatewayd-public` will mint a cert. **Doctor (ADR-120):** `GET /v1/domains/{domain}/doctor` returns the 5-check Render-style report (DNS found / points to Gregale / TLS / CAA permits / IPv6 conflict) with per-check remediation lines; backed by the persisted `domain_doctor_observations` table the `dns_poller` writes every 30s. See [`docs/domains/doctor.md`](../domains/doctor.md).

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

## 9.A. Connection-aware execution (ADR-098)

The platform is **stateless by contract** (§17 / ADR-046 / `docs/storage.md`). Customers bring their own datastore / cache / object store / APIs. §9.A makes the placement chooser aware of where those upstreams are so a wake lands as close as the control-plane fleet can get to where the data lives. Architecturally adjacent to §9 (deploy lifecycle) because both surface is the same `apid` write-path, but kept as a sibling section rather than a sub-subsection — the chooser changes (`pkg/sched/placement.go`) and a new probe loop (`pkg/meter/upstream_probe.go`) are not deploy-pipeline concerns.

The implementation is split across a 5-PR cluster documented in `docs/adr/098-pr-cluster-outline.md`. The summary here is the contract every PR conforms to:

- **Capture:** `apid` infers upstreams from env values on `PUT /v1/apps/{slug}/env/*` and `POST /v1/apps/{slug}/secrets`, matching keys against a fixed regex set: `(DATABASE|REDIS|MONGO|CLICKHOUSE|CASSANDRA|ELASTIC|OPENSEARCH|RABBITMQ|KAFKA|NATS|MEMCACHED|ETCD)_(URL|ENDPOINT|URI|DSN)`, `S3_(BUCKET|ENDPOINT|REGION)`, and `*_API_URL`. Host extract via `net/url.Parse`. Region inferred from a static provider→region table in `pkg/data/infer.go`. The DSN value never leaves the handler (§11 secret rule); only the host + port + inferred region are written. Plan-gated: Free apps never get inferred rows (`Limits.DataPlacementHintsPerApp` = 0 for Free); Hobby 3, Pro 10, Scale 50.
- **Probe:** `meterd` adds a seventh polling loop alongside its existing six timers (`pkg/meter/loop.go`). TCP-connect + TLS-handshake timing per `(host, region)` pair at 30 s × 5 min sliding window. Bounded worker pool (`Limits.UpstreamProbeMaxConcurrent = 64` global). Prom metric `data_upstream_rtt_ms{kind, host, region}` (median) + `data_upstream_probes_total{outcome}` (counter). Per-region SLO: p95 < 50 ms (info), alert at > 200 ms. Documented in §12 telemetry table.
- **Placement bias:** `pkg/sched/placement.go::Request` gains `PreferredRegion` (parallel to `PreferredNodeID`). `UpstreamAffinity` (`pkg/sched/upstream_affinity.go`) mirrors `WarmAffinity` — a TTL'd in-process `appID → regionScore` map fed by `pg_notify` from the probe's insert path. `betterCandidate` adds one tie-break **between vCPU-headroom and static-region** (`pkg/sched/placement.go:235`), gated on `Limits.UpstreamFitMinDeltaMs` (default 5 ms) so warm-affinity keeps snapshot reuse when the RTT delta is small. The chooser is fail-open — no data means legacy behaviour.
- **Customer surface:** `GET / POST / DELETE /v1/apps/{slug}/upstreams` — three new routes in `cmd/apid/handlers_upstreams.go` (sibling to `handlers_env.go`). GET returns the inferred + explicit rows joined to the latest RTT sample (last 5 min). Auth: `env:read` for GET, `env:write` for POST/DELETE (mirrors env-route auth per ADR-090).
- **Quota:** every new limit lives in `pkg/api/limits.go`. `DataPlacementHintsPerApp` is per-plan. `UpstreamProbeMaxConcurrent` and `UpstreamFitMinDeltaMs` are global. Error codes `data_upstream_quota_exceeded` (402) and `data_upstream_invalid` (400) join the package's RFC 7807 problem shape.
- **Rollout:** three feature flags — `FAAS_DATA_PLACEMENT=0` (apid), `FAAS_UPSTREAM_PROBE=0` (meterd), `FAAS_UPSTREAM_AFFINITY=0` (schedd). All default OFF for v1.10. Manual flip per node for v1.11 once CI is clean for one full month on `main`. The single-node install is byte-identical until ops flip the flags.

The §9.A payoff multiplies with each compute box added at M9. On a single-node install the feature is observable (metrics + GET endpoint reflect hints) but functionally invisible to the chooser (no multi-candidate scoring possible). On a multi-node fleet with distinct regions, the chooser biases wakes toward the node whose measured RTT to the customer's upstreams is lowest.

---

## 10. Metering and billing detail

- Unit: GB-RAM-hour, billed on provisioned `ram_mb + 8` per running second (§4.7). Definition published verbatim in docs — no surprise-RSS billing.
- Included quotas per plan per calendar month (UTC): 5 / 50 / 250 / 1,500 GB-h. Overage €0.01/GB-h, metered in millicents, Stripe usage records hourly, idempotent by `(subscription_item, hour)`.
- Requests are counted but not billed (v1). Per-instance customer egress bytes are metered and exposed through the usage surfaces (`usage_minutes.tx_bytes`, `usage_minutes.net_tx_bytes`, `usage_monthly.tx_bytes`, `usage_monthly.net_tx_bytes`, `GET /v1/usage*`), but **not** billed in this change. The host uplink remains 1 Gbit flat; per-plan shaping (10/25/100/250 Mbit, §7) is unchanged. Stripe/Paddle push shapes remain `gb_ram_hour` only. The columns are the seam for the future egress-billing PR which extends `Provider.PushUsageRecord` (ADR-046).
- Plan changes: upgrade immediate + prorated by Stripe; downgrade at period end; quota checks (deployed count, RAM sizes) run pre-downgrade and block with a task list ("delete 3 apps or reduce RAM…").
- The `usage` API (`GET /v1/usage?month=`) returns the billable fields (`mb_seconds`, `requests`) via the same query and code path as the invoice; the informational `cpu_usec`, `tx_bytes`, and `net_tx_bytes` fields are telemetry only and are not pushed to a billing provider. Future billing integration extends `Provider.PushUsageRecord` without changing the "invoice = usage" invariant for billable dimensions.

---

## 11. Security hardening checklist (ship-blocking)

**Host:** cgroups v2 unified only; kernel ≥ 6.8 HWE; `kernel.unprivileged_userns_clone=0` (nothing on the host needs it — builds are in VMs); auditd on execve in control-plane slices; unattended-upgrades security-only with reboot window Sun 04:00 UTC; nftables default-drop inbound.
**Jailer/VM:** unique uid/gid per instance; chroot; seccomp default filter (Firecracker's); `--daemonize` off, supervised by vmmd; no shared directories with guests — block devices only; virtio-rng always attached.
**Snapshot uniqueness:** resume hook re-seeds guest entropy + steps clock (§4.8); TLS session keys, UUID generators inside customer apps are their concern *after* our entropy re-seed is proven (test: two instances from one snapshot must produce different `/proc/sys/kernel/random/uuid` immediately post-resume).
**Control plane:** apid input validation is the trust boundary — fuzz it; API keys hashed; rate limit auth failures (10/min/IP); Postgres on unix socket only; secrets in `/etc/faas/secrets/` root:root 0400, never in env of tenant-reachable processes. `/etc/faas/sealed.env` is the apid-only env file — every other control-plane daemon (schedd, meterd, githubd, gatewayd-internal, vmmd, imaged, builderd) loads private material via `systemd LoadCredential=` + `Environment=KEY=%d/<id>`, and env-var overrides (billing-provider keys, GitHub App credentials, FAAS_NODE_NAME) via per-daemon `EnvironmentFile=-/etc/faas/secrets/<daemon>/*.env`. The shared cross-daemon `DATABASE_URL` lives at `/etc/faas/compute-db.env` (0440 root:faas). The static CI gate `scripts/ci/check_sealed_env_scope.sh` (wired into `make lint`) refuses any `EnvironmentFile={-,}/etc/faas/sealed.env` line in a non-apid unit. See ADR-127.
**Per-deployment authentication (issue #560):** `apps.require_authn bool NOT NULL DEFAULT false` — opt-in per-app token gate. Default is `false` so every existing customer is unaffected; Pro/Scale customers can PATCH the flag on (Free/Hobby receive 403 `plan_require_authn_not_allowed` at apid). When on, `gatewayd-internal` demands `Authorization: Bearer <token>` for every request, validates the key via `pkg/auth.Middleware.RequireSession` (SHA-256 hash, account-scoped), and rejects cross-account tokens with 403 `insufficient_scope`. The check sits after Host→app resolution and before the wake gate so anonymous traffic cannot trigger cold-boot on a token-gated app.
**Patch policy:** Firecracker/kernel CVE affecting guest isolation = same-day; everything else = weekly window. Subscribe to firecracker-microvm security advisories; drill the FC-upgrade-invalidates-snapshots path (it's routine, not an incident — ADR-005).
**Abuse:** signup requires email verification + one of (card, GitHub account ≥ 30 days); crypto-mining heuristic = sustained cpu.stat throttled + 100 % for > 15 min on Free/Hobby → auto-park + review queue; AUP bans mining, scanning, spam relaying.

**Response headers (issue #249):** every response leaving the public listener (`gatewayd-public` :443) and the apid loopback listener carries the six hardening headers below. Mounted by `pkg/httpsec` at the outermost wrapper of both daemons. The static set (HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy) runs on every response; Content-Security-Policy is gated so it only fires on apid-handled URLs (customer-app responses keep the customer's own CSP).

**Deploy-time signature enforcement (issue #472 / ADR-058):** for regulated workloads (healthcare / fintech / SOC 2 / ISO 27001 / PCI-DSS), "trust the developer's machine" is not an acceptable posture. The platform exposes a per-app `require_signed` flag (default false) plus a per-app trusted-publisher list. When an operator flips the flag on (admin scope + MFA, `PATCH /v1/apps/{slug}/security`), every OCI image deploy to that app must carry a valid cosign signature from a publisher in the list. The list lives at `/etc/faas/secrets/trusted-publishers/<signer_name>.pem` (mode 0444 per file, root:root); onboard via `gregale trusted-publishers add <slug> <name> <pub.pem>`. imaged verifies at deploy time (after `pg_notify` dispatch, before `PullDigest`); apid pre-flight gates reject the deploy with `403 deploy_signature_invalid` if the flag is on and the trust list is empty (operator-on / no-publishers footgun). Source-tarball deploys (Railpack path) bypass the gate by design — the tarball never asks the registry for a signature. Wire-shape parity with AWS Lambda's `CodeSigningConfig`.

**Metrics-listener canonical shape (ADR-122):** every daemon serving a loopback `/metrics` HTTP listener installs the canonical shape — `ReadHeaderTimeout=10s` (pre-existing Slowloris guard) + `ReadTimeout=10s` + `WriteTimeout=10s` + `IdleTimeout=60s` + `MaxHeaderBytes=1 MiB`. Per-daemon `Config.MetricsListener()` helper reads TOML fields with constant fallback (`pkg/api/limits.go::Metrics*SecondsDefault`). githubd's webhook listener uses the webhook variant — `ReadTimeout=30s` (the budget for a slow webhook client to upload 10 MiB at githubd's `readBody` cap), `WriteTimeout=30s`, `IdleTimeout=60s`, `MaxHeaderBytes=1 MiB`. The shape applies to meterd, schedd, vmmd, builderd, imaged, githubd; the per-listener constants live in `pkg/api/limits.go` so a future daemon inherits the same knob set without grep archaeology.

**Metrics-listener canonical shape — post-merge audit (ADR-122 amendment):** the audit that confirmed the canonical shape PR also surfaced four production HTTP listeners with a partial knob set. Each is now closed: (a) `cmd/gatewayd-public/main.go::buildServers` **publicSrv** (customer-facing TLS edge) — adds `IdleTimeout=120s` (matches apid's customer-facing listener at `cmd/apid/main.go:452` via `APIDIdleTimeoutSecondsDefault=120`); the customer-facing RT=60s / WT=300s are kept from ADR-121. (b) `cmd/gatewayd-public/main.go::buildServers` **controlSrv** (loopback :9092 healthz/readyz/metrics) — adopts the canonical metrics variant (RHT=10s + RT=10s + WT=10s + IT=60s + MHB=1 MiB), RHT bumped from 5s to 10s. (c) `pkg/gateway/control.go::NewControlHTTPServer` (the helper behind `RunControlServer`) — adds `MaxHeaderBytes=1 MiB` (was 0, stdlib default); the four timeout knobs (RHT=5s + RT=10s + WT=10s + IT=60s) pre-date this PR. The helper is exported so a test can pin the struct fields without binding a real listener (mirrors `pkg/githubd.NewWebhookHTTPServer`). (d) `pkg/gateway/synth.go::NewSynthServer` (SynthServer — internal compute loopback) — adds the four missing knobs (RT=10s + WT=10s + IT=60s + MHB=1 MiB); RHT=5s stays as the pre-existing H2C negotiation guard. Intentional exceptions: `cmd/vmmd-stream-bridge/main.go` (per-guest H2C, session-level deadline) and `cmd/gregale/templates/hello-go/main.go` (sample guest template, not production). Full rationale in §"Post-merge audit (issue #995 closure)" of ADR-122.

**Connection-aware execution host redaction (ADR-098 §11):** the `data_upstreams` + `data_upstream_probes` tables, the Prometheus metric labels, the pg_notify payload, and the schedd affinity map key all carry `host_redacted_hash` (64-hex `sha256(per-cluster-salt || plaintext-host)`) — the plaintext host NEVER leaves the apid handler. The salt is loaded from `/etc/faas/secrets/host_hash_salt` (32 raw bytes, 0o600 root:root) at boot; a missing or wrong-length salt file is a fatal boot-time error. The only permitted host-derived label on the metric surface is `host_redacted_hash`; a regression that introduces a `host` or `host_plaintext` label is tripwired by `pkg/data/quiescence_secret_rule_test.go` (C4) and the static-fail assertion in `cmd/e2e/connection_aware_e2e_test.go::TestConnectionAwareE2E_FlagsOn`. The env_values table itself stays plaintext (the user wrote the value; redacting it would break customer code that reads it back) — only the `data_upstreams` classifier-derived row is redacted.

| Header | Value | Notes |
|---|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | UAs ignore HSTS on plain HTTP per RFC 6797 §7.2 — cosmetic on dev plaintext listener. Disable via `FAAS_HSTS_ENABLED=false`. No `preload` until §11 policy review finalises. |
| `X-Frame-Options` | `DENY` | The dashboard is the only HTML surface; clicks are never meant to be framed. |
| `X-Content-Type-Options` | `nosniff` | Stops the browser guessing MIME on `/v1/*` responses. |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Sends only the origin on cross-origin navigations; full URL on same-origin. |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=(), usb=(), payment=()` | No browser feature is currently used by the dashboard; explicit close. |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self' 'nonce-{rand}' https://unpkg.com; style-src 'self' 'nonce-{rand}'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self' https://*.stripe.com https://billing.faas.example` | Per-request 128-bit nonce (base64-URL, 22 chars). Minted by `pkg/httpsec.Nonce`; consumed by `pkg/dashboard.Render` which stamps `nonce="…"` on every `<script>` and `<style>` tag. `https://unpkg.com` is on the `script-src` list because every dashboard template loads `htmx.org@2.0.4` (and SSE pages additionally load `htmx-ext-sse@2.2.2`) from there. SRI hashes pending as a separate ADR. |

The CSP gate is `cmd/gatewayd-internal/proxy.go::isApidPath` for `gatewayd-internal` and an unconditional `func(*http.Request) bool { return true }` for apid (apid serves only dashboard + JSON). The platform never emits CSP on a customer-app response — those apps govern their own CSP.

The inline `onclick="return confirm(…)"` in `pkg/dashboard/templates/account.html` was refactored to a per-page `<script nonce="…">` block using `addEventListener`. Browsers do not propagate `nonce` onto event-handler attributes, so leaving the inline handler in place would silently disable the delete-account confirm prompt the moment CSP ships.

**Active-passive standby topology (ADR-083, accepted 2026-08-16):** every
control-plane schedd cluster member carries a `StandbyState` gauge
(`warming | warm | draining`) and the standby side redirects writes to the
active leader. The redirection rate is exposed as
`gatewayd_internal_write_redirect_total{outcome,auth_kind}` per ADR-083
§"Open #2". Failure drill runbook at
`docs/runbooks/active-passive-ha.md`; two-node Lima harness at
`deploy/lima/faas-metal-2node-ha.yaml`. Probe timeout bounded by
`HAFailoverProbeTimeoutMS = 500 ms` in `pkg/api/limits.go`; drain
deadline bounded by `HADNSRecordStaleSeconds = 30`.

---

## 12. Observability and SLOs

Prometheus (node_exporter + per-daemon `/metrics`) → self-hosted Grafana OSS on the management bridge; alerting via Alertmanager → email + Pushover (ADR-031).

**The dashboard row that mirrors the financial model (check weekly, feed the sheet monthly):**

| Metric | Plan value | Alert |
|---|---|---|
| `snapshot_fleet_avg_mb` / `p95` | 130 / — | avg > 160 warn, > 200 page |
| resident GB per paying customer | 0.305 (=312 MB) | > 0.45 warn |
| `resident_ram_pct_of_target` | ≤ 100 % | > 80 % warn, > 92 % page |
| `lv_fc_used_pct` | — | > 80 % warn, > 90 % page |
| build queue wait p95 | < 60 s | > 300 s warn |
| `gateway_wake_latency_seconds` p95 | ≤ 0.8 s | > 1.5 s warn |
| `gateway_request_duration_seconds{app,class}` p95 | n/a (per-app) | none (ADR-042: customer dashboard) |
| `gateway_stream_flushes_total{app,plan}` rate | n/a (per-app) | none (ADR-047: streaming telemetry — see §12.5) |
| `gateway_response_bytes_total{app,plan}` rate | n/a (per-app) | none (ADR-047: per-flush + residual capture — see §12.5) |
| `gateway_stream_active{app,plan}` gauge | n/a (per-app) | none (ADR-047: in-flight streams — see §12.5) |
| `gateway_cold_boot_total{app}` share | < 2 % of wakes | none (ADR-042: customer dashboard; fleet wake latency is the SLO) |
| cold-boot fallback rate | < 2 % of wakes | > 10 % warn (snapshot rot) |
| `schedd_instance_cpu_pct{app,node}` | max over siblings | > 90 sustained page (hot loop) |
| `schedd_instance_rss_mb{app,node}` | sum over siblings | > plan × max_concurrency page |
| `schedd_instance_inflight_requests{app,node}` | sum over siblings | > max_concurrency × 2 page |
| `schedd_instance_stats_collect_seconds` p95 | < 0.05 s | > 0.2 s warn (dialer saturation) |
| `schedd_instance_stats_partial_errors_total{node}` | 0 | > 5 / min page (vmmd unreachable) |
| `gatewayd_internal_write_redirect_total{outcome,auth_kind}` rate | n/a (per-outcome) | `outcome="leader_unreachable"` > 0.1 / min page (ADR-083 §Open #2 closure); `outcome="loop_prevented"` > 1 / min page (redirect-storm DoS); `outcome="mTLS_failure"` > 0 page (cert / clock-skew); `outcome="cookie_blocked"` rate tracked (deferred, ADR-025 Tier 2 unblocks) |
| `gatewayd_internal_write_redirect_latency_seconds` p95 | ≤ 1.5 s | > 3 s warn (overlay degradation), > 5 s page (StandbyWriteRedirectTimeoutMS fires) |
| `gateway_edge_rule_apply_total{kind,result}` rate | n/a (per-kind) | per-kind `result="error"` rate > 1 / min warn (runbook `FaasEdgeRuleApplyHigh.md`) |
| `gateway_edge_rule_compile_error_total{kind}` rate | 0 | any non-zero page (runbook `FaasEdgeRuleCompileError.md`; ADR-091 Amendment 1: compile error = correctness signal, not headroom) |
| `gateway_edge_rule_match_total{kind,outcome}` rate | n/a (per-kind) | per-kind `outcome="failed"` for kind=jwt > 5 / min warn (runbook `FaasEdgeRuleJWTFailures.md`; audit-grep `data.err` for `context deadline exceeded` separates timeout from verifier error; CORS preflight short-circuits IP+JWT gates — filter `method != "OPTIONS"` before counting) |
| `gateway_response_cache_total{outcome}` rate | n/a (per-outcome) | `outcome="hit" / "miss"` ratio is the dashboard hit-rate surface; `outcome="bypass_authed" > 5 % / 5 m` warn (runbook `FaasResponseCache.md` — spike means a customer is hammering an authed cacheable path); `outcome="bypass_uncacheable"` is informational (origin decided); `outcome="store_skipped"` rate > 50 % warn (rule is over-matching a cached path); `outcome="stale_if_error_served"` rate > 5 % / 5 m warn (origin is flapping — cache is masking failures, not fixing them) |
| `gateway_response_cache_wakes_avoided_total{app_id}` rate | n/a (per-app) | counter, NOT a rate — dashboards compute `saved_gb_ram_hours = wakes_avoided × (plan.RAM_MB + 8) × billed_seconds / 3600 / 1024` against `pkg/api/limits.go` plan table; telemetry only, does NOT enter `gb_ram_hour` push (ADR-122 §Decision — honest saved-cost figure requires `HealthyCount == 0` gate; a hit against a warm app saves latency but no compute and must not be counted) |
| `gateway_response_cache_bytes` / `gateway_response_cache_entries` gauge | ≤ `DefaultResponseCacheMaxBytes` (32 MiB) / ≤ entries that fit the byte ceiling | gauge high-water alert runs at 80 % of `DefaultResponseCacheMaxBytes` (runbook `FaasResponseCache.md` — sustained pressure means LRU is evicting faster than the dashboard's hit-rate numerator can settle; consider shrinking `vary_on` cardinality) |
| `meterd_data_upstream_rtt_ms_bucket{kind,region,le}` p95 | < 5 ms | > 200 ms page (runbook `FaasUpstreamRttDegraded.md`); info at < 5 ms (`FaasUpstreamRttHealthy`) — connection-aware placement health (ADR-098 §9.A) |
| `meterd_data_upstream_probes_total{outcome}` rate | n/a (per-outcome) | `outcome="timeout"` / `outcome="refused"` > 5 % of `outcome="ok"` for 10 m warn (runbook `FaasUpstreamProbeHighFailureRate.md`); `outcome="tls_handshake"` > 0 page (cert validation is non-bypassable per ADR-098 §11) |
| `meterd_data_upstream_probe_duration_seconds` p95 | < 0.5 s | > 1 s warn (runbook `FaasUpstreamProbeSlow.md`); interaction with `meterd_data_upstream_rtt_ms` — payload size of the TLS handshake is bounded by the customer's CA chain, not our control |

The four `schedd_instance_*` gauges (ADR-036, issue #170) are the
new per-`(app,node)` rolled-up surfaces — max CPU, sum RSS, sum
inflight — emitted by schedd's per-instance metrics poller at
5 Hz. Per-instance data lives in the in-memory
`pkg/sched/instancestats.Reader`; the wire side is rolled up
explicitly because per-instance Prometheus cardinality is
unbounded under the §6.2 fan-out invariant. Future scale policy
work (#171 reaper, #169 scale-up trigger) reads from the Reader
directly, not from Prometheus.

**Per-node wake-latency surfaces:** `gateway_wake_latency_seconds` is a
single histogram (no `{node}` label). Adding `node` would blow
cardinality under the §6.2 invariant and would not survive the 90 s
two-node acceptance budget. Per-node wake attribution is achieved
*via* `wake_id` `prometheus.Exemplar` (ADR-097 P1B) — operators join the
histogram to `pkg/wire.OpsMetrics.WakeRPCDuration{phase,app}` and to the
`events` table on `wake_id`. The `wake_locality_host_total` (ADR-028
v1.1, line 793) already covers host-local vs. cross-node routing
counts at a non-cardinality-blowing granularity.

**Per-app wake-narrative surface (ADR-123 PR-A):** the dedicated
`/dashboard/apps/{slug}/wake-timeline` page surfaces the wake-boot
telemetry at the customer-facing layer — 24h summary card
(wake count + stable-sorted trigger histogram + at-cap count/%) +
50-row recent-wakes table (Trigger / Queued / Concurrency / At cap /
Ready columns, the five fields stamped on `wake.boot_started` via
the existing `events.data` jsonb payload). Aggregation happens at
the handler edge (`cmd/apid/handlers_dashboard.go::renderAppWakeTimeline`
+ `pkg/dashboard/views/wake_timeline.go`); the template is FuncMap-free.
The page is reachable from the recent-wakes section header on the
existing app-detail surface; pre-PR-A fleet rows render `—` per the
existing absent-value convention.

`gateway_request_duration_seconds{app,class}` (ADR-042, issue #273)
is the per-app full-request-duration histogram exposed on the
customer dashboard and the `GET /v1/apps/{slug}/metrics` endpoint.
ADR-042 documents the deviation from the #273 acceptance criteria:
the `route` label is dropped (`gatewayd-internal` is an opaque reverse proxy)
and the rename of `gateway_cold_wake_total` →
`gateway_cold_boot_total` is straight (zero external consumers).

**ADR-093 sidebar (issue #273 follow-up):** the per-route
breakdown is reintroduced as an opt-in surface for API-hosting
customers. Two extra series are emitted from `gatewayd-internal`
behind the two-level opt-in (operator kill-switch + per-app
`apps.route_metrics_enabled`):

- `gateway_requests_by_route_total{app,plan,route,code}` (counter)
- `gateway_request_duration_by_route_seconds{app,route,class}` (histogram)
- `gateway_request_failures_by_route_total{app,plan,route,code}` (counter)

The `route` label is method + raw path (pre-edge-rule-rewrite),
bounded per app to 50 distinct real routes + the reserved
`__route_other__` overflow bucket (ADR-093 D2). The bounded
admission set is the only thing standing between us and
`O(paths)` Prometheus cardinality under wildcard path patterns.
The in-memory reader is also exposed via the control listener at
`GET /v1/internal/apps/{slug}/routes` (loopback-only, mTLS-free
within the same box) and reverse-proxied by apid as
`GET /v1/apps/{slug}/routes` for the dashboard panel.

ADR-042 §1 is **partially superseded** by ADR-093 — the route
label is now opt-in for Hobby+ plans, not blanket-dropped. The
rest of ADR-042 (cold-boot rename, per-app histogram shape)
stands.

### 12.1 Autoscale decision telemetry (ADR-037, ADR-038)

Two paired counters surface the schedd scale decisions so an operator can
see "did the box scale, and why" without correlating instances:

| Metric name | Labels | Producer | Outcome semantics |
|---|---|---|---|
| `schedd_scale_up_decisions_total` | `app`, `outcome` | `pkg/wire.OpsMetrics.ObserveScaleUp` (per tick, per app that ran the trigger) | `admit` (signal above target, admitted an instance), `reject_at_cap` (signal above target but at `max_concurrency`), `no_signal` (trigger had no RPS/CPU data for this app yet), `cooldown_held` (per-app scale-out cooldown consult in `Engine.admitGate` skipped the wake — issue #462), `min_floor_already` (per-app `ScalingPolicy.MinInstances` already met and no traffic signal — issue #462, PR-C), `overage_cap_reached` (account overage cap reached — issue #561, `pkg/sched/engine.go:4876-4888`) |
| `schedd_scale_down_decisions_total` | `app`, `outcome` | `pkg/wire.OpsMetrics.ObserveScaleDown` (per tick, per app that ran the aggressive reaper) | `park` (≥ 1 instance parked above `max(min_instances, desired + 1)`), `keep` (signal said the running set is fine OR said "park to floor" and the floor matches the running count exactly), `min_floor_already` (per-app `ScalingPolicy.MinInstances` already met — issue #462, PR-C; semantic upgrade over `keep`), `cooldown_held` (per-app scale-in cooldown consult in `ReapAggressive` skipped the entire app — P1C; also emitted by the idle reaper branch since P1D — `ReapIdle` is the canonical emitter, `ReapAggressive` consults the shared `cooldownHeldByApp` set and skips its emission when the idle branch already recorded the same app in the same tick) |
| `schedd_wake_rpc_duration_seconds` | `app`, `phase` | `pkg/wire.OpsMetrics.WakeRPCDuration` (per successful wake, on the cold-boot / restore success path only — error branches emit `events.BootFailed` instead) | `admit_to_rpc` (gRPC handler → vmmd RPC start), `rpc_call` (vmmd `Create{FromSnapshot,ColdBoot}` round trip), `rpc_to_running` (RPC return → `state.StateRunning` transition). `wake_id` is attached as a `prometheus.Exemplar` on every observation so operators can join to `gateway_wake_latency_seconds` and to the `events` table. Bucket set is spec §6.3 verbatim plus a 0.01 s low-end bucket for `admit_to_rpc`. Empty-app sentinel rows are pre-instantiated for all three phases so the §12 wake-latency-decomposition panel surfaces zero rows from boot. ADR-097 (P1B). |

Both counters are per-daemon (the `schedd_` prefix is supplied by
`wire.NewOpsMetrics("schedd")`); the metric name is the operator-facing
identifier. Empty-app labels are pre-instantiated for both `park`/`keep`
and `admit`/`reject_at_cap`/`no_signal` so the panel exists at day 1 on
an idle box (precedent: OCI-pull histogram in `pkg/wire/metrics.go`).

The `scale_up` row is owned by ADR-037 (reactive scale-up trigger, issue
#169 / #172). The `scale_down` row is owned by ADR-038 (issue #171, this
PR). They are symmetric by design — the same Prometheus registry, the
same outcome-pre-instantiation pattern, the same card-label cardinality
bound. An operator can read both rows side-by-side at
`/metrics` after a single box has seen traffic.

Why paired: `scale_up` shows "traffic is asking for more", `scale_down`
shows "the cooldown path is keeping the box tight". A box with frequent
`scale_up` + `scale_down` flips is a customer with bursty traffic and a
misconfigured `autoscale_target_rps`; a box with only `scale_down` is a
healthy autoscale tail; a box with only `scale_up` is an app at the cap
(needs `max_concurrency` bumped). The `app` label cardinality is bounded
by `apps` (one row per customer app), not by `instances`.

### 12.2 Egress deny telemetry (PR-E)

Every catalog CIDR (spec §11) carries a stable nftables named counter (`drop_v4_<sanitized>` / `drop_v6_<sanitized>`) attached to its per-CIDR deny rule. Three surfaces expose the drops as Prometheus series:

| Surface | Metric name | Labels | Producer |
|---|---|---|---|
| Host nftables | `vmmd_egress_deny_total` | `cidr`, `family` | `cmd/vmmd/poller.go` reads `nft -j list counters` every 15 s and emits the per-counter delta |
| Per-netns nftables | (not exported) | — | per-VM cardinality is unbounded; available via `nft list counters` on the operator box for debugging |
| OCI user-space dialer | `imaged_oci_egress_deny_total` | `cidr`, `family` | `pkg/oci/egress.go::EgressDenyHook` invoked from `EgressDialContext` on denial; `cmd/imaged/main.go` wires the hook |

The `cidr` label is the canonical `DenyEntry.CounterName` (single source of truth — `pkg/netns/denylist.go`). The `family` label is the nft family keyword (`ip` / `ip6`). Cardinality is bounded by the catalog (~12 v4 + ~7 v6 entries). Every (cidr, family) tuple is pre-instantiated at boot so an idle box renders the panel as zero-valued rather than "no data" — same precedent as the OCI-pull histogram pre-instantiation.

**Sample dashboard panel**: `rate(vmmd_egress_deny_total{cidr=~"drop_v4_10_.*|drop_v4_192_168_.*"}[5m]) > 100` warns an operator that a tenant is hitting the RFC1918 drop storm — usually a misconfigured egress allowlist or a webhook firing at `http://10.x.x.x`. The OCI-side mirror (`imaged_oci_egress_deny_total`) differentiates "firewall blocked it" from "dialer refused it" — the two failure modes have different remediation paths (nftables rule vs. denylist catalog edit).

**Why two metrics**: the host-side counter is read from `nft list counters` (kernel-layer drops are observable to nftables). The OCI-side counter is incremented in user-space because the OCI dialer is an HTTP client, not a guest kernel — nftables counters don't see it. Together they let an operator see whether a tenant's blocked pull hit the firewall first (host counter) or the user-space check (OCI counter).

**Reset semantics**: nftables counters reset on table flush or snapshot resume (existing `faas_cap` precedent, spec §4.6). The poll adapter detects `curr < prev` and re-seeds `lastSeen` to `curr` without emitting a negative delta (Prometheus counters are monotonic — `Add(-N)` would panic).

**Sampling rate**: 15 s scrape interval matches the conventional Prometheus cadence and keeps per-tenant alert latency under one minute (alert rule uses `rate()` over `1m`).

### 12.3 Traffic anomaly detection (issue #303, ADR-039)

The apid counter `apid_request_total{account_id, route, code}` (issue #303) is the per-request total — paired with `apid_request_failures_total{account_id, route, code}` (issue #278, `code="err"` invariant) for the per-account error-rate view. Both counters share the same `accountLabelSet` (issue #278) so a customer is represented by the same `account_id` in both, or by `__other__` in both, and the `code` label mirrors between them.

The §12 "traffic anomaly" feature reads this counter through a dedicated `faas_anomaly_baseline` recording-rule group (above the existing `faas_slo` group in `deploy/ansible/roles/prometheus/files/faas.rules.yml`). Methodology and thresholds are decided in ADR-039; the alert family is `traffic_anomaly` and the existing `family`-based inhibition / silencing rules compose with it. Eight alerts fan out: 4 per-route / per-account rate pairs (PR #336) + 2 fleet error-rate pair (PR follow-up) + `FaasTrafficAnomaly` (10+ accounts simultaneously crossing 2× 3d baseline). Each alert has a `runbook_url` to `docs/runbooks/FaasTrafficAnomaly.md`.

Per-customer drill-down PromQL (see the runbook for the full set):

```
sum by (route) (rate(apid_request_total{account_id="<uuid>"}[5m]))
```

The `account_id="__other__"` series is the bounded overflow bucket — drill-down on this means the customer is past the 10 000 admission cap, and the operator must check the daemon slog for the original id (issue #278).

**SLOs (public, on the status page):** API availability 99.5 % monthly; wake p95 < 1 s; build success (non-`user_error`) 99 %. Error budgets, not promises — single-node deployments (until Gate A) are stated honestly on the status page.

Logs: journald → Loki free tier; tenant app stdout/stderr ring-buffered per instance (10 MB), surfaced via `GET /v1/apps/{app}/logs` (tail + follow).

### 12.4 Per-tenant noisy-customer gauge (issue #300, ADR-041)

Two presentation gauges — `apid_top_tenant_rps{account_id}` (apiserver-side, the authoritative source for tenant attribution) and `gateway_top_tenant_rps{account_id}` (edge-proxy side, where the label VALUE is the resolved app_id because `gatewayd-internal` is pre-auth and only sees hostname→app routing) — are sampled at 5s from the rolling per-account request total.

**Two-tier cardinality bound.** The gauge series set is bounded at top-1000 + 1 ("other" overflow). The underlying per-account counter (`apid_request_total{account_id, ...}`) keeps the deeper 10 000 + `__other__` bound from §12.3 / issue #278; the top-N is a presentation view layered above. The two labels are deliberately distinct (`account_id="other"` for the gauge, `account_id="__other__"` for the counter) so a panel can filter one without filtering the other.

**Sampling.** A sampler goroutine in `cmd/apid/topn.go` (mirrored by `cmd/gatewayd-internal/topn.go` on the gateway side) drives the gauge emission once per 5s from a single goroutine. The per-request path bumps the rolling-window count via `OpsMetrics.ObserveTopTenantRPS(id)`; the sampler computes the diff and calls `EmitTopTenantRPS` to drive the gauge. Pushing the gauge write to a single goroutine keeps the series set bounded at cap+1 deterministically — a per-request gauge Set would accumulate series for every id that ever transiently held a top-N slot.

**Rolling reset.** A 24h window; the sampler calls `ResetWindow()` when the window has elapsed. Sized so a one-shot noisy customer doesn't persist in the top-N forever (lifetime view would) and a quiet customer can't jump into the top-N at midnight UTC regardless of activity (daily-snapshot view would).

**Alert.** `FaasTenantAbuse` fires on `topk(20, max(apid_top_tenant_rps{account_id!="other"}) by (account_id)) > 500` for 10m. Severity: `warn`. Family: `tenant_abuse`. The `topk(20, ...)` aggregator preserves per-account labels so Alertmanager routes per-customer; the `account_id!="other"` matcher excludes the overflow bucket (which represents saturated admission, not abusive behavior). Response: rate-limit → notify → suspend deployment (the cascade is in `docs/runbooks/tenant-abuse.md` §Recover).

**Dashboard.** `deploy/grafana/top-tenants.json` (uid `faas-top-tenants`, byte-identical to the Ansible copy at `deploy/ansible/roles/grafana/files/top-tenants.json`). Four panels: top-10 by `apid_top_tenant_rps`, top-10 by `gateway_top_tenant_rps`, top-10 customer share of fleet traffic, and a single-stat for overflow bucket growth.

**Cardinality contract.** `apid_top_tenant_rps` cardinality is bounded at 1001 (top-1000 + 1 overflow) across the daemon's lifetime. The contract is pinned by `pkg/wire/topn_test.go::TestTopTenantRPS_BoundedCardinality` (fuzzed 50 000 ids → ≤ 1001 series) and the synthetic-fixture test `pkg/promqlrules/tenant_abuse_test.go` (5 promtool-driven scenarios: positive, sub-threshold, overflow-only, multi-customer, debounce).

### 12.5 Streaming response telemetry (ADR-047)

Three new surfaces expose the streaming path (ADR-047) so an operator can
see "is the buffered fallback firing more than expected" and "is the
per-flush `tx_bytes` actually matching the bytes the bridge handed the
guest" without reading the bridge logs:

| Metric name | Labels | Producer | Semantics |
|---|---|---|---|
| `gateway_stream_flushes_total` | `app`, `plan` | `pkg/gateway/metrics.go::ObserveStreamFlush` (one increment per `statusRecorder.doFlush`) | Per-flush boundary crossing — the (256 KiB / 200 ms) trigger that splits a streamed response into Telemetry rows. Pre-instantiated for the four plans under `app="__other__"`. |
| `gateway_response_bytes_total` | `app`, `plan` | `pkg/gateway/metrics.go::ObserveResponseBytes` (called from the per-flush `onFlush` callback plus the `finalFlush` residual capture) | Cumulative HTTP response bytes the gateway forwarded for this instance in this minute. On the streaming path the bytes accumulate per-flush; on the buffered path they accumulate once per response at `finalFlush`. ADR-046 additive seam; ADR-047 §Decision specifies the per-flush + residual model. |
| `gateway_stream_active` | `app`, `plan` | `pkg/gateway/metrics.go::ObserveStreamStart` / `ObserveStreamEnd` (Inc at `setupStreamingWriter`, Dec at handler defer) | In-flight streaming responses — the count of open streams at this instant. Buffered-path requests never touch the gauge. A non-zero `gateway_stream_active` that doesn't decay is a leaked stream (R3 in ADR-047). Pre-instantiated for the four plans under `app="__other__"`. |

**Why per-flush, not once-per-response.** A streamed response that
emits 1 KiB every 200 ms for 30 s totals 150 KiB but writes 0 bytes
through `flushNow` until the final `finalFlush`. Once-per-response
metering would under-report by the cumulative chunked body (the
billing seam's `tx_bytes` is read at `finalFlush` only, missing the
chunks that arrived before the last boundary). The per-flush delta
plus a residual capture at `finalFlush` (the bytes sent since the
last boundary) closes the gap: the sum of every `onFlush` delta
plus the residual equals the bytes the bridge handed the guest,
within 1 % under AC #2 (issue #471).

**Why three surfaces, not one.** The three metrics answer three
distinct questions:

- `gateway_stream_flushes_total` — is the streaming path activating?
  Expected: small on buffered apps, large on Hobby+ SSE.
- `gateway_response_bytes_total` — is the bridge accounting
  matching reality? Compare to `net_tx_bytes` (cgroup veth, source
  of truth) for drift.
- `gateway_stream_active` — is there a leak? Should return to zero
  after traffic drains.

**Single-registry invariant.** All three metrics live on the same
Prometheus registry as the rest of `gatewayd-internal`'s `wire.NewOpsMetrics`
set (memory `wire-opsmetrics-single-registry`). The streaming path
cannot accidentally construct a second registry; the tripwire is
`pkg/gateway/metrics_test.go::TestMetrics_StreamFlushesRegistered`.

**Dashboard.** `deploy/grafana/faas-fleet.json` (uid `faas-fleet`,
byte-identical to the Ansible copy at
`deploy/ansible/roles/grafana/files/faas-fleet.json`) carries three
new panels: a flush-rate per-plan group-by, a response-byte-rate
per-plan group-by, and a single-stat for the active-stream gauge.
The same panels mirror to `top-tenants.json` and
`top-throttled-apps.json` so an operator looking at "what is this
tenant doing" sees the streaming data alongside the rate-limit /
cold-boot data.

**Alert.** None at v1.0. The buffered-fallback rate (derived from
`streamingFallbackLog` per-app ratio) is a future candidate for a
"streaming config drift" warn alert (R8 in ADR-047); the metric is
in place but no alert fires until the rate stabilises across one
full PR cycle.

---

## 13. RAM budget ledger (enforced as systemd slices)

| Slice | Budget | Contents |
|---|---|---|
| `system.slice` | 2,048 MB | OS, sshd, journald, node_exporter, chrony |
| `faas-cp.slice` | 6,144 MB | postgres 1,536 · gatewayd-public + gatewayd-internal 512 · apid 256 · schedd 128 · vmmd 256 · builderd 128 · meterd 256 · imaged 512 (spikes during flatten) · loki/promtail agents 256 · slack 2,304 → **1 guaranteed builder VM (2,048 + 8) lives here** |
| `faas-tenant.slice` | 57,344 MB (`memory.max`, hard fence) | tenant microVMs; **schedd admits only to 47,600 MB** (85 % of the model's 56 GB budget) |
| headroom (inside tenant slice, above admission line) | ≈ 8.4 GB | spike absorption; opportunistic 2nd builder VM may borrow ≤ 2 GB of it only below 60 % tenant residency |

`memory.max` on each slice makes the ledger real: a control-plane leak OOMs the control plane, never tenants — and vice versa.

The per-VM request-concurrency bound (`concurrency_per_vm`, issue #559) is independent of the RAM ledger — a customer's `concurrency_per_vm` ceiling is the platform-advertised listener-level bound (Free 1 / Hobby 5 / Pro 25 / Scale 80), set by plan tier rather than by VM RAM. See §4.9.1 for the runner-level concurrency model.

---

## 14. Delivery plan (for agents; sequential, each gate = passing acceptance tests)

Conventions for all milestones: Go ≥ 1.23; integration tests that need KVM are tagged `//go:build metal` and run on a bare-metal x86_64 control-plane node (or any nested-KVM runner) via `make test-metal`; unit tests must pass with `make test` on any machine.

| M | Scope | Acceptance (executable) |
|---|---|---|
| **M0** | Repo scaffold, CI, host bootstrap (ansible: LVM, slices, nftables, cgroups v2 verify) | `make bootstrap` idempotent on fresh Ubuntu 24.04; `test-metal` runs a hello firecracker VM from CI kernel + busybox rootfs |
| **M1** | vmmd: jailer lifecycle, netns/TAP factory, cold boot, destroy | boot 50 VMs (128 MB) concurrently; invariant 6.2-5 checks pass; teardown leaks zero netns/taps/uids (`make leakcheck`) |
| **M2** | imaged: OCI→app-layer ext4 + guest-init; base/runner images | convert a hello app over `runner-node22` base; two-drive VM boots via overlayfs, serves :8080 in < 3 s cold; app layer < 50 MB |
| **M3** | Snapshots: pause/snapshot/restore; resume hooks | park→wake p50 < 350 ms over 100 cycles; two concurrent restores pass the uniqueness test (§11); FC-version-stale → cold-boot fallback works |
| **M4** | gatewayd-public + gatewayd-internal + schedd: routing, wake-blocking, idle reaper, admission | `curl` to a parked app returns 200 with wake; 1,000 rps to hot app adds < 2 ms p50; RAM admission refuses correctly at synthetic 85 % |
| **M5** | apid + Postgres + deploy pipeline with **prebuilt images only**; CLI (`faas deploy --image`) | end-to-end: `faas deploy` → parked → first request wakes; quotas enforced (plan matrix table-test) |
| **M6** | builderd + Railpack/Dockerfile in builder VMs | `faas deploy` a bare Node and Python repo (no config) → live; OOM bomb build kills only its VM (tenant latency unaffected — measured); cache makes 2nd build ≥ 2× faster |
| **M7** | meterd + Paddle (production billing provider, ADR-032 v2): usage, quotas, overage, dunning; functions runtime (runner-node22, runner-node24, runner-python312, runner-python313, runner-go124, runner-go124-alpine); cron | invoice shadow equals hand-computed GB-h for a scripted 24 h scenario (< 0.1 % delta); function hello-world p95 wake < 1 s; Free-tier hard stop verified; **egress metering (ADR-046):** scripted guest egress produces a `net_tx_bytes` row whose sum equals the kernel `vethHost.rx_bytes` delta within 0 bytes, `GET /v1/usage` reflects `tx_bytes` and `net_tx_bytes`, and `billing.Provider.PushUsageRecord` is **not** called for the new columns |
| **M7.5** | Git-deploy + thin dashboard (see `ux_spec.md` §5, §4; ADR-011/012): `githubd`/module, GitHub App, OAuth + repo picker, apps/usage/billing dashboard | push to `main` auto-deploys via the normal pipeline; commit status written back; dashboard connect-repo → live URL end-to-end; least-privilege scopes verified |
| **M8** | Hardening + ops: §11 checklist, backups + **timed restore drill**, status page, docs site, Gate-A runbook (2nd box active-passive; ADR-083 active-passive standby topology, accepted 2026-08-16); UX: cold-wake transparency surfaces (`ux_spec.md` §6), account export/delete (G6) | restore drill: PG + one app back serving on a clean VM < 30 min, documented as executed; security checklist signed off item-by-item; SLO dashboard live; first-time user reaches live URL < 5 min via CLI **and** GitHub connect |
| **M9** | Multi-box scale (ADR-025 axis 3 + ADR-066): cross-node live-instance migration, `OCIRegistryStorageBackend` end-to-end (ADR-054), per-node schedd + per-node placement (PR #509, Tier A4), Tier A6 migrating-instance watchdog (ADR-067). Two-node Lima fleet target (`make metal-lima-2node`) exercises the full four-phase handoff end-to-end. ADR-066 flips `Proposed → Accepted`. ADR cross-refs: ADR-062 (per-node schedd, accepted 2026-08-16), ADR-063 (snapshot de-localization, accepted 2026-08-16), ADR-083 (active-passive standby, accepted 2026-08-16), ADR-110 (declarative split-box manifest, accepted 2026-08-16). | drain a node live (`UPDATE compute_nodes SET active=false`): within `MigrateLiveLeaseSeconds` (90s) + ~5s, every RUNNING instance lands on a live peer; `select node_id, state, migrated_from_node_id from instances` shows `state='running'` + `node_id` flipped; `schedd_live_migration_decisions_total{outcome="migrated"}` increments per successful handoff; `apps.migrated_at` stamped in the same transaction as `instances.migrated_at`; `make leakcheck` zero leaked netns/TAPs/cgroups; `make test-metal` exercises the full four-phase path against the §14 source-of-truth bare-metal x86_64 control-plane node |

Post-M8 = private beta (founding doc roadmap M2–M3: hand-held first ten customers).

### 14.A Workstream A — Jobs (issue #1184, Mega-1)

The Jobs feature ships as a post-M8 workstream rather than as part of M0–M8 because it requires no new platform primitive — it composes `pkg/fcvm` (M1), `guest/init` (M2/M3), `pkg/sched` admission (M4), `pkg/meter` (M7), and the audit event family (M7) — and ships a customer-facing API surface (issue #1184 Workstream A, P0) that closes the long-standing "Cloud Run jobs equivalent" gap.

| Sub-M | Scope | Acceptance (executable) |
|---|---|---|
| **M10** | meter branch + audit events + plan caps (PR-#916 cherry-pick-rebuild foundation) | `meter_kind='job'` rows in `usage_daily`; `job.*` events in audit log; Hobby plan returns 402 `jobs_not_allowed` from POST `/v1/jobs` |
| **M11** | apid handlers + DTOs + routes (11 endpoints) | 13 unit tests in `cmd/apid/handlers_jobs_test.go` PASS; byte-identical 404 contract for missing + cross-account |
| **M12** | gregale CLI `jobs` subcommand family (10 leaves) | 11 CLI tests in `cmd/gregale/commands_jobs_test.go` PASS; flag-validation surface fails locally, never round-trips to apid for a 400 |
| **M13** | OpenAPI 11 paths + 10 schemas + SDK regen | `make sdk-check` green; `make sdk-smoke-node` + `make sdk-smoke-python` cover jobs endpoints |
| **M14** | unit + metal e2e tests | 13 apid + 11 CLI + 11 metal-tagged jobs e2e tests; `cmd/e2e/jobs_metal_test.go` compiles under `-tags metal` (real impls land in follow-up commit) |
| **M15** | docs: ADR-099 supplement + runbook + SPEC cross-link | this section; `docs/adr/099-supplement-jobs-mega1.md`; `docs/runbooks/FaasJobsQueueBacklog.md` |

Canonical references (read in order):
1. `docs/adr/099-jobs.md` — the v1 ADR (proposed).
2. `docs/adr/099-supplement-jobs-mega1.md` — as-built deviations from v1 (Mega-1).
3. `/Users/poyrazk/.claude/plans/logical-beaming-church.md` — implementation plan with slot bank + atomic commit sequence.

**Risks** (carried over from ADR-099 supplement):
- Cold-boot wake storm on fan-out — closed by per-plan parallelism cap + separate `JobDispatch` rate bucket.
- Job RAM starvation of app wakes — closed by `KindJob` admission wired through `RAMAdmissionCeiling=47,600 MB` + `JobConcurrentPerAccount` cap.
- Lease race during schedd restart — closed by reaper pre-CAS on `lease_expires_at > now()`.

**Cross-PR handoff:** ADR-134 (`worktree-feat-dispatch-contract-pr-a`) swaps `pkg/sched/lease.go::Leaser[T]` for `pkg/dispatch.Leaser[T]` post-Mega-1. Surface parity preserved so the refactor is mechanical.


## 15. Repository layout and conventions

```
faas/
  cmd/{apid,gatewayd,gatewayd-public,gatewayd-internal,schedd,vmmd,builderd,imaged,meterd,gregale}/   one main.go each (gregale = CLI)
  pkg/{api,state,fcvm,netns,oci,rootfs,meter,stripex,wire}/
  guest/{init,runners/{node22,node24,python312,python313,go124}}/
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
| Custom domains at launch or Gate B? | M4 (gatewayd-public) | Ship mechanism, gate behind Pro flag |
| Log retention & pricing (10 MB ring enough?) | M7 | Ring only in v1; object-storage archive as Pro add-on later |
| Postgres for customers (managed PG as a product)? | post-GA | No — stay a compute company until Gate C |
| Regional expansion = FSN + HEL pair at Gate A? | Gate A | Yes, matches founding doc R3 |
| Windows of scheduled maintenance vs live-migrate? | Gate A | Maintenance windows; snapshots make drain cheap |
| WebSocket/streaming for functions | M7 | Apps only in v1 |

---

## 17. Known gaps register (v1.0 review — resolve each with an ADR before the milestone named)

| # | Gap | Resolution lean | Decide by |
|---|---|---|---|
| G1 | **Registry unspecced** — §4.2 accepts `image: registry.gregale.dev/...` but no registry component exists | v1: accept **public registries only** (Docker Hub, ghcr.io), digest-pinned, pulled by imaged through the build egress policy. Own registry (CNCF `distribution` behind the public edge) only if private images become a paid ask | M5 |
| G2 | **Customer secrets** — apps need env secrets; no encryption/injection/redaction design | `faas secrets set KEY=…` → sealed with a host age key, stored encrypted in PG, injected into `/etc/faas/app.json` env at boot only, values redacted from build/app logs by pattern; never in snapshots of *other* deployments | **Closed by ADR-020 (PR #73 + M8 readiness PR for vmmd host-key lifecycle).** Sealed with X25519 host age key (filippo.io/age); injected into `/etc/faas/secrets.env` on every wake; values never cross the VM boundary in cleartext; vmmd generates/loads the host key at boot. |
| G3 | **Dashboard/web UI** — spec is API+CLI only | **RESOLVED (ADR-011, `ux_spec.md` §4/§11):** CLI-first, but a *thin* server-rendered dashboard (apid, Go templates + HTMX) ships **at launch** because GitHub connect-repo needs an OAuth callback + repo picker. Scope kept minimal: auth, GitHub connect, apps/logs, usage/billing, account | M7.5 (was post-M8) |
| G8 | **GitHub push-to-deploy** — chosen at launch; no component exists | **RESOLVED (ADR-012, `ux_spec.md` §5):** `githubd` (or apid module) — push-webhook receiver on `gatewayd-public /webhooks/github` (signature-verified), Checks-API status writer, per-repo install-token cache, least-privilege scopes. PR-preview envs deferred to v1.1 | M7.5 |
| G4 | **Transactional email provider** — verification, dunning, quota mails reference email; no provider | **RESOLVED (ADR-115, `docs/adr/115-transactional-email-provider-resend.md`):** Resend selected (free 3k/mo + 100/day, paid $20/mo / 50k); `pkg/mail.Sender` interface + `FAAS_MAIL_TRANSPORT` selector; fail-closed boot on missing credential (D5); fail-closed boot on unset/unknown transport in production (issue #246 extension); bounce / complaint webhook ingress at `POST /v1/webhooks/resend` (Svix HMAC + dedupe) → suppression list + dunning CAS; List-Unsubscribe (RFC 8058) on the quota-warning template only. Operator dry-run via `gregale mail dry-run`. | M7 — **Closed by mail-production-ready mega-PR (issue #246).** |
| G5 | **CLI auth UX** — `faas login` undefined | v1: browser-paste flow (dashboard shows key, CLI stores in OS keychain). OAuth device flow later | M5 — **Closed by PR #380 (issue #293):** keychain primary (macOS Keychain / Linux libsecret via D-Bus / Windows wincred via `github.com/zalando/go-keyring`); plaintext-file fallback retained for headless hosts with a WARN recommending `gnome-keyring`; one-shot legacy-file migration on first successful keychain save. |
| G6 | **GDPR self-serve** — export + delete endpoints absent (policy exists in founding doc, mechanics don't) | `GET /v1/account/export` (JSON bundle) and `DELETE /v1/account` (dunning-style staged deletion, 30-day grace); DPA template in docs site | M8 |
| G7 | **Long-lived connections vs idle reaper** — an app holding one websocket never parks | Rule: open connections count as activity (reaper checks conntrack for the instance); document that persistent connections bill as resident GB-h — the meter already handles it correctly | M4 (one line in schedd) |
| G9 | **Reactive scale-up** — autoscaling today is request-driven wake + idle reaper; no signal-driven admission means a customer must pre-allocate `min_instances = N` to handle burst traffic and pays for N resident instances even at idle | **RESOLVED (ADR-037, PR for #169 + #172):** per-app `autoscale_target_rps` (Hobby/Pro/Scale) and `autoscale_target_cpu_pct` (Pro/Scale) trigger pkg/sched/scaleup every 1s; when measured per-instance RPS / CPU exceeds the target, schedd admits another instance up to plan.MaxConcurrency. CPU source is pkg/sched/instancestats.Reader (PR #205); RPS source is `gatewayd-internal`'s local /metrics. The trigger is read-only on the ledger — it cannot bypass the cap. "Enabled" is inferred from non-null targets, no separate boolean. | M7 (preferences: between PR #205 and PR #171; this PR ships the trigger, both are follow-ups) |
| G10 | **Per-instance observability gap** — schedd cannot see CPU/RSS/inflight/last-request per VM; #171 reaper and #169 scale-up trigger are blind | **PR-A (issue #170, ADR-036) lands the read-only surface:** schedd polls per-instance at 5 Hz, populates an in-memory `instancestats.Reader` for future scale policy code, emits `{app,node}`-rolled-up gauges. **PR-B (deferred)** populates the vmmd side: `ActivityTracker` keyed by instance id, `pkg/vmmdgrpc/stats.go` extracts the Stats handler, `pkg/vmmdgrpc/forward.go` wraps the bridge call in `Begin`/`defer done()`. PR-B is a hot-path mutation on vmmd's forward — kept behind a separate PR boundary for review hygiene. #171 and #169 read from the Reader in subsequent PRs | M8 |
| G11 | **Webhook signature replay** — GitHub/Stripe/Paddle webhooks verify HMAC but never check the delivery UUID against a dedupe table; a replayed webhook within the signature-tolerance window runs the handler side-effects twice | **RESOLVED (ADR-042):** shared `webhook_deliveries` table (provider + delivery_id, 5-min TTL), `pkg/webhookdedupe` helper consumed by all three ingresses, single sweep goroutine in apid, `webhook.replay_rejected` audit row on each ingress, 200-on-replay response (idempotent — provider interprets as success and stops retrying). Closes issue #294 | M7 (tier-1 security) |
| G12 | **Server-side session revocation (IAM-3)** — dashboard `faas_sid` is a stateless AES-GCM envelope (no per-session id); leaked cookies are valid for 7 days, and the only mitigation is rotating `FAAS_SESSION_KEY` which logs out every customer on the box | **RESOLVED (ADR-039, issue #187 + #244 merged):** `sessions` table with one row per dashboard login, `sid` claim in the envelope, four new routes (`POST /v1/auth/logout`, `GET /v1/auth/sessions`, `DELETE /v1/auth/sessions/{id}`, `POST /v1/auth/sessions/revoke_all`). Per-session revocation via the row's `revoked_at`; sibling independence pinned (revoking sibling leaves caller valid). Pre-IAM-3 cookies (empty `sid`) fail closed as 401 `CodeSessionExpired` to force re-login — load-bearing acceptance for the rollout. Lands before PR #2 (email + password) so password reset can invalidate cookies on other devices. SOC 2 CC6.2 + CC6.3 compliance drivers; audit kinds `auth.session.created`/`auth.session.revoke`/`auth.sessions.revoke_all`/`auth.session.stolen` (the last is the operator signal for revoked-cookie replay, distinct from the silent missing-row case). Bearer API keys bypass entirely. | M7 |
| G13 | **Stateless contract unenforced** — the platform is year-one stateless-only but a customer can `faas deploy` a `postgres:16` base image, a Dockerfile with `VOLUME /data`, or a tarball with a top-level `data/` directory and only discover the data is gone after the first park. Today nothing in apid or imaged encodes "this platform is stateless" | **RESOLVED (Wave 0 PR-A, no ADR — the architecture is unchanged, we add a check):** new RFC 7807 code `CodeStatelessOnlyViolation` (422, mapped in `pkg/api/errors.go::StatusForCode`), apid tarball scan rejects `VOLUME`, `mkfs.ext4/xfs`, `mount -t ext4/xfs`, and top-level `data/`/`db/` at accept time (before any build slot is consumed), and imaged's `buildImageLayer` consults `pkg/imaged/base.go::StatefulDenyListMatch` against the resolved OCI ref (8 well-known stateful bases: postgres / redis / mysql / mariadb / mongo / cockroach / cassandra / clickhouse). Both paths feed `pkg/oci::SentinelToCode` → `deployments.error_code`. PR-B adds `faas init --template={s3-uploader,slack-bot,rest-api-postgres,cron-worker}` to teach the pattern. **PR-C (ADR-047) under review (draft PR #421):** guest-init `fanotify` advisory on runtime writes to state-shaped paths (`/data`, `/db`, `/var/lib/postgresql`, etc.) shipped over AF_VSOCK DGRAM (`port=1025, msg_type=2`, distinct from `VsockResumePort=1024`) to the host, debounced 1s per `(path, mask-set)`. vmmd forwards each batch to apid over a new `/run/faas/apid.sock` gRPC seam (`api/proto/onebox/faas/apid/v1/advisory.proto`), which writes one `stateless.advisory` audit row via `pkg/audit.Auditor.Emit`. Surfaced at `GET /v1/audit-events?kind_prefix=stateless.advisory` (plus `?app_id=` and `?include_anonymous=` for the dashboard drill-down) and the new `db.NotifyStatelessAdvisory` SSE channel. Customer surface: `faas audit-events [--kind-prefix=…] [--app-id=…] [--include-anonymous]`; `faas tail --include-stateless`; dashboard `/dashboard/audit-events` (+ app_detail.html "Stateless advisories" link). Advisory-only — spec §17 G13 explicitly forbids EROFS for Wave 0. vmmd becomes a gRPC client for the first time; default-local stays DB-less (Manager.advisoryClient == nil is a no-op). | M8 |
| G14 | **Account is the only tenant — no org / team abstraction** — `accounts.id` owns resources, carries the plan and provider identifiers, scopes gateway limits, and drives scheduler / metering admission. A 5-engineer team has no way to share a single billing identity, role-gate access, or rotate a colleague's key without nuking the host session key. Sales stalls at the first team-sized customer | **RESOLVED (ADR-061, issue #190 / IAM-6, 9-PR staged rollout across 5 migration phases: expansion, backfill, dual-write, cut-over, contraction):** introduce an `orgs` table as the tenant for ownership, plan, quota, billing, and audit. Keep `accounts` as a human authentication identity that belongs to one or more orgs. Path-scoped APIs under `/v1/orgs/{org_slug}/...`, automatic personal-org compatibility for every account, and one existing plan/quota pool per org. Paid per-seat charging and SSO are deferred to follow-up issues. Each `Limits` row gains `OrgMembersMax` and `OrgPendingInvitationsMax` (PR 1 ships 0/0 fail-closed; **PR 2 populates from the existing per-plan ladder — Free 0/0 (gate closed at the abuse-floor tier), Hobby 10/5, Pro 50/25, Scale 200/100, with the fail-closed `active >= limit` guard as the load-bearing invariant; the open follow-up is reconciliation against `ex44_faas_financial_model.xlsx` once the workbook is updated**). Twelve new RFC 7807 codes (`org_not_found`, `org_slug_invalid`, `org_slug_taken`, `org_member_cap_exceeded`, `org_invitation_cap_exceeded`, `org_role_forbidden`, `org_already_member`, `org_invitation_invalid`, `org_invitation_expired`, `org_last_owner`, `org_personal_immutable`, `org_api_key_requires_org`) added to `pkg/api/errors.go` with full `StatusForCode` mapping. Shared `OrgSlugPattern` constant exported for PR 5's handler validator. Roles: `owner`, `admin`, `developer`, `viewer`, `billing` — exactly one owner per non-personal org; ownership transfer is the only way to vacate the role. Personal orgs are immutable. Cookie sessions remain account-bound; org selection is per-request via URL. Detailed ownership inventory checked in at `docs/iam-6-ownership-inventory.md` | M8 (IAM-6 PR 1–9) |
| G15 | **Per-deployment auth is opt-in, not default (issue #560, scope-limited)** — every `{slug}.apps.gregale.dev` route is public-by-default. Customers deploying internal APIs, B2B services, or anything with PII must `PATCH require_authn=true` on every app; the global default is unchanged to avoid breaking existing customers. Out of scope for this PR: (a) flipping the global default to `true` and migrating every existing app to opt-out (different migration problem; ADR-061-style dual-write window required), (b) OAuth / IAP / SSO in front of the gateway (orgs/teams story, see G14), (c) mTLS for customer→gateway (terminated by the platform cert; per-app client certs is a customer-runtime concern). | **PARTIAL (issue #560, this PR):** new `apps.require_authn` column default `false`, per-app PATCH/CLI, gatewayd-internal authz branch, three audit kinds. **Open follow-ups:** (a) global default flip + grand-father migration, (b) OAuth/IAP via the post-G14 org identity, (c) per-app mTLS option (decide with §11 ship-blocking review). | M8 follow-up |
| G16 | **Spend cap is advisory, not workload-pausing (issue #561)** — `accounts.overage_cap_cents` (storage layer #279, `migrations/00054_account_credits.sql`) stopped meterd from pushing overage rows once the cap was reached, but `schedd` kept waking live instances and serving requests. Customers who set a spend cap expected Cloud-Run-style "budget pauses workload" behaviour. Issue #279 storage stayed unchanged; issue #561 layers the workload-side gate. | **RESOLVED (issue #561, this PR):** `schedd.Engine.admitGate` consults `pkg/sched/OverageChecker` (5 s TTL cache, fail-open on transient PG errors; meterd's quota loop is the safety net for sustained outages). When the cap is reached, the wake path lifts to `CodeAdmissionRefused` (`pkg/api/errors.go`) — HTTP 402 on the gateway edge, `FailedPrecondition` on the gRPC ingress. Existing live instances are **not** auto-parked by the cap alone (that path lives in `pkg/meter/quota.go::EnforceQuota` case "stop", out of scope for #561). Customers manage the cap via `POST /v1/account/overage-cap` (account-self-scoped) or the dashboard `Spend cap` form (CSRF-envelope POST `/dashboard/raise-overage-cap`). Audit row `overage.cap_reached` is emitted the first time each (account, UTC day) the gate refuses; UTC-day dedupe chosen over the issue body's calendar-month wording to prevent audit-table flooding. Cache invalidation via TTL only — explicit pg_notify-driven invalidation is a follow-up if 5 s proves too coarse in practice. | M7 (M7.5 follow-up) |
| G17 | **Wake-boot telemetry is blind** — `wake.boot_started` / `wake.boot_completed` events carry no info on *why* an instance started or what was queueing when it admitted. Cloud Run's per-cold-start `trigger` field has no Gregale analog; operators investigating Hobby-tier latency regressions cannot distinguish a request-driven cold-boot from a cron fire from a scale-up | **RESOLVED (ADR-123, this PR + PR-A follow-on):** additive fields on `BootStarted` + `BootCompleted` payloads — closed `trigger` enum (`gateway` / `floor` / `floor.deployment` / `scaleup` / `targets` / `cron.schedule` / `cron.manual` / `meterd`, see `pkg/sched/triggers.go`), `queued_count` (`ledger.Concurrency(app.ID)` at admit), `concurrency_at_admit` (same reading; 0 is cold start), `at_capacity` (true when `admitGate`'s `wakeAdmit` branch observed `concurrency+1 >= maxConc`, PR-A), `ready_in_ms` (`boot_completed.at - boot_started.at` via LEFT JOIN LATERAL, PR-A). Stamped via new `bootInput` fields + new `trigger string` parameter on `Engine.Wake` / `Engine.AdmitInstance` / `Engine.AdmitInstanceForDeployment` / `Engine.EnsureWake` / `scheddgrpc.Server.{Wake,AdmitInstance,EnsureWake}` / `gateway.Backend.Admit` / `gateway.Scheduler.{AdmitInstance,EnsureWake}` / `scheddgrpc.Client.{AdmitInstance,EnsureWake}`. Gated by the existing `events_wake_id_idx` partial index (migration 00114); no new migration strictly required (the optional analytics index is deferred). Surfaced via the existing `/v1/apps/{slug}/wakes/{wake_id}/timeline` endpoint, the CLI `gregale wake-timeline` (new `triggers: …` histogram header + per-event `trigger=… q=N c=N` context line), the dashboard "Recent wakes" table on `/dashboard/apps/{slug}` (five columns: Trigger / Queued / Concurrency / At cap / Ready), the new dedicated `/dashboard/apps/{slug}/wake-timeline` per-app wake-narrative page (24h summary card + 50-row table, PR-A), and the wake-boot telemetry pre-fill on `RecentInstanceItem`. Wire is additive per ADR-016 — pre-ADR-123 schedd + gateway pairs observe byte-identical behaviour | M8.5 |
| G18 | **`gregalectl` operator CLI surface drift** — the operator-only CLI (`cmd/gregalectl/`, 15 top-level commands, 44 leaf verbs) shipped per issue #911 / ADR-110 PR-6.5 is referenced by `Makefile` (build-clis, manifest-ansible, metal-lima-splitbox), 5 ansible roles (`control_plane_service`, `vmmd_service`, `log_archive`, `compute_only_service`, `githubd_service`, `builderd_service`, `firecracker`), `cmd/deployctl/upgrade.go` (4 verbs), `scripts/pre-release-check.sh`, and 4 e2e suites — but had no spec row, no docs/ops coverage beyond ~5 verbs, and no CI-pinned wire-shape discipline. The pre-investigation audit found: `backup init` dispatched-but-unimplemented; `manifest ansible` referenced by Makefile but absent from `cli_meta.go`; `release kgv init` aliased-but-deprecated with no deprecation line; `host-age status` / `prune-previous` / `sign-keys status` / `sign-keys rotate` had explicit "future patch" TODOs for `--json`; `compute-nodes list|show` and `pki list` (--daemon filter) were missing read-only introspection leaves; the doctor had literal panics in argument parsing; the operator quickstart covered ~10% of the binary | **RESOLVED (this mega-PR, no ADR — no architecture change):** wired `backup init` (Cluster A1); added `manifest ansible` to `cli_meta.go` (A2); corrected the stale header comment (A3); added `release kgv init` alias with deprecation line (A4); added `--json` to host-age status/prune-previous + sign-keys status/rotate + `--keep-old-pub` archive (B1-B4); added `compute-nodes list|show --json [--active-only]` + `pki list --json [--daemon] [--box-role]` (C1-C2); replaced doctor panics with error-returning parse + defensive-default finding (E1); rewrote `docs/ops/gregalectl-operator-quickstart.md` to cover all 44 verbs across 8 lifecycle sections (D1); added `gregalectl checks` section to `docs/ci-required-checks.md` (D2); documented the gate matrix here (G18 row, D3). Drift guards `commands_completion_test.go::TestCompletion_ManifestDrift` (dispatcher ↔ cli_meta ↔ comment header) and `json_parity_test.go::TestJSONOutputHonored` (every `--json`-honouring dispatcher has a `_test.go` exercise) prevent regressions on future PRs | M8 |
| G19 | **Per-deployment protocol selector knob never reached the wire** — PR #1023 / ADR-124 landed the customer-facing `apps.app_protocol ∈ {http1, http2, grpc}` closed-set field (default `http1`), and `gatewayd-internal` stamps `x-faas-protocol` for downstream observability, but the framing the guest's `:8080` actually receives is still HTTP/1.1 + chunked regardless of the customer's choice. A customer running `app_protocol=grpc` gets their gRPC trailers back through the edge as plain `Trailer:` headers over H1+chunked; the in-guest server only sees H1. The reason is `cmd/vmmd-stream-bridge` (issue #686 / PR #750): it speaks H2C inbound from the vmmd side but **re-frames to H1+chunked** before talking to the guest at `10.0.0.2:<port>`. The framing transition is the load-bearing gap; ADR-124 was the customer-side knob, the inner-leg wire-shape follow-on was always a separate ADR | **RESOLVED (ADR-126, this PR):** the bridge grew a per-stream framing switch (`FAAS_BRIDGE_PROTOCOL ∈ {h1, h2c}` read per-request from env, mirroring `FAAS_STREAM_BRIDGE_VERSION` per ADR-028). `app_protocol=http1` rides the legacy H1+chunked path verbatim (zero behavior change for every existing customer); `app_protocol ∈ {http2, grpc}` rides the new H2C terminator (`handleH2CStream` in `cmd/vmmd-stream-bridge/h2c_terminator.go`) which originates HTTP/2 prior-knowledge frames to the guest via `golang.org/x/net/http2.Transport{AllowHTTP:true}`. Wire-additive proto bump per ADR-016 (`ForwardHTTPRequestInit.app_protocol = string`; `ForwardHTTPResponseInit.trailers` for gRPC trailer HEADERS forwarding). Guest-side listener opts into H2C via stdlib `srv.Protocols.SetUnencryptedHTTP2(true)` (Go 1.24+); every shipped runner (`go124`, `node22`, `node24`, `python312`, `python313`) routes through the shared `internal.ListenAndServeH2C` helper so the closed-set behavior is uniform. Two rollback switches: `FAAS_BRIDGE_PROTOCOL=h1` (per-vmmd surgical, per ADR-126 §Decision 7) and `FAAS_STREAM_BRIDGE_VERSION=v1` (wholesale shell-bridge fallback, pre-existing ADR-028 amendment). Snapshot invalidation is contained to the opt-in slice — every app adopting `app_protocol ∈ {http2, grpc}` must adopt the new h2c-capable base image; the `app_protocol=http1` slice stays valid forever. Coverage: `pkg/vmmdgrpc/forward_v2_internal_test.go::TestStreamBridgeEnv_AppProtocolWiring`, `TestAppProtocolToBridgeProtocol_ClosedSet`; `cmd/vmmd-stream-bridge/framing_test.go` (per-stream env lookup + hop-by-hop); `cmd/vmmd-stream-bridge/h2c_terminator_test.go` (unary + gRPC trailers + dial-failure); `guest/runners/internal/h2c_listener_test.go` (H2 prior-knowledge + H1 fallback); `cmd/e2e/bridge_h2c_terminator_e2e_test.go` (real bridge binary against a loopback H2C guest listener); metal sibling at `cmd/e2e/bridge_h2c_terminator_metal_test.go` documents the §14 M8 row 5 acceptance gates for `make test-metal` | M8.5 |
| G-Async-Retention | **Async result retention unbounded** — `pkg/sched/retention.go` only prunes `instances`; `invocations` and `trigger_records` rows accumulate forever. A Hobby tenant running 1 RPS of async triggers for a year ends up with 31.5 M rows, ~3 GB of dead data the dashboard has to scan past on every list. | **RESOLVED (ADR-135, this PR):** per-row `result_retention_until TIMESTAMPTZ` on `invocations` + `trigger_records`, customer-controlled via `InvokeRequest.RetentionSeconds` (clamped by `Limits.MaxAsyncResultRetentionSeconds` per plan: Free 86400 / Hobby 604800 / Pro 2592000 / Scale 7776000). Reapers: `pkg/sched/retention_invocations.go` 60s tick / batch 500 (terminal-state rows with `result_retention_until < now()`), `pkg/sched/retention_triggers.go` 300s tick / batch 1000 (terminal-state rows). Partial index `(account_id, result_retention_until) WHERE result_retention_until IS NOT NULL` keeps the reaper scan cheap. Emits `invocations_reaped_total` + `trigger_records_reaped_total` Prometheus counters. Default retention for unset rows: plan-max ceiling (customers opt down, never up). | M7 |
| G-Account-Concurrency | **No per-account async concurrency cap** — `MaxQueueDepth` was per-app (`apps.max_queue_depth`, plan-bounded), but a customer could register N apps each with `max_queue_depth=plan_max` and run N × cap concurrent invocations across the account. The financial model assumes one cap per account (the GB-h math depends on it) | **RESOLVED (ADR-134, this PR):** counter-table pattern `account_async_quota (account_id UUID PK, max_inflight INT, current_inflight INT DEFAULT 0, updated_at TIMESTAMPTZ, CHECK max_inflight >= 0, CHECK current_inflight >= 0)`. `pkg/sched.drain.ClaimInvocationWithCap` does `UPDATE ... SET current_inflight = current_inflight + 1 WHERE account_id = $1 AND current_inflight < max_inflight RETURNING ...` in the same tx as the claim — atomic, no TOCTOU. `DecrementAccountAsyncInflight` fires from every terminal transition (`CompleteInvocation`, `FailInvocation`, `CancelInvocation`) so the counter stays balanced even on crash + drain-loop restart. `EnsureAccountAsyncQuota` lazily creates the row on first invoke with `max_inflight = Limits.MaxAsyncInvocationsPerAccount` (Free 100 / Hobby 1000 / Pro 10000 / Scale 100000). On cap hit, the drain leaves the row in `state='pending'` and the next tick retries — no error returned to the customer until the call attempts to claim through a downstream gate. Plan matrix lives at `pkg/api/limits.go`; `TestPlanLimitsMatchSpec` is the gate | M7 |

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
GET    /v1/usage?month=YYYY-MM          per-app monthly mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes (CPU and egress informational; not billed)
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

> **Superseded by [ADR-019](adr/019-jailer-invocation-and-jail-resource-ownership.md).**
> The invocation above predates the first metal run and does not work on the
> pinned jailer v1.7.0: jailer requires `--exec-file <firecracker>` (which also
> names the chroot dir) and execs it itself, so nothing but firecracker's own
> flags may follow `--` (no positional `firecracker`). ADR-019 also defines the
> jail resource-ownership rules (`vmmd` stages read-only images `o+r`, copies +
> chowns the writable drive1 to the jailer uid). `pkg/fcvm` is the source of truth.

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

Every row is an experiment with a pre-committed pass threshold. Run V1–V5 on a single rented bare-metal x86_64 host (**total budget: one month's rent**) before M1 work begins; failures change plan quotas and §1 constraints, so they are cheapest to absorb now. Results get recorded next to the row; a failed threshold triggers an ADR, not a shrug.

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
| V10 | Server quote €44 + €39 | Re-quote a comparable bare-metal x86_64 host at order time (post-June-2026 price adjustment) | Within §9.9 sensitivity range (€39–50) | at order |

Standing rules: (1) no number graduates from "assumption" to "fact" without a row here; (2) the §6.2 invariants are enforced as property-based tests, not prose; (3) each ADR gets one adversarial review pass before acceptance.

*End of spec. Deviations require an ADR. Keep the three fragile numbers on the dashboard.*
