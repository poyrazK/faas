# postgres_ha (ADR-1237 §3)

Self-run PostgreSQL high availability for one cell: three control nodes, one
per zone, each running

- **etcd**, Patroni's consensus store (mutual TLS, its own data path and a
  higher I/O priority than PostgreSQL);
- **Patroni**, which runs PostgreSQL 16 with one synchronous standby
  (RPO 0 for the loss of any one member or zone), `pg_rewind`, replication
  slots and the softdog watchdog (a primary that cannot renew its leader key
  resets before a replica is promoted);
- **pgBackRest**, archiving WAL asynchronously (`archive_timeout = 60s`) and
  taking full/differential backups from the current primary into two GCS
  repositories: repo1 in the cell's region, repo2 in a second US region
  (ADR-1237 region loss: off-region backups only).

Every node, control and compute, runs **HAProxy**: `127.0.0.1:5433` reaches
the current primary and `127.0.0.1:5434` the replicas, chosen by Patroni's
`/primary` and `/replica` health checks. A failover moves new connections in
seconds and closes connections still open to the old primary.

## Switching it on

The manifest decides: `postgresql.ha: true` (with three control-plane hosts,
each with a `zone`) makes `gregalectl manifest ansible` emit
`faas_postgres_ha_enabled: true` for every host. Then `bootstrap.yml`

- runs `postgres_ha` instead of `postgres`, `postgres_capacity` and
  `postgres_backup` on the control nodes;
- runs the HAProxy part (`faas_postgres_ha_haproxy_only`) on compute nodes;
- requires control-plane `sealed.env` to point `DATABASE_URL` at
  `postgres://faas:<password>@127.0.0.1:5433/faas?sslmode=disable`, and the
  TOML renderer stops pinning the local socket, because the primary may be
  on another node;
- skips `postgres_capacity` and `control_plane_peer_access` on node joins:
  `max_connections` is fixed (`faas_postgres_ha_max_connections`), so a join
  never restarts PostgreSQL.

Compute nodes keep their existing password DSN, re-pointed at
`127.0.0.1:5433`.

## Inputs

Vaulted (asserted, at least 16 characters):
`faas_postgres_ha_superuser_password`,
`faas_postgres_ha_replication_password`,
`faas_postgres_ha_rewind_password`,
`faas_postgres_ha_restapi_password`.

Operator-provided:

- `faas_pgbackrest_repo1_bucket` and `faas_pgbackrest_repo2_bucket` (distinct
  buckets in two US regions; the hosts' service account needs object admin
  on both);
- etcd TLS from the internal CA in `faas_etcd_tls_dir`: `server.crt/key`
  (one per member, with its overlay address in the SAN, used for both client
  and peer traffic) and `client.crt/key` for Patroni;
- a dedicated disk mounted at `faas_etcd_data_dir` (owner decision: etcd
  co-located with PostgreSQL on its own disk).

## Not covered here

- pgbouncer on compute nodes still assumes the peer socket for its server
  leg; with `postgres_ha` its server leg must go through HAProxy with a
  stored scram secret. Keep pgbouncer off until that change lands (below four
  compute nodes it costs more than it saves anyway).
- The first migration from the single-node cluster is a planned operation;
  follow docs/runbooks/PostgresHA.md.

Runbook: docs/runbooks/PostgresHA.md.
