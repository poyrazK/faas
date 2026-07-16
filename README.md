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
```

Go ≥ 1.23. Work milestones **M0 → M8 in order** (spec §14); a milestone is done
when its executable acceptance tests pass.

## Status

- **M0 — repo scaffold.** ✅ Tree, build/test/lint tooling, CI, the
  `pkg/api` limits table (single source of every plan quota),
  `deploy/ansible/` bootstrap (8 roles — cgroups_v2, grub, lvm, xfs,
  firecracker, systemd_slices, nftables, postgres — all idempotent on
  fresh Ubuntu 24.04), and a `TestMetalHelloBoot` acceptance test that
  boots a busybox guest from the pinned FC kernel. `make bootstrap`
  is the gate; it requires a fresh Hetzner EX44.
- **M1 — vmmd core.** ✅ The invariant-critical logic is done and unit-tested
  under `-race`:
  - `pkg/fcvm` slot allocator — every per-instance resource (jail uid/gid, host
    IP, iface names) derives from one unique slot, so §6.2-5 (no shared
    IP/netns/uid) holds by construction; proven with a concurrent property test.
  - `pkg/netns` — per-instance netns/veth/tap topology (ADR-009) as a testable
    `ip`-command plan.
  - `pkg/fcvm` cold-boot config + jailer argv builders (Appendix B).
  - `Manager` — full lifecycle with a **guaranteed no-leak unwind** on every
    failure path (tested with fakes).
  - `ExecRunner` + `JailerVMM` metal layer; M1 acceptance lives in
    `manager_metal_test.go` (`//go:build metal`, run on the EX44).
  - **gRPC control surface** — five RPCs (CreateFromSnapshot,
    CreateColdBoot, PauseAndSnapshot, Destroy, Stats) over a unix-domain
    socket at `/run/faas/vmmd.sock` (ADR-013/014/016); handlers in
    `pkg/vmmdgrpc`, wire shape in `api/proto/onebox/faas/vmmd/v1/vmmd.proto`,
    error envelope round-trip via `pkg/grpcerr`, ops metrics via `pkg/wire`.
    End-to-end coverage via `pkg/vmmdgrpc/bufconn_test.go`.

  Remaining: KVM + root to run `sudo make test-metal` on the EX44 (the
  gRPC + JVM-free code path is fully exercised in-process with fakes;
  the metal gate adds the firecracker/jailer side).
- **M2 — imaged + guest-init.** 🚧 The OCI→app-layer pipeline is done and tested:
  - `pkg/oci` — layer diff that extracts only the layers ABOVE the matched base
    (the two-drive scheme); refuses images not built FROM the base.
  - `pkg/rootfs` — layer applier (whiteouts + path-escape rejection), app-layer
    sizing + plan-cap enforcement, guest-init/app.json injection, and the
    `mkfs.ext4 -d` build. A real-mkfs integration test runs in Linux CI.
  - `pkg/api` — the `app.json` guest contract + app-layer-too-large error.
  - `guest/init` — PID 1: overlay assembly + crash-loop supervisor; pure logic
    unit-tested, Linux syscalls behind build tags. Guest IP via kernel `ip=`
    autoconfig (ADR-009), so no networking code in the guest.
  - `images/` — base/runner/builder Dockerfiles.

  Remaining: base-image→ext4 conversion, snapshot GC + fleet metrics, and the
  two-drive boot on metal.
- **M3 — snapshots + wake.** 🚧 Park/wake with the ADR-005 fallback, tested:
  - `pkg/fcvm` snapshot model + `PlanWake` — restore only when the snapshot is
    non-stale and matches the running Firecracker version; else cold boot.
  - `Manager.Wake` — restore-or-cold-boot where a **restore miss/failure falls
    back to cold boot** into the same netns and reports it (so schedd
    re-snapshots); `Manager.Park` snapshots then frees all RAM (§6.2-4).
  - `JailerVMM.Restore/Snapshot` via the Firecracker API over the jail socket;
    `DetectFirecrackerVersion` pins snapshot compatibility.
  - `guest/init` resume hook — re-seed entropy **before** the clock step (so a
    restored guest can't mint a duplicate UUID/TLS key, test V6); ordering
    unit-tested, entropy/clock ops behind Linux build tags.
  - Metal park→wake latency gate (`//go:build metal`).

  Remaining: vsock resume trigger wiring, and the metal latency/uniqueness runs.
- **M4 — gatewayd + schedd.** 🚧 Routing, wake-blocking, admission, reaping —
  all unit-tested:
  - `pkg/state` — the instance state machine (§6.1): legal transitions +
    which states count toward the concurrency / RAM invariants (§6.2-1/2).
  - `pkg/sched` — the **admission ledger** (the 47,600 MB headroom guard,
    per-app concurrency, 160 vCPU slots), the idle reaper, and eviction
    selection (LRU, never <30 s, Scale last).
  - `pkg/gateway` — per-app token-bucket rate limiter, host→app LRU route cache,
    and the **wake gate** (single-flight per app, 512/30 s cap), composed into a
    wake-blocking HTTP handler proven end-to-end with httptest (cold wake → 200
    + `x-faas-wake: cold`; 50 concurrent cold requests → 1 wake).
  - `schedd`/`gatewayd` wired to their cores; gatewayd serves HTTP.
  - `pkg/gateway` **`PGBackend`** (M5) — the production edge backend: host→app
    routing over Postgres (read-only; apid/schedd own the writes) with the
    10k-entry LRU route cache, plus schedd over gRPC (`pkg/scheddgrpc.Client`,
    ADR-018) for wakes. The app-target cache is seeded by a successful wake and
    kept coherent from the `instance_changed` / `app_changed` / `domain_changed`
    pg_notify channels. `cmd/gatewayd` now builds it in `run()` (the M4
    `unwiredBackend` stays as the test seam). Wake-denials round-trip their RFC
    7807 status via `pkg/grpcerr` (a lifted `*api.Problem` now recovers its HTTP
    status from the stable `Code`).

  Remaining: CertMagic TLS; wiring the gateway's last-seen flush to schedd
  `ReportActivity` (client method is ready).
- **M5 — apid + deploy pipeline + CLI.** 🚧 The control plane and CLI work
  end-to-end (verified live against a running apid):
  - `migrations/00001_init.sql` — the full schema (spec §5) with CHECK
    constraints and account-leading indexes.
  - `pkg/state` — domain types + the `Store` interface with an in-memory impl
    (the Postgres/sqlc store drops in behind the same interface).
  - `pkg/api` — API-key generation/hashing (SHA-256), wire DTOs, and app-config
    validation.
  - `apid` — the REST API: key auth, **plan-quota enforcement before work**
    (RAM/concurrency/app-count, verified by a table test across all four plans),
    image deploys, and Idempotency-Key replay.
  - `faas` CLI — `login`/`whoami`/`apps`/`deploy --image`, rendering the API's
    RFC 7807 problems in the three-line CLI shape (UX §3.3).
  - `apid`/`schedd` run on the pgx-backed `state.PgStore`; `gatewayd`'s
    `PGBackend` (see M4) closes the routing → schedd-wake half of the request
    path, so a request to a routed app now drives `schedd.Wake` over gRPC.

  Remaining (needs KVM, so it lands on the EX44, not in unit CI): imaged's
  snapshot-prime (cold-boot once → snapshot → app `PARKED`) and the metal
  park→wake so `faas deploy --image` → parked → first request wakes end-to-end.
  The prebuilt-image acceptance is the M5 gate (§14); builder microVMs are M6.
