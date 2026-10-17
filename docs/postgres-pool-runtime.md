# PostgreSQL pool adoption and rotation

Managed PostgreSQL bindings deliver a connection URI in an atomic
`{revision,secrets}` snapshot. The Go, Node and synchronous/async Python SDKs can build
a fresh application pool, validate it, publish it and retire older pools after
their last lease releases. The application supplies its database driver.

Register the binding's secret consumer with `SecretReloadCoordinator`, alongside
every other secret user, and start serving only after coordinator startup
succeeds. The coordinator installs SIGHUP before readiness, adopts the initial
snapshot and sends an applied ACK only after every consumer succeeds. It never
ACKs pool construction as readiness: your required validator must connect,
check readiness and release the connection. A later consumer failure invalidates
the pool too. This is application adoption; platform release verification and
promotion still require their existing evidence.

New acquisitions fail closed during adoption, after invalid configuration or
validation failure, and on revocation/shutdown. Already-acquired leases can
finish. A failed or invalidated candidate is retired without publication,
including a factory/validator result that arrives after cancellation. Callbacks
run outside the lifecycle lock. Invalidation and Go/coordinator cancellation
remain prompt even when an application callback ignores cancellation; cleanup
must wait until that callback returns. Direct Node reloads await their callbacks,
and direct synchronous Python reloads run on the caller's thread.

## Node

Import the server-only `@gregale/sdk-node/postgres/runtime` entrypoint. It adds no
driver dependency to the SDK and stays outside browser imports.

```js
import pg from 'pg';
import {PostgresPoolBinding} from '@gregale/sdk-node/postgres/runtime';
import {SecretReloadCoordinator} from '@gregale/sdk-node/secrets/runtime';

const database = new PostgresPoolBinding('DATABASE_URL', config => {
  const pool = new pg.Pool({...config, connectionTimeoutMillis: 3000, query_timeout: 3000});
  pool.on('error', () => console.error('Postgres idle connection failed'));
  return pool;
}, {
  validate: async (pool, signal) => {
    signal?.throwIfAborted();
    const connection = await pool.connect();
    try {
      signal?.throwIfAborted();
      await connection.query('SELECT 1');
      signal?.throwIfAborted();
    } finally {connection.release();}
  },
  retire: pool => pool.end(),
});
const coordinator = new SecretReloadCoordinator({consumers: [database.secretConsumer()]});
await coordinator.start();
```

Acquire one lease per complete unit of database work. Return the checked-out
connection before releasing the lease, including on rollback or errors. For
full transaction handling and graceful shutdown, see the runnable
[pg.Pool service](../examples/postgres-pool-rotation/README.md).

## Go

The leaf SDK uses only the standard library. The application registers its own
`database/sql` PostgreSQL driver, or supplies a different pool type.

```go
database, err := faas.NewPostgresPoolBinding("DATABASE_URL",
    func(_ context.Context, config faas.PostgresPoolConfig) (*sql.DB, error) {
        return sql.Open("pgx", config.ConnectionString) // Register pgx in your app.
    }, faas.PostgresPoolOptions[*sql.DB]{
        Validate: func(ctx context.Context, pool *sql.DB) error { return pool.PingContext(ctx) },
        Retire: func(pool *sql.DB) error { return pool.Close() },
    })
// Check err; register database.SecretConsumer() with the coordinator and start it.
lease, err := database.Acquire()
// Check err before using the lease.
defer lease.Release()
pool, err := lease.Current()
// Check err; keep this lease until Commit/Rollback returns the connection.
```

Bound connect/query waits in the driver and pass the operation's context.
`Reload(ctx, values)` clones its map before queueing and permits prompt caller
cancellation. If a factory returns a nonnil owned pool with an error, that pool
is retired too. Validation errors and panics are sanitized and retire candidates.

## Python

`faas_sdk.postgres_pool_runtime` supports synchronous, thread-safe drivers such
as `psycopg_pool.ConnectionPool`. Factory and validator callbacks receive a
cancellation object with `is_set()`. Validators must return `None`; async
factories and validators are rejected.

```python
from psycopg_pool import ConnectionPool
from faas_sdk.postgres_pool_runtime import PostgresPoolBinding

def factory(config, cancel):
    return ConnectionPool(conninfo=config.connection_string, min_size=0,
                          max_size=10, timeout=3, open=True,
                          kwargs={"connect_timeout": 3, "options": "-c statement_timeout=3000"})

def validate(pool, cancel):
    if cancel is not None and cancel.is_set():
        raise RuntimeError("Cancelled")
    with pool.connection(timeout=3) as connection:
        connection.execute("SELECT 1").fetchone()
    if cancel is not None and cancel.is_set():
        raise RuntimeError("Cancelled")

database = PostgresPoolBinding("DATABASE_URL", factory, validate=validate,
                               retire=lambda pool: pool.close(timeout=3))
# Register database.secret_consumer(); start the coordinator on the main thread.
with database.acquire() as lease:
    with lease.current().connection(timeout=3) as connection:
        connection.execute("SELECT CURRENT_TIMESTAMP").fetchone()
```

The inner driver context commits/rolls back and returns its connection before
the outer lease releases. Retirement runs on a separate thread, so hooks must
be thread-safe.

## Async Python

Use `faas_sdk.async_postgres_pool_runtime.AsyncPostgresPoolBinding` with
`faas_sdk.async_secret_reload_runtime.AsyncSecretReloadCoordinator`. Factories,
validators and retirement hooks return awaitables and run on one owning event
loop, including late-candidate cleanup. The SDK imports no PostgreSQL driver.
The synchronous helper's worker/retirement threads must not own async driver
objects. See the runnable
[FastAPI/psycopg service](../examples/postgres-pool-rotation-python/README.md) for
TLS policy, bounded SQL, partial-allocation cleanup and lifespan wiring.

```python
from faas_sdk.async_postgres_pool_runtime import AsyncPostgresPoolBinding
from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCoordinator

# App-supplied async callbacks; validator must connect and return its connection.
database = AsyncPostgresPoolBinding("DATABASE_URL", make_pool,
                                   validate=check_pool, retire=close_pool)
coordinator = AsyncSecretReloadCoordinator([database.secret_consumer()])
await coordinator.start()  # Main-thread event loop; before request admission.
try:
    async with database.acquire() as lease:
        async with lease.current().connection(timeout=3) as connection:
            await connection.execute("SELECT CURRENT_TIMESTAMP")
finally:
    # Stop application request admission first. stop/invalidate/release are
    # synchronous gates; every driver callback remains asynchronous.
    coordinator.stop()
    await coordinator.wait_stopped(timeout=5)
    await database.wait_for_retirement(timeout=5)
```

Start/stop and all pool/lease operations must use the same event loop. The
coordinator owns SIGHUP and refuses an existing callable handler. Signal bursts
coalesce; a signal during adoption schedules another read afterwards. Register
every secret user through `AsyncSecretConsumer`: its `apply(snapshot, cancel)`
is async, while `invalidate()` must immediately fence acquisition/publication,
return `None`, and schedule any I/O cleanup instead of awaiting it.

`await coordinator.reload(timeout=5, cancel=asyncio_event)` supports a total
per-attempt budget, including queueing, adoption and ACK. Startup's 30-second
default also bounds the complete initial attempt. Cancellation before admission
does not disturb an earlier successful adoption. Once admitted, cancellation
invalidates all consumers and fences late results. Native task cancellation
preserves `asyncio.CancelledError`; explicit event/deadline expiry reports a
sanitized `SecretReloadError`. A later manual reload can recover. Startup failure
and `stop()` are terminal for that coordinator.

Pool hooks receive `AsyncSecretReloadCancellation` with `is_set()`, `check()` and
async `wait()`. Direct `await database.reload(values, cancel)` accepts that token;
native cancellation or token expiry returns promptly with `CancelledError`.
The helper retains ownership of admitted work even if a callback suppresses
cancellation; late pools are rejected and closed once the callback returns.
Factories own partial resources until return. Validators/retire hooks must
return `None`; callback errors are sanitized and retirement failures stay sticky.

`stop()` fences immediately. `wait_stopped()` drains coordinator tasks;
`wait_for_retirement()` separately drains admitted pool callbacks, retired leases
and cleanup. Timeouts/cancellation of those waits leave owned work running.
Keep the event loop alive until draining finishes and bound driver I/O. A lease
neither extends credential validity nor replays transactions. Cancellation cannot
retract an ACK already sent, or undo database work already committed.

## Ownership, credentials and shutdown

Factory results must exclusively own fresh resources. Never share an underlying
pool between candidates. The helpers reject obvious aliases of currently
tracked pools before validation, without closing the earlier borrower's pool;
they cannot detect shared internals or reuse of a previously disposed resource.
Factories own partial allocations until they return a pool. Validators must
throw/return an error on failure, rather than swallow it or return a boolean.

The URI must explicitly supply a `postgres`/`postgresql` scheme, host, port,
nonempty user/password and database. Keyword DSNs, fragments, control characters,
malformed escaping and connection-identity query overrides are rejected before
the factory runs. Escaped credentials/database names and IPv6 are supported.
An explicit port prevents driver fallback to ambient `PGPORT`. The original URI
and TLS options are passed unchanged; the SDK never supplies ambient credentials
or decides the application's TLS trust policy. Config and
lease JSON/inspection (Go/Node) and Python repr hide credentials. Explicit field
access, copies/spreads, driver errors and driver objects remain sensitive: avoid
logging them. SDK adoption/retirement errors omit driver payloads.

Leases retain local resources; they do not extend database credential validity,
prevent server termination, or replay queries/transactions. Keep a lease through
the entire transaction and connection release. Pool creation plus `SELECT 1`
proves only the readiness your validator checks, not application schema or
authorization suitability. Add those checks in your validator when required.

Stop request admission and reloads, stop the coordinator (or explicitly
invalidate), then call Go `WaitForRetirement(ctx)`, Node
`waitForRetirement(signal)` or Python `wait_for_retirement(timeout, cancel)`.
These waits include admitted factories, validators, retired leases and cleanup,
and ignore the active pool unless it has been invalidated. Timeout/cancellation
ends only the wait; it never forcibly closes a leased transaction. Retirement
runs once per pool without retries and can overlap across pools. Bound its work.
Failures remain sticky and sanitized; optional retirement-error observers cannot
poison cleanup. A future successful adoption can restore acquisition, but does
not erase a prior cleanup failure.

## Snapshots: checkpoint and restore

A snapshot captures every open pool connection. The first capture of a
deployment happens right after readiness, while your validator's connection is
usually still idle in the pool. A restored guest then holds dead sockets, and
the captured server session lingers until TCP keepalive closes it. Declare both
lifecycle hooks and serve them only to loopback requests carrying the exact
marker header (`X-Faas-Before-Checkpoint: 1` or `X-Faas-After-Restore: 1`):

```yaml
lifecycle:
  before_checkpoint: {path: /internal/before-checkpoint, timeout_ms: 2000}
  after_restore: {path: /internal/after-restore, timeout_ms: 2000}
```

- **before_checkpoint** stops request admission, cancels in-flight work, calls
  `invalidate()` and waits for retirement (Node `waitForRetirement(signal)`, Go
  `WaitForRetirement(ctx)`, Python `wait_for_retirement(timeout)`) within the
  hook budget. Answer 2xx only after retirement finishes. Refuse secret reloads
  until restore, since a pool built after the drain would be captured. A failed
  capture may be retried, so make the callback idempotent.
- **after_restore** (also called on frozen thaw) cancels captured work,
  invalidates, rereads the latest secret snapshot even at the same revision
  (`coordinator.reload(...)`), waits for retirement, and only then reopens
  admission.

Declaring `before_checkpoint` disables warm captures for the app (ADR-351). If
you omit it, `after_restore` still replaces captured pools, but a capture can
strand a server session. A lifecycle hook reaches one interpreter process: a
function running several workers cannot drain every worker's pool this way
(ADR-1540).

## Built-in PostgreSQL starters

Python's `rest-api-postgres-python` starter vendors the complete canonical async
runtime closure and supplies a FastAPI notes API, verified TLS, separate release
migrations, schema-backed readiness and bounded snapshot reconnect. Its restore
gate replaces captured pools even at the same revision and stays closed after
failure. See the
[Python starter guide](../cmd/gregale/templates/rest-api-postgres-python/README.md)
for deployment and exact-artifact PostgreSQL acceptance.

`rest-api-postgres` and `secret-reload-node` vendor these canonical runtime
modules and use the same database adapter. Their image labels enable SIGHUP and
required startup readiness. Both declare the checkpoint and restore hooks above:
the adapter's `checkpoint()` drains every session and `restore()` reloads the
latest atomic snapshot. The REST starter also validates the notes schema and
keeps migration isolation. The Python starter's adapter implements the same
`checkpoint()` and `restore()`. Managed deployments require the complete modern guest contract;
local REST startup still accepts an explicit `DATABASE_URL` without guest fields.

Node `coordinator.reload(signal)` can bound one manual refresh, including queue
wait and ACK transport. A cancelled queued refresh is skipped. Cancellation
after admission invalidates consumers and fences late publication. This permits
the REST restore hook to retain its existing 1.5-second budget without a delayed
refresh later publishing or acknowledging its cancelled candidate. These are
application-hook guarantees; native VM acceptance remains separate.

Repository maintainers synchronize complete runtime import dependencies and the
shared adapter with `npm --prefix sdk/node run starter:sync` after building the
SDK. The existing `sync-storage-starter.mjs --check` command remains a compatible
entrypoint and now checks all binding starters.
