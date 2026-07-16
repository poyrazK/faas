# One-Box FaaS

Scale-to-zero Functions-as-a-Service on Firecracker microVMs, running on a single
Hetzner EX44. Customer apps park as snapshots on disk and wake on request in
< 350 ms p50. Solo-operated.

- **Spec (source of truth):** [`docs/faas_implementation_spec.md`](docs/faas_implementation_spec.md)
- **UX spec:** [`docs/faas_ux_spec.md`](docs/faas_ux_spec.md)
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

- **M0 — repo scaffold.** ✅ Tree, build/test/lint tooling, CI, and the `pkg/api`
  limits table (single source of every plan quota). Remaining: ansible host
  bootstrap + hello-Firecracker metal test (need an EX44).
- **M1 — vmmd core.** 🚧 The invariant-critical logic is done and unit-tested
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

  Remaining: gRPC control surface for vmmd, and running the metal tests on KVM.
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

  Remaining: Postgres-backed routing + schedd gRPC Backend (M5), CertMagic TLS.
