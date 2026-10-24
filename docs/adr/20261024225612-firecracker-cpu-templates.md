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

## Not covered

- Restore paths outside the app wake: crash-capture forks, function
  migration, execution-runtime snapshots and builder warm snapshots keep their
  current node choice. In a cell where every node runs the same template their
  captures are already portable; a mixed template-less cell remains unsafe for
  them, as today.
- vmmd does not re-check compatibility on the restore RPC.
- Custom templates and `cpu-template-helper` fingerprints.
