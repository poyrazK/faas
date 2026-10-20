# livequeryd_service

Runs the live-query hub (`livequeryd`, ADR-20261016151947) for one managed
database on the control plane, and wires apid for live queries.

Live queries are a dark-launched preview. Nothing here runs unless the
inventory sets `faas_livequeryd_managed: true`. Customers see live queries
only when the `live_queries` platform flag (ADR-1700) includes their account.
Operators set that flag at runtime in the operator console; it is off by
default.

## What the role does

- Installs `faas-livequeryd.service`. The unit is generated from
  `pkg/daemonunitspec/livequeryd.go`, so run `make generate` after changing
  that file, never this copy.
- Writes `faas-livequeryd.service.d/50-faas-livequeryd.conf`. This drop-in
  holds the hub's command line, and loads its secrets as systemd credentials:
  - the database connection string;
  - the control-plane DSN the live keys are read from;
  - the live-key age identity;
  - the TLS key;
  - the Neon API key, when one is configured.

  The secrets live in `/etc/faas/secrets/livequeryd/` as 0400 root:root and
  reach the hub as files under `%d`, never on its command line.
- Gives apid the identity's public recipient as
  `FAAS_APID_LIVE_KEY_RECIPIENT_FILE`
  (`faas-apid.service.d/99-faas-live-queries.conf`). It does not set
  `FAAS_LIVE_QUERIES_ACCOUNTS`, which is only the flag's boot fallback.
- Keeps the checkpoint in `/var/lib/faas/livequeryd`, so a restart resumes from
  the replication slot.
- Starts the hub and waits for `/readyz` on `faas_livequeryd_metrics_listen`
  (127.0.0.1:9471, which Prometheus also scrapes).

With `faas_livequeryd_enabled: false`, the role:
- stops and disables the hub;
- removes both drop-ins. Without the recipient, apid answers
  `live_queries_unavailable` even to accounts the flag includes.

A release (`cd-controlplane`) updates and restarts the hub only on hosts that
have the hub drop-in.

## One database per host

livequeryd serves one database. A backend's `live_hub_url` (in the managed
Postgres provider registry, `FAAS_MANAGED_POSTGRES_CONFIG`) names one hub.
For example:

```
"live_hub_url": "wss://cp-1.internal:9480/{database_id}/live"
```

The hub answers its own database at `/<database_id>/live` (and at `/live`).
Any other database's path returns not found, so a second live database on
the same backend fails closed until livequeryd serves several databases.

## Inventory

```yaml
faas_livequeryd_managed: true
faas_livequeryd_database_id: 3f1c0b6e-...
faas_livequeryd_database_url_src: secrets/live/database-url      # postgres://...; REPLICATION role
faas_livequeryd_keys_dsn_src: secrets/live/keys-dsn              # read-only control-plane DSN
faas_livequeryd_keys_identity_src: secrets/live/keys.age         # age-keygen output
faas_livequeryd_keys_recipient_src: secrets/live/keys.recipient  # age-keygen -y keys.age


# Neon: let the database scale to zero.
faas_livequeryd_release_after: 5m
faas_livequeryd_awake: neon
faas_livequeryd_neon_api_key_src: secrets/live/neon-api-key
faas_livequeryd_neon_project: <project id>
faas_livequeryd_neon_endpoint: <endpoint id>
```

## Certificates

realtimed and the hub authenticate each other with fleet-PKI **feature
leaves**. These are leaves of the internal CA that a host carries only when
this feature is on. No host role requires them, so rollouts and the daily
renewal of the required leaves never depend on them.

- **The hub's server leaf** (`livequeryd.faas`,
  `/etc/faas/tls/livequeryd/server.{crt,key}`) is issued by this role on the
  control plane with `gregalectl pki issue-feature`.
- **realtimed's client leaf** (`realtimed.faas`,
  `/etc/faas/tls/realtimed/livequeryd-client.{crt,key}`) is issued on the
  control plane for each compute node, and copied there as root:faas 0440.
  Set this on the compute hosts that run realtimed:

  ```yaml
  faas_realtime_live_tls_managed: true
  ```

  realtimed then verifies every hub as `livequeryd.faas`, whatever host
  `live_hub_url` names.
- **The hub accepts only realtimed.** With `-client-name realtimed.faas`, any
  other leaf of the fleet CA is refused (`faas_livequeryd_client_name`).
- **Renewal.** Leaves last 90 days. `pki_renew.yml` reissues each within 30
  days of expiry, and the roles restart only the daemon whose leaf changed.
  `gregalectl pki metrics` exports their expiry with the other leaves.

To use certificates from elsewhere instead, set
`faas_livequeryd_tls_cert_src` and `faas_livequeryd_tls_key_src` on the
control plane. On compute hosts, leave `faas_realtime_live_tls_managed` off
and set `faas_realtime_live_tls_ca_file`, `_cert_file` and `_key_file`.

The nftables role opens the hub's port to `faas_compute_allowed_cidrs` when
`faas_livequeryd_managed` is set.

## Not covered yet

- Several databases per host, and native databases (ADR-1027), where the
  engine runs inside the database VM.
- Paging alerts. Prometheus scrapes the hub; `livequery_*` and
  `livequeryd_daemon_ready` are on the metrics endpoint.
