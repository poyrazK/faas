# agentd_service

Installs and runs **agentd**, the Gregale Agents run driver (ADR-20261027090010,
ADR-20261027090014). agentd claims queued agent runs from Postgres and drives each
through the local schedd's `RunAgent` stream, then delivers the run's result:
webhooks, GitHub comments through githubd, Gregale bucket writes and the
email records apid's mail capture picks up.

The role is included by `bootstrap.yml` only when `faas_agentd_managed=true`.
Agents are dark-launched: apid shows the agent API only to accounts the
internal `agents` capability flag is released to, and schedd serves
`RunAgent` only with `FAAS_SCHEDD_AGENT_RUNS=1` and an agent pool.

## What it writes

| Path | Contents |
| --- | --- |
| `/etc/systemd/system/faas-agentd.service` | the generated unit (`pkg/daemonunitspec/agentd.go`); it leaves agentd idle |
| `/etc/systemd/system/faas-agentd.service.d/50-faas-agentd.conf` | `FAAS_AGENTD_AGENT_RUNS=1` and the feature switches below |
| `/etc/faas/secrets/agentd/agentd.env` | `DATABASE_URL`, from `faas_agentd_env_src`, 0400 root:root |

agentd also reads `/etc/faas/storage.env` (the artifact store for checkouts
and output files) and dials `/run/faas/schedd.sock` and `/run/faas/githubd.sock`.

## Variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `faas_agentd_enabled` | `true` | `false` stops agentd and removes its drop-in and secret |
| `faas_agentd_env_src` | `""` | controller-local file with `DATABASE_URL`; required |
| `faas_agentd_concurrency` | `8` | runs one replica drives at once, 1-256 |
| `faas_agentd_checkouts` | `true` | prepare runs' checkouts through githubd |
| `faas_agentd_output_files` | `true` | keep output file contents in the artifact store |
| `faas_agentd_deliveries` | `true` | run the delivery worker |
| `faas_agentd_s3_endpoint` | `""` | the S3 gateway's https origin; empty fails bucket deliveries |
| `faas_agentd_metrics_listen` | `127.0.0.1:9109` | loopback `/metrics`, scraped by the prometheus role |

## Not yet

- A `pkg/db.DaemonMaxConnections` entry, so `postgres_capacity` reserves
  agentd's pool; until then it uses the default budget.
- A readiness probe for release lanes: agentd is an optional control-plane
  unit there, restarted only where it is installed, without a `/readyz`.
- Cross-node schedd (`FAAS_AGENTD_SCHEDD_TARGET` over tcp:// with mTLS).
