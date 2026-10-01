# ADR-388 · Managed PostgreSQL migration credential delivery

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Deliver managed `migration` bindings only to persisted release
  tasks, using the existing deployment release gate and sealed-secret transport.
- **Why:** SQL privilege separation from ADR-387 is ineffective if the serving
  application also receives a schema-owner connection. The PostgreSQL starters
  need a deployment migration path that works with restricted runtime roles.

Scoped app-secret reads project access from the canonical binding catalog in
one sqlc query. The non-persistent MemStore receives equivalent metadata from
the trusted credential sink. Missing or unknown managed access fails closed.
The shared delivery policy excludes migration bindings from serving, companions,
manual tasks, cron tasks, and runtime reload. Explicit references cannot override
that boundary. The scheduler verifies the persisted task kind before authorizing
release delivery, and supplements a serving allowlist with migration bindings.
Environment names never determine privilege or delivery audience.

Migration rotation publishes the new secret and makes retirement reconcilable
without a serving restart. The atomic retirement claim checks that no app task
in the same account/app/scope remains restoring/running. A task that loaded an
older generation entered restoring before that load; a queued/new task loads
the latest committed secret. The fence includes every task kind for adoption of
older binaries. Retry retains the existing lease/generation fences. An active
release can therefore finish with its old connection before the provider login
is disabled and its sessions terminated.

The data migration invalidates existing snapshots for apps with migration
bindings, stamps runtime configuration changes, and resumes pending migration
rotations. Operators update the complete fleet and restart old resident
workloads before opening the canary gate. Old snapshots cannot be made safe by
filtering future cold boots alone. Invalidation and finished rotations are not
reversed on downgrade.

The starters run schema changes in the existing release task with advisory
transaction locks and bounded SQL waits. Runtime startup performs no DDL. Failed
release commands preserve the preceding app revision; schema changes still
require compatibility with that revision. Migration history/checksums belong to
the application's chosen migration tool. Gregale does not add a second migration
executor or promise to roll back committed database changes.

Ordinary customer secrets retain their existing semantics. External PostgreSQL
users keep migration credentials local and run schema changes before deploying,
or supply their own explicit credential-delivery mechanism. Naming a plain
secret `MIGRATION_DATABASE_URL` does not turn it into a managed binding.

Validation covers scoped SQL access projection, serving/reference/sidecar/reload
exclusion, persisted release authorization, retirement during restoring/running
tasks, existing release-gate success/failure/replay behavior, and actual starter
scripts with restricted PostgreSQL roles and overlapping migration attempts.
