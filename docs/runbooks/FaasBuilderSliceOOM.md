# FaasBuilderSliceOOM

## Symptom

`FaasBuilderSliceOOMKill` means the parent `faas-cp-build.slice` OOM-kill
counter increased while builderd owned its single admitted build slot.
Builderd records the customer-visible terminal result as `build_oom` and logs
the correlated build ID, deployment ID, node, and Firecracker instance.

## Check

1. Find the matching structured builderd log and inspect the build and
   deployment IDs.
2. Read `/sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice/memory.events`
   and `memory.peak` on the affected node.
3. Check `journalctl -k` for the victim and compare the timestamp with
   `node_vmstat_oom_kill` and `builderd_builder_slice_oom_kills_total`.

## Recover

1. Keep builder admission at one VM while the parent `MemoryMax` is 5 GiB.
   Drain the node before changing that fence or the per-builder memory budget.
2. Retry the customer build only after memory pressure is back below the
   parent limit and no staging operation is sharing the builder slice.

If `FaasHostOOMKill` fires without the builder counter, identify the victim
from the kernel log and inspect the owning systemd cgroup before restarting it.
