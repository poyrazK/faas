# Status

Spec §14 milestones M0 → M8. The README has the one-line version;
this file is the long form (which PR closed which issue, what each
milestone actually shipped, what's left on the board). Update this
when a milestone lands — readers coming from the README land here
for context.

## M0 — repo scaffold. ✅

Repo tree, build/test/lint tooling, CI, `pkg/api` limits table,
8-role ansible bootstrap, hello-boot acceptance test. `make bootstrap`
gates it on a fresh bare-metal x86_64 control-plane node (reference deploy:
the original Hetzner EX44).

## M1 — vmmd core. ✅

Invariant-critical VM lifecycle: slot allocator (`pkg/fcvm`),
per-instance netns/TAP (`pkg/netns`, ADR-009), cold-boot config +
jailer argv (Appendix B / ADR-019), `Manager` with no-leak unwind,
metal layer (`manager_metal_test.go`), and the 5-RPC gRPC surface
at `/run/faas/vmmd.sock` (ADR-013/014/016, `pkg/vmmdgrpc`). KVM +
root required for the metal gate.

## M2 — imaged + guest-init. ✅

OCI→app-layer pipeline, two-drive scheme (`pkg/oci` diff + `pkg/rootfs`
applier), base→ext4 auto-stage (`pkg/imaged::EnsureBaseExt4`), real-mkfs
build in Linux CI, `guest/init` overlay + crash supervisor, two-drive
boot verified metal-side (`cmd/e2e/deploy_wake_metal_test.go`).

**Fixture follow-up:** the body/trim mismatch originally flagged in PR #55
was resolved by PRs #151, #159, #135; `deploy_wake_metal_test.go` is now
exercised by the M8 netns + egress test path. Reference-node + Lima sign-off
on the §14 metal acceptance gate is tracked under [What's next](#whats-next).

## M3 — snapshots + wake. ✅

Park/wake with the ADR-005 restore-or-cold-boot fallback, FC version
pinning (`snapshots.fc_version`), and the vsock post-restore resume
hook (ADR-022) that re-seeds entropy + steps clock — V6 acceptance
green in `pkg/fcvm/v6_resume_ext4_metal_test.go`.

**Remaining:** §14 V2 latency loop driver (100 cycles, p50 ≤ 350 ms)
— see [What's next](#whats-next).

## M4 — gatewayd-public + gatewayd-internal + schedd. ✅

Routing, wake gate, admission ledger (47,600 MB headroom / 160 vCPU),
G7 flow-aware reaper (`pkg/sched/flowcount`), `PGBackend` PG routing,
schedd-over-gRPC (ADR-018), last-seen flush, 1k rps CI-asserted
hot-path load test (PR #44), per-VM `memory.max` + per-plan `tc`
egress (PR #37, closes #31 + #33).

## M5 — apid + deploy pipeline + CLI. 🚧

Production wiring is in via the pgx-backed `state.PgStore`, real
`rootfs.Builder` in `pkg/imaged::handleDeployment` (PR #26),
plan-quota table-tests (`cmd/e2e/quota_e2e_test.go`), the
snapshot-prime handshake that flips a deployment to `live` after
one cold-boot priming cycle, and the G2 sealed-secrets path
(PR #42); `faas` CLI renders RFC 7807 problems (UX §3.3).

**Fixture follow-up:** the body/trim mismatch flagged in PR #55
was resolved by PRs #151, #159, #135 (same fixture exercised by
the M8 netns + egress path). Reference-node + Lima sign-off on the §14
metal acceptance gate is tracked under [What's next](#whats-next).

**Beta ship-blockers landed** — PR #136 (`PR-A`, ship-blockers
for the beta cohort) and PR #154 (`PR-B`, atomic supersede +
durable build queue).

**Authentication surface rewritten** — PR #161 added Google
OAuth 2.0 (`cmd/apid/handlers_google.go`, routes mounted at
`cmd/apid/server.go:426-427`); PR #162 auto-creates the user
account and issues an active Bearer API key + session cookie
on signup; PRs #163 and #164 retired the stale magic-link login
email (the `/auth/verify` route remains in the codebase but is
no longer reachable through `/login`); PR #174 hardened
`POST /login` against pre-auth takeover
(`cmd/apid/handlers_auth.go:112`, closes #165).

**CLI SDK promoted** — PR #157 lifted `cmd/faas/client.go` to
`pkg/api/client.go` as the public SDK (38 exported methods
covering apps, deployments, plans, domains, crons, keys,
secrets, usage, and the OAuth device-code flow).

**CLI token now lives in the OS keychain (issue #293, closes gap G5)**
— `cmd/faas/config.go` writes through `github.com/zalando/go-keyring`
(macOS Keychain / Linux libsecret via D-Bus / Windows wincred); the
plaintext file at `~/.config/faas/token` is retained only as a
fallback for headless hosts with no D-Bus session (CI runners,
SSH-only servers), and a WARN recommends installing `gnome-keyring`.
First successful keychain save one-shot-deletes the legacy
plaintext file so customers do not keep a redundant copy on disk
after upgrading.

**Mutable per-app env vars (issue #395, ADR-045)** — three new
plaintext routes mirror `/v1/apps/{slug}/secrets` minus the seal step:

```
GET    /v1/apps/{slug}/env
PUT    /v1/apps/{slug}/env/{KEY}
DELETE /v1/apps/{slug}/env/{KEY}
```

A new `app_envs` table (migration 00061) stores plaintext `value TEXT`
under the same `^[A-Z][A-Z0-9_]*$` SQL CHECK that gates sealed
secrets. Migration 00063 widens `api_keys_scopes_vocab_chk` to admit
`env:read` / `env:write` (both ship in the same PR so the DB CHECK
never rejects a freshly-minted API key during the rollout window).
Per-plan quotas (`EnvVarsMax` 8/32/64/256 Free–Scale,
`EnvValueMaxBytes` 4K/8K/16K/32K) live in `pkg/api/limits.go`
alongside the secrets quota. Semantics: applies on next wake — no
snapshot invalidation, no new vmmd RPC. The wake path stages
`/etc/faas/env.json` next to the existing `/etc/faas/secrets.env`
sibling, and `guest/init::BuildEnvWithSecrets` gains a fourth
precedence layer with the order `OS environ < manifest env <
api env < secrets` (pinned by
`guest/init/app_test.go::TestBuildEnv_FourLayerPrecedence`). Audit
kinds `env.set` / `env.deleted` are distinct from `secret.set` /
`secret.deleted` so a sealed-secret audit row still means only
"credential change". The console Env Vars page becomes writable in a
separate `faas-frontend` PR — this PR ships the API surface only.

## M6 — builderd + real image pulls. ✅

Build-in-microVM is wired through (`cmd/builderd`, `pkg/builderd`
orchestration + executor, PRs #39/#40/#43); the metal lifecycle is
in `vm_metal.go` (`//go:build metal`) and calls vmmd over gRPC, with
`vm_stub.go` returning `ErrNotMetal` for non-metal builds. OCI
puller hardened (`pkg/oci/egress.go` — denied CIDRs cover RFC1918,
CGN, loopback, IMDS, ULA), streamed layer blobs. `cmd/imaged`
auto-stages the canonical `/srv/fc/base/runner-builder-<arch>.ext4` on startup.

Source-tarball staging + Dockerfile dispatch are in via PR #56
(closes #54): `pkg/builderd/drive.go::CreateBuildDrive1` copies
`VMRequest.SourcePath` into drive1 at `/build/src.tar` and re-stats
a sha256 against the host source to catch torn copies;
`pkg/builderd/dispatch.go::MapFramework` translates the host
`FrameworkDocker` enum into `api.FrameworkDockerfile` so guest-init
dispatches to `buildctl --frontend dockerfile` per ADR-004 instead
of falling through to Railpack-auto.

§14 orchestrator e2e closes M6 (PR #60, closes #57):
`cmd/e2e/build_metal_test.go` exercises the full chain
`apid → pg_notify('build_queued') → builderd → vmmd → firecracker
→ in-VM Railpack/buildctl → OCI image.tar → imaged →
deployments.Live` across three fixture paths (Node, Python,
Dockerfile). Reference-node sign-off remains the §14 source of truth per
CLAUDE.md.

## M7 — metering, billing, functions, cron. 🚧

The sampling/quota shapes are in `cmd/meterd` and
`pkg/billing/stripe`, the dunning state machine is
`pkg/state.MarkAccountDeletionPending` (ADR-021), GB-h = plan RAM
+ 8 MB per running second is in `pkg/meter`. Functions:
`guest/runners/{node22,node24,python312,python313,go124}` (handler
contract per spec §4.9; `go124` apps deploy with a static binary
emitted by Railpack's go plan, functions reuse the per-request
subprocess model. `node24` and `python313` are the Tier 1 runtime
additions — additive on top of `node22` / `python312` with the
same envelope contract; handler paths `/app/node24.js` and
`/app/handler.py` respectively. See `docs/runtimes/{node24,
python313}.md` for the per-runtime detail and ADR-052 for the
canonical procedure to add a runtime. PR #373 (cron limits), PR
#423 (Move 4 — async-invoke) etc. Cron: `pkg/sched/cron.go`,
single-flight per scheduled fire, loop-tested in `cron_loop_test.go`. Cron caps (per-app
and per-account, Free gated to 402) live in `pkg/api/limits.go` and are
enforced by `apid`'s `createCron` under an apps `FOR UPDATE` row lock
(mirrors `CreateAppIfUnderQuota`); store-side check at
`pkg/state.PgStore::CreateCronIfUnderQuota`. Email:
`pkg/mail` interface with Resend + Postmark backends (gap G4).

**Billing-provider extraction (PR #155)** — the Stripe
implementation moved from `pkg/stripex` to `pkg/billing/stripe/`
and the `billing.Provider` interface is defined at
`pkg/billing/provider.go:39`. PR #173 later added the 5th method
`CreateUpgradeTransaction` to the interface.

**Paddle MoR provider (PR #158)** — `pkg/billing/paddle/` ships
with HMAC webhook verification (`pkg/billing/paddle/webhook.go`)
and an overage accumulator (`pkg/billing/paddle/usage.go`).

**apid + meterd dispatch (PR #173)** — both daemons now route
through `billing.Provider` via
`pkg/billing/loader/loader.go::LoadProviderForAPID` /
`LoadProviderForMeterd`. Operator selects via
`FAAS_BILLING_PROVIDER=polar`; empty selects the Polar public-release
provider. Paddle and Stripe remain explicit compatibility paths. apid also mounts
`/v1/webhooks/polar` with Standard Webhooks verification. ADR-032 records the
decision; the operator runbook is
`docs/ops/billing-provider-switch.md`.

**Webhook signature replay protection (PR for issue #294, ADR-042)**
— the three webhook ingresses (GitHub via `gatewayd-public`, Stripe + Paddle
via apid) verify HMAC but never checked the delivery UUID against a
dedupe table; a replayed webhook within the signature-validity
window succeeded twice. Closes the gap with a single shared
`webhook_deliveries` table (provider + delivery_id, 5-min TTL), a
`pkg/webhookdedupe` helper consumed by all three ingresses, one
sweep goroutine in apid, and a `webhook.replay_rejected` audit row
on each ingress. Replays return 200 (idempotent — provider
interprets as success and stops retrying). New `gatewayd-public` audit seam
mirrors the apid pattern.

**§14 M7 acceptance test (24h GB-h shadow, integer-arithmetic exact)**
— landed via PR #126. See
`pkg/meter/meter_test.go::TestInvoiceShadow24h` (local math),
`pkg/meter/pusher_shadow_test.go::TestPushHour_Shadow24h`
(push-side integer equality), and
`pkg/billing/stripe/sandbox_test.go::TestInvoiceShadow24h_Sandbox`
(live Stripe SDK — asserts `record.Quantity == 6187` exactly,
zero delta). Cadence switched from per-hour float (`qty =
int64(gbHours * 1000)`, 0.315 % short over 24h) to per-day
integer (`qty = mbSeconds * 1000 / 1024 / 3600`). The
`pkg/stripex/` directory no longer exists post-PR-#155 rename.

**Idempotent billing + observability surface (PR #75, not #71)**
— `usage_minutes` flipped to `ON CONFLICT DO NOTHING` and a parity
test was added for the shared `BillableRAMMB` helper. `cmd/meterd`
also got `/metrics` via `wire.NewOpsMetrics("meterd")`
(`cmd/meterd/main.go:256/278/285`) and an inline `/healthz`
(`cmd/meterd/main.go:293`). *(Earlier revisions of this file
attributed both to PR #71; that's the CLI-only `feat/m7-beta-hardening`
PR — corrected.)*

**M7 customer email coverage (PR #133)** — dunning entry /
recovery and quota-warning bodies in `pkg/mail/account.go`:
`PaymentFailedBody` (line 131), `AccountSuspendedBody` (96),
`AccountRestoredBody` (169), `QuotaWarningBody` (205). apid's
webhook handler fires `PaymentFailedBody` / `AccountRestoredBody`
on the success branch of `MarkDunningStep`; meterd's quota loop
fires `QuotaWarningBody` alongside `db.NotifyQuotaWarning` on the
first warning of each UTC day (dedupe gate at
`accounts.last_quota_warning_at`).

**Paddle e2e (PR #173)** — `cmd/e2e/paddle_e2e_test.go` exercises
three flows: signed `transaction.paid` on past_due → active within
5 s; signed `transaction.payment_failed` on active → past_due
within 5 s; bad HMAC → 400 with `validation_failed` problem.

## M7.5 — waitUntil post-response tail primitive (issue #667). ✅

`ctx.waitUntil(promise)` lands as a first-class guest-init
primitive, gated on ADR-078. PRs 1+2+3 of the issue ship as
PR #671 (commit `add1105f`); PRs 4+5+6 ship as one consolidated
PR covering the wire-up: schedd reaper gate, `AppendUsage
tailSeconds`, meter sampler/rollup, the three new `pkg/wire`
metrics, the permanent `TestPushHour_ExcludesTailSeconds` guard
test, the docs fixes below, and the metal acceptance test.

- **Wire:** 16-byte DGRAM on vsock port 1027 lead byte `0x04`
  (`[type][outcome][reserved 6B][elapsed_ms BE uint64]`); instance
  resolved from peer CID — the runner↔guest-init seam is the
  in-process WaitGroup + per-task `context.WithTimeout` draining a
  `tail_pipe_path` JSONL file the handler subprocess appends to.
  Reuses the existing 0x01/0x02/0x03 discriminator + port 1026
  host receiver. No new port, no new CID, no new gRPC RPC.
- **Reaper/park gates:** `ReapIdle` and `ReapAggressive` skip
  instances with `tail_count > 0` (mirrors the G7 OpenConns gate);
  `snapshotAndPark` installs a 5 s watchdog
  (`ParkTailDrainTimeoutSeconds = 5`) before snapshotting, and
  force-parks with a `wake.tail_failed{reason=forced_at_park}` audit
  row on watchdog fire.
- **Migration 00151:** `instances.tail_count integer NOT NULL
  DEFAULT 0` + `usage_minutes.tail_seconds bigint NOT NULL
  DEFAULT 0`. Replay-safe. Companion test
  `00151_wait_until_tail_test.go` pins the column types + the
  non-negative floor on `DecrementInstanceTailCount`.
- **Limits matrix (already in main from PR #671):**
  Free 5 / 16 / 4, Hobby 15 / 16 / 16, Pro 30 / 16 / 64,
  Scale 60 / 16 / 256 (TailTimeoutS / TailCapMax /
  ConcurrentTailsPerInstance). `TailCapMax = 16` is structural
  (the issue's hard ceiling); the per-plan
  `ConcurrentTailsPerInstance` controls how aggressive the cap is
  across concurrent requests.
- **Metrics (issue #667 / ADR-078):**
  `vmmd_guest_tail_seconds{plan, runtime, outcome}` (60 series),
  `vmmd_guest_tail_failed_total{plan, reason}` (16 series),
  `vmmd_tail_cap_reached_total{plan}` (4 series). All closed-set
  labels pre-instantiated at boot — the §12 tail-watchdog panel
  has zero rows from idle fleet and non-zero as soon as the first
  tail fires. Cardinality pinned by
  `pkg/wire/metrics_test.go::TestOpsMetrics_GuestTailSeconds_PreInstantiated`
  + siblings.
- **Informational only — load-bearing invariant:** `tail_seconds`
  MUST NOT enter `Math.GBHours`, `Provider.PushUsageRecord`, or
  any billing path. The permanent guard test
  `pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds`
  pins this — removing it requires removing ADR-078 §"Tail is
  informational" (a new ADR). The financial model and the
  customer-facing bill shape are unchanged.
- **Acceptance gate:** `pkg/fcvm/tail_metal_linux_test.go` (//go:build
  metal && linux) exercises the full path — handler returns, runner drains
  the tail, schedd park path observes the post-drain `tail_count
  == 0`, snapshot taken. Run with `make metal-lima RUN_ARGS='-run
  TestMetal_TailEndToEnd'`. `TestPushHour_ExcludesTailSeconds` and
  the cardinality tests run on every `go test` invocation and are
  the load-bearing pre-metal pre-merge checks.
- **Acceptance-checkboxes #6, #7, #8 closed (issue #667 follow-up, PR
  feat/issue-667-followup).** The original PR shipped the envelope
  shape + host-side receipt handler + schedd reaper gate, but
  deferred the runner-side tail host to this follow-up. Without
  it, customers could stamp `envelope.WaitUntilSec` and serialize
  it to JSON, but no runner acted on it. The runner-side tail host
  (`guest/runners/internal/tail_host.go`) plus the new
  `/run/guest-init/tail-events.sock` unix-domain proxy
  (`guest/init/tail_events_proxy_linux.go`) close the loop. **#6** =
  pathological tail killed at the ceiling
  (`guest/runners/internal/tail_host_test.go`, 6 unit tests pinning
  the TailHost surface). **#7** = cross-runtime parity
  (`guest/runners/internal/runnerparity/tail_host_pin_test.go`
  file-walk + 5 per-runtime `TestHandle_WaitUntilEnvelopeRoundTrip`
  tests). **#8** = metal end-to-end
  (`pkg/fcvm/tail_metal_linux_test.go::TestMetal_TailFullEndToEnd`
  fires a REAL 0x04 DGRAM via vsock to `VMADDR_CID_HOST:1027` and
  pins the wire encoding). Behavior change: `signal.SignalReady(...)`
  now fires AFTER the tail host's `Drain()` returns (not on the first
  non-5xx response as the original ADR described). The 5 s
  `snapshotAndPark` watchdog is the hard ceiling — if the tail host
  hangs, the park gate fires `tail_failed{reason=forced_at_park}`.
  Metal run verified on Lima nested KVM. ADR-078 §"Amendment"
  documents the change.

## M7.6 — extracted Next.js dashboard + githubd. ✅

The frontend was extracted to the dedicated `faas-frontend`
repository by PR #160 (commit `42814d6` deleted `website/`). The
Go `html/template` dashboard described in earlier revisions of
this file is historical — see the external repo for the shipped
implementation. `pkg/dashboard` may remain in this repo for
backward compatibility but is not the production frontend.

`pkg/githubd` + `cmd/githubd` still live in this repo and provide
HMAC-verified webhook ingress, GitHub App OAuth + repo picker,
Checks-API status writer, and a per-install token cache with
proactive refresh. The production auth surface (Google OAuth +
dual Bearer / session cookies, `POST /login` takeover hardening,
retired magic-link machinery) is described under [M5](#m5--apid--deploy-pipeline--cli-)
via PRs #161–#164 and #174. SSE live updates on `/v1/events` and
`deployment_logs` persistence landed via PR #41, ADR-011,
ADR-012.

## M8 — hardening & ops. 🚧

All §11 ship-blockers and §12 ops surfaces from this milestone's
closeout are in via PRs #46 / #47 / #48 / #49 (G6 GDPR + 30-day
staged deletion per ADR-021; V6 vsock resume hook per ADR-022;
G7 flow-aware reaper in `pkg/sched/flowcount`; `AuthLimit` shared
per-IP bucket across `/v1/*` per §11 "10/min/IP"; per-VM cgroup
scope via jailer `--cgroup cpu.weight`; cold-wake UX surfaces
3+4+5 with `x-faas-wake: cold|cache|ready` and dashboard N+1
spinner) and PR #51 (the closeout batch):

- **§11 IPv6 egress** — `pkg/netns/policy.go` and
  `pkg/netns/config.go` now deny `fe80::/10, fc00::/7, ff00::/8,
  ::1/128, ::/128` via `ip6 daddr { … } drop` (ADR-023), in both
  the host firewall and the per-instance netns ruleset. Closes #32.
- **§11 cgroup fence verified** — #33 `memory.max = plan + 8 MB`
  after bringUp; unit tests in `pkg/fcvm/cgroup_test.go` green;
  metal test in `pkg/fcvm/manager_metal_test.go::TestMetalMemoryMaxFenceEnforced`
  runs on a reference control-plane node (`make test-metal`) and Lima (`make metal-lima`),
  not on a bare dev box.
- **§12 SLO dashboard pipeline** — `fcvm_snapshot_fleet_avg_bytes`,
  `fcvm_snapshot_fleet_p95_bytes`, `fcvm_resident_ram_pct`,
  `fcvm_lv_fc_used_pct` (schedd-owned), plus
  `vmmd_cold_boot_fallback_total` (vmmd-owned, ADR-016) and
  `gateway_wake_queue_wait_seconds` (`gatewayd-internal`-owned; legacy Prometheus job label `gatewayd` retained). Prometheus
  + node_exporter are ansible roles with SHA-256-pinned binaries,
  scrape config template at
  `deploy/ansible/roles/prometheus/templates/prometheus.yml.j2`.
  Grafana dashboard export at `deploy/grafana/faas-fleet.json`.
  **Build metrics (ADR-030):** `builderd_ops_total{op="build",code}`,
  `builderd_build_duration_seconds{outcome}`, `builderd_build_queue_wait_seconds`
  now emit from the build lifecycle, and `apid /status` computes the
  build-success SLO from real build data instead of the old vmmd
  cold-boot proxy (which measured wake, not build).
- **§12 public status page** — `apid` serves `GET /status` (static
  HTML, `deploy/statuspage/index.html`) and `GET /status/slo.json`
  (4 PromQL queries against the local Prometheus with a 30 s
  in-process cache and graceful degradation on transient failures;
  never 5xx the route). The fourth query drives the `degraded` flag
  surfaced by the alert pipeline — see
  [M8 — alert pipeline](#m8--alert-pipeline--this-pr) below.
- **§14 restore drill wired** —
  `deploy/scripts/faas-m8-restore-drill.sh` plus WAL-archiving
  knobs in the postgres ansible role. A timed reference-node run (PG + one
  app back serving < 30 min) is the next action; the dated record
  file `docs/drills/2026-07-20-restore-drill.md` is the template.
- **`leakcheck.sh` glob fix** matches the v1.7 jailer `--id`
  constraint.
- **CPU-hour visibility shipped (issue #279 / PR #346 / ADR-039)** —
  per-instance CPU consumption is now exposed end-to-end:
  `schedd_instance_cpu_seconds_total{app,node}` (sum rollup,
  monotonic, regression-guarded), `usage_minutes.cpu_usec` (new
  column, additive `ON CONFLICT` merge; `mb_seconds` retains
  first-write-wins), `GET /v1/usage`, `/v1/usage/summary`, and
  `/v1/account/export` all expose `cpu_usec` / `used_cpu_hours`,
  and `faas usage` shows a CPU panel. **Informational only — no
  billing change.** `pkg/billing/provider.go`, `pkg/api/limits.go`,
  and the financial model are explicitly untouched. The data
  path is the seam for the future billing PR (extends
  `Provider.PushUsageRecord` with `cpu_usec`).
- **Egress-byte visibility (issue #<TBD> / PR #<TBD> / ADR-046) —
  in progress** — per-instance customer egress is sampled from the
  kernel byte counter on root-side `vethHost.rx_bytes` (vmmd
  `pkg/fcvm/netstats.Cache` reads
  `/sys/class/net/<vethHost>/statistics/rx_bytes`, cumulative
  with regression-safe deltas) and from the gateway HTTP response
  writer (`pkg/gateway/handler.go:statusRecorder.Bytes`). Both
  accumulate additively in `usage_minutes.net_tx_bytes` (vmmd)
  and `usage_minutes.tx_bytes` (gateway). `usage_monthly` sums
  both columns; `GET /v1/usage`, `/v1/usage/summary`,
  `/v1/account/export`, and `faas usage` expose the per-(account,
  app, month) totals; `pkg/appmetrics` rolls up
  `gateway_response_bytes_total{app,plan}` for the dashboard.
  **Informational only — no billing change.**
  `pkg/billing/{provider.go,stripe,paddle}`,
  `pkg/meter/pusher.go`, `pkg/api/limits.go`, plan limits, the
  Stripe/Paddle `gb_ram_hour` push shape, and the financial model
  remain untouched. The columns are the seam for the future
  egress-billing PR (extends `Provider.PushUsageRecord`).

**Networking & egress (PRs #128, #151, #159):**

- **PR #151 (tier-1 tenant egress)** — `pkg/netns/policy.go:77`
  adds `MasqueradeCIDR` (default `10.100.0.0/16`, line 121);
  `Render()` emits a `postrouting` nat chain at lines 198–201 with
  `ip saddr <CIDR> oifname "eth0" masquerade`. The persistent
  `br-tenants` bridge is brought up by
  `deploy/ansible/roles/br-tenants-up/` before
  `nftables.service` / `vmmd.service`. Metal regression in
  `pkg/netns/policy_metal_test.go`. Closes #134.
- **PR #128** — `pkg/netns/config.go:124` installs the netns
  default route inline in `Config.SetupCommands`; the forward
  chain uses the `BridgeName = "br-tenants"` constant
  (`pkg/netns/config.go:21`). Pinned by
  `TestSetupInstallsNetnsDefaultRouteViaBridge` /
  `TestSetupInstallsDefaultRouteAfterAddressing`
  (`pkg/netns/config_test.go:128,155`).
- **PR #159 (tier-2 per-app outbound IP allowlist, ADR-031)** —
  `migrations/00029_app_egress_allowlist.sql` adds an
  `egress_allowlist cidr[]` column to `apps` (v4-only BEFORE-row
  trigger). State path: `pkg/state/pgstore.go:336,455,493`,
  `pkg/state/memstore.go:585,602`. Metal regression in
  `pkg/netns/allowlist_metal_test.go::TestMetalAllowlistRuleInstalled`.
  Per spec §7 the feature is gated to **Pro / Scale** plans.
- **ADR-119 per-app static outbound IP (BYOIP, Scale-only, single-node v1)** —
  `migrations/00303_apps_static_egress_ip.sql` adds a nullable
  `static_egress_ip INET` + `static_egress_ip_set_at TIMESTAMPTZ` to
  `apps`, with a `family(INET)=4` CHECK (IPv6 deferred) and a partial
  unique index `apps_static_egress_ip_key` defending against cross-app
  IP collision. State path: `pkg/state/types.go::App.StaticEgressIP` +
  `pkg/state/pgstore_apps.go::UpdateApp` clear/set sentinels; apid
  handler in `cmd/apid/handlers_apps_static_egress_ip.go` (env gate
  `FAAS_STATIC_EGRESS_IP_ENABLED`, plan gate `StaticEgressIPAllowed`).
  Wire: `pkg/sched/vmmclient.go::VMMClient.UpdateStaticEgressIP` +
  `pkg/sched/egress_drift.go::onAppChanged` fans the pg_notify
  through the same drift subscriber the egress allowlist rides. vmmd
  applies the patch via `pkg/fcvm/manager.go::UpdateStaticEgressIP`;
  the per-VM netns renderer (`pkg/netns/config.go::NftCommands`)
  emits a sibling MASQUERADE-sibling rule in the per-netns
  `postrouting` chain — `oifname <VethPeer> ip saddr 10.0.0.2 snat to
  <CustomerIP>`. Operator TOML bundle at `/etc/faas/egress/static_egress_ips.toml`
  is loaded at vmmd startup + on SIGHUP via
  `cmd/vmmd/egress_static_ip_bundle.go::watchStaticEgressIPBundleReload`
  (same SIGHUP pattern as the egress allowlist bundle). CLI surface:
  `gregale app <slug> static-egress-ip {show|set <ip>|clear}`. Metal
  acceptance gates in `pkg/netns/static_egress_ip_metal_test.go`
  (nft syntax check + `ifconfig.me` round-trip from inside the netns).

**Observability & dashboards (PR #156):**

- Self-hosted Grafana role at `deploy/ansible/roles/grafana/`
  (templates: `grafana.ini.j2`, `provisioning-dashboards.yml.j2`,
  `provisioning-datasources.yml.j2`; files: `faas-fleet.json`,
  `grafana-server.service`). Includes the M1/M2 fleet panels and
  the D1–D5 dashboard fixes per ADR-031. The Prometheus pipeline
  + alertmanager + status-page `degraded` flag (PR #140) feed
  these dashboards end-to-end.

**Security & acceptance gates (PRs #130, #149, #150, #152, #153):**

- **PR #130 — per-wake stable `wake_id`** — minted in
  `Engine.Wake` (`pkg/sched/engine.go:282`) and on the Prime path
  (line 665). Schema in `migrations/00028_instances_wake_id.sql`;
  v4 fallback metric `faas_wake_id_v4_fallback_total`.
- **PR #149 — OpenAPI spec gate** — `make spec-check` invokes
  `vacuum lint` with `VACUUM_VER := v0.29.10` (`Makefile:225`);
  CI installs the vacuum binary from a SHA-256-pinned tarball
  (`.github/workflows/ci.yml`, `spec-check` job).
- **PR #150 — `changePlan` Stripe subscription gate** —
  `cmd/apid/handlers_ext.go:671-705` returns a 402 Problem with
  `CodePayment` when `acct.StripeSubscriptionItem == ""` and the
  requested plan requires a Stripe upgrade. Closes #142.
- **PR #152 — per-IP `AuthLimit` restored across loopback**
  (closes #89). `pkg/middleware/authlimit.go::defaultClientIP`
  trusts `X-Forwarded-For` only when `RemoteAddr` is loopback;
  the `gatewayd-internal` pin is at `cmd/gatewayd-internal/proxy.go:215-222`.
- **PR #153 — §11 security-hardening e2e sweep**.
  `cmd/e2e/sec11_sweep_test.go` (4 tests:
  `TestSec11_AuthLimitPerIP_CrossProcess`,
  `TestSec11_ApiKeyHashedAtRest`,
  `TestSec11_UnixSocketOnlyDSN`,
  `TestSec11_HostKey0400_Required`) plus
  `cmd/e2e/sec11_host_linux_test.go` (5 host-side checks:
  cgroups-v2 unified, kernel ≥ 6.8, unprivileged user-ns disabled,
  unattended-upgrades security-only, nftables policy file
  in-sync).

The §14 M8 gates still on the board are listed in [What's next](#whats-next).

- **Active-passive standby topology (ADR-083, accepted 2026-08-16):**
  lex-min leader election over `compute_nodes.name WHERE active=true`; `pkg/gateway/leader`
  package (`ElectLeader`, `Leader`, `LeaderStore`); `StandbyState` gauge
  (`<prefix>_gateway_standby_state`, enum warming/warm/draining) +
  `ActivePassiveFailoversTotal` counter (`<prefix>_gateway_active_passive_failovers_total{outcome}`,
  outcomes: `dns_flipped` | `dns_stale` | `peer_unreachable` | `manual_drain`); probe
  timeout bounded by `HAFailoverProbeTimeoutMS = 500` (in `pkg/api/limits.go`); drain
  deadline bounded by `HADNSRecordStaleSeconds = 30`. Failure drill: `make ha-failover-drill`
  on two-node Lima fleet (`deploy/lima/faas-metal-2node-ha.yaml`); standalone runbook at
  `docs/runbooks/active-passive-ha.md`. Closes the Gate-A row "2nd box active-passive" in
  spec §14 M8.

## M9 — multi-box scale. 🚧

- **ADR-066** (Tier A5 cross-node live-instance migration, accepted 2026-08-07): four-phase handoff (Park → mint lease → `MigrateInstanceOwner` → ack), `schedd_live_migration_decisions_total{outcome}` counter, `apps.migrated_at` + `instances.migrated_at` stamped in the same transaction. Bundled with PR #509 (Tier A4 per-node schedd), PRs in the ADR-066 → 067 → 068 cluster.
- **ADR-062** (per-node schedd + async placement claim, accepted 2026-08-16): single-writer-per-host invariant survives multi-host deploys; `apid_control_plane_only` depguard in `.golangci.yml` prevents a control-plane path from calling a compute-only peer.
- **ADR-063** (snapshot de-localization, revised 2026-08-26; issue #1054): snapshots use the shared OCI backend as the authoritative transport, while each active node's vmmd asynchronously prepositions both restore blobs through a durable event-cursor plus `snapshot_replicas` queue. Origin metadata restricts new fan-out to the producer's region; wake placement prefers ready local replicas and retains on-demand restore/cold-boot fallback. The two-node ≤200 ms prepositioned-wake measurement and 100-cycle leak drill remain M9 acceptance work.
- **ADR-067** (migrating-instance watchdog, accepted 2026-08-16): 1 s ticker self-heals stuck `state='migrating'` rows that never committed (peer died mid-handoff, gRPC dropped, operator killed the new owner). The watchdog is the only writer that can move a row out of `migrating` without a peer commit.
- **ADR-110** (declarative split-box manifest, accepted 2026-08-16): versioned YAML + typed schema at `deploy/manifest/splitbox.yaml` + `pkg/manifest/`; SemVer `schema_version (1.0.0)`; canonical validation through `gregalectl manifest validate` + the renderer + the release bundle installer + the doctor + the metal harness. PR-cluster shipped (PRs #912 #913 #914 #915 #917 #918 #919 #920 #921 #922 #923 #924).
- **ADR-141** (durable imaged→apid audit delivery, accepted 2026-09-03): migration 00590 adds a deduplicated `audit_event_outbox`; imaged keeps `pg_notify` as the fast wakeup, while apid transactionally writes the audit row and replays pending or expired-lease handoffs every two seconds. Failed deliveries back off, dead-letter after twelve attempts, and queue metadata is pruned after 90 days without deleting audit evidence. This closes the signature-audit loss window identified in ADR-058.

End-to-end smoke: `make metal-lima-2node` exercises the full four-phase handoff against a two-node Lima fleet. The acceptance row in spec §14 M9 is the gating test for the cluster.

### M8 — alert pipeline. ✅ (this PR)

The §12 dashboard pipeline is wired end-to-end:

- **Alert rules** at `deploy/ansible/roles/prometheus/files/faas.rules.yml`
  encode the §12 thresholds verbatim — fifteen rules under a single
  `faas_slo` group (twelve §12-mandated + three TLS observability rules
  from ADR-024 H3: `FaasTLSCertExpiryPage`, `FaasTLSCertExpiryWarn`,
  `FaasTLSOnDemandDeniedHigh`), three severity tiers (`info` / `warn`
  / `page`), every annotation carries a `runbook_url:` pointing at the
  `docs/runbooks/<AlertName>.md` stub index below.
- **Alertmanager role** at `deploy/ansible/roles/alertmanager/` mirrors
  the prometheus role's shape (defaults / tasks / templates / handlers /
  systemd unit), SHA-256-pins the 0.27.0 tarball, and binds 127.0.0.1:9093
  on loopback only. Secret material (SMTP password, Pushover token)
  loads via `_FILE` indirection from operator-provisioned files —
  same precedent as `FAAS_HOST_AGE_RECIPIENT_PATH` (gap G2 lean §17,
  sealed at rest).
- **Severity routing:** `info` → no notification (suppressed);
  `warn` → ticket-only email via `faas-warn` (4 h repeat);
  `page` → operator email + Pushover via `faas-page` (1 h repeat,
  `priority: 2` to bypass device quiet hours).
- **Scrape-config corrections** — PR #132's bind-address defaults
  (apid 9101, imaged 9102, schedd 9103, vmmd 9104, meterd 9106) plus
  the sibling-path overrides (`vmmd /metrics/fallback`,
  `schedd /metrics/fcvm`) so the alert rules' data sources are
  actually scraped. New jobs added: `builderd 9105`, `githubd 8083`.
- **Status page degraded flag** — `cmd/apid/status.go::fetch` runs a
  fourth PromQL
  `count(ALERTS{alertstate="firing",severity=~"page|warn"}) > 0`
  alongside the existing three. The boolean lands on
  `pkg/api.StatusPage.Degraded` and `deploy/statuspage/index.html`
  renders a red "Service degraded" pill driven by it. The public page
  now shows prospects and customers the same picture the operator's
  pager sees.

#### Status page degraded-flag contract

- `Source = "prometheus"` — clean snapshot, no degraded pill.
- `Source = "degraded: firing alerts"` — at least one warn- or
  page-severity alert is currently firing; the pill is visible.
- `Source = "degraded: <error>"` — the full Prometheus pipeline is
  unreachable; the handler returns the last cached snapshot with the
  error stringified. Pre-existing graceful-degradation contract from
  PR #51 (status page must never 5xx during a transient Prometheus
  hiccup).
- The alert query failing in isolation (Prometheus reachable but
  `ALERTS{}` not yet populated, e.g. on a freshly-reloaded Prometheus)
  is treated as "no firing alerts" rather than poisoning the snapshot
  — the flag is intentionally conservative.

#### Runbook index

| Alert | Runbook | Severity |
|---|---|---|
| `FaasHighResidentRam`, `FaasHighResidentRamWarn` | [HighResidentRam](runbooks/FaasHighResidentRam.md) | page / warn |
| `FaasSnapshotFleetAvgHighPage`, `…Warn` | [SnapshotFleetHigh](runbooks/FaasSnapshotFleetHigh.md) | page / warn |
| `FaasLvFcUsageHighPage`, `…Warn` | [LvFcUsageHigh](runbooks/FaasLvFcUsageHigh.md) | page / warn |
| `FaasBuildQueueBacklog` | [BuildQueueBacklog](runbooks/FaasBuildQueueBacklog.md) | warn |
| `FaasWakeLatencyHigh` | [WakeLatencyHigh](runbooks/FaasWakeLatencyHigh.md) | warn |
| `FaasColdBootFallbackHigh` | [ColdBootFallbackHigh](runbooks/FaasColdBootFallbackHigh.md) | warn |
| `FaasApiAvailabilityLow` | [ApiAvailabilityLow](runbooks/FaasApiAvailabilityLow.md) | page |
| `FaasBuildSuccessLow` | [BuildSuccessLow](runbooks/FaasBuildSuccessLow.md) | warn |
| `FaasDaemonDown` | [DaemonDown](runbooks/FaasDaemonDown.md) | page |

CI gate: `promtool check rules` runs in `lint + build` against
the same tarball the production ansible role pins (`prom_version: "2.54.1"`),
catching malformed PromQL or dangling matchers at PR time.

---

Post-M8 = private beta (founding doc M2–M3 hand-held phase).

### Post-M8 — per-app reserved eviction tier (issue #475). ✅ (this PR)

Customer-facing opt-in to a reserved tier that protects an app
from cross-account RAM-pressure eviction. Closed 6-tuple counter
set pre-instantiated at boot; idle-still-park guarantee enforced
by leaving `ReapIdle` and `ReapAggressive` unchanged.

- **Schema** — `apps.eviction_priority` text NOT NULL DEFAULT
  'best_effort' + `apps_eviction_priority_chk` CHECK
  (migration 00138; replay-safe via ADD COLUMN IF NOT EXISTS +
  DO-block `pg_catalog.pg_constraint` guard). Pre-#475 rows stay
  on the historical LRU path bit-for-bit.
- **Plan tier** — `Plan.EvictionPriorityReservedAllowed` (Free = false,
  Hobby / Pro / Scale = true) + `Plan.ReservedConcurrencyPerAccount`
  (Hobby 1, Pro 2, Scale 4). The cap counts APPS, not instances
  (issue #475 binding interpretation).
- **Reaper** — `SelectEvictions` sort comparator prepends a tier
  early-out (best_effort before reserved) so cross-account RAM
  pressure parks every best_effort candidate before any reserved.
  `ReapIdle` and `ReapAggressive` are intentionally unchanged.
- **Counter** — `schedd_evicted_priority_total{priority, reason}`
  (priority ∈ {best_effort, reserved}, reason ∈ {idle,
  eviction_aggressive, eviction_ram}). Pre-instantiated so the
  §12 panel has zero rows from idle fleet.
- **apid PATCH** — `PATCH /v1/apps/{slug}` accepts
  `eviction_priority`. 402 `plan_eviction_priority_reserved_not_allowed`
  on Free; 422 `plan_eviction_priority_reserved_quota` when the
  per-account cap is exhausted. 422 `validation_failed` for any
  value other than the closed set. Audit kind
  `app.eviction_priority_changed` (subject `&acct.ID`) emitted
  from apid only on actual value change.
- **SDK** — `sdk/go::Client.SetAppEvictionPriority(ctx, slug,
  priority)` thin one-liner + `UpdateAppRequest.EvictionPriority
  *string` for bundled PATCH payloads.
- **CLI** — `gregale app <slug> --eviction-priority=best_effort|reserved`
  (Free rejected server-side). Text output surfaces the current
  tier alongside the other per-app knobs.

ADR-075 / issue #475 / migration 00138.

## M8 — Outbound webhook delivery reliability

- **Schema** — `app_webhooks` (slot 140) + `app_webhook_deliveries`
  (slot 141). Slot 139 carries a fence reservation matching
  `00130_reserve_slot.sql` (ADR-041). The deliveries partial index
  `(status, next_attempt_at) WHERE status IN ('pending','in_flight')`
  keeps the dispatcher's claim query O(due-rows) rather than
  O(table). The `(account_id, created_at DESC)` index backs the
  customer-facing deliveries endpoint without a sort.
- **State** — `CreateAppWebhookIfUnderQuota` mirrors
  `CreateCronIfUnderQuota` (apps-row FOR UPDATE + per-account
  count under tx). `ClaimDueAppWebhookDeliveries` uses a
  `FOR UPDATE SKIP LOCKED` claim transaction with
  `ORDER BY account_id, next_attempt_at LIMIT $cap` — per-account
  fairness emerges from the query, no token bucket needed.
- **Dispatcher** — `pkg/webhook.Dispatcher` runs as a third
  goroutine in schedd (alongside the cron drain + scheduler
  watchdog). 5s tick, 32-row cap, 7-attempt DLQ. Backoff schedule
  `30s, 2m, 10m, 1h, 6h` with ±25% jitter via `crypto/rand`.
  Clock injection via `Sleeper` + `Now` struct fields (not package
  vars) makes the 7.5h DLQ path testable in ≤1s wall. Property test
  `TestDispatcher_Fairness_PerAccountRoundRobin` (5 accounts × 100
  rows × 10 ticks) pins the round-robin claim.
- **Header set** — `X-Faas-Webhook-Signature`,
  `X-Faas-Webhook-Timestamp`, `X-Faas-Webhook-Attempt`,
  `X-Faas-Delivery-Id`. The delivery id is stable across retries —
  customers can dedupe by it. The alert path keeps `X-Faas-Alert-*`
  unchanged (the `pkg/webhookout.HeaderSet` seam separates the
  two).
- **Secret sealing** — `secretbox.SealBytes(recipient, "APP_WEBHOOK",
  plaintext, 256)` at apid-write time; dispatcher unseals with
  `secretbox.OpenBytesMulti` against the host age identity. The
  plaintext is destroyed at function exit and never crosses the
  wire after the create round-trip — the response carries only
  `webhook_secret_sealed_masked: "***"`.
- **API** — 8 endpoints under `/v1/apps/{slug}/webhooks[/...]`:
  list, create, get, update, delete, rotate-secret,
  list-deliveries, retry-delivery. Plan-tier gate (`WebhookPerApp
  == 0` → 402 `plan_webhooks_not_allowed`); quota gate
  (per-app / per-account → 422 `plan_webhook_quota`). Closed enum
  drift on `retry_policy` and `event_filter` surfaces as 400
  `app_webhook_invalid` BEFORE the row is created.
- **CLI** — `gregale webhooks <list|add|update|rm|deliveries|retry>`
  (mirrors `gregale crons`). Closed-set drift on `--retry-policy`
  surfaces locally before the round-trip (same posture as
  `--eviction-priority` from PR #647).
- **SDK** — `sdk/go/internal/api` + Node generator regen both
  carry the new surface; `make sdk-gen` + `sdk-gen-node-twice`
  pass. Python regen deferred (venv not available locally).
- **Audit** — `app.webhook_created`, `app.webhook_updated`,
  `app.webhook_deleted`, `app.webhook_secret_rotated`,
  `app.webhook_delivery_retried`, plus the dispatcher-emitted
  `webhook.delivered` / `webhook.failed` / `webhook.dead`.

ADR-076 / issue #476 / migrations 00140 + 00141.

## M8 — API maintenance mode (ADR-091 amendment). 🚧

Two complementary customer-facing primitives that ship together as one feature cluster:

1. **`apps.maintenance_mode` bool** — coarse per-app gate; one PATCH flips the whole app into maintenance, every request returns 503 + `Retry-After: 60` + `Problem.code = "app_maintenance_mode"`.
2. **`kind=maintenance` edge rule** — fine-grained per-route gate; a `(match_host, match_path, match_methods)` tuple returns 503 + `Retry-After` (per-rule, default 60 s, hard cap 24 h) + `Problem.code = "edge_rule_maintenance"`. Optional `message` (≤512 B) goes into `Problem.detail`.

The coarse gate runs BEFORE the fine-grained rule, so when both fire on the same request the customer sees `app_maintenance_mode`, not `edge_rule_maintenance` — the load-bearing customer-facing guarantee is "an app I put into maintenance is in maintenance; the per-rule contract is a no-op in that case". Both primitives fire BEFORE auth, BEFORE wake, so a maintenance 503 never pays a cold-boot cost.

- **Schema** — migrations `00222_edge_rules_kind_maintenance.sql` (DROP+ADD on `edge_rules_kind_check`, widening to `{route, rewrite, redirect, headers, cors, jwt, ip, validate, limit, maintenance}`) and `00223_apps_maintenance_mode.sql` (`ADD COLUMN IF NOT EXISTS maintenance_mode boolean NOT NULL DEFAULT false` + partial index `WHERE maintenance_mode = true` + `apps_maintenance_mode_notify` AFTER UPDATE trigger that emits `pg_notify('app_changed', NEW.id::text)` ONLY when `maintenance_mode IS DISTINCT FROM old.maintenance_mode`).
- **Cluster shape** — three-PR cluster (PR-A control-plane widening + fence, PR-B runtime surface + gateway hot-path, PR-C rollout-closer + e2e + spec backfill). Mirrors the kind=validate cluster (PR #840 / #841 / #848). PR-A consumed slots 220 + 221 via fences; PR-B consumed both slots and removed the fences.
- **Hot-path slot** — `pkg/gateway/handler.go:2943` inserts `applyAppsMaintenanceMode` (coarse gate on the substituted app) + `applyEdgeRuleMaintenance` (fine-grained rule) at the TOP of the `haveApp` chain (§4.1.2.0 + §4.1.2.14), BEFORE redirect/rewrite/headers/CORS/JWT/IP/limit/validate/auth/wake. Same deny-before-cost posture as ADR-091 D4 codifies for every other cost-control kind.
- **Cache invalidation** — new `db.NotifyAppChanged = "app_changed"` pg_notify channel; gatewayd listener drops only that app's entry from the apps LRU (`Backend.ResetApp(appID)` at `pkg/gateway/pgbackend.go:1025`), not wholesale `FlushRoutes`. A maintenance flip on one app doesn't evict every other app's cache entry — `TestHandleInvalidation` pins the differential.
- **Per-rule quota** — counts against the existing `EdgeRulesPerApp` quota (5/25/100/500 by plan). No new per-rule counter for v1 (D20.10 if customers ask).
- **Defaults** — `api.EdgeRuleMaintenanceRetryAfterSeconds = 60` (env-overridable via `FAAS_EDGE_RULE_MAINTENANCE_RETRY_AFTER_SECONDS`). Cap: `api.MaxEdgeRuleMaintenanceRetryAfterSeconds = 86400` (24 h, hard, enforced at apid Validate).
- **Free-tier-allowed.** No `IsPaidOnly()` on either primitive.
- **Metrics** — new `gateway_app_maintenance_total{plan}` (pre-instantiated at the closed `{free, hobby, pro, scale}` set, plan=string(app.Plan)) + new `gateway_edge_rule_match_total{kind="maintenance", outcome=match|miss|blocked}` pre-instantiation loop (mirrors the 9 prior kinds). No existing metric names changed.
- **Audit** — `app.maintenance_mode_match`, `edge_rule.maintenance_matched`, `edge_rule.maintenance_blocked`. Cross-account rules silently fall through (`outcome=blocked` + audit `maintenance_blocked`, same posture as every other kind, ADR-091 D5).
- **E2E** — `cmd/e2e/edge_rules_maintenance_e2e_test.go` (3 tests: match-returns-503, default-retry-after, cross-account-falls-through) + `cmd/e2e/apps_maintenance_mode_e2e_test.go` (2 tests: patch-true-returns-503, coarse-gate-beats-edge-rule).
- **TTL / auto-disable** — out of scope for v1; deferred to D20.7.

ADR-091 amendment. No new ADR slot.

## M8 — Per-route observability (ADR-093). 🚧

Opt-in per-app route breakdown on the gatewayd-internal hot path,
surfaced as Prometheus `_by_route` series and a control-listener
reader at `GET /v1/apps/{slug}/routes`. Bounded by a 50-route cap
with a non-evicting `__route_other__` overflow bucket (ADR-093 D2).

- **Schema / State** — `apps.route_metrics_enabled boolean` (migration
  00216). Partial index `WHERE route_metrics_enabled = true` keeps
  the col-store cheap on Free-tier (where the column is always false).
- **Plan gate** — Hobby/Pro/Scale default-on; Free stays off. Two
  accessors at `pkg/api/limits.go:2649` `RouteMetricsEnabled()`
  (create-time default) and `:2662` `RouteMetricsResponseAllowed()`
  (PATCH-time gate) so a Free PATCHing true surfaces
  `plan_route_metrics_not_allowed` (403, `pkg/api/errors.go:720`).
- **Metric primitives** — `pkg/gateway/metrics.go:404-417`
  `requestsByRoute`, `durationByRoute`, `failuresByRoute`. The
  `_by_route` suffix is required so the new `{route}` label set
  doesn't collide with the pre-existing `{app, code}` CounterVec
  (Prometheus rejects two CounterVecs with the same name but
  different label sets at registration time).
- **API** — `GET /v1/apps/{slug}/routes` (apid reverse-proxy to
  gatewayd-internal `GET /v1/internal/apps/{slug}/routes`).
  OperationId `getAppRoutes` (api/openapi.yaml:1394-1427, `x-issue: 273`).
- **Operator kill-switch** — `route_metrics_enabled` toml + env
  `FAAS_GATEWAY_ROUTE_METRICS` (`cmd/gatewayd-internal/config.go:107-121`).
  Cluster default wired by commit 5 of this PR
  (`deploy/ansible/roles/gatewayd_internal_service/templates/gatewayd.toml.j2`).
- **Alert** — `GatewayWildcardRoute` — sustained
  `__route_other__` overflow > 1 reqps for 10m. Runbook
  `docs/runbooks/GatewayWildcardRoute.md`. Recording rules + the
  alert land on the box via
  `deploy/ansible/roles/prometheus/tasks/main.yml` (commit 1 of
  this PR — G1+G2 closure).

## M8 — Per-service wire-protocol selector (issue #67 / ADR-124). 🚧

Customer knob: pick the wire protocol at the public edge
(`http1 | http2 | grpc`) via `apps.app_protocol`. Default `http1`
universal; `http2` universal; `grpc` Hobby+/Pro/Scale only.
Closes issue #67 (Cloud Run parity for k8s/Cloud-Run migrators
carrying gRPC services).

- **Schema** — migration `00382_apps_app_protocol.sql`
  (`ADD COLUMN IF NOT EXISTS app_protocol text NOT NULL DEFAULT
  'http1'` + `apps_app_protocol_chk` CHECK mirroring the
  `eviction_priority` precedent at `00346_deployments_annotation.sql:54-60`).
  Round-trip + replay-safety pinned at `00382_apps_app_protocol_test.go`
  (replay-safe ADD COLUMN IF NOT EXISTS + DROP/ADD CONSTRAINT IF
  EXISTS pattern from the streaming migration).
- **Plan gate** — `pkg/api/limits.go::Plan.AppProtocolAllowed(protocol)`
  mirrors the `StreamingResponseAllowed` precedent. `grpc` is gated
  per-plan on `AppProtocolGrpcAllowed bool` (Free=false,
  Hobby/Pro/Scale=true). apid applies the gate at `CreateApp` AND
  at `UpdateApp` — Free PATCH to `grpc` surfaces
  `plan_app_protocol_grpc_not_allowed` (RFC 7807, 403). Invalid
  values (`!∈ {http1, http2, grpc}`) surface
  `app_protocol_invalid` (400). The column-level CHECK is the
  belt-and-braces backstop, not the primary validator.
- **Closed-set DTO + validator** — `pkg/api/dto.go`: `CreateAppRequest.AppProtocol *string`,
  `UpdateAppRequest.AppProtocol *string`, `AppResponse.AppProtocol string`. `pkg/api/diff.go`:
  `DiffAppConfigPatch.AppProtocol *string` for the `deploy --diff`
  path. CLI: `cmd/gregale/commands2.go`, `commands5.go`,
  `commands_diff.go` wire `--app-protocol http1|http2|grpc` (create,
  update, diff).
- **Read-side plumbing** — `cmd/gatewayd-internal/backend.go::toApp`
  copies `state.App.AppProtocol` onto the gateway's local
  `gateway.App.AppProtocol` so the cached app struct carries
  the closed-set value without re-reading the apps row.
- **Header stamp** — `pkg/gateway/handler.go::decideProtocol`
  + `r.Header.Set("x-faas-protocol", decideProtocol(h, r, app))`
  at the site `x-faas-stream` is stamped today (~5131 streaming
  path; ~5261 buffered path). Empty→`"http1"` preserves
  pre-ADR-124 behaviour bit-for-bit. `pkg/gateway/forwardproxy.go::fwdStreamOnceWithEvents`
  reads the header and emits a `slog.Debug` "framing selection"
  line on the gatewayd-internal debug stream so operators with
  debug logging on can correlate per-app protocol choice with
  bridge-side framing behaviour.
- **OpenAPI / SDK** — `api/openapi.yaml` adds `app_protocol` to
  `AppResponse`, `CreateAppRequest`, `UpdateAppRequest`,
  `DiffAppConfigPatch` (type `string`, `enum: [http1, http2, grpc]`,
  `example: http1`). `make spec-sync` mirrors to
  `pkg/apid/openapi.yaml`. SDK node + Python regenerated.
- **Diff engine** — `pkg/deploydiff/{diff,engine,quota,render_text}.go`
  gain the `app_protocol` branch (closed-set Modify; Free-account
  gate on `grpc` Add). Mirrors the streaming/require_authn
  pattern at `pkg/deploydiff/quota.go:105`.
- **E2E** — `cmd/e2e/app_protocol_test.go` pins (CI, no metal):
  Free-app `grpc` rejected, Hobby default = `"http1"`,
  Hobby persistence of `"http2"` round-trip, PATCH of
  invalid value → 400 `app_protocol_invalid`.
- **Out of scope (filed §17 G19)** — end-to-end H2 framing on
  the customer→guest leg (i.e. `grpc` actually reaches the
  guest's `:8080` as gRPC trailers, not as H1+chunked). The
  bridge already speaks H2C on the vmmd side and H1+chunked
  on the guest side per PR #750 / ADR-079; this PR ships the
  customer knob without changing the bridge. G19 is a separate
  multi-week ADR (bridge-side termination on the guest side
  + coordination with the guest base image).

ADR-124 (new file at `docs/adr/124-app-protocol-selector.md`).
Closes §17 G18; opens §17 G19.

### Tier A — observation period

Operational follow-up (no code change). 2-week observation window
after PR #856 lands. Empirical answer to "does the 50-cap actually
bound cardinality or do apps collapse into `__route_other__`?" feeds
either Tier B hardening (per-method cap, post-rewrite route label,
`AppRoutesResponse` cap-visibility) or Tier C bigger ADRs (per-route
SLI, per-plan budget).

- **Alert surface** — `GatewayWildcardRoute` is `severity: info`
  and routes to the EMPTY `faas-silent` receiver
  (`alertmanager.yml.j2:53-56`). No page, no email, no Slack. Visible
  in Prometheus `/alerts` and Grafana only. The status-page degraded
  pill filters `severity=~"page|warn"` (line 519-541), so this alert
  does **not** move the pill by design.
- **Weekly review** — pull firing alerts directly:

  ```sh
  curl -s http://127.0.0.1:9090/api/v1/alerts \
    | jq '.data.alerts[] | select(.labels.alertname=="GatewayWildcardRoute")'
  ```

- **Mid-sprint Hobby-tier audit** — run
  `make hobby-route-audit` (uses `deploy/scripts/adr093-hobby-audit.sh`)
  against the control plane. Counts `__route_other__` vs real-route
  entries per Hobby-tier app. Read-only.
- **Post-deploy smoke** — operator-side runbook at
  `docs/runbooks/PostDeployAdr093.md`. Four artefact checks (rule
  file, systemd unit, toml, Grafana panels 103/104) + a rule-load
  check + an end-state snapshot. Read-only.
- **Decision at end-of-sprint**:
  - *Cap sufficient* (no Hobby-tier app saturated) → Tier B polish;
    Tier C (per-route SLI + per-plan budget) becomes the next major ADR.
  - *Cap partial* (some Hobby apps saturated, most didn't) → Tier B
    per-method cap.
  - *Cap insufficient* (most Hobby apps immediately saturated) →
    ADR-093 needs re-scoping (lower default cap, revert Hobby's
    default-on, or move to a customer-driven enable per plan).

## Auth default flip — issue #695 / ADR-080

Closes spec §17 G15. Cloud Run's analogue is IAM-authenticated by
default with `--allow-unauthenticated` opening a route; this flip
makes Gregale match that posture for newly-created apps.

- **Schema** — `apps.auth_default_flipped_at timestamptz NULL`
  (migration 00156, slot 155→156). Nullable so the migration is
  forward-only contraction (down-migration drops the column).
- **Grandfather mechanism** — migration stamps every pre-flip
  row at flip-time (`UPDATE apps SET auth_default_flipped_at =
  COALESCE(auth_default_flipped_at, now()) WHERE
  auth_default_flipped_at IS NULL`, idempotent), and emits one
  batch audit row `apps.auth_default_global_flipped` with the
  migrated count (replay-safe via `WHERE NOT EXISTS`).
- **Per-plan defaults** — `Plan.RequireAuthnDefault()` +
  `Plan.PublicAuthModeDefault()` in `pkg/api/limits.go`. Truth
  table:
  | Plan  | require_authn | public_auth_mode |
  |-------|---------------|-------------------|
  | Free  | `false`       | `"open"`          |
  | Hobby | `true`        | `"open"`          |
  | Pro   | `true`        | `"bearer"`        |
  | Scale | `true`        | `"bearer"`        |
  Hobby stays on `"open"` because bearer scope is gated off at
  that tier — defaulting to a mode a customer can't realise
  would strand them.
- **apid** — `buildApp` stamps both per-plan defaults onto the
  returned `state.App` at create-time. Wire shape unchanged:
  `AppResponse.require_authn` + `AppResponse.public_auth` already
  shipped in #560 / #477.
- **DTO** — new read-only field `AppResponse.auth_default_flipped_at`
  (omitted via `omitempty` on fresh-create apps; surfaces RFC3339
  for grandfathered apps so dashboards can render the
  "since YYYY-MM-DD" suffix).
- **CLI** — `faas apps list` adds an AUTH column. `AUTH: open` /
  `AUTH: required` / `AUTH: required + basic`, suffixed with
  `· since YYYY-MM-DD` on grandfathered apps. Per-app opt-out:
  `faas app <slug> --no-require-authn`.
- **Dashboard** — new `Page.ActionRequiredSurface` banner on the
  account view. The banner surfaces the migration date and the
  universal opt-out command (one place; no per-app link rot).
- **E2E** — `cmd/e2e/deploy_wake_metal_test.go`,
  `cmd/e2e/streaming_metal_test.go`,
  `cmd/e2e/wake_timeline_metal_test.go` opt out at create-time
  via `RequireAuthn: &falsy` because their anonymous probes
  aren't testing the authn surface.

ADR-080 / issue #695 / migration 00156.

## M8 — Deploy configuration contract (ADR-143). ✅

The "read but never set" outage class (PR #1286 function runner paths, PR
#1287 AppErrors listener) is closed at the root:

- **One unit source.** `deployctl generate` now emits every service role's
  `files/faas-*.service` (8 roles) + `deploy/systemd/`; the retired
  `deploy/controlplane/` tombstone is deleted (ADR-110 Phase 2). vmmd,
  gatewayd-internal, gatewayd-public and builderd copies had drifted.
- **Declared environment.** `pkg/daemonunitspec/envcontract.go` +
  `envcontract_test.go`: every `FAAS_*` a daemon reads is declared with a
  delivery source and the test proves delivery both ways; table rendered
  to `docs/ops/env-contract.md`. Found and fixed three more silently-off
  gates: streaming (`streaming_enabled=true` in gatewayd-internal.toml),
  jobs dispatch (explicit `FAAS_JOBS_DISPATCH=0` until Mega-1.5), GeoIP
  database (new `geoip` role, pinned DB-IP release).
- **Restart handlers** on every service role (`daemon-reload` +
  `try-restart`), notify on every unit/drop-in/config task.
- **Verified convergence.** `vars/daemons.yml` topology (lockstep-tested),
  `fleet_verify` role at the end of both bootstrap plays, strict
  `verify.yml` / `make verify-fleet`; CI runs `scale_check.yml` for real
  and `ansible-lint` (production profile).
- **Spec §11 host posture** as `host_hardening` (sshd drop-in with lockout
  guard, fail2ban, unattended security upgrades without reboot, auditd,
  kernel sysctls).

## What's next

M0 → M8 are the spec-defined milestones (spec §14, lines 444–461).
Items below are operator verification still on the M8 board plus
explicitly open issues that the doc otherwise implies are closed.

### M6

*(Closed — PR #60 closes #57. See [M6](#m6--builderd--real-image-pulls-) above.)*

### M7

- ~~**`cmd/meterd/main.go` wiring** — `defaultDeps` leaves `parker`
  and `stripe` nil.~~ **Closed by PR #69** (`worktree-harden-meterd`).
- ~~**`pkg/stripex/usage.go::PushUsageRecord`** — `nil`-returning
  `TODO stripe-go`.~~ **Closed by PR #69.**
- **Provider-pluggable billing (Stripe + Paddle)** — see the M7
  body above. **Note:** the dashboard / CLI surface for
  `paddle_checkout_url` rendering is still outstanding (the original
  PR #4 in the paddle-mor series). Track via the issue search.

### M8

- **CertMagic TLS** for `gatewayd-public` (`*.apps.gregale.dev` via DNS-01;
  on-demand HTTP-01 gated by `custom_domains` allowlist). Plumbing
  landed across `pkg/gateway/tls*.go`, `dns01_hetzner.go`,
  `allowlist.go`, `acme.go`, `cmd/gatewayd-public/{main,config,secrets}.go`,
  the systemd unit, and the ansible role; `caddyserver/certmagic`
  v0.25.4 is pinned in `go.mod:14`. PR #87 closed the reference-node cut-over
  + the structured acceptance tests; ADR-024 declared H3 (TLS
  observability — cert-expiry gauge + on-demand-denial counter) and
  H4 (file-watch secret reload) as known follow-ups. H3 closes in
  this PR via `pkg/gateway/metrics.go::tlsCertExpiry` + `tlsOnDemandDenied`
  + `pkg/gateway/cert_expiry.go` refresher, wired into
  `cmd/gatewayd-public/main.go`; three alert rules land in `faas.rules.yml`;
  operator runbook at `docs/ops/gatewayd-public-tls-cutover.md` (the legacy `docs/ops/gatewayd-tls-cutover.md` retains the pre-PR-A cut-over steps; current process lives in the public-edge runbook).
- **§14 V2 latency driver** — 100 park→wake cycles per app class,
  p50 ≤ 350 ms / p95 ≤ 800 ms. The Hobby-class gate is wired via
  `TestDeployWakeMetal/wake-latency-p50p95-100cycles` (extends the
  prior 10-cycle mean-only subtest). Per-app-class (Express, Next.js,
  Flask, FastAPI, Go static) gating is the M8 follow-up. Runs on
  `make metal-lima RUN_ARGS='-run TestDeployWakeMetal'`.
- **Documented timed restore drill** — §14 M8: PG + one app back
  serving on a clean VM < 30 min, recorded as executed. Run
  `deploy/scripts/faas-m8-restore-drill.sh` on a reference node and fill
  in `docs/drills/2026-07-20-restore-drill.md` (template present).
- **Status page + SLO dashboard** — public SLOs from spec §12
  (API 99.5 % monthly, wake p95 < 1 s, build success ≥ 99 %).
  Pipeline (Prometheus scrape + Grafana JSON + `apid /status` +
  `apid /status/slo.json`) in via PR #51; Grafana provisioning +
  D1–D5 threshold fixes + M1/M2 panels via PR #156 (ADR-031).
  Operator verification (Grafana panels render non-zero data, SLO
  JSON returns denominators) is the reference-node follow-up.
- **§11 checklist item-by-item sign-off** (cgroups v2 only,
  `unprivileged_userns_clone=0`, auditd, unattended-upgrades,
  etc.). The IPv6 egress item (ADR-023) is now in via PR #51;
  remaining items are operator verification on a reference node.
- **Gate-A runbook** — 2nd-node active-passive (founding doc R3).
- **M2 / M5 §14 metal gate sign-off** — the body/trim fixture
  mismatch flagged in PR #55 is resolved at the code level
  (PRs #151, #159, #135). The remaining item is a clean-checkout
  `make metal-lima` run on reference node / Lima recording the gate green.

### Open security & infrastructure issues

These are still open on GitHub. Earlier revisions of this file
sometimes implied they were closed; they aren't.

- **#695** — Flip `apps.require_authn` global default to `true`
  (Cloud Run `--no-allow-unauthenticated` parity). Migration
  `00156` landed with a grand-father stamp on every pre-flip
  row + one batch audit row; new apps stamp the per-plan default
  via `api.Plan.RequireAuthnDefault()` /
  `api.Plan.PublicAuthModeDefault()` at `apid::buildApp` time.
  Per-plan truth table: Free `false/"open"`, Hobby `true/"open"`,
  Pro `true/"bearer"`, Scale `true/"bearer"`. Hobby stays on
  `"open"` because bearer scope is gated off at that tier — a
  bare-bearer-default without a usable scope would strand the
  customer. Existing apps are reachable exactly as they were at
  flip-time; opt-out per app via
  `faas app <slug> --no-require-authn`. Operator dashboard renders
  the migration banner on the account view when
  `count(apps where auth_default_flipped_at is not null) > 0`.
  See ADR-080.
- ~~**#254** — App logs SSE stream is a stub (tier-1 ship-blocker).
  Move 4: per-instance ring + schedd StreamAppLogs RPC + vmmd
  Logs RPC end-to-end. Consumer side (Move 3, PR #291) shipped.
  Producer side (per-instance ring + schedd fan-out + vmmd Logs
  RPC + apid handler) shipped in this PR; ADR-043 records the
  decision. The follow-up PR wires the production schedd
  StreamAppLogs in cmd/apid so the apid stub is replaced by the
  real fan-out.~~ *(closed by Move 4 — see ADR-043)*
- **#144** — `NftResetCommands` missing ip6 reset (snapshot-restore
  Wake fails on second add).
- ~~**#146** — host egress chain deny lines were dead-code; the
  forward-chain ordering fix in PR #128 / #151 closed the original
  bug, and the remaining audit (shared catalog provenance + OCI
  6to4/Teredo + cross-renderer invariant + generated operator
  artifact) closed in PR-D. See `docs/denylist.md` and
  `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.~~ *(closed by PR-D — moved to the closed list below)*
- **#147** — `stripeWebhook customer.subscription.updated` should
  validate `Plan` via `api.Plan.Valid()`.
- **#148** — `bootstrap.sh` should pin the Go toolchain via
  SHA-256 (closes a toolchain-supply-chain gap; sister to #143,
  which is closed). RETIRED 2026-08-15 by issue #911 / PR-1:
  the v1 bootstrap.sh is a tombstone now; the v2 path is the
  renderer + release install (PR-2 + PR-3). The toolchain-pinning
  gap lives on in the cd-controlplane.yml / `make build` paths.
- **#145** — streamed OCI blob SHA-256 verification against the
  URL-path digest (spec's digest-pinned immutability).
- **#125** — `sqlc-check` in the CI bundle to prevent sqlc source
  drift.

#### Closed via PRs (full audit entry in PR-D)

- **#146** — closed by PR-D. See `docs/denylist.md` and
  `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.
- **#90** — document `/v1/*` as a permanent platform path
  reservation (issue #85 follow-up).

## Wave 0 — stateless-only contract. 🚧 (PR-C draft pending review)

G13 ("the platform is stateless-only but nothing encodes it") is
closed across three PRs and an ADR; PR-C is staged as draft PR #421
and will flip to ✅ once the review-blocking items clear.

- **PR-A (PR #413, merged):** deploy-accept gate. New RFC 7807 code
  `CodeStatelessOnlyViolation` (422); apid tarball scan rejects
  `VOLUME` / `mkfs.{ext4,xfs}` / `mount -t ext4/xfs` / top-level
  `data/`+`db/` before any build slot is consumed;
  `pkg/imaged::buildImageLayer` consults `pkg/imaged/base.go::
  StatefulDenyListMatch` against the resolved OCI ref
  (postgres / redis / mysql / mariadb / mongo / cockroach /
  cassandra / clickhouse). Both paths feed
  `pkg/oci::SentinelToCode` → `deployments.error_code`.
- **PR-B (PR #417, merged):** customer-facing pattern. Four
  `faas init --template={s3-uploader, slack-bot,
  rest-api-postgres, cron-worker}` templates plus
  `docs/storage.md` teaching the managed-service pattern.
- **PR-C (ADR-047, draft PR #421):** runtime advisory. Guest-init
  `fanotify` mark on the closed path set (`/data`, `/db`,
  `/var/lib/postgresql`, etc.) → debounced 1s per
  `(path, mask-set)` batch → AF_VSOCK DGRAM
  (`port=1025, msg_type=2`, distinct from
  `VsockResumePort=1024`) → vmmd gRPC client
  (`/run/faas/apid.sock`, first vmmd-issued gRPC client) →
  apid `pkg/audit.Auditor.Emit("stateless.advisory", ...)`.
  Surfaced via `GET /v1/audit-events?kind_prefix=stateless.advisory`
  (plus `?app_id=` and `?include_anonymous=` for the dashboard
  drill-down), the new `db.NotifyStatelessAdvisory` SSE channel,
  `faas audit-events [--kind-prefix=…] [--app-id=…]
  [--include-anonymous]`, `faas tail --include-stateless`, and the
  dashboard `app_detail.html` "Stateless advisories" link into
  `/dashboard/audit-events?kind_prefix=stateless.advisory&app_id=…`.
  Advisory-only — spec §17 G13 explicitly forbids EROFS for Wave 0.
  vmmd default-local stays DB-less
  (`Manager.advisoryClient == nil` short-circuits to a no-op).

The three-ship-blockers contract: PR-A blocks at deploy time,
PR-B teaches the right pattern, PR-C makes the runtime failure
visible. Wave 1 follow-ups: EROFS on state-shaped paths (gated
on PR-C telemetry showing customer misuse), apps_list "warnings
detected: N" badge (deferred — backend count endpoint not in
this PR's scope), and vmmd-side `vmmd_node_id` stamp on the
audit row for traceability (currently `actor='apid'`).

## M8 — Wire-protocol selector: bridge-side H2C terminator (ADR-126). ✅

Closes spec §17 G19. PR #1023 / ADR-124 landed the customer-facing
`apps.app_protocol ∈ {http1, http2, grpc}` knob (closed-set
validation, default `http1`), and `gatewayd-internal` stamps
`x-faas-protocol` for downstream observability, but the framing
the guest's `:8080` actually received was still HTTP/1.1 + chunked
regardless of the customer's choice. This PR closes the wire-shape
follow-on: every `app_protocol ∈ {http2, grpc}` customer now
reaches their guest's `:8080` as native H2 frames with native gRPC
trailers — no bridge-side re-framing, no H1 downgrade inside the VM.

- **Bridge-side framing switch (ADR-126 §Decision 1).** New
  `cmd/vmmd-stream-bridge/h2c_terminator.go::handleH2CStream`
  originates HTTP/2 prior-knowledge frames to the guest via
  `golang.org/x/net/http2.Transport{AllowHTTP:true}`; HPACK state
  owned by the transport, no hand-rolled framing. The legacy
  `writeH1RequestHead` + `writeChunkedBody` path stays verbatim as
  `handleH1Stream` — zero behavior change for `app_protocol=http1`.
  Per-stream dispatch via `FAAS_BRIDGE_PROTOCOL ∈ {h1, h2c}` env
  var (read per-request in `framing.go::currentBridgeFraming()`,
  mirrors the per-request `FAAS_STREAM_BRIDGE_VERSION` pattern).

- **Guest-side listener opt-in.** Every shipped runner
  (`go124`, `node22`, `node24`, `python312`, `python313`) routes
  through the shared `guest/runners/internal/h2c_listener.go::ListenAndServeH2C`
  helper. The helper opts into H2C via stdlib
  `srv.Protocols.SetUnencryptedHTTP2(true)` (Go 1.24+; replaces the
  deprecated `golang.org/x/net/http2/h2c.NewHandler`) AND
  `SetHTTP1(true)` — the latter is load-bearing because stdlib's
  `Protocols` struct starts with NO protocols set, so calling only
  `SetUnencryptedHTTP2(true)` breaks every H1 caller with
  `connection reset by peer`. Caught by
  `TestH2CListener_H1Fallback`.

- **Wire-additive proto bump (ADR-016).** `ForwardHTTPRequestInit.app_protocol = string`
  (new field 7) carries the per-app protocol from vmmd to the
  bridge; `ForwardHTTPResponseInit.trailers` (new repeated field 7)
  carries gRPC trailer HEADERS forward. No SQL migration; the vmmd
  closed-set validation lives at the customer-intent layer
  (`apid` rejects out-of-set values with
  `400 app_protocol_invalid` before the gRPC frame leaves apid).

- **Two rollback switches (ADR-126 §Decision 7).**
  `FAAS_BRIDGE_PROTOCOL=h1` on the bridge process (per-vmmd
  surgical, forces legacy H1+chunked for any app_protocol), and
  `FAAS_STREAM_BRIDGE_VERSION=v1` on vmmd (wholesale shell-bridge
  fallback, pre-existing ADR-028 amendment). Both are env-var
  only, no restart of unrelated processes.

- **Snapshot invalidation is contained.** Every app adopting
  `app_protocol ∈ {http2, grpc}` must adopt the new h2c-capable
  base image (via the next `make images-push` cycle); the
  `app_protocol=http1` slice stays valid forever (ADR-005 cold-boot
  fallback handles the rebuild transparently).

- **Coverage:** `pkg/vmmdgrpc/forward_v2_internal_test.go` (closed-
  set translator + env-var derivation, 6 sub-tests); `cmd/vmmd-stream-bridge/framing_test.go`
  (per-stream env lookup + shared helpers + hop-by-hop trim);
  `cmd/vmmd-stream-bridge/h2c_terminator_test.go` (unary + gRPC
  trailers + dial-failure + dispatch); `guest/runners/internal/h2c_listener_test.go`
  (H2 prior-knowledge + H1 fallback + PORT env override);
  `cmd/e2e/bridge_h2c_terminator_e2e_test.go` (real bridge binary
  against a loopback H2C guest listener); metal sibling at
  `cmd/e2e/bridge_h2c_terminator_metal_test.go` documents the
  §14 M8 row 5 acceptance gates for `make test-metal`.

- **Operator-facing changes:** zero. The closed-set validation,
  per-app opt-in, and per-stream framing dispatch are all
  transparent to existing customers — `app_protocol=http1` rides
  the legacy path verbatim; `app_protocol ∈ {http2, grpc}` opts
  into the new wire shape by changing one customer-side CLI flag.

## M8.6 — Wire-protocol selector hardening (ADR-127 / G19.1). 🚧

Hardening follow-on to ADR-126 / G19 (the wire-shape shipped in
PR #1050). The post-merge audit run the same day surfaced four
production-readiness gaps that the shippable surface did not
address; this PR closes three of them and ships the docs + ops
material for the fourth. **Layer 12 (metal acceptance) is
deliberately out of scope** and lands in two follow-on issues
(G19.2 un-stub `cmd/e2e/bridge_h2c_terminator_metal_test.go`; G19.3
add `deploy/ansible/roles/metal-h2c-acceptance/`).

- **Layer 6 — snapshot invalidation on `app_protocol ∈ {http2, grpc}`
  adoption.** New `pkg/fcvm.FAAS_BASE_IMAGE_VERSION = "v1"` stamps the
  h2c-capable base rootfs (mirrors `Snapshot.FCVersion` per ADR-005;
  apps adopting the new wire-shape must adopt the new image). New
  `pkg/state.PgStore.MarkAllSnapshotsStaleByAppProtocol` + `MarkSnapshotStaleByAppProtocol`
  (mirror the F2 bulk + single-row patterns at `pkg/state/pgstore.go:9739`
  and `:9654` respectively). New `pkg/imaged.Handler.MarkAppProtocolSnapshotsStale`
  caller, invoked by `pkg/imaged/loop.go::runFCSweep` after F2 (the
  existing FC-version sweep); emits `warm_snapshot_stale` audit kind
  with subject `"app_protocol:v1"`. `app_protocol=http1` rows stay
  valid forever.

- **Layer 9 — bridge listener + transport security pins.** Three
  outbound transports (`cmd/vmmd-stream-bridge/h2c_terminator.go::newGuestH2CTransport`,
  `pkg/vmmdgrpc/forward.go::newStreamBridgeH2CTransport`,
  `pkg/gateway/internal_proxy.go::newInternalProxyH2CTransport`)
  carry `MaxReadFrameSize=1 MiB` + `MaxHeaderListSize=1 MiB` +
  `StrictMaxConcurrentStreams=true`. Inbound bridge listener
  (`cmd/vmmd-stream-bridge/main.go::srv`) wraps the handler with
  `golang.org/x/net/http2/h2c.NewHandler` (stdlib `Protocols.Get("h2")`
  has no per-protocol knob exposure; the wrapper is the canonical way
  to set per-protocol server limits) and pins
  `MaxConcurrentStreams=100` + `MaxReadFrameSize=1 MiB` +
  `IdleTimeout=60s` + `ReadIdleTimeout=30s` + `PingTimeout=15s` +
  `WriteByteTimeout=30s`; server-side `MaxHeaderBytes=1 MiB` via
  `api.DefaultMaxHeaderBytes`. `handleH2CStream` now has two
  `defer`s at the top of its body: `defer recover()` (logs the
  panic with stack + writes `500 bridge panic: ...` so a guest-side
  bug cannot crash the bridge) and `defer transport.CloseIdleConnections()`
  (so transport leaks do not pin guest-side fds).

- **Layer 7 — bridge observability.** New
  `pkg/wire.OpsMetrics.bridgeFramingTotal *prometheus.CounterVec`
  registered as `vmmd_bridge_framing_total{app_protocol, bridge_protocol, framing}`;
  pre-instantiated across the closed cross-product (3 × 2 × 2 = 12
  series) at boot so the dashboard renders a zero row from idle
  fleet (`TestOpsMetrics_BridgeFramingTotal_PreInstantiated`).
  `framing ∈ {match, mismatch}` where `mismatch` means
  `bridge_protocol` ≠ the canonical `appProtocolToBridgeProtocol`
  translation — the operator-forced surgical-rollback signal.
  `cmd/vmmd-stream-bridge/main.go::newHandler` now emits an
  `slog.Info` framing-selection line on every request with
  fields `framing`, `app_protocol_env`, `guest`, `method`, `path`
  (Info not Debug because `FAAS_BRIDGE_PROTOCOL` env flip is the
  operator's primary rollback signal). New dashboard
  `deploy/grafana/bridge-protection.json` (UID
  `faas-bridge-protection-adr-127`, schemaVersion 39) with four
  panels: framing rate by (app_protocol, bridge_protocol, framing),
  MISMATCH rate with red threshold 0.1 ops, active
  `bridge_protocol=h1` count on `http2|grpc` apps, and bridge H2C
  handshake latency p99. Companion Prometheus alerts at
  `deploy/ansible/roles/prometheus/files/bridge.rules.yml` under
  `family: bridge`: `FaasBridgeFramingMismatch` (warn, `> 0.1 ops`
  for `1h`) + `FaasBridgeRollbackStuck` (page, mismatch rate sustained
  for `4h`). Runbook at `docs/ops/h2c-rollback.md`.

- **Layer 11 — docs parity.** Spec §4.1 line 115 above this row is
  rewritten to drop the pre-ADR-126 "filed in §17 G19 as a multi-week
  follow-on ADR" language and surface both ADR-126 (wire-shape in
  fleet) and ADR-127 (hardening follow-on) as the load-bearing pair.
  §17 G19 row stays `RESOLVED` — ADR-126 is the close, ADR-127 is
  the hardening overlay. `docs/ops/h2c-rollout.md` + `docs/ops/h2c-rollback.md`
  shipped next to `docs/ops/secrets-rotation.md` cover the rollout
  + two-switch rollback + escalation policy.

- **Out of scope (filed as follow-on issues).**
  - **G19.2** — `cmd/e2e/bridge_h2c_terminator_metal_test.go`: un-stub
    5 metal tests. Replace unconditional `t.Skip(...)` lines with
    `metalAvailable(t)` gates using the canonical pattern at
    `cmd/e2e/deploy_override_port_metal_test.go:165`. Build tag
    `//go:build metal` preserved. Depends on G19.1 + G19.3.
  - **G19.3** — `deploy/ansible/roles/metal-h2c-acceptance/`. The
    opt-in role is now implemented with the five-app fixture contract,
    KVM/x86_64 preflight, root-owned secret environment, non-root oneshot
    harness, and five named §14 M8 row 5 gates. It is invoked only through
    `deploy/ansible/metal-h2c-acceptance.yml`; the normal production
    `bootstrap.yml` remains unchanged. G19.2 still owns enabling the Go
    metal tests once a real acceptance host is available.

## M8.7 — Public-release backend hardening foundation (ADR-140). 🚧

This slice closes three backend failure modes that are disproportionately
expensive in a public deployment:

- warm placement hints are committed only after instance creation and ledger
  admission;
- stale schedd residency data fails closed after 30 seconds, preserving only
  the guaranteed builder slot; and
- HTTP and raw gateway streams actively cancel and drain request-body copying
  on upstream failure or client disconnect.

Remaining public-release gates are tracked separately: durable event replay,
M9 two-node and leak-drill acceptance, service-replica convergence, real OTLP
export, and the remaining state/export scale work.
