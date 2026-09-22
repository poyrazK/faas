# ADR-203 · Role-aware node capacity from one source of truth

- **Status:** accepted
- **Date:** 2026-09-21
- **Decision:** Two changes. (1) The spec §13 reserve becomes **role-aware**:
  a `compute-only` host holds back `ComputeNodeDaemonReserveMB` (1,536 MB) +
  `BuilderSlotReserveMB` (2,048 MB) instead of the full
  `ControlPlaneReserveMB` (6,144 MB). (2) The admission ceiling and the
  tenant cgroup fence are both derived from
  `api.DeriveNodeSizingForRole` via a new
  `gregalectl compute-nodes sizing --json`, so ansible no longer carries its
  own copy of the arithmetic.
- **Why:** On the production 15,985 MB compute nodes, three different
  numbers claimed to be the tenant capacity, and no two agreed.

| source | tenant ceiling | owner |
| --- | ---: | --- |
| `faas-tenant.slice` base unit | 57,344 MB | `systemd_slices` role (64 GB reference figure) |
| `faas-tenant.slice.d/90-host-capacity.conf` | 8,192 MB | **nobody** — hand-written on the host, no role produced it |
| ansible join env `FAAS_COMPUTE_ADMISSION_CEILING_MB` | 13,587 MB | `node_join.yml`, as `MemTotal * 85 / 100` |
| `api.DeriveNodeSizing` (ADR-192 follow-on) | 6,468 MB | `pkg/api/limits.go` |

  schedd therefore admitted up to **13,587 MB** onto a host whose kernel
  fenced tenants at **8,192 MB**. The surplus is not theoretical: the Sep 20
  `CONSTRAINT_MEMCG` OOM kills of tenant firecracker processes on
  compute-node-1 are what over-admission looks like from the kernel's side.
  Notably there has never been a host-level (`CONSTRAINT_NONE`) OOM on these
  nodes — every kill was a cgroup fence doing its job.

  The `85 / 100` formula in ansible reserved nothing at all: not the 2,048 MB
  host OS reserve, not the daemons, not the builder slot. Meanwhile the Go
  derivation reserved too much, because it charged every host the
  *control-plane* slice. That slice pays for Postgres, apid, meterd, githubd,
  and gatewayd-public — none of which may even start on a `compute-only`
  host, where `pkg/role` refuses them. Measured steady state on fsn-1 and
  fsn-3 is ~490 MB RSS across every faas daemon and ~390 MB across the host
  agents, so 6,144 MB was funding roughly 2.5 GB of absent processes.

- **What this buys.** On a 15,985 MB compute node:

| | reserve | tenant slice | budget | admission ceiling |
| --- | ---: | ---: | ---: | ---: |
| single-box reserve (before) | 8,192 | 7,793 | 7,610 | 6,468 |
| role-aware (after) | 5,632 | 10,353 | 10,110 | **8,593** |

  That is +2,125 MB of admissible tenant RAM per node, a 33% gain, and the
  cgroup fence rises from the unmanaged 8,192 MB to the derived 10,353 MB.
  The 64 GB single-box reference is unchanged and still resolves to exactly
  57,344 / 56,000 / 47,600; a test pins this.

- **Why the builder slot is reserved, not admitted.** `builderd` is a
  compute-only daemon, and spec §4.5 guarantees one 2,048 MB build VM per
  node. Funding it out of the tenant slice would let a build and a tenant
  wake compete for the same page, which is exactly the "builds never outrank
  tenant wakes" rule inverted. It is held back instead. A node that does not
  run builderd could reclaim it, but no deployment shape does that today, so
  the reserve is unconditional for `compute-only` rather than guessing.

- **Why not simply keep 13,587 MB.** It was never backed by the machine.
  With no swap, 13 instances at their plan RAM plus the host OS exceeds
  16 GiB, and the cgroup fence would kill them one at a time. Raising the
  fence to match would trade legible per-instance OOM kills for host-level
  OOM, which takes down vmmd and every other tenant with it.

- **Consequences.**
  - Compute nodes re-register with a lower ceiling than the 13,587 MB
    currently in `compute_nodes`, and a higher one than ADR-192's follow-on
    would have produced. Apps pinned by `min_instances` above the new
    per-node ceiling will not all fit on one node; that is the true capacity
    speaking, not a regression.
  - `node_join.yml` now fails closed when `gregalectl compute-nodes sizing`
    cannot read `/proc/meminfo`, rather than registering single-box
    constants on a small host.
  - `faas-tenant.slice.d/90-host-capacity.conf` is now owned by ansible.
  - `pkg/hostsize` is shared by vmmd and gregalectl so the probe cannot
    drift between the daemon and the deploy layer.

- **Alternatives rejected.** Mirroring the new arithmetic into Jinja: that is
  the failure mode being removed, not a fix. Making the fence advisory
  (`memory.high`): it would soften the tenant slice into a reclaim hint and
  lose invariant §6.2-2's hard guarantee.
