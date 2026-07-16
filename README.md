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
