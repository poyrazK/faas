# Gregale

[![ci](https://github.com/poyrazK/faas/actions/workflows/ci.yml/badge.svg)](https://github.com/poyrazK/faas/actions/workflows/ci.yml)
[![CodeQL](https://github.com/poyrazK/faas/actions/workflows/codeql.yml/badge.svg)](https://github.com/poyrazK/faas/security/code-scanning)
[![codecov](https://codecov.io/gh/poyrazK/faas/graph/badge.svg)](https://codecov.io/gh/poyrazK/faas)

Scale-to-zero Functions-as-a-Service on Firecracker microVMs, deployable on any
bare-metal x86_64 host. Customer apps park as snapshots on disk and wake on
request in < 350 ms p50. Gregale runs one box today and is engineered to scale
out to many control-plane nodes without a code rewrite (see
[`docs/scale_out_and_workload_classes.md`](docs/scale_out_and_workload_classes.md)).

- **Spec (source of truth):** [`docs/faas_implementation_spec.md`](docs/faas_implementation_spec.md)
- **UX spec:** [`docs/faas_ux_spec.md`](docs/faas_ux_spec.md)
- **API hosting product roadmap:** [`docs/api-hosting-roadmap.md`](docs/api-hosting-roadmap.md)
- **API hosting capability matrix:** [`docs/capabilities.md`](docs/capabilities.md)
- **Scale-out & workload classes (forward plan):** [`docs/scale_out_and_workload_classes.md`](docs/scale_out_and_workload_classes.md)
- **Decisions:** [`docs/adr/`](docs/adr/) (ADR-001–010 inline in spec §3; ADR-011–023 filed separately)
- **Status / what's next:** [`docs/STATUS.md`](docs/STATUS.md)
- **Agent guide:** [`CLAUDE.md`](CLAUDE.md)

## Layout

```
cmd/      one Go binary per daemon + the gregale CLI
pkg/      shared libraries (api = the single limits table)
guest/    code that runs inside every microVM (PID1 + runners)
images/   Dockerfiles for shared base rootfs images
deploy/   ansible bootstrap, systemd slices, nftables, ops scripts
migrations/  goose, legacy-sequential then timestamped, append-only
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

The metal tests run on any bare-metal x86_64 host with `/dev/kvm` and root. On
an Apple Silicon **M3+ Mac (macOS 15+)** you can run them locally via Lima
nested KVM (arm64) — see [`deploy/lima/README.md`](deploy/lima/README.md). This
is a fast dev loop for the arch-agnostic VM lifecycle; a production x86_64
control-plane node remains the acceptance source of truth.

## Status

Spec §14 milestones. Long form (per-PR attribution, what's left on
each board) lives in [`docs/STATUS.md`](docs/STATUS.md).

[^legacy]: M0–M6 used the monolithic `gatewayd` daemon; the public edge
    was split into `gatewayd-public` (TLS) and `gatewayd-internal`
    (routing + wake + proxy) per ADR-070 in PR-A. Milestone labels
    here still reference the M-era monolithic name for continuity.

- **M0–M6 ✅** — scaffold, vmmd, imaged, apid, gatewayd[^legacy], builderd.
- **M7 ✅** — meterd wiring + stripe-go SDK landed in PR #59 (closes #52).
- **M8 🚧** — §11 hardening + SLO dashboard landed; §14 drills +
  CertMagic pending.
- **Tier A 🚧** — multi-host control plane (placement, snapshot de-localization,
  cross-node live migration). See [`docs/scale_out_and_workload_classes.md`](docs/scale_out_and_workload_classes.md).

Post-M8 = private beta. Multi-host capacity will roll out incrementally behind
the same §14 acceptance gates.

## What's next

M6 / M7 / M8 §14 acceptance gates still on the board — see
[`docs/STATUS.md`](docs/STATUS.md) for the working list (it gets
updated as issues close).
