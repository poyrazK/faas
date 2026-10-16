# pgproxyd_service

Installs pgproxyd (ADR-1027), the PostgreSQL proxy of native managed
databases. It is the only public listener on a database node. Run it through
`deploy/ansible/database_node.yml` (`make bootstrap-database-nodes`), which
also converges the database node firewall and authorizes pgproxyd on the
control-plane database.

Inputs (all required when `pgproxyd_enabled: true`):

| Variable | Meaning |
| --- | --- |
| `pgproxyd_host_suffix` | SNI suffix; clients connect to `<database-id>.<suffix>` |
| `pgproxyd_schedd_target` | control-plane schedd over the overlay |
| `pgproxyd_tls_cert_src`, `pgproxyd_tls_key_src` | publicly trusted `*.<suffix>` certificate and key |
| `pgproxyd_db_url` | read-only DSN of the `faas_pgproxyd` role; `gregalectl manifest ansible` derives it from `faas_pgproxyd_postgres_password` (Vault) |

Optional, with more than one database node: `pgproxyd_instance_id` (a
distinct 0-255 per node) and `pgproxyd_cancel_peers` (the other nodes'
overlay listeners by id). A client's cancel request can reach any node's
pgproxyd; it is forwarded to the instance that issued the client's key.

The node's mTLS bundle (vmmd server leaf and the pgproxyd client leaves) is
staged by the `database_node` role (`database_node_pki_source`).

Files written on the host:

- `/etc/faas/pgproxyd.toml`: configuration.
- `/etc/faas/pgproxyd/{tls.crt,tls.key,db-url,decoy}`: owner-only files of
  `faas-pgproxyd`. pgproxyd refuses a secret that its group or others can
  read. The decoy key is minted once and kept.
- `/etc/faas/tls/pgproxyd/{schedd,vmmd}-client.{crt,key}`: the
  `pgproxyd.faas` mTLS leaves.

Operations: `/healthz`, `/readyz` and `/metrics` are served on
`pgproxyd_ops_addr`: port 9109 on the overlay address when the manifest
renders `database_node_metrics_host` (control-plane Prometheus job
`pgproxyd`), loopback otherwise. Metrics: `pgproxyd_sessions_total{result}`,
`pgproxyd_active_sessions`, `pgproxyd_wake_seconds`,
`pgproxyd_cancel_requests_total{result}`, `pgproxyd_tls_cert_expiry_seconds`,
`pgproxyd_tls_cert_reloads_total{result}`.

pgproxyd reloads a renewed public certificate in place. It checks every
30 s, and the role sends SIGHUP after installing new files. Changing
`pgproxyd_tls_cert_src` / `pgproxyd_tls_key_src` therefore does not
restart it or drop sessions.
