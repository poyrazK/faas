# Live queries

Alerts in the `live_queries` family (`faas_live_queries` in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`). Live queries are a
dark-launched preview (ADR-20261016151947), so every alert warns rather than
pages.

## How it fits together

- **The hub.** `livequeryd` runs on the control plane and serves the
  databases listed in `faas_livequeryd_databases`
  (`/etc/faas/livequeryd/databases.json`), each at `/<database_id>/live`. For
  every database it holds:
  - a replication slot (`gregale_live_<id>` by default);
  - a checkpoint in `/var/lib/faas/livequeryd/<id>.checkpoint`;
  - the database's live keys, read from the control plane.
- **realtimed** carries every browser's subscriptions for a database over
  one connection to the hub. A hub that is unavailable makes those
  subscriptions stale until realtimed reconnects and resubscribes them.
- **Per-database series.** Each database's series carry a `database` label;
  a hub that serves one database through flags has none.

## Look first

- **Which databases are served.** On the control plane:

  ```
  curl -s http://127.0.0.1:9471/databases
  ```

  This lists each database with `running` and `restarts`.
- **Logs.** `journalctl -u faas-livequeryd` carries one line per failed
  start, tagged with the database.
- **Dashboard.** The live-queries dashboard (`deploy/grafana/live-queries.json`)
  shows each database's state, latency, slot lag and resumes.

## Alerts

### FaasLiveQueryHubDown

Prometheus cannot scrape livequeryd at all, so no database gets new results.

1. Check `systemctl status faas-livequeryd`. systemd stops restarting a hub
   that fails five times in a minute; `systemctl reset-failed` and start it
   once the cause is fixed.
2. A process that exits at once usually has a configuration error, which
   its journal names. Examples: a bad `databases.json` entry, a missing
   credential, or a TLS file.

### FaasLiveQueryDatabaseNotServed

The process runs, but this database's hub has not run for 5 minutes, and
realtimed gets 503 for it. The hub retries with backoff, at most once a
minute.

The journal names the error. Common causes:

| Error mentions | Cause and fix |
|---|---|
| connect, password, `does not exist` | The connection string in the database's credential is wrong, or the database is gone. Fix `database_url_src` and rerun the role. |
| `permission denied`, `must be superuser or replication role` | The role lacks `REPLICATION`, or event-trigger rights for the schema-change log. |
| `replication slot ... is active` | Another process decodes the slot. Find it with `SELECT * FROM pg_replication_slots`. |
| `was not created in this database` | Two databases share a slot name on one server. Give one entry its own `slot`; livequeryd refuses this at start when both are listed. |

### FaasLiveQueryHubRestarting

The database's hub keeps starting and then stopping on an error. Each stop
drops its subscribers until realtimed resubscribes. Read the journal for the
error that ends each run.
- A capture that stopped means the replication connection keeps breaking.
  Check the database server and the network.

### FaasLiveQueryResumeFailing

The hub released the database so it could scale to zero, and it cannot
reconnect. Commits made meanwhile wait.

1. Check the database's compute. On Neon, `-awake neon` polls the
   endpoint's state, so an endpoint that reports active but refuses
   connections fails here.
2. Check that the slot still exists. A missing slot is recreated, and its
   queries are read again whole (see FaasLiveQuerySlotLost).

### FaasLiveQuerySlotLagHigh

The slot holds back more than 1 GiB of WAL, and the database's storage grows
until the hub confirms it.

1. If the database is no longer served, drop its slot on the database:
   `SELECT pg_drop_replication_slot('gregale_live_<id>')`. A database removed
   from `faas_livequeryd_databases` leaves its slot behind.
2. If the hub serves it, check `livequery_round_duration_seconds` and its
   journal for a stuck round.

### FaasLiveQuerySlotLost

The hub restored from its checkpoint, but its slot was gone. Every live query
of the database was read again from scratch, and clients received fresh
results, so nothing is wrong now. Find what dropped the slot so it does not
recur: a database restore, a Neon branch reset, or an operator.

### FaasLiveQueryKeysUnreadable

The hub cannot read the database's live keys from the control plane.
Existing seals still open with the keys it has, but new or rotated keys are
not picked up, and revoked ones still work.
- Check the read-only control-plane connection in
  `/etc/faas/secrets/livequeryd/keys-dsn` and the control-plane database.

### FaasLiveQueryCommitToDeltaSlow

p95 from commit to frames has exceeded 1 s for 15 minutes; the ADR measured
p95 near 100 ms. On the dashboard, compare round duration, statements per
round and slot lag:
- **Slow rounds** point at expensive maintenance queries or a busy database.
- **Growing lag** means the hub falls behind the WAL.

### FaasLiveQuerySlowConsumer

The hub closed a connection that fell too far behind its frames. Usually that
connection is realtimed's, so its subscribers resubscribe. Check realtimed's
health and the network between the compute node and the control plane.

### FaasLiveQueryUpstreamFlapping

realtimed on a compute node keeps losing its connections to the hub.
1. Check that the hub runs.
2. Check its certificates. realtimed verifies it as `livequeryd.faas`, and
   the hub accepts only `realtimed.faas`; `gregalectl pki metrics` shows
   their expiry.
3. Check the network path to port 9480, which nftables opens to compute
   CIDRs only.
