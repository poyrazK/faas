# App VM memory headroom above the billable fence

Status: accepted

Amends: spec §4.4 and §11 ("cgroup scope `memory.max = plan + 8 MB`"),
ADR-005 (cold boot must always work).

## Context

Every app and job VM's cgroup `memory.max` was `ram_mb + 8 MB`
(`api.BillableRAMMB`), the same number admission and billing use.

On prod, every kernel OOM kill on the compute nodes from 2026-11-15 to
2026-11-16 (six) was the Firecracker process of a 128 MB app, killed at its
136 MiB fence during a **cold boot**. Each kill had the same shape: guest
memory 123–129 MB anonymous plus ~16 MB of Firecracker's own file-backed
pages. A cold-booting guest touches all of its RAM; the VMM's own pages do not
fit in 8 MB on top of a full guest. Snapshot restores fit because guest memory
is faulted in lazily, so these apps only started from a snapshot. Every time
a snapshot was missing or refused (four of the six followed "snapshot backing
images changed" after a base image update), the wake failed and the queued
request got a 502. 25 active apps run at 128 MB. That breaks ADR-005: cold
boot must always work.

## Decision

1. An app or job VM's `memory.max` is `api.AppVMMemoryMaxMB(ram_mb)` =
   `ram_mb + PerVMOverheadMB (8) + AppVMHeadroomMB (24)`. Every place that
   writes or records the fence uses it: the jailer `--cgroup memory.max`
   argument, the post-boot fence, the fence restored after a snapshot widen,
   native snapshot memory limits and the adoption journal.
2. Admission, billing, metering and invariant 2 are unchanged: they keep
   `BillableRAMMB` (`ram_mb + 8`). The headroom is a kill fence, not a
   reservation. Measured admission (ADR-909) already sees what VMs really use.
3. No `memory.high` below the fence. Database VMs use one (ADR-1027) because
   their excess is reclaimable page cache; an app guest's excess is hot
   anonymous memory and VMM pages, which `memory.high` would only throttle.
4. Builder VMs (`BuilderMemoryMaxMB`) and database VMs keep their own fences.

24 MB covers the observed ~17–19 MB excess over `ram_mb` with margin.

## Consequences

- A VM can hold up to 24 MB more host RAM than its billable size. Across the
  2× measured-admission overcommit this is bounded by measured memory and the
  eviction ceiling, as before.
- VMs already running keep their old fence until their next wake: adoption
  compares against the value recorded at boot, and a mismatch is repaired to
  the new fence, never refused.
- Small apps no longer depend on a snapshot to start.

## Alternatives

- **Give the guest less RAM than its plan.** The customer gets less than they
  pay for; rejected.
- **A 256 MB minimum app size.** Breaks 25 existing apps and the 128 MB tier
  customers chose; rejected.
- **Raise `PerVMOverheadMB`.** Moves billing and admission for every VM to fix
  a fence problem; rejected.
