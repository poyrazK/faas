# FaasBuilderSliceOOM

## Symptom

`FaasBuilderSliceOOMKill` means the parent `faas-cp-build.slice` OOM-kill
counter increased while one build was the slice's only builder. Builderd
records the customer-visible terminal result as `build_oom` and logs the
correlated build ID, deployment ID, node, and Firecracker instance. On a node
provisioned with more than one reserved slot (`builder_reserved_slots`,
ADR-20261118154632 amendment 1), a build that shared the slice is not blamed
for the counter; its own Firecracker exit status (137) still reports its OOM.

## Check

1. Find the matching structured builderd log and inspect the build and
   deployment IDs.
2. Read `/sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice/memory.events`
   and `memory.peak` on the affected node.
3. Check `journalctl -k` for the victim and compare the timestamp with
   `node_vmstat_oom_kill` and `builderd_builder_slice_oom_kills_total`.

## Recover

1. Check that the slice's `MemoryMax` and `CPUQuota` match the node's
   `builder_reserved_slots` (`gregalectl compute-nodes sizing
   --builder-reserved-slots N --json` prints the expected values; node_join
   writes them as `faas-cp-build.slice.d/90-builder-slots.conf`). Drain the
   node before changing that fence, the reserved count, or the per-builder
   memory budget.
2. Retry the customer build only after memory pressure is back below the
   parent limit and no staging operation is sharing the builder slice.

If `FaasHostOOMKill` fires without the builder counter, identify the victim
from the kernel log and inspect the owning systemd cgroup before restarting it.
