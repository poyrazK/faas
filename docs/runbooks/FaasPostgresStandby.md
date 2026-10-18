# FaasPostgresStandby* — warm-standby replica (ADR-1344 §8)

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, family
`database_availability`. Metrics: `faas_postgres_standby_*`, written on the
**primary** by `faas-postgres-connection-metrics` from `pg_replication_slots`
and `pg_stat_replication`. They exist only when the manifest declares
`postgresql.standby`.

| Alert | Severity | Meaning |
|---|---|---|
| `FaasPostgresStandbyDisconnected` | warn | The `faas_standby` slot has had no WAL receiver for 5 minutes. |
| `FaasPostgresStandbyLagHigh` | warn | The connected standby is more than 256 MiB of WAL behind for 10 minutes. |
| `FaasPostgresStandbySlotRetentionHigh` | warn | The primary keeps more than half of `max_slot_wal_keep_size` for the slot. |
| `FaasPostgresStandbySlotLost` | page | The slot passed `max_slot_wal_keep_size`. The standby can never catch up and must be reseeded. |

While the standby is behind or gone, the failover RPO is no longer seconds:
everything written since it stopped replaying would be lost by a promotion.
Customer traffic is not affected.

## How the standby works

- The manifest's `postgresql.standby` (`name`, `address`, optional
  `ssh_host`) renders the `postgres_standby` inventory group and
  `faas_postgres_standby_address` on the control plane.
- The control plane's `postgres` role creates the `replicator` role (password
  `faas_postgres_replication_password` from Ansible Vault), the physical slot
  `faas_standby`, the `pg_hba` replication line for the standby's address,
  and `max_slot_wal_keep_size` (default 16GB). nftables admits the standby's
  address on 5432.
- Replication runs over TLS that the standby verifies, so it is encrypted
  and authenticated whatever network carries it (a `static` overlay has no
  encryption of its own):
  - the primary issues a self-signed certificate in `/etc/postgresql/faas-tls/`
    for the names the standby dials (its private address and inventory name;
    override with `faas_postgres_replication_tls_names`) and serves it from
    `conf.d/80-faas-replication-tls.conf`;
  - `pg_hba` admits the replicator with `hostssl` only;
  - the standby pins a copy at `/var/lib/postgresql/faas-primary-replication.crt`
    and connects with `sslmode=verify-full`.

  The primary reissues the certificate when its names change or within 30
  days of its 10-year expiry; the standby then needs a converge to pin it.
- `make bootstrap-postgres-standby` seeds the standby with `pg_basebackup`
  and starts it as a hot standby. `conf.d/90-faas-standby.conf` holds
  `primary_conninfo`, the slot, and the primary's `max_connections` and
  other limits that a hot standby must match.
- The standby is outside the `control_plane` group. No faas daemon runs
  there and nothing connects to it.

## Check

On the primary:

```bash
sudo -u postgres psql -X -c "SELECT slot_name, active, wal_status, safe_wal_size,
  pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained
  FROM pg_replication_slots"
sudo -u postgres psql -X -c "SELECT application_name, client_addr, state, sync_state,
  write_lag, flush_lag, replay_lag,
  pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)) AS replay_behind
  FROM pg_stat_replication"
df -h /var/lib/postgresql
```

On the standby:

```bash
systemctl status 'postgresql@*-main' --no-pager
sudo -u postgres psql -X -c "SELECT status, sender_host, last_msg_receipt_time,
  latest_end_lsn FROM pg_stat_wal_receiver"
sudo -u postgres psql -X -Atc "SELECT pg_is_in_recovery(), now() - pg_last_xact_replay_timestamp()"
journalctl -u 'postgresql@*-main' --since '-30m' --no-pager | tail -40
```

## Act

**Disconnected.** Fix the cause first:
- the standby is down, so start it;
- the overlay link is down;
- the standby's disk is full;
- the password changed, so reconverge both hosts;
- the standby's PostgreSQL log shows `certificate verify failed`: the
  primary reissued its certificate and the standby pins the old one, so run
  `make bootstrap-postgres-standby` to pin the new one;
- the log shows `does not match host name`: the standby dials a name that
  is not on the certificate. Add it to `faas_postgres_replication_tls_names`
  on the control-plane host, converge the control plane, then the standby;
- the log shows `no pg_hba.conf entry ... no encryption`: something
  connected without TLS, which the primary refuses. Check that the standby's
  `conf.d/90-faas-standby.conf` has `sslmode=verify-full`.

To rotate the certificate by hand, delete `/etc/postgresql/faas-tls/server.san`
on the primary, converge the control plane, then the standby.

It catches up from the slot by itself. Rerunning
`make bootstrap-postgres-standby` is safe: it doesn't reseed a seeded
standby, and it waits until WAL streams again.

**Lag high, standby connected.** Check the link throughput between the
providers and the standby's disk I/O (`iostat -x 5`). Large migrations and
bulk deletes produce bursts of WAL, so a spike that drains on its own needs
nothing.

**Retention high.** The standby is gone or far behind, and the primary is
holding WAL for it. Bring it back before the cap is reached. If it won't come
back soon and the primary's disk is at risk, remove `postgresql.standby` from
the manifest and converge the control plane; that drops the slot. Then
re-add it and reseed later.

**Slot lost.** Reseed:

```bash
ANSIBLE_CONFIG=deploy/ansible/ansible.cfg ansible-playbook \
  -i deploy/ansible/.generated/inventory/hosts.ini \
  deploy/ansible/postgres_standby.yml -e faas_postgres_standby_reseed=true
```

Reseeding needs a free slot on the
primary. If the slot is invalidated, converge the control plane after
dropping it (`SELECT pg_drop_replication_slot('faas_standby')`) so the role
creates a new one.

Seeding wipes the standby's data directory. The role refuses to wipe a
cluster that is not in recovery and holds the `faas` database, or one that
does not answer, because that may be a promoted primary.
`-e faas_postgres_standby_wipe_primary=<standby host name>` overrides the
guard. Use it only when you are certain that host is not the current
primary.

## Promotion

Promotion and planned switchover follow `docs/runbooks/ControlPlaneFailover.md`
(`make postgres-promote`), which fences the old primary, starts the daemons
on the new host and points the compute nodes at it. Do not run `pg_promote()` on its own: the
control-plane daemons connect over the local socket and only run on the
primary's host.
