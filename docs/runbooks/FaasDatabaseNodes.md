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
a time. A production-class database with a synchronized standby on an
active node switches over to it instead, running or not. That takes
seconds and copies nothing (`schedd_database_failovers_total{trigger="switchover"}`;
the failover alerts ignore it). The database then runs without a standby
until the monitor attaches a new one, which copies it.

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

## FaasDatabaseArchiveOffsiteStale (warn)

`dbarchive_offsite_pending_oldest_seconds` has been above an hour for 30
minutes. That is the age of the oldest archive file, or journaled
deletion, hold or handoff, that the off-site mirror has not applied. The
bucket is unaffected: the mirror uploads apart from it. But losing the
bucket now would lose the restore points the mirror lacks.

1. `dbarchive_offsite_errors_total` rising means mirror passes fail.
   `journalctl -u faas-dbbackupd | grep "off-site pass failed"` names the
   error, usually the mirror's credentials, endpoint or quota. A
   `corrupt off-site journal entry` error names a file under
   `<store_dir>/.offsite-journal/` that dbbackupd did not write. Move it
   aside, then investigate how it got there, since replay stops at it.
2. With no errors, the node archives faster than it reaches the mirror
   (`dbarchive_offsite_pending_files` keeps growing). Check egress to the
   mirror's provider.

### Losing the bucket

If the bucket, or its provider account, is gone for good, the mirror
becomes the bucket:

1. On every database node, in dbbackupd.toml, move the `[offsite]`
   table's values into `[remote]` and drop `[offsite]`, or add a new
   mirror. In Ansible, set the `dbbackupd_s3_*` variables to the mirror's
   values.
2. Stop dbbackupd. Under `dbbackupd_store_dir`, remove every database's
   `uploaded/` directory: those markers name files the old bucket had,
   and without them the uploader ships every local file to the new
   bucket. Remove `uploaded-offsite/` and `.offsite-journal/` too, so a
   new mirror, if any, starts from scratch.
3. Start dbbackupd. Restores, pruning and holds now read the former
   mirror. Restore points are complete up to the mirror's recovery point
   when the bucket went (`dbarchive_offsite_pending_oldest_seconds`),
   plus whatever the nodes still hold locally.

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
| `schedd_database_standby_resumes_total{result}` | Wakes that resumed a synchronized standby, so commits waited for it from the first one: `ok`, or `failed` (the wake went on asynchronous). |
| `schedd_database_moves_total{result}` | Moves between nodes. |
| `schedd_database_stale_vms` | Running databases (or their standbys) on an outdated base or launch at the last sample. |
| `schedd_database_stale_restarts_total{result}` | Idle always-on databases schedd restarted onto the current base. |
| `schedd_database_restore_drills_total{result}` | Daily restore drills: `ok`, `failed`, `skipped` (no capacity, or a source on an outdated guest). |
| `schedd_database_restore_drill_last_ok_timestamp_seconds{node}` | When the node's last drill passed (0: none yet). |
| `schedd_database_storage{level}` | Running databases at the last storage sample (every 2 min): `sampled`, `low` (short of the space that ends read-only), `read_only`, `standby_low` (the standby is low while its primary is not). |

### FaasDatabaseRestoreDrillFailed and FaasDatabaseRestoreDrillStale (warn)

Every day, schedd restores one database per active node from that node's
backups. First the source commits the drill's id into
`gregale.restore_drill_mark`, a one-row table in its `postgres`
maintenance database that only the guest's superuser can reach. The drill
then restores to just after that commit. It uses the same restore as a
customer's point-in-time restore and node-loss recovery: the archive is
flushed, the base backup extracted, WAL replayed, and the copy promoted
and backed up. Once the copy parks, schedd wakes it and checks that it
holds the mark. The copy is a throwaway database. Once the drill is
recorded in `database_restore_drills`, the copy and its archive are
deleted. Customers never see it.

1. `journalctl -u faas-schedd | grep 'restore drill'` shows each drill's
   node, source, target and outcome (`recovered_to`, or an `error_code`).
2. A `failed` drill means a restore from that node's backups does not
   work right now. Treat it as you would a failed customer restore: check
   `FaasDatabaseArchiveKeeperDown`, dbbackupd's log on the node, and the
   source's `last_backup_at`. The next day's drill picks another database.
3. `drill_mark_missing` is the most serious failure. The restore ran,
   but the copy lacks the commit the source made just before the target,
   so WAL the source committed never reached the archive, or recovery
   stopped early. Customers' point-in-time restores from that node may
   be missing their last commits. Check dbbackupd and archive_command
   failures on the source (`pg_stat_archiver` through an admin session),
   and compare the drill's `recovered_to` with its `target_at`.
4. `drill_mark_failed` means the source could not commit the mark, so
   nothing was restored. A full volume or a crashed guest is the usual
   cause; the read-only storage guard is not, since the guest's
   superuser writes past it. The source's own health alerts say more.
5. `drill_no_capacity` (`skipped`) means the node had no room for the
   throwaway database. `drill_source_outdated` (`skipped`) means the
   source still runs a guest from before drill marks, until its next
   boot. Neither is a backup failure, but both leave the node unproven,
   and `FaasDatabaseRestoreDrillStale` fires after two days without a
   pass.
6. `SELECT * FROM database_restore_drills ORDER BY started_at DESC` keeps
   90 days of drills.

### FaasDatabaseStaleVMs (warn)

Database VMs survive vmmd restarts, so a release changes a running
database only when it restarts. That covers a new PostgreSQL minor (the
image digest for its major), a new guest-init, or launch settings such as
the cgroup limits. vmmd reports a VM as stale while it runs a base or
launch older than the node's current one. Every 2 minutes schedd restarts
up to two stale always-on databases that have been idle for 2 minutes:
it parks them and wakes them on the current base, standby included. A
scale-to-zero database picks the new base up at its next idle park.

The alert fires when some stayed stale for a day, because they never went
idle. A security minor should not wait for that.

1. `journalctl -u faas-schedd | grep 'stale'` lists the restarts schedd
   made. The databases still stale are the running ones that vmmd's
   status reports `stale` for.
2. In a quiet window, restart each one in place (see "Restarting one
   database").

### FaasDatabaseStorageReadOnly (warn)

A database's volume is its storage limit. When a primary's free space
falls under 5% of the volume (at least 128 MiB, at most 2 GiB), the
guest's storage guard sets `default_transaction_read_only = on` and
reloads. Writes fail with SQLSTATE 25006, and reads still work.
Otherwise PostgreSQL would PANIC on its next WAL write once the volume
filled. The guard turns the database writable again once free space is
twice that threshold. Platform sessions (credentials, replication, the
import's bookkeeping) are exempt.

1. Find the databases: `journalctl -u faas-schedd | grep 'short of
   storage'` gives each `database_id` with its free and total bytes.
2. The customer either grows storage (`PATCH` storage; the restart onto
   the larger volume ends read-only at boot), or deletes data. A session
   that runs `SET default_transaction_read_only = off` can DELETE or
   TRUNCATE, and the guard turns writable once space comes back.
   `VACUUM` returns deleted rows' space to the database, not to the
   volume; `TRUNCATE`, `DROP` or `VACUUM FULL` (which needs room for the
   table's copy) free volume space.
3. If a session overrode read-only and kept writing, the guard releases
   a ballast file (up to 64 MiB, under 32 MiB free) so PostgreSQL can
   keep running. It lays the ballast again once the database is writable.

A stalled WAL archive fills a busy volume too, because WAL stays until
it is archived. Check `FaasDatabaseArchiveKeeperDown` first when several
databases turn read-only together.

### FaasDatabaseStandbyStorageLow (warn)

A production-class database's standby has had less free space than the
storage guard's writable level (twice the read-only threshold) for 15
minutes, while its primary has more. The guard governs only the primary.
The standby has the same volume size and no ballast, so normally it has
more room than the primary. A standby with less holds data the primary
does not. Usually that is WAL in its `pg_wal` that its restartpoints
have not let go. Once its volume fills, the standby stops replaying, the
primary drops to asynchronous (`FaasDatabaseStandbyMissing` follows), and
re-attaching fails the same way until the space is back.

1. Find the databases: `journalctl -u faas-schedd | grep 'standby is
   short of storage'` gives each `database_id`, the standby's node, and
   its free and total bytes.
2. Restart the database in place in a quiet window (see "Restarting one
   database"). A clean standby shutdown ends with a restartpoint, which
   recycles the WAL it no longer needs, so the standby boots with its
   space back.
3. If it comes back low, the standby keeps something a restartpoint
   cannot recycle. Check its console log on its node for restartpoint or
   replay errors. No operator command re-copies a standby in place yet;
   until one exists, the database keeps running and drops to
   asynchronous on its own when the standby stops.

### FaasDatabaseStandbyMissing (warn)

A running production-class database has had no synchronized standby for
30 minutes, so it cannot fail over. `schedd_database_standby_drops_total`
shows whether standbys keep being dropped. If they are `lagging`, the
standby's node or the link between the nodes is too slow. If the status is
`none`, no other active node offering the database's major has room. Add
capacity, or end a node's drain. `schedd_database_standby_resumes_total`
with `result="failed"` rising means wakes could not set their primary
synchronous, so those wakes ran without a standby until it attached.

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

### FaasDatabaseUpgradeFailed (page)

An in-place PostgreSQL major upgrade failed. The database is `failed`, with
`last_error = upgrade_failed`, and its clients cannot connect. The cause is
one of two:

- pg_upgrade broke the cluster after it began linking files.
- The upgrade never finished within 2.5 h, retries included.

(A refusal by pg_upgrade's check is not a failure: the database stays on
its major, parked with `last_error = upgrade_refused`, and is counted
`refused`.)

1. `journalctl -u faas-schedd | grep 'dbsched: upgrade'` names the
   database, the majors and each attempt's error. The VM's console on the
   node (guest-init's `database upgrade:` lines) has pg_upgrade's last
   output.
2. Restore the database to a point before the upgrade began. The restore
   recovers on the old major, from that major's backups and WAL: guest-init
   archived the old cluster's last WAL before pg_upgrade ran. The node must
   still have the old major's base.
3. Retry the upgrade once the cause is fixed, as a new upgrade request:
   one request never upgrades twice. Upgrades go through apid's catalog so
   it stays in step; that customer surface is not built yet. A `refused`
   upgrade needs whatever pg_upgrade's check reported removed first (a
   `reg*` column, an extension without a build for the new major).

An upgrade still in progress shows as `state = 'upgrading'` in
`database_vms`, with its lease in `upgrade_lease_until`; `upgrade_operation`
and `upgrade_result` keep the last upgrade's name and outcome. Any schedd replica
resumes one whose lease lapsed; do not stop its VM, which may be inside
pg_upgrade.

## Internal mTLS leaves

A database node's vmmd and pgproxyd authenticate to schedd and to each
other with internal mTLS leaves. The daily internal-PKI renewal
(`pki-renew.yml`) renews them with the rest of the fleet and sends both
daemons SIGHUP. Neither restarts, and no session drops. Their expiry
appears with every other host's under the FaasInternalMTLS* alerts; see
the runbooks for those alerts. A database node is reached with the
renewal workflow's deploy key and pinned through `COMPUTE_KNOWN_HOSTS`.
Add both before declaring the node in the manifest, or the renewal run
fails on it.

## Restarting one database

To put a running database onto its node's current base (a PostgreSQL
minor, guest-init or launch change; `FaasDatabaseStaleVMs`), restart it in
place:

```sh
gregalectl database-nodes restart --database <id> [--reason minor_update]
```

It needs an operator session. apid records a `database_restart` operator
intent, and schedd's database scheduler parks the database (a standby
stops after its primary) and wakes it again. That takes a few seconds,
during which clients wait in pgproxyd and idle sessions drop. A parked
database is left as it is: its next wake boots the current base. A
database moving, waking, parking or failed is refused (`conflict`).

## Switching a database over

To take a production-class database off its node without copying it, for
maintenance or to test that its standby can take over, switch it over to
its synchronized standby:

```sh
gregalectl database-nodes switchover --database <id> [--reason node_maintenance]
```

It needs an operator session. apid records a `database_switchover`
operator intent. schedd stops a running primary cleanly while its standby
is synchronous, promotes the standby, and points the database at the
standby's node. That takes a second or two, clients reconnect, and no
commit is lost. A parked database moves to its standby's node, which
promotes the standby at its next wake. The database then runs without a
standby until the monitor attaches a new one, which copies it
(`FaasDatabaseStandbyMissing` would follow if none can attach). A
database without a synchronized standby on an active node is refused.
If the promotion fails, the database is parked where it was, standby
still trusted, and its next wake brings both back. Draining a node does
this for every such database on it.

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


## Previewing major upgrades

In-place major upgrades (ADR-1027) are hidden from customers until they
are tested on production. Outside `FAAS_MANAGED_POSTGRES_UPGRADE_ACCOUNTS`
(apid, comma-separated account UUIDs; `*` at launch; unset is nobody),
their routes answer exactly like unregistered ones. They are not in the
OpenAPI spec, the generated SDKs, `major_upgrade` capabilities or CLI help.
For a listed account:

```sh
gregale postgres upgrade DATABASE --major 17 --request-id REQUEST_UUID
gregale postgres upgrade-status DATABASE REQUEST_UUID --json
```

These call `POST /v1/postgres/databases/{id}/upgrade` (`request_id`,
`postgres_major`) and `GET /v1/postgres/databases/{id}/upgrades/{upgrade_id}`.
The upgrade shares the compute-change journal with resizes, so one change
runs at a time. A request UUID never upgrades twice. A later major the
backend does not offer is `managed_postgres_unsupported`.

- `succeeded`: the database serves on the new major. Clients reconnected
  once; planner statistics are rebuilt in the background.
- `failed` with `upgrade_refused`: PostgreSQL's check refused the cluster
  (a `reg*` column, an extension without a build for the new major). The
  database stays on its major, unchanged. Remove the cause, then request a
  new upgrade.
- `failed` with `upgrade_failed`: see FaasDatabaseUpgradeFailed above.

Restores keep working across an upgrade: a point before it restores on the
old major, and the restore request must name that major.

At launch, set `*`, then add the routes, schemas and `major_upgrade` back
to `api/openapi.yaml` and regenerate the SDKs. Drop them from both
`routeExclude` lists (`scripts/ci/speccompliance` and `cmd/sdk-coverage`),
and add the subcommands back to `cli_meta.go`. Move this section to
docs/managed-postgres.md.

## Previewing storage growth

Growing a database's storage limit (ADR-1027) is hidden from customers
until it is tested on production. Outside
`FAAS_MANAGED_POSTGRES_STORAGE_RESIZE_ACCOUNTS` (apid, comma-separated
account UUIDs; `*` at launch; unset is nobody), `storage_limit_bytes` is an
unknown resize field. Resize views carry no storage limits, and
capabilities no `storage_resize`. None of it is in the OpenAPI spec or the
generated SDKs (spec-compliance `previewFields`), and `gregale postgres
resize --storage-bytes` is out of help. What a listed account gets, and
what the customer guide will say at launch:

A database's storage limit starts at the plan's per-database allowance unless
the create request names a lower one. A backend that advertises
`storage_resize` grows it in place through the resize request, alone or
together with a class change:

```sh
gregale postgres resize DATABASE --storage-bytes 21474836480 --request-id REQUEST_UUID
gregale postgres resize DATABASE --class burstable --storage-bytes 21474836480 --request-id REQUEST_UUID
```

The POST body takes `storage_limit_bytes`; `service_class` becomes optional,
and at least one of the two is required. Omitting the limit, or repeating the
current one, keeps it. Storage only grows: a lower limit is rejected
(`managed_postgres_invalid`), because a volume cannot shrink under its data. A
limit above the plan's per-database allowance fails with
`managed_postgres_quota_exceeded`, carrying `limit`, `observed` and
`docs_url`; the backend's own ceiling (`storage_limit_bytes` in capabilities)
also applies. The resize view reports `from_storage_limit_bytes` and
`target_storage_limit_bytes`, and the same request UUID rules apply: replaying
the same target returns progress, a different one conflicts.

On the native backend (ADR-1027) a running database parks, its volume grows
offline (`e2fsck`, then `resize2fs`) and it wakes on the larger filesystem;
a parked database grows before its next wake. Storage is billed by the
provisioned limit, so the larger limit is metered from completion on. Neon
does not advertise `storage_resize`.

A native database that nearly fills its limit turns read-only rather than
crash. Below 5% free space (at least 128 MiB, at most 2 GiB), writes fail
with SQLSTATE `25006` ("cannot execute … in a read-only transaction"), and
reads keep working. Growing the storage ends it at the next wake. Freeing
space ends it within seconds once twice that much is free: run `SET
default_transaction_read_only = off` in a session, then `TRUNCATE`, `DROP`,
or `DELETE` plus `VACUUM FULL`. A plain `VACUUM` keeps the space inside the
table.

Qualification artifacts record `storage_resize`. A backend that advertises it
must pass `storage_resize_probe`: the limit grows on the disposable
qualification database with its identity, data, credentials and a stable
replay intact, and the database reports the new limit. The probe runs last,
since storage cannot return to the qualification spec.

At launch, set `*`, then add `storage_limit_bytes`, the resize view's
storage limits and `storage_resize` back to `api/openapi.yaml` and
regenerate the SDKs. Drop them from `previewFields`, put `--storage-bytes`
back in the resize usage line, and move this section back to
docs/managed-postgres.md.
