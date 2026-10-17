# ADR-1601 · Verified Application Auth release migrations

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Apply the image's verified GoTrue SQL and Gregale overlay in the
  Node release runner. Preserve upstream version records, Auth schema ownership,
  and serving through the explicit GoTrue `serve` command.

## Problem

The real-runtime Product Control team preflight reproduced a failure on a clean
PostgreSQL 16 database. GoTrue v2.197.0's
[`migrate` command](https://github.com/supabase/auth/blob/v2.197.0/cmd/migrate_cmd.go)
passes `EmbeddedMigrations` to its migrator. It does not use
`GOTRUE_DB_MIGRATIONS_PATH` to select the files copied into our image. Replacing
an extracted SQL file therefore does not apply the Gregale overlay to that
command. The embedded RLS migration unconditionally grants to `postgres`, which
may not exist in a managed database.

This corrects the release-task packaging in ADR-1190. Its historical acceptance
description does not establish that the stock binary executed the shipped
overlay. Qualification must use the actual release runner and pinned runtime.

## Release contract

The Docker build retains the raw migrations, their checked-in SHA-256 manifest,
and the overlay. The build checks every upstream file against the pinned image
manifest. `upstream-migrate.mjs` repeats that verification before issuing SQL,
including for already applied versions. Extra files, unknown overlays, duplicate
versions, checksum drift and unsupported template actions fail closed.

The 75 pinned migrations use only the `Namespace` template action. The runner
replaces that action with the fixed `auth` schema and rejects other template
delimiters. It applies the checked-in overlay after verifying the original bytes.
This is not a general Go-template interpreter or a user-selected schema runner.

An advisory lock serializes upstream release migrations. Each migration and its
version record commit together; failures roll back that migration. Previously
committed versions remain recorded and a retry skips them. The runner uses the
existing `auth.schema_migrations` table and upstream version strings, allowing
databases already migrated by GoTrue to retain their history. It does not adopt
a customer tool's migration table in `public` or roll migrations backward.

The existing Auth binding creates and owns only `auth` and `gregale_auth` and
sets the login's effective owner and search path. The release connection uses
that binding and its existing migration statement timeout. After upstream SQL,
the release task applies Gregale's numbered migrations and `AUTH_AUTHZ` through
the existing runner. Advisory locks serialize the two migration families in
their existing order; no new platform database schema is introduced.

Serving continues to invoke the explicit `auth serve` command. A cold boot or
snapshot restore performs no migration. The release runner never falls back to
the binary's embedded migration command. Updating GoTrue requires a new image
digest, migration manifest and review of template actions and overlays.

## Verification and rollout

Unit tests cover raw checksums before overlays, unsupported inputs before SQL,
existing-version recovery and transactional failure. The local preflight runs
all 75 files twice and checks native GoTrue version-table compatibility, using a
restricted Auth owner without superuser, database-creation, role-creation or
RLS-bypass privileges. It then exercises real ES256 sessions, reviewed team
actions, revocation and hosted invitation acceptance in Chromium.

This proves a local Auth runtime boundary. It does not qualify native Operations,
an internal remote deployment, customer exports, or real email-provider delivery.
Those remain live pilot requirements. Product Control and its Auth team routes
retain their separate default-off feature flags.
