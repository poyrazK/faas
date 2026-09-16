# FaasBuildExportRetention

`FaasBuildExportBytesHigh` means completed builder VM exports on a compute
node remain above the default 8 GiB cap. `FaasBuildExportCleanupFailing` means
builderd could not inventory, classify, lease, or remove at least one export.
These are compute SSD alerts: the export tree shares the disk used by snapshot
working state, so sustained growth can eventually break deploy and restore
work.

## Handoff and retention contract

A source build writes
`/srv/fc/builder/out/<build-id>/build/out/image.tar`. The deployment's
`rootfs_path` is the durable imaged ownership record. While imaged reads and
publishes that archive it holds a shared file lease. builderd removes a
directory only after PostgreSQL says the handoff was released and builderd can
take the exclusive file lease.

The production defaults are:

- released, failed, and cancelled exports: removed on the next sweep;
- sweep cadence: once at builderd startup, then every 5 minutes;
- active handoff recovery window: 24 hours;
- legacy/orphan grace before pressure cleanup: 1 hour;
- reclaimable export cap: 8 GiB, oldest eligible directory first.

An active `rootfs_path` handoff is not pressure-evicted. This can temporarily
put the tree above 8 GiB while imaged is unavailable; the 24-hour ceiling keeps
that failure mode finite and the byte alert makes it visible first.

## Triage

On the affected compute node:

```bash
curl -fsS http://127.0.0.1:9105/metrics | grep 'builderd_build_export_'
du -sh /srv/fc/builder/out
find /srv/fc/builder/out -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -n | head
journalctl -u faas-builderd.service --since '30 minutes ago' | grep 'build export sweep'
journalctl -u faas-imaged.service --since '30 minutes ago' | grep 'snapshot_boot\|build export'
```

For an old directory, use its basename as the build ID and inspect the durable
handoff before touching the filesystem:

```sql
select b.id, b.status as build_status, d.id as deployment_id,
       d.status as deployment_status, d.rootfs_path
from builds b
join deployments d on d.id = b.deployment_id
where b.id = '<build-id>';
```

If `rootfs_path` still names that directory's `image.tar`, imaged owns the
handoff. Check imaged health and notification recovery. If it names the final
application `.ext4`, the next builderd pass should remove the directory. A
rising error counter with permission errors means the export root ownership or
systemd `ReadWritePaths` contract drifted; reconverge the builderd service role
and restart builderd to run an immediate startup pass.

Avoid deleting a referenced `image.tar` by hand. The database reference is the
retry contract after a lost notification or imaged restart.

## Recovery verification

After recovery, confirm all four conditions:

1. `builderd_build_export_cleanup_errors_total` stops increasing.
2. `builderd_build_export_bytes` falls below the configured cap and no
   directory older than 24 hours remains.
3. A new source deploy reaches `live`, and its export directory disappears no
   later than the next five-minute sweep.
4. Redeploying the same source records a builder cache hit, reaches `live`, and
   creates no new retained export directory.
