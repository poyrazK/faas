# ADR 20261024225612 · Firecracker CPU templates and CPU-compatible snapshot restore

- **Status:** proposed
- **Date:** 2026-10-25
- **Amends:** ADR-005 (what a snapshot is pinned to), spec §6.3 (wake
  placement)
- **Related:** ADR-025 (multi-node placement), ADR-1344 §8 (OVH launch cell)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A cell may need compute nodes with different CPUs. For the OVH launch in
Beauharnois (2026-10-25) the low-stock SYS-5 (Xeon Silver 4214R, Cascade
Lake) and the high-stock RISE-6 (Xeon Gold 6312U, Ice Lake) are the two
candidates, and the owner wants to be able to run both in one cell.

Today nothing ties a snapshot to the CPU it was captured on:

- Guests see the host's full CPUID; no Firecracker CPU template is set.
- `snapshotRejection` checks the Firecracker version, RAM, drive, base image,
  secrets policy and runtime config, but not the CPU.
- Restore placement only *prefers* nodes holding the snapshot; deployment
  primes use fleet-wide placement, so snapshots routinely restore on a node
  other than the one that captured them.

Firecracker's position (v1.17.0, `docs/snapshotting/snapshot-support.md`):
snapshots must resume on identical hardware; its example is an Ice Lake
snapshot that cannot be restored on Cascade Lake. A guest picks code paths
from CPUID at boot (glibc, OpenSSL, V8), so a restore onto a CPU missing an
instruction can fail to load or crash later. CPU templates normalise the
CPUID a guest sees. v1.17.0 still ships the static templates (deprecated
since v1.5.0) and accepts custom templates through `cpu-config`:

| Template | Firecracker says it is safe on |
|---|---|
| T2CL | Intel Cascade Lake, Ice Lake |
| T2S | Intel Skylake, Cascade Lake |
| C3, T2 | Intel Skylake, Cascade Lake, Ice Lake |
| T2A | AMD Milan |

## Decision

1. **vmmd boots every guest with the node's CPU template.** vmmd gains
   `cpu_template` (`FAAS_CPU_TEMPLATE` overrides; empty means none, today's
   behaviour). Every boot path goes through `JailerVMM.boot`, which sets
   `machine-config.cpu_template`. A restore keeps the CPUID its snapshot
   recorded, as Firecracker does.
2. **vmmd refuses a template its CPU cannot carry.** At startup vmmd reads
   `/proc/cpuinfo` and classifies the host (Skylake, Cascade Lake, Ice Lake,
   Milan, and others by vendor, family, model and stepping). A template whose
   table row does not list that class stops vmmd with an error. A guess is
   never made: an unknown class supports no template.
3. **Nodes and snapshots record their CPU identity.** `compute_nodes` gains
   `cpu_template` and `cpu_model`; `snapshots` gains the same two columns.
   `cpu_model` is the host's signature, `<class>/<vendor>-<family>-<model>-<stepping>/<flags hash>`.
   vmmd writes its own row at registration; schedd stamps the capturing
   node's identity into `snapshot_written`, and imaged persists it.
4. **A snapshot restores only on a CPU-compatible node.**
   - Captured with template *T*: any node whose CPU class is safe for *T*,
     whatever template that node now boots with.
   - Captured without a template: only a node with the same `cpu_model`.
   - Legacy rows (no identity): only template-less nodes, and only while
     every template-less active node reports one `cpu_model`. After a
     template rollout they cold-boot once and are recaptured, as on a
     Firecracker upgrade.
   The wake checks this before placement (no compatible active node: cold
   boot, reason `cpu_incompatible`) and restore placement chooses only among
   compatible nodes. If those are full while another node has room, the wake
   cold-boots on the other node instead of returning capacity (ADR-005: cold
   boot always works).
5. **schedd warns about a fleet that cannot share snapshots.** When active
   nodes run different templates, or template-less nodes report different
   CPU models, schedd logs a warning at start and whenever that changes, so
   an operator sees why restores fall back to cold boot. Wakes that cold-boot
   for this reason count under `cpu_incompatible` in the existing cold-reason
   metric.

The rule in 4 does not depend on rollout order: changing a node's template
only changes what its *new* captures carry.

## Rollout for a mixed Cascade Lake and Ice Lake cell

1. Set `vmmd_cpu_template: T2CL` for the cell's compute nodes (Ansible
   `vmmd_service` role; it renders `FAAS_CPU_TEMPLATE`) and converge every
   compute node. Each vmmd re-registers with the template.
2. Apps cold-boot once as their pre-template snapshots fall out of use, and
   park again with T2CL captures.
3. Add nodes of the other CPU. Their vmmd refuses to start unless the class
   is Cascade Lake or Ice Lake.
4. Before relying on it, run the cross-host check on the two real machines:
   capture on one, restore on the other, in both directions.

## Consequences

- One-time cold boot per app when a cell turns a template on or off.
- Guests on Ice Lake lose the instructions Cascade Lake lacks (for example
  AVX-512 VBMI, GFNI, VAES, SHA-NI). CPU-bound customer code can get slower.
- Static templates are deprecated upstream. Before a Firecracker release that
  removes them, replace the static name with an equivalent custom template
  sent as `cpu-config`; the snapshot identity then names the custom template.
- Firecracker does not promise cross-host restore even with a template; the
  check in rollout step 4 is the evidence, not this ADR.

## Amendment 1 (2026-10-25): one live snapshot per CPU platform

**Context.** Decision 4 keeps snapshots on compatible nodes, but a
deployment still had one live row per tier (`snapshots_deployment_tier_key`).
In a cell whose nodes cannot share snapshots (an AMD node joining Intel
nodes, or template-less nodes on different CPUs) an app that cold-booted on
the other side either reused the incompatible row at park and never captured,
or its capture was discarded as a duplicate. That app cold-booted on that
side on every wake. The owner asked for the scheduler to know which platform
each snapshot belongs to and to have a new platform produce its own.

**Decision.**

1. **A platform is a snapshot slot.** The platform is the capturing node's
   template (`template:T2CL`), or its host signature when it has none
   (`state.SnapshotCPUPlatform`). The unique index becomes
   `snapshots_deployment_tier_platform_key` on (deployment, tier, platform)
   for live rows, so each platform keeps its own live row per tier. Rows
   from before the ADR share the empty platform.
2. **A park captures for its node's platform.** Park reuse and the freeze
   decision count a live row only if it restores on the instance's own node.
   The first idle period of an app on a new platform parks and captures; the
   capture lands in that platform's slot and the other platforms keep theirs.
3. **A wake tries each platform.** The wake takes the newest live row that
   passes its checks, warm before init. If every node that can restore it is
   full, it tries the other platforms' rows on their nodes, then cold-boots
   wherever there is room (decision 4).
4. **imaged keeps per-platform rows.** A capture conflicts only with its own
   platform's live row; the drive and Firecracker-version replacement rules
   apply to that row alone.
5. **Invalidation covers every platform.** Liveness, image-healthcheck,
   force-restart, OOM, disk-pressure and egress-abuse paths mark every live
   row of the tier stale, not only the newest.

**Consequences.** A deployment can hold up to one row per tier per platform:
more stored bytes in a mixed cell, none in a uniform one. The migration is
`contract` (it drops the old unique index): a binary from before the ADR
would not choose between two platforms' rows.

## Evidence

- 2026-10-25, `gregale-internal-test-1` (n2, nested KVM, Intel Cascade Lake
  6/85/7, Firecracker v1.17.0): `TestMetalCPUTemplateBootAndRestore` passed.
  vmmd classified the host `cascadelake/GenuineIntel-6-85-7/7402428c5915`, a
  guest cold-booted with T2CL, `GET /machine-config` reported `T2CL`, and the
  VM parked and restored under the template. The template-less control
  `TestMetalFreePageReportingBalloon` passed in the same run; leakcheck was
  clean before and after. Log: `/mnt/e2e-scratch/cputpl-20261025/logs/fcvm.log`.
- Cross-host restore between Cascade Lake and Ice Lake is not yet shown; it
  needs the two real machines (rollout step 4).

## Not covered

- Restore paths outside the app wake: crash-capture forks, function
  migration, execution-runtime snapshots and builder warm snapshots keep their
  current node choice. In a cell where every node runs the same template their
  captures are already portable; a mixed template-less cell remains unsafe for
  them, as today.
- vmmd does not re-check compatibility on the restore RPC.
- Custom templates and `cpu-template-helper` fingerprints.
