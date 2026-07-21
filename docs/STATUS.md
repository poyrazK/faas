# Status

Spec §14 milestones M0 → M8. The README has the one-line version;
this file is the long form (which PR closed which issue, what each
milestone actually shipped, what's left on the board). Update this
when a milestone lands — readers coming from the README land here
for context.

## M0 — repo scaffold. ✅

Repo tree, build/test/lint tooling, CI, `pkg/api` limits table,
8-role ansible bootstrap, hello-boot acceptance test. `make bootstrap`
gates it on a fresh EX44.

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

**Remaining:** the same `deploy_wake_metal_test.go` has a body/trim
fixture mismatch that also blocks the M5 §14 gate. PR #55 in flight.

## M3 — snapshots + wake. ✅

Park/wake with the ADR-005 restore-or-cold-boot fallback, FC version
pinning (`snapshots.fc_version`), and the vsock post-restore resume
hook (ADR-022) that re-seeds entropy + steps clock — V6 acceptance
green in `pkg/fcvm/v6_resume_ext4_metal_test.go`.

**Remaining:** §14 V2 latency loop driver (100 cycles, p50 ≤ 350 ms)
— see [What's next](#whats-next).

## M4 — gatewayd + schedd. ✅

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

**Remaining:** the same `deploy_wake_metal_test.go` body/trim
fixture mismatch — the M5 §14 metal acceptance gate does not pass
on a clean checkout. PR #55 in flight.

## M6 — builderd + real image pulls. ✅

Build-in-microVM is wired through (`cmd/builderd`, `pkg/builderd`
orchestration + executor, PRs #39/#40/#43); the metal lifecycle is
in `vm_metal.go` (`//go:build metal`) and calls vmmd over gRPC, with
`vm_stub.go` returning `ErrNotMetal` for non-metal builds. OCI
puller hardened (`pkg/oci/egress.go` — denied CIDRs cover RFC1918,
CGN, loopback, IMDS, ULA), streamed layer blobs. `cmd/imaged`
auto-stages `/srv/fc/base/builder-base.ext4` on startup.

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
Dockerfile). EX44 sign-off remains the §14 source of truth per
CLAUDE.md.

## M7 — metering, billing, functions, cron. 🚧

The sampling/quota shapes are in `cmd/meterd` and `pkg/stripex`,
the dunning state machine is `pkg/state.MarkAccountDeletionPending`
(ADR-021), GB-h = plan RAM + 8 MB per running second is in
`pkg/meter`. Functions: `guest/runners/node22` +
`guest/runners/python312` (handler contract per spec §4.9). Cron:
`pkg/sched/cron.go`, single-flight per scheduled fire, loop-tested
in `cron_loop_test.go`. Email: `pkg/mail` interface with Resend +
Postmark backends (gap G4).

**Remaining (pre-PR-#69 claim, now stale):** `cmd/meterd/main.go::defaultDeps` ships
nil `parker` and nil `stripe` collaborators; production never wires
`scheddgrpc.Dial(...)` or `stripex.NewClient(...)`, so quota hard-stop and
hourly Stripe usage push are not operational.
`pkg/stripex/usage.go::PushUsageRecord` is a `nil`-returning stub
(`TODO stripe-go`). PR #59 in flight.

**Actually remaining after #69 (which landed the wiring + dunning + SDK):**

- **Idempotent billing** — `usage_minutes` used to upsert-add on redelivered
  minutes, silently inflating bills under any meterd restart. PR #71
  (`feat/m7-beta-hardening`) flips to `ON CONFLICT DO NOTHING` and adds a
  parity test for the shared `BillableRAMMB` helper.
- **observability surface** — `cmd/meterd` previously had no `/metrics` or
  `/healthz`. Same PR #71 wires both via `wire.NewOpsMetrics("meterd")` and
  an inline `/healthz` (returning 200 unconditionally — richer semantics
  follow-up).
- **§14 M7 acceptance test (24h GB-h shadow, <0.1 % delta)** — not in tree.
  Required before §14 close; separate PR.
- **A3 (transactional suspend-and-park)**, **A4 (Free restore)**,
  **A5 (quota/dunning ordering race)** — separate PRs, polish.

## M7.5 — thin dashboard + githubd. ✅

`pkg/dashboard` ships server-rendered Go `html/template` pages
(apps, billing, usage, login, account, deployment-detail); ADR-011
keeps dashboard on the apid loopback, gatewayd reverse-proxies
`/dashboard/*` and `/oauth/*`. `pkg/githubd` + `cmd/githubd`
provide HMAC-verified webhook ingress, GitHub App OAuth + repo
picker, Checks-API status writer, and a per-install token cache
with proactive refresh. Magic-link auth lives in `pkg/state`
(`IssueLoginToken` / `ConsumeLoginToken`) with sealed cookies in
`pkg/session`. SSE live updates on `/v1/events`; `deployment_logs`
persistence landed. PR #41, ADR-011, ADR-012.

**Caveat:** `pkg/dashboard/templates/` load HTMX 2.0.4 but no
`hx-*` attributes are used yet (`apps_list.html` uses
`<meta refresh>`); HTMX polling is a follow-up.

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
  runs on EX44 (`make test-metal`) and Lima (`make metal-lima`),
  not on a bare dev box.
- **§12 SLO dashboard pipeline** — `fcvm_snapshot_fleet_avg_bytes`,
  `fcvm_snapshot_fleet_p95_bytes`, `fcvm_resident_ram_pct`,
  `fcvm_lv_fc_used_pct` (schedd-owned), plus
  `vmmd_cold_boot_fallback_total` (vmmd-owned, ADR-016) and
  `gateway_wake_queue_wait_seconds` (gatewayd-owned). Prometheus
  + node_exporter are ansible roles with SHA-256-pinned binaries,
  scrape config template at
  `deploy/ansible/roles/prometheus/templates/prometheus.yml.j2`.
  Grafana dashboard export at `deploy/grafana/faas-fleet.json`.
- **§12 public status page** — `apid` serves `GET /status` (static
  HTML, `deploy/statuspage/index.html`) and `GET /status/slo.json`
  (3 PromQL queries against the local Prometheus with a 30 s
  in-process cache and graceful degradation on transient failures;
  never 5xx the route).
- **§14 restore drill wired** —
  `deploy/scripts/faas-m8-restore-drill.sh` plus WAL-archiving
  knobs in the postgres ansible role. A timed EX44 run (PG + one
  app back serving < 30 min) is the next action; the dated record
  file `docs/drills/2026-07-20-restore-drill.md` is the template.
- **`leakcheck.sh` glob fix** matches the v1.7 jailer `--id`
  constraint.

The §14 M8 gates still on the board are listed in [What's next](#whats-next).

---

Post-M8 = private beta (founding doc M2–M3 hand-held phase).

## What's next

The M6 / M7 / M8 §14 acceptance gates still on the board. Pick one
and open an issue if you want it.

### M6

*(Closed — PR #60 closes #57. See [M6](#m6--builderd--real-image-pulls-) above.)*

### M7

- ~~**`cmd/meterd/main.go` wiring** — `defaultDeps` leaves `parker`
  and `stripe` nil. Wire `scheddgrpc.Dial(...)` for the quota
  hard-stop's `ScheddParker`, and `stripex.NewClient(...)` for the
  hourly push. Until then, the sampling loop runs but doesn't act
  on quota breaches or send Stripe usage records.~~ **Closed by
  PR #69** (`worktree-harden-meterd`).
- ~~**`pkg/stripex/usage.go::PushUsageRecord`** — currently
  `nil`-returning `TODO stripe-go`. Bring the SDK in, write a
  test against the Stripe sandbox.~~ **Closed by PR #69.**
- **§14 M7 acceptance test (24h GB-h shadow, <0.1 % delta)** — not
  in tree. Required before §14 close; separate PR.
- **Idempotent billing + meterd `/metrics` + `/healthz`** —
  PR #71 (`feat/m7-beta-hardening`).

### M8

- ~~**CertMagic TLS** for gatewayd (`*.apps.DOMAIN` via DNS-01;
  on-demand HTTP-01 gated by `custom_domains` allowlist).
  `pkg/gateway/tls.go` is a config bucket; `caddyserver/certmagic`
  not yet in `go.mod`.~~ **Closed by PR #70** (`worktree-m8-gateway-tls-wake-firstbyte`).
- **§14 V2 latency driver** — 100 park→wake cycles per app class,
  p50 ≤ 350 ms / p95 ≤ 800 ms. The Hobby-class gate is wired via
  `TestDeployWakeMetal/wake-latency-p50p95-100cycles` (extends the
  prior 10-cycle mean-only subtest). Per-app-class (Express, Next.js,
  Flask, FastAPI, Go static) gating is the M8 follow-up. Runs on
  `make metal-lima RUN_ARGS='-run TestDeployWakeMetal'`.
- **Documented timed restore drill** — §14 M8: PG + one app back
  serving on a clean VM < 30 min, recorded as executed. Run
  `deploy/scripts/faas-m8-restore-drill.sh` on the EX44 and fill
  in `docs/drills/2026-07-20-restore-drill.md` (template present).
- **Status page + SLO dashboard** — public SLOs from spec §12
  (API 99.5 % monthly, wake p95 < 1 s, build success ≥ 99 %). The
  pipeline (Prometheus scrape + Grafana JSON + `apid /status` +
  `apid /status/slo.json`) is in via PR #51; the operator
  verification step (Grafana panels render non-zero data, SLO
  JSON returns denominators) is the EX44 follow-up.
- **§11 checklist item-by-item sign-off** (cgroups v2 only,
  `unprivileged_userns_clone=0`, auditd, unattended-upgrades,
  etc.). The IPv6 egress item (ADR-023) is now in via PR #51;
  remaining items are operator verification on the EX44.
- **Gate-A runbook** — 2nd-box active-passive (founding doc R3).
