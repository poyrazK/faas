# rest-api-postgres-python

A Python 3.12 FastAPI REST API (`GET /notes`, `POST /notes`) with separate runtime
and migration credentials, canonical async pool adoption, verified TLS, and a
bounded snapshot reconnect hook. Its vendored SDK runtime and pinned requirements
are self-contained; no SDK checkout or separately published package is needed.

## Deploy with managed PostgreSQL

Use a database from Gregale's qualified Neon-backed preview, or create one with
`gregale postgres create notes --region <region>`. Reserve the app before attaching
credentials, then deploy the generated source:

```sh
gregale init --template rest-api-postgres-python --path notes-api
gregale deploy --create-only --template rest-api-postgres-python --name <slug>
gregale postgres attach notes <slug> --access read_write --env DATABASE_URL
gregale postgres attach notes <slug> --access migration --env MIGRATION_DATABASE_URL
# Pro/Scale: allow PostgreSQL on TCP 5432 when required by the egress policy.
gregale app <slug> egress-ports add 5432
cd notes-api
gregale deploy --name <slug>
```

`python migrate.py` is the release command in both `gregale.yaml` and `Procfile`.
It runs before activation, using the managed migration binding's direct
connection. Managed migration credentials reach only the release task. The
serving process refuses `MIGRATION_DATABASE_URL` in either its environment or an
atomic snapshot, even if it also has a valid runtime binding. It performs no DDL
at startup. The migration uses an advisory transaction lock, a 30-second lock
timeout and a 120-second statement timeout. A failed release keeps the previous
deployment serving; application rollback does not undo committed schema changes.

For external PostgreSQL, run the migration separately with an explicit
`MIGRATION_DATABASE_URL`, then remove the `release:` line from `Procfile` and the
`release` section from `gregale.yaml`. Grant the serving role schema USAGE, notes
SELECT and INSERT, and sequence USAGE. Store only its `DATABASE_URL` with
`gregale env set --app <slug>` or a protected `--secrets-file` outside this
directory. An ordinary secret named `MIGRATION_DATABASE_URL` does not obtain the
managed release-only isolation: never attach that secret to a serving workload.

## Startup, rotation and requests

Run `python handler.py`, one process and one event loop. The Dockerfile uses an
unprivileged user and opts into SIGHUP and required secret reload readiness.
Do not add Uvicorn workers/reload mode or enable proxy-header trust: the
coordinator owns SIGHUP, and restore authorization depends on the actual peer.

The complete modern guest contract is required:
`FAAS_SECRETS_SNAPSHOT_FILE`, `FAAS_SECRETS_RELOAD_READY_FILE`,
`FAAS_SECRETS_RELOAD_ACK_ENDPOINT`, and `FAAS_SECRETS_RELOAD_GENERATION`.
Startup reads an atomic `{revision,secrets}` envelope, creates a fresh pool,
connects and reads the existing notes schema, and obtains an applied ACK before
serving. Missing schema, malformed/incomplete delivery and failed validation
fail closed. There is no stale environment or legacy-file credential fallback.
Local runs need the same contract, including an ACK endpoint; the tests provide it.

URIs must explicitly supply user, password, host, port and database. Managed
`sslmode=require` and `verify-ca` are upgraded to `verify-full`; hostname and
certificate-chain verification are always enabled. The bundled psycopg binary
uses system trust explicitly, or the URI's `sslrootcert` for a private CA. Place
that CA at a readable path in the application image. Insecure TLS modes are
rejected. Ambient `PGSSLROOTCERT` and `PGSSLMODE` do not override this policy.

SIGHUP or `await database.reload()` validates a replacement pool before adoption.
New requests fail closed during adoption, revocation or failure. Requests already
holding leases may finish across rotation; the old pool closes after its final
connection returns and lease releases. Every factory returns a fresh, exclusively
owned pool. Driver callbacks and retirement run on the owning event loop.

`GET /healthz` queries the notes schema; it is database readiness, not process
liveness. Each request has a three-second total SQL budget, bounded connection
admission, a transaction-local SQL timeout and a pool lease through connection
return. HTTP disconnects cancel the operation. A late connection/driver result
retains its lease until cleanup actually completes. No query, write or uncertain
COMMIT is replayed. Applications needing retry-safe writes must supply their own
idempotency contract. Pool sizes, request limits and timeouts are app settings.

## Snapshot checkpoint, restore and shutdown

The two-second `lifecycle.before_checkpoint` hook calls
`/internal/before-checkpoint` before each capture. The adapter closes
admission, cancels unfinished work and waits until every SQL session has
closed, so no snapshot holds a connection or open transaction. Repeated calls
report the completed drain; only a restore reopens the pool. The platform turns
off warm captures for apps that declare this hook.

The two-second `lifecycle.after_restore` hook calls `/internal/after-restore`.
Each hook requires an actual loopback peer and its exact marker header
(`X-Faas-Before-Checkpoint: 1` or `X-Faas-After-Restore: 1`).
Forwarded-address headers cannot authorize either.

Before awaiting work, the adapter closes database readiness, cancels captured
requests and invalidates the captured pool. It rereads the latest atomic snapshot
even at the same revision, validates a fresh connection/schema, and drains old
resources within a maximum 1.5-second application budget. Concurrent restore
calls coalesce. Success reopens readiness; failure leaves it closed so Gregale
can reject restoration and cold-boot the deployment. A later successful hook can
recover. An empty notes table is valid; a missing schema is not.

The deadline includes reloads queued behind SIGHUP and ACK transport. A cancelled
or timed-out attempt cannot later publish its candidate or initiate an applied
ACK. Cancellation cannot retract an ACK already sent or committed SQL. Driver
callbacks that suppress cancellation remain owned until they return, and their
failed candidates are retired. New readiness remains closed after failed restore
even if an independent rotation later succeeds.

Shutdown stops admission/reloads, cancels active requests and waits up to five
seconds for coordinator work and retired leases/pools. Timeout reports incomplete
cleanup without forcibly closing a leased transaction. Driver hooks must bound
their own I/O. Do not log configuration, snapshots, driver objects or raw driver
errors; application responses and lifecycle failures are sanitized.

## Verify the generated artifact

Install `requirements.txt`, then run portable lifecycle tests:

```sh
python -m unittest discover -s tests -p test_database.py -v
```

The opt-in SQL tests need a disposable, TLS/SCRAM superuser `DATABASE_URL` using
`sslmode=verify-full` and its explicit `sslrootcert` when appropriate:

```sh
GREGALE_PYTHON_POSTGRES_TEST=1 python -m unittest discover -s tests -p test_postgres.py -v
```

They create/remove their own database and roles, run the actual migration CLI,
rotate credentials with an open transaction, reject stale credentials and runtime
DDL, revoke new work, replace terminated pooled sessions, recover a missing
schema, and fence active/queued restore timeouts against a real ACK HTTP server.
Repository CI materializes the CLI artifact before running these tests. This is
portable application-hook evidence; native x86_64 microVM restore acceptance is
a separate platform gate.

Repository maintainers synchronize the complete canonical runtime import closure
with `python sdk/python/scripts/sync_binding_starters.py` and check drift with
`--check`. Edit SDK sources, then regenerate; do not edit `runtime/` copies.
