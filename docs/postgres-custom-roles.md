# Custom Postgres roles

Some self-hosted tools expect PostgreSQL group roles that an administrator
creates before their first start. Windmill, for example, needs
`windmill_user` and `windmill_admin`, and `windmill_admin` must bypass
row-level security. Gregale never hands out an administrator login, so it
creates these roles for you: you declare them on a managed database and
grant them to the database's own logins, which switch to them with
`SET ROLE`. This is a preview capability, on Hobby and above
([ADR-20261116213000](adr/20261116213000-custom-postgres-group-roles.md)).

## Declare roles

```sh
gregale postgres roles create windmill-db windmill_user
gregale postgres roles create windmill-db windmill_admin --bypass-rls --member-of windmill_user
gregale postgres roles list windmill-db
```

- A custom role can never log in. `--bypass-rls` is the only special
  attribute you can ask for; superuser, `CREATEROLE`, `CREATEDB` and
  replication are never available.
- `--grant-to` names the access modes whose logins may `SET ROLE` to the role:
  `migration`, `read_write` (session logins too) and `read_only`. The default
  is `migration,read_write`. Logins issued later, after a rotation or a new
  binding, get the role too.
- `--member-of` makes the role a member of other custom roles of the same
  database, with their privileges.
- Names use lowercase letters, digits and underscores. Names starting with
  `gregale`, `pg_`, `neon` and other provider prefixes are reserved. A
  database holds at most 16 custom roles.

Change who can use a role, or drop it:

```sh
gregale postgres roles grant  windmill-db windmill_user --to read_only
gregale postgres roles revoke windmill-db windmill_user --from read_only
gregale postgres roles delete windmill-db windmill_admin
```

Dropping a role revokes it from every login and hands any object it owns to
the database's schema owner. A role another role is a member of is dropped
after its members.

Over the API: `GET /v1/postgres/databases/{id}/roles`,
`PUT /v1/postgres/databases/{id}/roles/{name}` with
`{"bypass_rls": true, "granted_to": ["migration", "read_write"], "member_of": ["windmill_user"]}`
and `DELETE /v1/postgres/databases/{id}/roles/{name}`. A role reports
`pending` until it is applied to the database; Gregale retries on its own and
`last_error_code` says why it is waiting.

## BYPASSRLS, Data API and Auth

A login that switches to a `BYPASSRLS` role reads rows that row-level
security would hide. Gregale's Data API and Auth rely on row-level security,
so a database holds either a `BYPASSRLS` custom role or a Data API or Auth
binding, never both: each is refused with `postgres_bypass_rls_conflict`
while the other exists. Run tools that need `BYPASSRLS` on their own
database.

`BYPASSRLS` never adds table privileges: a role still reads only what it has
been granted.

## Windmill

1. Create a database and declare Windmill's roles:

   ```sh
   gregale postgres create windmill-db
   gregale postgres roles create windmill-db windmill_user
   gregale postgres roles create windmill-db windmill_admin --bypass-rls --member-of windmill_user
   ```

2. Bind the server and the workers with `read_write`, and give the server a
   `migration` binding as well. A running app never receives the `migration`
   login; only release commands do:

   ```sh
   gregale postgres attach windmill-db windmill --env DATABASE_URL --access read_write
   gregale postgres attach windmill-db windmill --env MIGRATION_DATABASE_URL --access migration
   gregale postgres attach windmill-db windmill-worker --env DATABASE_URL --access read_write
   ```

3. Run Windmill's migrations in a release command. Windmill migrates its
   schema when it starts, so the release command starts the server against
   the `migration` login, waits until it answers, and stops it:

   ```sh
   #!/bin/sh
   # migrate.sh
   set -eu
   DATABASE_URL="$MIGRATION_DATABASE_URL" PORT=8001 MODE=server windmill &
   pid=$!
   i=0
   while [ "$i" -lt 600 ]; do
     if curl -fsS http://127.0.0.1:8001/api/version >/dev/null 2>&1; then
       kill "$pid"; wait "$pid" 2>/dev/null || true; exit 0
     fi
     kill -0 "$pid" 2>/dev/null || exit 1
     sleep 1; i=$((i + 1))
   done
   kill "$pid"; exit 1
   ```

   ```yaml
   # gregale.yaml
   hosting:
     port: 8000
     health: /api/version
   release:
     command: /usr/src/app/migrate.sh
   ```

   The upstream Windmill images are larger than the app filesystem, so build
   from a Dockerfile that copies only the `windmill` binary from
   `ghcr.io/windmill-labs/windmill-slim` onto `debian:trixie-slim`.

4. Deploy. Windmill's own role setup only notices that the roles exist, its
   migrations grant them their tables, and its requests switch to them with
   `SET LOCAL ROLE`, as they do on a database prepared with Windmill's
   `init-db-as-superuser.sql`.

## Branches and restores

A branch or a restore copies the database's roles and their table grants,
but not their declaration. Declare the roles again on the new database
(`gregale postgres roles create`); Gregale adopts the copies it finds.
