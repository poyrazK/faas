# One-Box FaaS

[![ci](https://github.com/poyrazK/faas/actions/workflows/ci.yml/badge.svg)](https://github.com/poyrazK/faas/actions/workflows/ci.yml)
[![CodeQL](https://github.com/poyrazK/faas/actions/workflows/codeql.yml/badge.svg)](https://github.com/poyrazK/faas/security/code-scanning)
[![codecov](https://codecov.io/gh/poyrazK/faas/graph/badge.svg)](https://codecov.io/gh/poyrazK/faas)

Scale-to-zero Functions-as-a-Service on Firecracker microVMs, running on a single
Hetzner EX44. Customer apps park as snapshots on disk and wake on request in
< 350 ms p50. Solo-operated.

- **Spec (source of truth):** [`docs/faas_implementation_spec.md`](docs/faas_implementation_spec.md)
- **UX spec:** [`docs/faas_ux_spec.md`](docs/faas_ux_spec.md)
- **Scale-out & workload classes (forward plan):** [`docs/scale_out_and_workload_classes.md`](docs/scale_out_and_workload_classes.md)
- **Decisions:** [`docs/adr/`](docs/adr/) (ADR-001–010 inline in spec §3)
- **Agent guide:** [`CLAUDE.md`](CLAUDE.md)

## Layout

```
cmd/      one Go binary per daemon + the faas CLI
pkg/      shared libraries (api = the single limits table)
guest/    code that runs inside every microVM (PID1 + runners)
images/   Dockerfiles for shared base rootfs images
deploy/   ansible bootstrap, systemd slices, nftables, ops scripts
migrations/  goose, numbered, append-only
docs/     spec, UX spec, ADRs
```

## Develop

```
make build       # build every daemon into ./bin
make test        # unit tests — any machine, no KVM
make test-metal  # integration tests (//go:build metal) — needs KVM + root
make leakcheck   # assert zero leaked netns/TAPs/jails/cgroups
make lint        # vet + gofmt (golangci-lint if installed)
make metal-lima  # run metal tests locally on an M3+ Mac via Lima nested KVM
```

Go ≥ 1.23. Work milestones **M0 → M8 in order** (spec §14); a milestone is done
when its executable acceptance tests pass.

The metal tests normally need the x86_64 EX44. On an Apple Silicon **M3+ Mac
(macOS 15+)** you can run them locally via Lima nested KVM (arm64) — see
[`deploy/lima/README.md`](deploy/lima/README.md). This is a fast dev loop for the
arch-agnostic VM lifecycle; the EX44 stays the acceptance source of truth.

## Status

- **M0 — repo scaffold.** ✅ Repo tree, build/test/lint tooling, CI,
  `pkg/api` limits table, 8-role ansible bootstrap, hello-boot
  acceptance test. `make bootstrap` gates it on a fresh EX44.
- **M1 — vmmd core.** ✅ Invariant-critical VM lifecycle: slot
  allocator (`pkg/fcvm`), per-instance netns/TAP (`pkg/netns`,
  ADR-009), cold-boot config + jailer argv (Appendix B / ADR-019),
  `Manager` with no-leak unwind, metal layer
  (`manager_metal_test.go`), and the 5-RPC gRPC surface at
  `/run/faas/vmmd.sock` (ADR-013/014/016, `pkg/vmmdgrpc`).
  KVM + root required for the metal gate.
- **M2 — imaged + guest-init.** ✅ OCI→app-layer pipeline, two-drive
  scheme (`pkg/oci` diff + `pkg/rootfs` applier), base→ext4
  auto-stage (`pkg/imaged::EnsureBaseExt4`), real-mkfs build in
  Linux CI, `guest/init` overlay + crash supervisor, two-drive boot
  verified metal-side (`cmd/e2e/deploy_wake_metal_test.go`) — see M5
  *Remaining* below (same file has a known body/trim fixture mismatch
  that also blocks M5's §14 gate).
- **M3 — snapshots + wake.** ✅ Park/wake with the ADR-005
  restore-or-cold-boot fallback, FC version pinning
  (`snapshots.fc_version`), and the vsock post-restore resume hook
  (ADR-022) that re-seeds entropy + steps clock — V6 acceptance
  green in `pkg/fcvm/v6_resume_ext4_metal_test.go`. §14 V2 latency
  loop driver (100 cycles, p50 ≤ 350 ms) still missing — see
  *What's next*.
- **M4 — gatewayd + schedd.** ✅ Routing, wake gate, admission
  ledger (47,600 MB headroom / 160 vCPU), G7 flow-aware reaper
  (`pkg/sched/flowcount`), `PGBackend` PG routing, schedd-over-gRPC
  (ADR-018), last-seen flush, 1k rps CI-asserted hot-path load test
  (PR #44), per-VM `memory.max` + per-plan `tc` egress (PR #37).
- **M5 — apid + deploy pipeline + CLI.** 🚧 Production wiring is in
  via the pgx-backed `state.PgStore`, real `rootfs.Builder` in
  `pkg/imaged::handleDeployment` (PR #26), plan-quota table-tests
  (`cmd/e2e/quota_e2e_test.go`), the snapshot-prime handshake that
  flips a deployment to `live` after one cold-boot priming cycle,
  and the G2 sealed-secrets path (PR #42); `faas` CLI renders
  RFC 7807 problems (UX §3.3).
  Remaining: `cmd/e2e/deploy_wake_metal_test.go` has a body/trim
  mismatch on its own fixture — the M5 §14 metal acceptance gate
  does not pass on a clean checkout. See *What's next*.
- **M6 — builderd + real image pulls.** 🚧 Build-in-microVM is wired
  through (`cmd/builderd`, `pkg/builderd` orchestration + executor,
  PRs #39/#40/#43); the metal lifecycle is in `vm_metal.go`
  (`//go:build metal`) and calls vmmd over gRPC, with `vm_stub.go`
  returning `ErrNotMetal` for non-metal builds. OCI puller hardened
  (`pkg/oci/egress.go` — denied CIDRs cover RFC1918, CGN, loopback,
  IMDS, ULA), streamed layer blobs (`b79e370`). `cmd/imaged`
  auto-stages `/srv/fc/base/builder-base.ext4` on startup
  (`50c01c1`).
  Remaining: (a) `pkg/builderd/drive.go` writes `build.json` into
  the builder VM but does not copy `VMRequest.SourcePath`, so no
  real `npm install` / `pip install` runs; (b) the Dockerfile kind
  enum (`pkg/api/build.go` ↔ `pkg/builderd/detect.go`) currently
  falls through to Railpack-auto for `kind=dockerfile` — the §14
  M6 gate requires it to dispatch to `buildctl` per ADR-004. See
  *What's next*.
- **M7 — metering, billing, functions, cron.** 🚧 The sampling/quota
  shapes are in `cmd/meterd` and `pkg/stripex`, the dunning state
  machine is `pkg/state.MarkAccountDeletionPending` (ADR-021), GB-h
  = plan RAM + 8 MB per running second is in `pkg/meter`.
  Functions: `guest/runners/node22` + `guest/runners/python312`
  (handler contract per spec §4.9). Cron: `pkg/sched/cron.go`,
  single-flight per scheduled fire, loop-tested in
  `cron_loop_test.go`. Email: `pkg/mail` interface with Resend +
  Postmark backends (gap G4).
  Remaining: `cmd/meterd/main.go::defaultDeps` ships nil `parker`
  and nil `stripe` collaborators; production never wires
  `scheddgrpc.Dial(...)` or `stripex.NewClient(...)`, so quota
  hard-stop and hourly Stripe usage push are not operational.
  `pkg/stripex/usage.go::PushUsageRecord` is a `nil`-returning
  stub (`TODO stripe-go`). See *What's next*.
- **M7.5 — thin dashboard + githubd.** ✅ `pkg/dashboard` ships
  server-rendered Go `html/template` pages (apps, billing, usage,
  login, account, deployment-detail); ADR-011 keeps dashboard on
  the apid loopback, gatewayd reverse-proxies `/dashboard/*` and
  `/oauth/*`. `pkg/githubd` + `cmd/githubd` provide HMAC-verified
  webhook ingress, GitHub App OAuth + repo picker, Checks-API
  status writer, and a per-install token cache with proactive
  refresh. Magic-link auth lives in `pkg/state`
  (`IssueLoginToken` / `ConsumeLoginToken`) with sealed cookies in
  `pkg/session`. SSE live updates on `/v1/events`;
  `deployment_logs` persistence landed. PR #41, ADR-011, ADR-012.
  Caveat: `pkg/dashboard/templates/` load HTMX 2.0.4 but no
  `hx-*` attributes are used yet (`apps_list.html` uses
  `<meta refresh>`); HTMX polling is a follow-up.
- **M8 — hardening & ops.** 🚧 All §11 ship-blockers and §12 ops
  surfaces from this milestone's closeout are in via PRs
  #46 / #47 / #48 / #49 (G6 GDPR + 30-day staged deletion per
  ADR-021; V6 vsock resume hook per ADR-022; G7 flow-aware reaper
  in `pkg/sched/flowcount`; `AuthLimit` shared per-IP bucket across
  `/v1/*` per §11 "10/min/IP"; per-VM cgroup scope via jailer
  `--cgroup cpu.weight`; cold-wake UX surfaces 3+4+5 with
  `x-faas-wake: cold|cache|ready` and dashboard N+1 spinner) and
  PR #51 (the closeout batch):
  - **§11 IPv6 egress** — `pkg/netns/policy.go` and
    `pkg/netns/config.go` now deny `fe80::/10, fc00::/7, ff00::/8,
    ::1/128, ::/128` via `ip6 daddr { … } drop` (ADR-023), in both
    the host firewall and the per-instance netns ruleset.
  - **§11 cgroup fence verified** — `#33` `memory.max = plan + 8 MB`
    after bringUp; metal test
    `pkg/fcvm/manager_metal_test.go::TestMetalMemoryMaxFenceEnforced`
    is green on Lima (the EX44 sign-off remains the §14 source of
    truth per CLAUDE.md).
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
  - **#32 cleanup** — `docs/adr/021-vsock-resume-hook.md` removed
    (superseded by ADR-022); `deploy/scripts/leakcheck.sh` glob fix
    matches the v1.7 jailer `--id` constraint.
  The §14 M8 gates still on the board are listed in *What's next*.

Post-M8 = private beta (founding doc M2–M3 hand-held phase).

## What's next

The M6 / M7 / M8 §14 acceptance gates still on the board. Pick one
and open an issue if you want it.

**M6**

- **`pkg/builderd/drive.go`** — copy `VMRequest.SourcePath` into
  the builder VM's scratch disk. Without it the builder VM has no
  source tarball, so no real `npm install` / `pip install` runs.
- **Dockerfile kind enum dispatch** — `pkg/api/build.go`'s enum is
  `"railpack_node" / "railpack_python" / "dockerfile"` but
  `pkg/builderd/detect.go` casts `"docker"` through Railpack-auto.
  Wire `"dockerfile"` → `buildctl --frontend dockerfile` per
  ADR-004. Needs a smoke test on the metal builder VM before the
  §14 M6 gate is green.

**M7**

- **`cmd/meterd/main.go` wiring** — `defaultDeps` leaves `parker`
  and `stripe` nil. Wire `scheddgrpc.Dial(...)` for the quota
  hard-stop's `ScheddParker`, and `stripex.NewClient(...)` for the
  hourly push. Until then, the sampling loop runs but doesn't act
  on quota breaches or send Stripe usage records.
- **`pkg/stripex/usage.go::PushUsageRecord`** — currently
  `nil`-returning `TODO stripe-go`. Bring the SDK in, write a
  test against the Stripe sandbox.

**M8**

- **CertMagic TLS** for gatewayd (`*.apps.DOMAIN` via DNS-01;
  on-demand HTTP-01 gated by `custom_domains` allowlist).
  `pkg/gateway/tls.go` is a config bucket; `caddyserver/certmagic`
  not yet in `go.mod`.
- **§14 V2 latency driver** — 100 park→wake cycles per app class,
  p50 ≤ 350 ms / p95 ≤ 800 ms. `cmd/e2e/deploy_wake_metal_test.go`
  does one cold wake; the loop driver doesn't exist. Runs on the
  EX44 via `make test-metal`.
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
