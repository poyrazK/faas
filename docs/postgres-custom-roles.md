# Custom Postgres roles

Some self-hosted tools expect PostgreSQL group roles that an administrator
creates before their first start. Windmill, for example, needs
`windmill_user` and `windmill_admin`, and `windmill_admin` must bypass
row-level security. Gregale never hands out an administrator login, so it
creates these roles for you: you declare them on a managed database and
grant them to the database's own logins, which switch to them with
`SET ROLE`. Custom roles are in beta, on Hobby and above.

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
- `--default-for` names the access modes (`read_write`, session logins too,
  and `read_only`) whose logins start every session as the role, as if each
  session began with `SET ROLE`. Use it for a tool whose own connection must
  act as the role; poolers refuse a role set in the connection string. The
  role must be granted to those modes, a login starts as at most one role,
  and `--default-for ''` clears it. `migration` logins always start as the
  schema owner.
- Names use lowercase letters, digits and underscores. Names starting with
  `gregale`, `pg_`, `neon` and other provider prefixes are reserved. A
  database holds at most 16 custom roles.

Change who can use a role, or drop it:

```sh
gregale postgres roles grant  windmill-db windmill_user --to read_only
gregale postgres roles grant  windmill-db windmill_admin --to read_write --default
gregale postgres roles revoke windmill-db windmill_user --from read_only
gregale postgres roles delete windmill-db windmill_admin
```

A custom role can run the functions and procedures in schema `public` that
your migrations create, as the `read_write` logins can.
Table privileges are yours to grant from a migration.

Dropping a role revokes it from every login and hands any object it owns to
the database's schema owner. A role another role is a member of is dropped
after its members.

Over the API: `GET /v1/postgres/databases/{id}/roles`,
`PUT /v1/postgres/databases/{id}/roles/{name}` with
`{"bypass_rls": true, "granted_to": ["migration", "read_write"], "member_of": ["windmill_user"], "default_for": ["read_write"]}`
(leave `default_for` out to keep the role's current defaults)
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
   gregale postgres roles create windmill-db windmill_admin --bypass-rls --member-of windmill_user --default-for read_write
   ```

   Windmill's own connection must bypass row-level security, so its
   `read_write` logins start every session as `windmill_admin`. Its user
   requests still switch to `windmill_user` with `SET LOCAL ROLE`.

2. Bind the app with `read_write`, and give it a `migration` binding as
   well. A running app never receives the `migration` login; only release
   commands do:

   ```sh
   gregale postgres attach windmill-db windmill --env DATABASE_URL --access read_write
   gregale postgres attach windmill-db windmill --env MIGRATION_DATABASE_URL --access migration
   ```

   A separate worker app binds `DATABASE_URL` with `read_write` only.

3. Run Windmill's migrations in a release command. Windmill migrates its
   schema when it starts, so the release command starts the server against
   the `migration` login, waits until it answers, and stops it:

   ```sh
   #!/bin/sh
   # migrate.sh
   set -eu
   DATABASE_URL="$MIGRATION_DATABASE_URL" PORT=8001 MODE=server SKIP_MIGRATION=false windmill &
   pid=$!
   i=0
   while [ "$i" -lt 600 ]; do
     if curl -fsS http://127.0.0.1:8001/api/version >/dev/null 2>&1; then
       kill "$pid"; wait "$pid" 2>/dev/null || true
       # Windmill adds a daily audit table only 3 days ahead, at runtime,
       # which a read_write login cannot do; a missing one fails logins.
       psql "$MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 -X -q -c "DO \$\$
       DECLARE d date; BEGIN
         FOR d IN SELECT generate_series(current_date, current_date + 365, interval '1 day')::date LOOP
           EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_partitioned FOR VALUES FROM (%L) TO (%L)',
                          'audit_' || to_char(d, 'YYYYMMDD'), d, d + 1);
         END LOOP; END \$\$"
       exit 0
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

   Set `SKIP_MIGRATION=true` in the image for the serving app: Windmill
   otherwise creates its migrations table at every start, which a
   `read_write` login cannot. The upstream Windmill images are larger than
   the app filesystem, so build from a Dockerfile that copies only the
   `windmill` binary from `ghcr.io/windmill-labs/windmill-slim` onto
   `debian:trixie-slim`, with `postgresql-client` for the release command.
   For TypeScript jobs, add Bun (`COPY --from=oven/bun:1.4.4
   /usr/local/bin/bun /usr/bin/bun`) and run `MODE=standalone`, the API and
   one worker in one app. Set `BASE_URL` to the app's URL.

   The release command creates the audit tables a year ahead, so redeploy
   at least once a year. Windmill still logs `permission denied for schema
   public` when it tries to create them itself; the tables exist, so those
   lines are harmless.

4. Deploy. Windmill's own role setup only notices that the roles exist, its
   migrations grant them their tables, and its requests switch to them with
   `SET LOCAL ROLE`, as they do on a database prepared with Windmill's
   `init-db-as-superuser.sql`. Log in with Windmill's default
   `admin@windmill.dev` / `changeme` and change the password.

While the app is scaled to zero its worker does not run, so schedules and
queued jobs wait for the next request. Keep one instance warm if they must
run on time.

## Branches and restores

A branch or a restore copies the database's roles and their table grants,
but not their declaration. Declare the roles again on the new database
(`gregale postgres roles create`); Gregale adopts the copies it finds.
