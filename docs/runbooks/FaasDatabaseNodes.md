# Native managed PostgreSQL database nodes

ADR-1027 database nodes run each customer database in its own Firecracker
VM. On each node, `pgproxyd` is the only public listener, `vmmd` boots and
parks the VMs, and `dbbackupd` keeps the WAL archive and base backups and
ships them to the bucket. Control-plane Prometheus scrapes all three and
`node_exporter` on each node's overlay address. The targets come from the
manifest's database hosts. Rules: `deploy/ansible/roles/prometheus/files/database_nodes.rules.yml`.

| Job | Port | Daemon |
| --- | --- | --- |
| `node-database` | 9100 | node_exporter (host disk, memory and clock alerts in `faas.rules.yml`) |
| `vmmd-database` | 9104 | vmmd in database mode |
| `pgproxyd` | 9109 | pgproxyd |
| `dbbackupd` | 9110 | dbbackupd |

The `node` label is the manifest host name, which is also the node's id in
schedd's `[database_scheduler]`. No series carries a database or account
id. To find one customer's database, look up its `node_id` in
`database_vms` on the control plane.

## FaasDatabaseNodeDaemonDown (page)

`pgproxyd` or `vmmd` on a database node has not answered a scrape for 2
minutes.

- If `pgproxyd` is down, no client reaches any database on the node, and
  app guests lose the app route to it.
- If `vmmd` is down, nothing on the node wakes, boots, parks or archives
  WAL. Running databases keep serving and cannot park. A restarted vmmd
  recovers their jails.

1. Check whether the whole node is unreachable. If `node-database` is also
   down, treat it as a node loss (below).
2. On the node: `systemctl status faas-pgproxyd faas-vmmd` and
   `journalctl -u faas-pgproxyd -u faas-vmmd --since -30m`. Both are
   `Type=notify` with a watchdog, so systemd restarts a hung daemon. A
   daemon that stays down failed at startup. Read its last error: a
   missing TLS file, a refused control-plane DSN (pgproxyd) or an
   unbuildable base (vmmd).
3. A scrape can fail while the daemon is healthy. Check that the node's
   firewall admits the control plane on 9104/9109: the overlay address in
   `database_node_metrics_cidrs`.

**Node loss.** Running production-class databases fail over to their
synchronized standbys by themselves once the node has not answered schedd
for `auto_failover_after` (default 30 s). Parked ones fail over the same
way. Every other database waits for the operator. Mark the node
`lifecycle = "unavailable"` in schedd's `[database_scheduler]` and
restart schedd. schedd then fails over what it can and parks the rest
with `node_lost`. With `shared_archive`, they restore onto another node
from the bucket. Each loses at most what its archive had not shipped (see
the stale alert below).

## FaasDatabaseArchiveKeeperDown (warn)

`dbbackupd` has been down for 10 minutes. Each database's `archive_command`
fails, so PostgreSQL keeps its WAL in `pg_wal` on its own volume, and the
off-node recovery point stops advancing. Nothing is lost while the node
lives. A long outage fills volumes with WAL.

`systemctl status faas-dbbackupd` and `journalctl -u faas-dbbackupd`. It
refuses to start with an unreadable store directory or a bad `[remote]`
block. Once it returns, PostgreSQL's archiver catches up by itself.

## FaasDatabaseArchiveOffNodeStale (warn)

`dbarchive_pending_oldest_seconds` is above 15 minutes: the oldest archive
file on the node that is not yet in the bucket. If the node were lost now,
the commits it holds would be lost too.

1. `dbarchive_upload_errors_total` rising means uploads are failing.
   `journalctl -u faas-dbbackupd | grep "upload pass failed"` names the
   error, usually bucket credentials, endpoint reachability or a quota.
2. With no upload errors, the node is producing archive faster than it
   uploads (`dbarchive_pending_files` keeps growing). Check the node's
   egress bandwidth.

## FaasDatabaseArchivePruneFailing (warn)

At least two hourly prune passes failed in 3 hours
(`dbarchive_prune_errors_total`). Archives keep files past their retention,
locally and in the bucket. The `node-database` disk alerts fire before the
archive disk fills. `journalctl -u faas-dbbackupd | grep prune` names the
failing database or bucket call.

## FaasDatabaseWakeRefusals (warn)

More than a fifth of authenticated sessions on a node got `57P03` ("the
database is starting or unavailable") for 10 minutes. Clients retry, but
these sessions found their databases unwakeable.

1. schedd's logs (`journalctl -u faas-schedd | grep dbsched`) show why
   wakes fail or queue: the node's RAM ceiling, boot slots, a moving or
   resizing database, or a failed boot.
2. If one database dominates, check its row in `database_vms` (`state`,
   `last_error`) before looking at the node.

## FaasDatabaseWakeSlow (warn)

The p95 of `pgproxyd_wake_seconds`, from an authenticated client to a
running database, has been above 5 s for 15 minutes. A cold wake takes
about a second (930 ms on the reference node). A wake that adopts a spare
VM takes about 400 ms.

- Wakes queue when a node reaches its `admission_ceiling_mb` or its
  `boot_slots`. Raise the ceiling only within the node's RAM, or add a
  node.
- Without spares for the common class shapes, every wake cold-boots. See
  `database_vmmd_spares`.
- A slow volume shows as high `node_disk_io_time_seconds_total` on the
  node.
