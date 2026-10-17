# zswap

Memory-only compressed swap for frozen VMs (ADR-906 decision 6, ADR-1151).
Off by default; set `faas_zswap_enabled: true` for a compute host.

A frozen VM is paused in place after its idle timeout. Without swap its
memory stays resident until memory pressure parks it. With this role, schedd
first asks vmmd to compress a frozen VM under pressure: its pages move into
zswap, compressed in RAM, and the VM stays thawable. Thaws of a compressed
VM take longer (pages fault back in), but still avoid a snapshot restore.

| What | Where |
|---|---|
| zswap on, `lz4`, pool ≤ `faas_zswap_max_pool_percent` of RAM, no shrinker | `faas-zswap.service` → `/usr/local/sbin/faas-zswap-setup` |
| A swapfile that only provides zswap's slots (`faas_zswap_swapfile`, `faas_zswap_swapfile_size_mb`) | written once, never written again |
| `memory.zswap.writeback=0` on `faas-tenant.slice`: tenant memory never reaches the disk | `faas-zswap-setup` |
| `MemoryZSwapMax=faas_zswap_tenant_max` on `faas-tenant.slice` | `faas-tenant.slice.d/80-faas-zswap.conf` |
| `MemorySwapMax=0` on `faas-cp.slice`, `faas-cp-build.slice`, `system.slice`, `user.slice` and `init.scope` (PID 1) | `80-faas-noswap.conf` drop-ins |

vmmd closes swap on every running VM (`memory.swap.max=0`) and opens it only
while a VM is frozen, and only when it finds zswap enabled, a swap device and
writeback disabled on an ancestor of the VM's cgroup. Requires Linux 6.8+ and
systemd 253+; the role asserts both.

Watch it on the **faas frozen tier and quiet pause** dashboard
(`schedd_frozen_compress_total`, zswap pool size) and in
`docs/runbooks/FaasFrozenTier.md`.

## Turning it off

Setting `faas_zswap_enabled: false` does not undo anything. On the host:

1. Turn off the frozen tier on its schedd, or wait until no VM is frozen:
   `swapoff` pulls every compressed page back into RAM.
2. `systemctl disable --now faas-zswap.service`, then
   `swapoff /var/lib/faas/zswap/swapfile`.
3. `echo N > /sys/module/zswap/parameters/enabled`, remove the drop-ins and
   `/etc/systemd/system/faas-zswap.service`, and `systemctl daemon-reload`.
