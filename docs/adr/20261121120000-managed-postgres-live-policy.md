# ADR-20261121120000: Managed PostgreSQL operator policy is hot runtime configuration

- Status: Accepted
- Date: 2026-11-21
- Related: ADR-155 (provider registry), ADR-20261028103115 (production
  provisioning approval and fleet policy), ADR-1700 (runtime
  config and platform flags)

## Context

apid read every managed PostgreSQL operator value once at start, from the
provider registry file (`FAAS_MANAGED_POSTGRES_CONFIG`) and the
`FAAS_MANAGED_POSTGRES_PRODUCTION_*` environment: the provisioning switch,
the per-account database limit, the fleet ceilings, the usage ceilings and
provider rates, and the production approval. Opening managed PostgreSQL at
launch (2026-11-21) took five hand edits of a file on the control plane and
five apid restarts, and putting the values in Ansible group_vars would have
frozen them in git. The owner asked for these limits to be changeable live.

apid already has hot, versioned, audited runtime configuration with apply
receipts (platform flags, builder slots).

## Decision

1. A runtime config key `managed_postgres_policy` (kind of the same name,
   apply mode hot) carries `managedpostgres.LivePolicy`:
   `provisioning_enabled`, `max_databases_per_account`, `fleet`, `usage`
   (ceilings and rates only) and `production_approval`. Every field is
   optional; an omitted field keeps the file's (or the environment's) value.
   `{}`, the default, changes nothing.
2. The registry keeps the file's policy and serves an effective policy, the
   live value laid over the file, swapped atomically. Readers go through
   `ProvisioningEnabled()`, `MaxDatabasesPerAccount()`, `UsagePolicy()`,
   `Fleet()` and `ProductionApproval(fallback)`; the production gate reads the
   approval on every check.
3. Not live, by design: backends (their settings are each database's placement
   fingerprint) and usage metering on/off with its collection timing (the
   usage collector, metrics and cap enforcer are built from them at start).
   Cap enforcement modes stay environment settings for now: `on` needs
   qualification artifacts loaded at start.
4. A value is checked against this apid's registry before it is stored
   (unknown approval backend, invalid merged usage policy) and again when
   applied; a refused value never becomes effective and its receipt says why.
   A stored value that no longer fits at start is logged and the file's values
   serve, so a bad policy cannot keep apid down.
5. Each applied policy re-reports production readiness and the approval's end
   for the expiry alerts.

## Consequences

- Operators change limits and renew approvals with `gregalectl config set`
  (step-up, audit reason) and roll back with `gregalectl config rollback`.
- The provider registry file shrinks to backends, metering and health;
  Ansible stops carrying commercial values.
- Usage metering and cap modes still need a restart to change.

## Amendment 2026-11-23: scale-to-zero suspend timeout

The Neon suspend timeout was hard-coded at 300 s, so every wake billed at
least five minutes of compute and anything touching a database more often
than that kept it awake. It is now the policy field `suspend_timeout_seconds`
(60..604800, default 120). The registry pushes it to providers that implement
`SuspendTimeoutSetter` at start and on every applied policy; new databases,
restores and compute-policy changes use it at once. Existing databases are
moved by `SuspendTimeoutSweeper`, on its own 5-minute loop on every apid
(independent of usage metering): it PATCHes only a primary endpoint that is
suspended, enabled and not changing, so a serving or held compute is never
touched. It remembers, per database, the value it confirmed and the row's
`updated_at`, and reads the provider only when either moved (a value
change, a compute-policy change, restore or cutover) or the database was
deferred. Snapshot-copy checks compare scale-to-zero against always-on,
not the exact timeout, so a changed setting is not drift.

Customer override (same day): a customer sets a scale-to-zero database's own
timeout with `gregale postgres suspend-timeout set DB 1m..1h|default`
(`PUT /v1/postgres/databases/{id}/suspend-timeout`). It lives in
`managed_postgres_suspend_timeouts` (one row per database, 60..3600 s), not
in the database's spec, so resize and compute-policy journals, their JSON
checks and triggers are unchanged. The policy value stays the default for
databases without a row. Setting it writes the provider at once when the
compute is suspended, else the sweep does; a longer value passes the usage
guardrail. The sweep re-reads a database's value just before writing, so a
customer change made during a sweep is not overwritten. Database views show
`suspend_timeout_seconds` and `suspend_timeout_custom`.
