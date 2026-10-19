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

# TLS for realtimed on compute nodes. The certificate's SANs must cover the
# host in live_hub_url; the client CA verifies realtimed's client certificates.
faas_livequeryd_tls_cert_src: secrets/live/server.crt
faas_livequeryd_tls_key_src: secrets/live/server.key

# Neon: let the database scale to zero.
faas_livequeryd_release_after: 5m
faas_livequeryd_awake: neon
faas_livequeryd_neon_api_key_src: secrets/live/neon-api-key
faas_livequeryd_neon_project: <project id>
faas_livequeryd_neon_endpoint: <endpoint id>
```

On compute nodes, give realtimed the CA and its client certificate. The
gatewayd_internal_service role's realtimed drop-in reads these variables:

```yaml
faas_realtime_live_tls_ca_file: /etc/faas/tls/ca/ca.crt
faas_realtime_live_tls_cert_file: /etc/faas/tls/realtimed/live-client.crt
faas_realtime_live_tls_key_file: /etc/faas/tls/realtimed/live-client.key   # readable by faas
```

`gregalectl pki` does not issue these certificates yet; provision them by
hand.

The nftables role opens the hub's port to `faas_compute_allowed_cidrs` when
`faas_livequeryd_managed` is set.

## Not covered yet

- Several databases per host, and native databases (ADR-1027), where the
  engine runs inside the database VM.
- Issuing the hub's and realtimed's certificates through `gregalectl pki`.
- Paging alerts. Prometheus scrapes the hub; `livequery_*` and
  `livequeryd_daemon_ready` are on the metrics endpoint.
