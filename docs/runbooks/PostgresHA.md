# PostgreSQL HA (Patroni) runbook

ADR-1152 §3. Applies to a cell whose manifest sets `postgresql.ha: true`
(Ansible role `postgres_ha`). All commands run on a control node as root
unless stated; `patronictl` reads `/etc/patroni/config.yml`.

```sh
alias pctl='patronictl -c /etc/patroni/config.yml'
pctl list            # members, roles, lag, timeline, pending restarts
```

Healthy state: one `Leader`, one `Sync Standby`, one `Replica`, lag near 0,
all `running`.

## Migrating from the single-node cluster (one time)

Planned write pause of a few minutes; warm traffic keeps serving from the
gateways' last-known routes (ADR-1152 §5).

1. Provision the two new control nodes (other zones) and the etcd TLS
   leaves; add them and `zone` to the manifest; set `postgresql.ha: true`;
   create the pgBackRest buckets.
2. Take a fresh off-host backup with the existing timers and confirm it.
3. Put the new `DATABASE_URL` (HAProxy DSN) into the control nodes'
   `sealed.env` and the compute nodes' `compute-db.env`.
4. Stop the control-plane daemons (`systemctl stop faas-apid faas-schedd
   faas-meterd ...`). Compute daemons wait for the database (P1 boot wait).
5. Run `bootstrap.yml` against the control plane. On the existing node
   Patroni adopts the data directory and becomes leader; the new members
   bootstrap from it with `pg_basebackup` and one becomes the sync standby.
6. Check `pctl list`, then `pgbackrest --stanza=<cluster> check` on the
   leader, then start the daemons and converge compute nodes (HAProxy).
7. Run the node-loss drill below before declaring done.

## Planned switchover (maintenance, minor upgrade)

```sh
pctl switchover --leader <current> --candidate <sync standby> --force
```

Writes pause for a few seconds; HAProxy follows within one check interval
(2 s × 2). Use the sync standby as candidate so no transaction is lost.

## Unplanned failover

Patroni promotes the sync standby on its own after about `ttl` (30 s) plus
one loop (10 s). Confirm with `pctl list`; the old leader rejoins as a
replica after `pg_rewind` when it comes back. If a member stays `stopped`,
see "Rebuild a member".

## Rolling restart (restart-only parameters)

`pctl list` shows `Pending restart` after a config change that needs one.

```sh
pctl restart <cluster> <replica-1> && pctl restart <cluster> <replica-2>
pctl switchover --candidate <a restarted sync standby> --force
pctl restart <cluster> <old leader>
```

## Rebuild a member

```sh
pctl reinit <cluster> <member>
```

Patroni wipes the member's data directory and re-clones it from the leader.

## etcd quorum loss

With two of three etcd members down, Patroni demotes the leader to
read-only (it cannot renew its key): writes fail, warm traffic keeps
serving. Restore a member rather than forcing a new cluster:

```sh
etcdctl --endpoints https://<addr>:2379 --cacert ... --cert ... --key ... endpoint health
systemctl restart etcd   # on the members that are down
```

If a member's data is lost, remove and re-add it (`etcdctl member remove`,
then `member add`, wipe `faas_etcd_data_dir`, start with
`ETCD_INITIAL_CLUSTER_STATE=existing`).

## Region rebuild (region lost)

Target RPO ≤ 60 s, RTO about 2–4 hours (ADR-1152: off-region backups only).

1. Provision three control nodes and the compute nodes in the paired US
   region from the same manifest with addresses and zones updated.
2. On the first new control node, restore from repo2:
   `pgbackrest --stanza=<cluster> --repo=2 --type=default restore`.
3. Run `bootstrap.yml`; Patroni adopts the restored data directory and the
   other members clone from it.
4. Re-point the cell's edge load balancer pool (deploy/cloudflare/
   load-balancer) at the new edges.
5. Apps cold-boot (ADR-005); snapshots rebuild lazily.

Drill this before launch (ADR-1152 P2 exit criterion).
