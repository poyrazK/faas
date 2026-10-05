# FaasHostDiskSpaceLow / FaasHostDiskSpaceCritical / FaasHostDiskFillingFast / FaasHostInodesLow

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_host_resources`).
Metrics: `node_filesystem_avail_bytes`, `node_filesystem_size_bytes`,
`node_filesystem_files_free`, `node_filesystem_files` (node_exporter;
`job="node"` on the control plane, `job="node-compute"` on compute hosts).
Severity: Low and Inodes warn; Critical and FillingFast page.

## Symptom

A host filesystem is running out of bytes or inodes.

- `FaasHostDiskSpaceLow`: below 15% free for 15 minutes.
- `FaasHostDiskSpaceCritical`: below 5% free for 5 minutes.
- `FaasHostDiskFillingFast`: below 25% free and the six-hour trend reaches
  zero within four hours.
- `FaasHostInodesLow`: below 10% free inodes for 15 minutes.

tmpfs (including the `/srv/fc/jail` chroots), EFI and loop-mounted image
filesystems are excluded. `/srv/fc` is also covered by
[FaasLvFcUsageHigh](FaasLvFcUsageHigh.md); this family measures the
filesystem directly, so it also works on hosts without LVM.

What fills first:

| Host | Mount | Usual writers |
|---|---|---|
| control plane | `/` | PostgreSQL data and `pg_wal` under `/var/lib/postgresql`, Prometheus TSDB under `/var/lib/prometheus`, release directories under `/opt/faas/releases`, the journal |
| compute | `/srv/fc` | snapshots, app layers and the runtime cache (`/var/lib/faas/cache` is a bind of `/srv/fc/cache`) |
| compute | `/` | release directories, `/var/log/faas/vm-*.console`, log-archive spools under `/var/log/faas`, the journal |

## Check

```bash
df -h -x tmpfs -x devtmpfs -x squashfs
df -i -x tmpfs -x devtmpfs -x squashfs
sudo du -xh --max-depth=2 <mountpoint> 2>/dev/null | sort -rh | head -20
journalctl --disk-usage
```

On the control plane, a growing `pg_wal` usually means WAL archiving is
failing; confirm before deleting anything:

```bash
sudo -u postgres psql -Atc "select archived_count, failed_count, last_failed_wal, last_failed_time from pg_stat_archiver"
sudo du -sh /var/lib/postgresql/*/main/pg_wal
```

If `failed_count` is rising, follow [PostgresBackup](PostgresBackup.md).
Never delete files from `pg_wal` by hand.

On a compute host, compare `/srv/fc` against the snapshot rows that should
exist and check that imaged's GC is running:

```bash
journalctl -u faas-imaged --since '-1h' --no-pager | grep 'gc tick' | tail -3
```

For inode exhaustion, count files per directory:

```bash
sudo find <mountpoint> -xdev -type f | cut -d/ -f2-4 | sort | uniq -c | sort -rn | head
```

## Recover

- Journal: `sudo journalctl --vacuum-size=256M` (host_hardening bounds it
  at 512 MiB; a larger journal means the bound was not applied).
- Old releases: keep the directory `/opt/faas/current` points at and the
  previous one for rollback; remove older ones only after confirming no
  unit references them (`systemctl cat 'faas-*' | grep /opt/faas/releases`).
- VM console logs: `/var/log/faas/vm-*.console` for instances that no
  longer exist can be removed; check the instance is not live first.
- Snapshots: let imaged reclaim them. Deleting snapshot files by hand
  leaves rows that point at missing data and forces cold boots.
- PostgreSQL: fix archiving first; once `failed_count` stops rising the
  archiver drains `pg_wal` on its own.
- If the volume itself is too small, grow the provider disk and the
  filesystem (`xfs_growfs /srv/fc`, `resize2fs` for ext4 roots), then
  confirm `df` shows the new size.

## Silence

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 silence add \
  'alertname=~"FaasHost(DiskSpace.*|DiskFillingFast|InodesLow)"' instance="<instance>" \
  --duration=2h --comment='disk cleanup in progress'
```
