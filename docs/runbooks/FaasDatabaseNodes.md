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
way. Every other database waits for the operator. Set the host's
`database.lifecycle: unavailable` in the manifest, run `make
manifest-ansible`, and converge the control plane. Rendering schedd's
`[database_scheduler]` restarts schedd. schedd then fails over what it
can and parks the rest with `node_lost`. A parked one stays on the lost
node until it is recovered by a point-in-time restore. With
`shared_archive`, that restore runs on another node from the bucket, up
to the end of the archive. Each loses at most what its archive had not
shipped (see the stale alert below). Then converge the
compute and database nodes, which drops the lost node from the app route
and the cancel peers. Prometheus stops scraping it, so this alert
resolves.

To retire a node that still works, set `lifecycle: draining` with
`managed_postgres.evacuate_draining`. schedd moves its databases off one at
a time.

## FaasDatabaseProxyCertExpiring (warn) and FaasDatabaseProxyCertExpiryImminent (page)

The public `*.<host_suffix>` certificate pgproxyd serves on a node has
less than 14 days left (warn) or less than 72 hours (page). Clients verify
it (`verify-full`), so once it expires no client reaches the node's
databases. `pgproxyd_tls_cert_expiry_seconds` is the time left on the
certificate in use.

1. Renew the certificate for `*.<host_suffix>`.
2. Point `pgproxyd_tls_cert_src` and `pgproxyd_tls_key_src` at the new
   chain and key, and converge the database nodes.

pgproxyd checks its files every 30 s, and immediately on SIGHUP, which
the role sends after installing them. It swaps the certificate without
dropping sessions. If the files don't load (a chain without its key yet,
an expired or unreadable file), it keeps the one in use and counts
`pgproxyd_tls_cert_reloads_total{result="failed"}`. To find the cause,
run `journalctl -u faas-pgproxyd | grep "certificate reload"`.

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

## schedd's database scheduler

schedd reports the catalog and its own events. Every replica reports the
whole catalog, so take `max`. Each replica counts only its own events, so
take `sum`.

| Metric | Meaning |
| --- | --- |
| `schedd_database_vms{state}` | Databases by state. |
| `schedd_database_standbys{state,status}` | Replicated databases, running or parked, whose standby is `synced` (promotable), `unsynced` or `none`. |
| `schedd_database_node_memory_mb{node}` / `_memory_ceiling_mb` | Admitted memory against the node's admission ceiling. |
| `schedd_database_node_volume_bytes{node}` / `_volume_capacity_bytes` | Committed volume (databases, standbys, incoming moves) against capacity. |
| `schedd_database_node_lifecycle{node,lifecycle}` | 1 for the node's lifecycle. |
| `schedd_database_failovers_total{trigger,result}` | Failovers by `lost_node`, `running` or `parked`. |
| `schedd_database_standby_drops_total{reason}` | Standbys given up (`lagging`, `node_gone`, `stale_claim`). |
| `schedd_database_moves_total{result}` | Moves between nodes. |

### FaasDatabaseStandbyMissing (warn)

A running production-class database has had no synchronized standby for
30 minutes, so it cannot fail over. `schedd_database_standby_drops_total`
shows whether standbys keep being dropped. If they are `lagging`, the
standby's node or the link between the nodes is too slow. If the status is
`none`, no other active node offering the database's major has room. Add
capacity, or end a node's drain.

### FaasDatabaseFailedOver (warn)

schedd promoted a standby: the operator marked a node lost (`lost_node`),
or a running or parked database's node stopped answering for
`auto_failover_after`. The database now runs on its standby's node, with
`last_error = failed_over`, and the monitor attaches a new standby.
Investigate the old node before it returns. When it comes back, the orphan
sweep removes the old primary's volume. If the node is gone for good, mark
it `unavailable`.

### FaasDatabaseFailoverFailed (warn)

A promotion failed, so the database stays on its silent node and schedd
retries. `journalctl -u faas-schedd | grep "fail over\|failover"` names
the error. A standby whose own node is also silent is never promoted.
If the original node does not return, mark it `unavailable` (see
FaasDatabaseNodeDaemonDown for what happens then).

### FaasDatabaseNodeVolumeNearFull (warn)

More than 90% of a node's `volume_capacity_bytes` is committed. Placement,
storage growth and standbys go to other nodes, and fail when none has
room. Add a node, or drain this one with `evacuate_draining`.

### FaasDatabaseMoveFailed (warn)

A move between nodes failed. The database stays on its node (woken again if
it was running) with `last_error = move_failed`. The copy on the target is
discarded. Evacuation retries the move on its next sweep. schedd's
`dbsched: move failed` log names the failed step: copy, archive handoff or
switch.

## Moving one database

To take load off a node, or to move a noisy database, move it by its id
(the UUID in `database_vms`):

```sh
gregalectl database-nodes move --database <id> [--node db-2] [--reason rebalance]
```

It needs an operator session (`gregalectl auth`) and `shared_archive`.
apid records a `database_move` operator intent. schedd's database scheduler
parks the database, records it `moving` under the move's lease and
answers the intent. A database already moving, waking or parking is
refused as busy. The copy runs in the background, so the command returns
before it finishes.

- **Success:** the database wakes on its new node, if it was running or is
  always-on.
- **Failure:** the database stays where it was with `last_error =
  move_failed`, and FaasDatabaseMoveFailed fires.

If schedd stops mid-copy, the move aborts the same way. Without `--node`,
schedd picks the active node with the most free volume capacity that
offers the database's major.

