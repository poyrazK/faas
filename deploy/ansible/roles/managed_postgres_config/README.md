# managed_postgres_config

Renders apid's managed PostgreSQL provider registry, the JSON file
`FAAS_MANAGED_POSTGRES_CONFIG` names (`pkg/managedpostgres/registry.go`),
from `faas_managed_postgres_config`, and points apid at it with the drop-in
`faas-apid.service.d/zz-faas-managed-postgres.conf`.

Without that file apid answers every managed PostgreSQL request with
`Managed PostgreSQL is not configured for this environment`. Before this role
the file and its `sealed.env` line were installed by hand, so a control-plane
move (GCP to OVH, 2026-11) left prod without it.

## Turning it on

Set `faas_managed_postgres_config` to the registry as YAML, for example in
`group_vars/control_plane/managed_postgres.yml`. The shape is
`deploy/managed-postgres.example.json`:

```yaml
faas_managed_postgres_config:
  default_region: us-east-2
  defaults: {us-east-2: neon-us}
  max_databases_per_account: 10
  provisioning_enabled: true
  health: {enabled: true, interval_seconds: 60, stale_after_seconds: 300}
  backends:
    - id: neon-us
      driver: neon
      region: us-east-2
      namespace: org-...              # Neon organization ID
      settings:
        region_id: aws-us-east-2
        database_name: gregale
        max_storage_bytes: "107374182400"
        max_restore_window_seconds: "604800"
      secret_env: {api-key: FAAS_NEON_API_KEY}
```

It holds no secrets. A backend names the `sealed.env` variables holding its
keys in `secret_env`; settings that look like secrets are refused.

Converge the control plane. Before apid sees the file, the role checks:

- **Shape.** Every backend has an ID, driver and region; `defaults` names
  known backends; `secret_env` names variables, not values.
- **Secrets.** `/etc/faas/sealed.env` sets each `secret_env` variable. With
  `faas_neond_broker` on, a Neon backend's `api-key` variable must be absent
  instead, because neond holds the key and apid refuses to start with both.
- **No stale override.** `sealed.env` does not set its own
  `FAAS_MANAGED_POSTGRES_CONFIG`. An `EnvironmentFile` line would override
  the drop-in, so remove it once the role owns the setting.
- **Existing databases still resolve.** Each database records the backend
  ID and fingerprint it was placed on. The fingerprint is sha256 over the
  backend's driver, region, namespace and settings. The role reads every
  non-deleted database's pair from the local `faas` catalog and refuses a
  config under which any would not resolve. Changing `max_storage_bytes` or
  `max_restore_window_seconds` is allowed by listing the old settings in
  `previous_settings`. Set `faas_managed_postgres_config_verify_catalog:
  false` only where the catalog is not this host's local database.

apid refuses to start on a config it cannot load. The role restarts a
running apid and fails the converge if it does not come back, pointing at
`journalctl -u faas-apid`.

## Recreating a lost config

Fill in the backend from the Neon console (organization ID, region) and the
values the old file had. If the converge refuses with "would not resolve",
the driver, region, namespace or a setting differs from what the databases
were created with. Read the catalog's pairs with:

```sql
SELECT backend_id, backend_fingerprint, count(*)
FROM managed_postgres_databases WHERE state <> 'deleted' GROUP BY 1, 2;
```

`files/managed-postgres-config-check.py CONFIG --fingerprints` prints a
candidate's fingerprints for comparison.

## Secrets outside sealed.env, approval and cap switches

The same drop-in also carries what GCP prod kept in a hand-made
`zzzz-managed-postgres-canary.conf`:

- `faas_managed_postgres_env_files`: extra `EnvironmentFile=` lines (prod's
  Neon key is in `/etc/faas/secrets/managed-postgres/provider.env`, part of
  the cp-state bundle). Each must exist under `/etc/faas/secrets` and not be
  world-readable; `secret_env` variables may be set there or in `sealed.env`.
- `faas_managed_postgres_production_approval`: `backend`, `fingerprint`,
  `approved_at`, `until` render `FAAS_MANAGED_POSTGRES_PRODUCTION_*`
  (ADR-20261028103115). It is the owner's go-live decision for new databases.
  The role refuses one apid would reject: not the default backend, not its
  current fingerprint, a window over 90 days, or already expired.
  `files/managed-postgres-config-check.py CONFIG --fingerprints` prints the
  fingerprint.
- `faas_managed_postgres_cap_enforcement` /
  `faas_managed_postgres_storage_cap_enforcement`: `on`, `shadow` or `off`
  (empty keeps apid's default, shadow).

A `sealed.env` line or another apid drop-in setting any of these would
override the role, so the converge refuses until they are removed.

Production's values are in `group_vars/control_plane/managed_postgres.yml`.

## Turning it off

Leaving `faas_managed_postgres_config` empty, the default, touches nothing:
a hand-installed file keeps working. To remove a role-managed config, delete
the drop-in and the file, then restart apid.
