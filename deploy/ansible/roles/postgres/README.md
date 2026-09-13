# `postgres` ansible role

Installs the distro-supported PostgreSQL major, creates the `faas` system user / role /
database, and (spec §11) hardens the cluster to unix-socket-only
listening by default. A generated split-box inventory may set
`faas_postgres_listen_addresses` and `faas_postgres_allowed_cidrs` on the
control-plane host; that narrow mesh exception enables compute daemons to
share the database without opening it to the public interface.

## What this role does

1. apt-installs the distro meta-packages `postgresql`,
   `postgresql-contrib`, and `libpq-dev`, then discovers the installed
   cluster major and uses its config directory.
2. Enables + starts the cluster.
3. Creates the `faas` system user (home `/var/lib/faas`, nologin shell).
4. Creates the `faas` postgres role + database, with `createuser`
   fallback for hosts missing the `community.postgresql` collection.
5. Fixes `/run/postgresql` ownership to `postgres:postgres` and installs
   an explicit `faas_map` so dedicated control-plane service users can use
   peer auth as the `faas` database role.
6. **§11 hardening**: `listen_addresses=''` (restart), then a local-peer
   `pg_hba.conf` (reload) that rejects all TCP auth. In split-box mode the
   generated compute `/32` entries use `scram-sha-256`; the role requires
   `faas_postgres_password` from Ansible Vault before enabling that path.
7. **§14 M8 restore-drill wiring**:
   - `wal_level = replica` (restart-needed)
   - `archive_mode = on` (restart-needed)
   - provider-neutral `archive_command` with local-first WAL archival and
     best-effort `rclone` push (reload)
   - `max_wal_senders = 3` (reload)
   - Creates `/var/lib/pgsql/archive` owned `postgres:postgres 0750`.
8. Hands connection sizing to the adjacent `postgres_capacity` role. The
   bootstrap and node-join playbooks both run that role so two active public-
   beta compute nodes fit before admission continues.

The archive directory is the local authoritative copy — the M8 restore
drill script (`deploy/scripts/faas-m8-restore-drill.sh`) replays WAL from
there after rsyncing a nightly basebackup. The off-host rclone mirror is
best-effort and is installed by the `postgres_backup` role (issue #250).

### `archive_command` quoting constraint

`archive_command = '...'` is a **shell string** that PostgreSQL passes
to `sh -c`. `%p` is the full path of the WAL segment to archive, `%f`
is the filename. The current command copies locally first and then
best-effort mirrors the local file through the staged `offhostbox`
rclone remote.

**Constraint**: keep the value on one line and preserve the local-copy
failure as the only non-zero exit path. Remote failures must be logged
but must not block PostgreSQL WAL recycling. The restore-drill script's
cleanup sed (`/^# --- faas-m8-restore-drill:/,
/^recovery_target_action = /d`) relies on the value being a single
line — multi-line archive commands will break the range match.

The same applies to `restore_command` in the recovery stanza written
by the drill script.

## Idempotency

The hardening tasks use `register: <name>` + `failed_when: false` so
the role converges on hosts without a PostgreSQL config directory (CI /
chroot bootstrap) without halting. On a real control-plane node the handlers in
`handlers/main.yml` issue the restart + reload via systemd.

## Carve-outs

- The role does not run migrations (M5 owns `migrations/`; that's a
  separate `migrate` role).
- `community.postgresql.postgresql_user` may fail silently on hosts
  without the collection — the role has an explicit `psql` fallback.
- `listen_addresses=''` is **destructive**: any client currently
  connected via TCP will drop. Split-box operators must deliberately
  opt into the mesh listener and provide the database password through
  a secret source; the generated firewall only permits the declared
  compute CIDRs.

## Refs

- Spec §11 (security baseline), §Component ownership (apid is the
  only writer; gatewayd-internal does not connect to Postgres).
- `deploy/scripts/faas-m75-smoke.sh` step 7 verifies the conf post-
  bootstrap.
