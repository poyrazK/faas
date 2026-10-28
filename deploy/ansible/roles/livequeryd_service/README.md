# livequeryd_service

Runs the live-query hub (`livequeryd`, ADR-20261016151947) on the control
plane for the managed databases listed in `faas_livequeryd_databases`, all in
one process, and wires apid for live queries.

Live queries are a dark-launched preview. Nothing here runs unless the
inventory sets `faas_livequeryd_managed: true`. Customers see live queries
only when the `live_queries` platform flag (ADR-1700) includes their account.
Operators set that flag at runtime in the operator console; it is off by
default.

## What the role does

- Installs `faas-livequeryd.service`. The unit is generated from
  `pkg/daemonunitspec/livequeryd.go`, so run `make generate` after changing
  that file, never this copy.
- Writes `/etc/faas/livequeryd/databases.json`, the hub's list of databases
  (`-databases-file`). It names no secret.
- Writes `faas-livequeryd.service.d/50-faas-livequeryd.conf`. This drop-in
  holds the hub's command line and memory ceiling, and loads its secrets as
  systemd credentials:
  - each database's connection string (`<id>.database-url`);
  - the control-plane DSN the live keys are read from;
  - the live-key age identity;
  - the TLS key;
  - the Neon API keys, when configured: one shared key, and any database's
    own.

  The secrets live in `/etc/faas/secrets/livequeryd/` as 0400 root:root and
  reach the hub as files under `%d`, never on its command line. A database
  removed from the list has its secrets removed.
- Gives apid the identity's public recipient as
  `FAAS_APID_LIVE_KEY_RECIPIENT_FILE`
  (`faas-apid.service.d/99-faas-live-queries.conf`). It does not set
  `FAAS_LIVE_QUERIES_ACCOUNTS`, which is only the flag's boot fallback.
- With `faas_livequeryd_signing_key_src` and
  `faas_livequeryd_signing_pub_src` (ADR 20261028161502), gives apid its
  live signing key as a credential (`FAAS_APID_LIVE_SIGNING_KEY_FILE`) and
  the hub the verify keys (`-live-signature-keys`). The hub refuses a key or
  login whose signature does not verify.
  `faas_livequeryd_require_signatures: true` also refuses unsigned ones;
  set it only after `gregalectl live-query sign-existing` reports none.
- Keeps one checkpoint per database in `/var/lib/faas/livequeryd`
  (`<id>.checkpoint`), so a restart resumes from each replication slot.
- Starts the hub, waits for `/readyz` on `faas_livequeryd_metrics_listen`
  (127.0.0.1:9471, which Prometheus also scrapes), then fails the run unless
  `GET /databases` shows every database served.

With `faas_livequeryd_enabled: false`, the role:
- stops and disables the hub;
- removes both drop-ins. Without the recipient, apid answers
  `live_queries_unavailable` even to accounts the flag includes.

A release (`cd-controlplane`) updates and restarts the hub only on hosts that
have the hub drop-in.

## Several databases, one process

A backend's `live_hub_url` (in the managed Postgres provider registry,
`FAAS_MANAGED_POSTGRES_CONFIG`) names the hub with the database in its path.
For example:

```
"live_hub_url": "wss://cp-1.internal:9480/{database_id}/live"
```

The hub serves each listed database at `/<database_id>/live`:
- **Separate per database.** Each database has its own hub, replication slot
  (`gregale_live_<id>` by default: slot names are unique per PostgreSQL
  server, which databases may share), checkpoint and live keys. Only the process, the listener, the SQL parser
  and the control-plane connection are shared.
- **Failures stay contained.** A database that cannot be opened, or whose hub
  stops on an error, answers 503 and is retried with backoff (one second,
  doubling, at most a minute). The others keep serving. realtimed retries the
  503 too.
- **Visible state.** `GET /databases` on the metrics listener and
  `livequery_hub_up{database=...}` show which databases are served.
  `livequery_hub_restarts_total` counts the retries.
- **Unlisted databases.** A database not in the list returns not found.
  `/live` answers only when the list has one database.
- **Changing the list restarts the hub.** Every database then resumes from
  its checkpoint, and realtimed reconnects.

## Discovering databases

With `faas_livequeryd_discover_backends`, the hub serves every live database
of those managed Postgres backends without an inventory entry:
- **Enabling** (`gregale postgres live enable`) makes the database's provider
  prepare it and issue its live login. apid seals the login to this hub's
  identity, and the hub picks it up within `-discover-every` (15 s).
- **Disabling** revokes the login and drops the slot, and the hub stops
  serving the database.
- **Scale to zero.** `faas_livequeryd_discover_release_after` releases
  discovered Neon databases when idle; it needs
  `faas_livequeryd_neon_api_key_src`.
- **Listed databases** in `faas_livequeryd_databases` are served beside
  discovered ones, and discovery leaves them alone.

```yaml
faas_livequeryd_discover_backends: [neon-us-1]   # BackendConfig IDs whose live_hub_url names this hub
faas_livequeryd_discover_release_after: 5m
```

A backend's provider must issue live logins (the Neon provider does). On any
other backend, enabling live queries issues no login, and the database must
be listed.

## Inventory

```yaml
faas_livequeryd_managed: true
faas_livequeryd_keys_dsn_src: secrets/live/keys-dsn              # read-only control-plane DSN
faas_livequeryd_keys_identity_src: secrets/live/keys.age         # age-keygen output
faas_livequeryd_keys_recipient_src: secrets/live/keys.recipient  # age-keygen -y keys.age
faas_livequeryd_neon_api_key_src: secrets/live/neon-api-key      # one key for the Neon account
faas_livequeryd_memory_max: 1G                                   # grows with the databases' live results

faas_livequeryd_databases:
  - id: 3f1c0b6e-...
    database_url_src: secrets/live/3f1c.dsn                      # postgres://...; REPLICATION role
  - id: 9a2b44d1-...
    database_url_src: secrets/live/9a2b.dsn
    # Neon: let this database scale to zero.
    release_after: 5m
    awake: neon
    neon_project: <project id>
    neon_endpoint: <endpoint id>
```

Each database may also set `slot`, `publication`, `install`,
`create_publication`, `awake_every` and its own `neon_api_key_src`. Settings a
database leaves out take the role-wide `faas_livequeryd_*` defaults.

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

## Re-sealing live keys and logins (ADR-20261016151947, release 2)

apid seals each live key and login to its database
(`livequery:<database>`, `livequery-login:<database>`). Rows written before
that release use the unbound legacy namespaces, which livequeryd still opens.
Re-seal them before the release that stops opening the legacy form. Only the
hub's identity opens what apid sealed, so run this on the hub host:

```sh
sudo -u faas /opt/faas/current/bin/livequeryd -reseal \
  -keys-identity /etc/faas/secrets/livequeryd/keys.age \
  -reseal-dsn-file <file holding a control-plane DSN that may UPDATE
                    managed_postgres_live_keys and managed_postgres_live_logins>
```

The hub's own `keys-dsn` is read-only, so give `-reseal-dsn-file` a role
that can write those two tables, such as apid's runtime role. The command:
- seals each row's plaintext again, unchanged, to the same recipient. The
  customer's secret and the database login stay as they are, and the hub
  keeps serving throughout;
- writes one row at a time, and only while the row still holds the seal it
  read, so a rotation or revocation made meanwhile wins;
- is idempotent: run it again until it reports no refused rows;
- logs counts and the IDs of refused rows, never a secret or a connection
  string.

It exits non-zero when a row was left as it is. A refused row is one that
doesn't open with this identity, is bound to another database, or is a key
holding another key's secret. Resolve each one before release 3: rotate the
key, or disable and re-enable live queries on the database.

## Not covered yet

- Live logins on Neon are unverified: the owner login may not be allowed to
  grant `REPLICATION`, create event triggers or a publication `FOR ALL
  TABLES` (see the ADR).
- Native databases (ADR-1027), where the engine runs inside the database VM.
- Paging. The `live_queries` alerts (`docs/runbooks/FaasLiveQueries.md`)
  warn rather than page while live queries are a preview. The
  live-queries Grafana dashboard shows each database.
