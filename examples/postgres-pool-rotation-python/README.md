# Async Python PostgreSQL pool rotation

A FastAPI service with a `psycopg_pool.AsyncConnectionPool`, using the SDK's
event-loop-owned secret coordinator and PostgreSQL binding. Python 3.11+.

From this directory, install `requirements.txt` in a virtual environment, then run:

```sh
uvicorn app:app --host 0.0.0.0 --port 8080
```

Deploy with the modern Gregale secret reload contract: `FAAS_SECRETS_SNAPSHOT_FILE`,
`FAAS_SECRETS_RELOAD_READY_FILE`, `FAAS_SECRETS_RELOAD_ACK_ENDPOINT`, and
`FAAS_SECRETS_RELOAD_GENERATION`. The managed binding supplies `DATABASE_URL` in
the atomic snapshot. There is no process-environment credential fallback.
Use one worker, without Uvicorn reload mode: the coordinator owns SIGHUP on the
main-thread event loop and refuses to replace an existing application handler.

The URI must explicitly supply user, password, host, port and database. This
example additionally requires `sslmode=verify-full`; supply `sslrootcert` for a
private CA. It preserves those TLS settings through rotation. Driver options,
pool size, queue capacity and timeouts here are application settings.

The lifespan waits for validated adoption and its applied ACK before serving.
`GET /db-time` holds a pool lease through transaction completion and connection
return. `GET /healthz` is process liveness and remains responsive while adoption
waits for the database. Pool creation, validation and retirement run on the
application event loop. Only secret-file I/O is offloaded to a thread.

SIGHUP or `await app.state.coordinator.reload(timeout=5)` reads the latest atomic
snapshot and validates a fresh pool before acknowledging it. Register every
secret user with this one coordinator. Revocation or adoption failure blocks new
database requests; outstanding transactions keep their lease and may finish.
Old and failed pools close once after their last lease releases. Leases do not
extend credential validity, and requests/transactions are never replayed.

Task cancellation fences publication and ACKs immediately. A driver that ignores
cancellation may finish later; the SDK retains ownership and closes its rejected
pool. Hooks must bound their own I/O. Never pass a pool between event loops,
return an existing pool from a factory, retain a connection beyond its lease, or
log driver/configuration objects. Startup and requests fail with generic errors.

Uvicorn stops request admission before lifespan shutdown. The coordinator then
disables new leases and waits up to five seconds for adoption and retired pools.
A timeout reports incomplete cleanup; it does not forcibly close a leased
transaction. There is no automatic retry of retirement failures.

Real TLS/SCRAM PostgreSQL integration, including this exact ASGI app (run from
the repository root against a disposable, password-authenticated superuser DB):

```sh
GREGALE_ASYNC_POSTGRES_TEST=1 DATABASE_URL='postgresql://...' \
  PYTHONPATH=sdk/python python -m pytest -q sdk/python/tests/test_async_postgres_integration.py
```

The database URI must use `sslmode=verify-full`. Tests create and remove their own
login role and table, rotate credentials with a transaction open, reject revoked
credentials, cancel work, exercise atomic snapshot files and a real ACK HTTP
endpoint, and verify liveness and pool draining. This is application-level
evidence; native microVM restore acceptance remains a separate platform gate.
