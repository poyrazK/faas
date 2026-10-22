# PostgreSQL pool rotation

A Node service using the server-only `@gregale/sdk-node/postgres/runtime`
helper with `pg.Pool`. The SDK does not import a database driver. Requests use
`GET /db-time`; each transaction pins its pool through connection release.

From this checkout, build the SDK with `npm --prefix sdk/node run build`, then
run `npm install` in this directory. Deploy the service with a managed PostgreSQL
binding that delivers `DATABASE_URL`, using `npm start` as its start command.
Enable Gregale's secret reload contract so the workload receives
`FAAS_SECRETS_SNAPSHOT_FILE`, `FAAS_SECRETS_RELOAD_READY_FILE`,
`FAAS_SECRETS_RELOAD_ACK_ENDPOINT`, and `FAAS_SECRETS_RELOAD_GENERATION`.
`DATABASE_URL` comes from the versioned snapshot, rather than process environment.
The URI must explicitly contain host, port, user, password and database. TLS options
are preserved exactly; configure them for your database's trust requirements.

On startup or SIGHUP, the helper builds a fresh pool, connects and executes
`SELECT 1` before publication. The coordinator sends an applied ACK only after
every consumer succeeds. Adoption failure or revocation blocks new acquisitions.
Transactions already holding leases may finish; the old pool closes after its
last lease releases. Failed/unpublished candidates are closed too.

Always return the driver's connection before releasing the lease. Do not retain
a pool/connection beyond its lease, share resources between factory results, or
construct a second pool from stale environment values. A lease does not extend
server credential validity. This example never retries or replays transactions;
applications must handle uncertain commit outcomes according to their own rules.

Driver connection/query waits are bounded to three seconds. The validator checks
cancellation before and after those waits; the SDK fences late results even when
a driver cannot interrupt them. SIGTERM stops reloads and acquisition, then waits
up to five seconds for retirement. A drain timeout reports incomplete cleanup
without forcibly closing leased transactions. Pool limits/timeouts here are
application settings, not platform quotas. The factories own partial allocations
until they return a pool to the helper. Python has separate synchronous and
event-loop-owned async helpers; see the
[FastAPI/psycopg example](../postgres-pool-rotation-python/README.md).

Local PostgreSQL integration (from the repository root, with a disposable,
password-authenticated superuser database in `DATABASE_URL`):

```sh
npm --prefix sdk/node run test:postgres-runtime
```

The test creates and removes its own login role. It rotates real credentials
while an old transaction remains open, checks failed validation and revocation,
and proves all owned pools drain after the transaction finishes.
