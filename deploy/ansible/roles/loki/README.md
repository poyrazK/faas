# `loki` Ansible role

Installs a pinned single-binary Loki backend on the control plane
(ADR-20261023124955 §2, amending ADR-143's dedicated host).

- `bootstrap.yml` runs the role when `faas_obs_logs_enabled` is true.
- Every FaaS host sends its platform journald records to it through the
  `alloy` role (ADR-20261023185953).
- Loki runs in `faas-obs.slice` with `MemoryMax=768M`, below PostgreSQL and
  the control-plane daemons for CPU.

Production requirements:

- **Object storage** (`loki_storage_backend: s3`). Chunks and index live in a
  dedicated S3-compatible bucket (`loki_s3_endpoint` as `host[:port]`,
  `loki_s3_bucket`).
  - The keys come from `loki_s3_credentials_file`, which holds
    `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` and must be `root:root 0400`.
  - Only the WAL, index cache and compactor scratch stay on local disk, so
    losing the control plane loses only the un-flushed head.
  - Filesystem storage is accepted only with `loki_dev_mode`.
- **mTLS material.** The `platform_logs_pki` role issues the leaves from the
  fleet CA before this role runs (`pkg/pki` feature `platform-logs`):
  - `loki_tls_cert_file` and `loki_tls_key_file` default to
    `/etc/faas/tls/loki/server.{crt,key}`, named `loki.faas`;
  - `loki_tls_ca_file` is the fleet CA. Loki accepts any leaf that CA signed,
    and the private-network rule on its port limits who can connect.
  - This role makes the key readable by `root:loki`.
- **A private bind address.** `loki_http_listen_address` defaults to
  `faas_obs_loki_address`, the control plane's private transport address that
  compute nodes reach. Wildcard and loopback binds are rejected outside dev
  mode.

The role never reads secret contents. It validates the files' presence and
permissions and renders only their paths.

Retention defaults to 14 days, enforced by the compactor. Add a bucket
lifecycle rule a little longer than that as the backstop.

Setup and query recipes: `docs/ops/logs.md`.
