# rest-api-postgres

A Node.js REST API (`GET /notes`, `POST /notes`) using separate runtime and
migration connections. The Procfile release command creates the notes schema
before activation; serving instances use a small connection pool and perform
no schema changes on startup.

The included `gregale.yaml` declares a two-second `after_restore` hook. Before
readiness, its loopback-only callback closes captured SQL sessions, opens a new
pool, and reads the existing notes table within a 1.5-second budget. A failure
rejects snapshot restoration so Gregale can cold-boot the deployment. Cold boot
uses `/healthz`; an empty notes table is valid, a missing schema is not. Existing
apps must deploy the new source and lifecycle configuration to adopt this hook.

It also declares a two-second `before_checkpoint` hook. Before each capture, the
callback closes admission, cancels unfinished work and waits until every SQL
session has closed, so no snapshot holds a connection or open transaction.
Repeated calls report the completed drain; only a restore reopens the pool. The
platform turns off warm captures for apps that declare this hook; remove it if
you prefer warm captures, and `after_restore` still replaces captured pools.

Requests have a five-second SQL deadline and bounded pool admission. HTTP
disconnects cancel unfinished transactions; idle and active connection errors
discard the failed session. Writes are never automatically replayed. Cancellation observed
before COMMIT rolls back; a connection failure during COMMIT may leave an
uncertain result, so applications needing retry-safe writes must add their own
idempotency keys. The example does not promise exactly-once writes.

## Credential rotation and application adoption

The Dockerfile opts into SIGHUP delivery and required reload readiness. The
vendored canonical coordinator reads `FAAS_SECRETS_SNAPSHOT_FILE` and validates
a new pool by reading the notes schema before publication and an applied ACK.
All driver connections use the existing certificate/hostname verification
policy. Serving refuses migration credentials in either environment or snapshot.

Hold a pool lease through each transaction and driver connection release.
Rotation and revocation block new acquisitions while admitted writes can finish;
old pools close after their last lease releases. Failed validation never keeps
using old credentials for new work. Neither writes nor uncertain COMMITs are
replayed automatically. Cancellation retains a lease until a late driver
connection is returned and destroyed.

The existing restore hook aborts captured work, fences pending candidates and
reloads the latest atomic snapshot even if its revision has not changed. It
validates the schema and drains captured resources within its 1.5-second budget.
A timeout cancels queued/active adoption, prevents late publication or applied
ACKs from that attempt, and keeps readiness closed. A successful later restore
can recover; the platform can otherwise fall back to cold boot.

The complete atomic snapshot/readiness/ACK/generation contract is required in a
managed guest. Missing fields or legacy-only projections fail closed. For local
`npm start`, `DATABASE_URL` is used when no guest reload contract is advertised;
the URI must explicitly contain user, password, host, port and database. Do not
set guest protocol variables locally unless providing a complete test contract.
HTTP serving starts only after initial SQL validation and, for a managed guest,
accepted adoption. Redeploy existing apps to obtain the updated image and helper.

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
then remove the `release:` line from the Procfile and the `release` section from
`gregale.yaml`. Grant your runtime login
SELECT/INSERT/UPDATE/DELETE on notes and USAGE/SELECT on its sequence. Deploy
only the runtime connection:

```sh
# Create this 0600 file outside the source directory.
# File contents: DATABASE_URL=postgres://runtime:password@host:5432/db?sslmode=require
gregale deploy --secrets-file ../rest-api-postgres.secrets
# After app creation, update it with:
gregale secrets set --app <slug> DATABASE_URL='postgres://runtime:password@host:5432/db?sslmode=require'
```

For an already reserved app, use `gregale deploy --name <slug> --secrets-file ...`.
Use TLS with certificate verification for remote PostgreSQL. This scaffold does
not run a PostgreSQL server inside the application VM.
`sslmode=require`, `verify-ca`, and `verify-full` all retain certificate and
hostname verification. For a private CA, use `NODE_EXTRA_CA_CERTS` pointing at
your deployed CA bundle. Certificate/key paths in the URL and opportunistic TLS
modes and alternative URL SSL overrides are rejected. Explicit `sslmode=disable`
is available for local development.
Serving refuses `MIGRATION_DATABASE_URL`; run migrations in a separate process.

## Run and test locally

```sh
npm ci --ignore-scripts
# Set MIGRATION_DATABASE_URL to the direct schema-owner connection.
npm run migrate
# Remove MIGRATION_DATABASE_URL from the serving environment.
unset MIGRATION_DATABASE_URL
# Set DATABASE_URL to the restricted runtime connection.
npm start
npm test
```

The real SQL regression suite is explicit and destructive within its fixture:
set `GREGALE_POSTGRES_STARTER_TEST_DISPOSABLE=1` and
`GREGALE_POSTGRES_STARTER_TEST_URL` to an empty disposable database administrator
URL, trust its TLS certificate, then run `npm run test:postgres`. It creates and
removes only its notes workload and two temporary logins, and tests HTTP writes,
session termination, restore readiness, cancellation, credential rotation during
an open write, revocation and concurrent restore/reload fencing.
Missing fixture inputs fail the suite. The repository's Go wrapper materializes
this exact starter into a private directory and gives it a private database;
both PR CI and full CI run it against TLS/SCRAM PostgreSQL.

## Try it

```sh
curl https://<slug>.gregale.dev/healthz
curl -X POST -H 'content-type: application/json' -d '{"body":"hello"}' \
  https://<slug>.gregale.dev/notes
curl https://<slug>.gregale.dev/notes
```

After edits, deploy from this directory with `gregale deploy --name <slug>`.

Canonical runtime modules and the shared database adapter are synchronized with
`npm --prefix sdk/node run build && npm --prefix sdk/node run starter:sync` in the
Gregale repository. `database.js` here is the adapter source for both PostgreSQL
starters. The synchronization gate validates their full runtime import closure.
