# `postgres` ansible role

Installs PostgreSQL 15, creates the `faas` system user / role /
database, and (spec §11) hardens the cluster to unix-socket-only
listening: `listen_addresses=''` plus a `pg_hba.conf` that peer-auths
local connections and `reject`s every TCP source.

## What this role does

1. apt-installs `postgresql-15`, `postgresql-contrib-15`, `libpq-dev`.
2. Enables + starts the cluster.
3. Creates the `faas` system user (home `/var/lib/faas`, nologin shell).
4. Creates the `faas` postgres role + database, with `createuser`
   fallback for hosts missing the `community.postgresql` collection.
5. Fixes `/run/postgresql` ownership to `postgres:postgres` so peer
   auth works for the `faas` user.
6. **§11 hardening**: `listen_addresses=''` (restart), then a 4-line
   `pg_hba.conf` (reload) that rejects all TCP auth.

## Idempotency

The hardening tasks use `register: <name>` + `failed_when: false` so
the role converges on hosts without `/etc/postgresql/15/main/` (CI /
chroot bootstrap) without halting. On a real EX44 the handlers in
`handlers/main.yml` issue the restart + reload via systemd.

## Carve-outs

- The role does not run migrations (M5 owns `migrations/`; that's a
  separate `migrate` role).
- `community.postgresql.postgresql_user` may fail silently on hosts
  without the collection — the role has an explicit `psql` fallback.
- `listen_addresses=''` is **destructive**: any client currently
  connected via TCP will drop. Spec §11 forbids TCP listeners; on a
  fresh EX44 this is a no-op.

## Refs

- Spec §11 (security baseline), §Component ownership (apid is the
  only writer; gatewayd does not connect to Postgres).
- `deploy/scripts/faas-m75-smoke.sh` step 7 verifies the conf post-
  bootstrap.