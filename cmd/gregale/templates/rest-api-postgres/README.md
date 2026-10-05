# rest-api-postgres

A Node.js REST API (`GET /notes`, `POST /notes`) using separate runtime and
migration connections. The Procfile release command creates the notes schema
before activation; serving instances use a small connection pool and perform
no schema changes on startup.

## Deploy with Gregale managed PostgreSQL

Use an existing database from Gregale's qualified Neon-backed preview, or create
one with `gregale postgres create notes --region <region>`. Reserve the app and
attach both bindings before deploying:

```sh
gregale deploy --create-only --template rest-api-postgres --name <slug>
gregale postgres attach notes <slug> --access read_write --env DATABASE_URL
gregale postgres attach notes <slug> --access migration --env MIGRATION_DATABASE_URL
# Pro/Scale: permit PostgreSQL on TCP 5432 when required by your egress policy.
gregale app <slug> egress-ports add 5432
gregale deploy --name <slug>
```

`DATABASE_URL` provides public-schema data access. The managed
`MIGRATION_DATABASE_URL` binding uses a direct connection and reaches only the
release task. Serving processes, companions, and ordinary manual/cron tasks
cannot request that binding, even through an explicit secret reference.
The release task uses a database advisory lock to serialize overlapping schema
changes, a 30-second lock timeout, and a 120-second statement timeout. A failed
release keeps the previous deployment serving. Application rollback does not
undo committed schema changes; use migrations compatible with the previous app.

## External PostgreSQL

An ordinary secret named `MIGRATION_DATABASE_URL` does not acquire the managed
binding's delivery restrictions. Keep an external schema-owner connection on
your operator machine. Run `MIGRATION_DATABASE_URL=... npm run migrate` locally,
then remove the `release:` line from the Procfile. Grant your runtime login
SELECT/INSERT/UPDATE/DELETE on notes and USAGE/SELECT on its sequence. Deploy
only the runtime connection:

```sh
# Create this 0600 file outside the source directory.
# File contents: DATABASE_URL=postgres://runtime:password@host/db?sslmode=require
gregale deploy --secrets-file ../rest-api-postgres.secrets
# After app creation, update it with:
gregale secrets set --app <slug> DATABASE_URL='postgres://runtime:password@host/db?sslmode=require'
```

For an already reserved app, use `gregale deploy --name <slug> --secrets-file ...`.
Use TLS with certificate verification for remote PostgreSQL. This scaffold does
not run a PostgreSQL server inside the application VM.

## Run and test locally

```sh
npm install --ignore-scripts
# Set MIGRATION_DATABASE_URL to the direct schema-owner connection.
npm run migrate
# Set DATABASE_URL to the restricted runtime connection.
npm start
npm test
```

## Try it

```sh
curl https://<slug>.gregale.dev/healthz
curl -X POST -H 'content-type: application/json' -d '{"body":"hello"}' \
  https://<slug>.gregale.dev/notes
curl https://<slug>.gregale.dev/notes
```

After edits, deploy from this directory with `gregale deploy --name <slug>`.
