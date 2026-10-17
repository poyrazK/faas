# Async Python object-storage rotation

A FastAPI download service using `aiobotocore` with the SDK's
`AsyncObjectStorageBinding` and one `AsyncSecretReloadCoordinator`. SDK core
imports neither the S3 driver nor FastAPI. Python 3.11+.

From this directory, install `requirements.txt` in a virtual environment, then:

```sh
python app.py
```

The service listens on port 8080. `GET /objects/<key>` streams a bucket object;
`GET /healthz` checks the application's readiness object. Use one worker and one
main-thread event loop. The coordinator owns SIGHUP and refuses an existing
callable handler; do not enable Uvicorn reload mode.

Configure the modern guest contract: `FAAS_SECRETS_SNAPSHOT_FILE`,
`FAAS_SECRETS_RELOAD_READY_FILE`, `FAAS_SECRETS_RELOAD_ACK_ENDPOINT`, and
`FAAS_SECRETS_RELOAD_GENERATION`. The atomic `{revision,secrets}` envelope supplies:

```text
FILES_ENDPOINT
FILES_REGION
FILES_BUCKET
FILES_ACCESS_KEY_ID
FILES_SECRET_ACCESS_KEY
FILES_ADDRESSING_STYLE=path
```

There is no process-environment credential fallback. The factory configures
explicit SigV4 credentials, path-style addressing and `when_required` checksum
behavior. It sets finite connection/read waits, a bounded connection pool, and
one total driver attempt: failed requests, writes and stream reads are not
replayed. Ambient AWS access keys/session tokens and proxy environment do not
replace these settings. HTTPS uses certificate verification.

Before startup, create a small `readiness.txt` object in the bound bucket and
grant this example permission to HEAD it and read the objects it serves. The
required async validator calls `HeadObject` before publication and the applied
ACK. This tests that one operation on one object succeeds; it does not prove
every future operation is authorized. Choose a different application validator
for other permissions/workloads. The core helper performs no provider requests
itself. Startup waits for adopted credentials and an accepted ACK before serving.

Each download owns a lease before `GetObject` starts and holds it until the
response body closes. The response owns its task even if the browser disconnects
before headers or before the first body iteration. Cancellation returns control
without abandoning a late driver response: it is closed before releasing the
lease. `aiobotocore`'s pinned `StreamingBody.close()` closes the upstream response
synchronously, including on read errors and cancellation. Failed responses use
generic errors; a truncated response closes the connection without replay.

SIGHUP or `await app.state.coordinator.reload(timeout=5)` adopts a fresh client.
New acquisitions fail closed during adoption, revocation and failure. An active
stream retains its old client until its body closes; rotation never closes that
client beneath it. This does not extend server credential validity or guarantee
that the server will finish a revoked request. Late/failed candidates are closed
once, and all lifecycle callbacks execute on the owning event loop.

Downloads have a 15-second total budget and three-second driver inactivity waits.
These are application settings. Factories own partial allocations until returning
the `OwnedS3` wrapper; its `AsyncExitStack` owns the driver's context manager.
Do not share underlying sessions between factory results, retain clients outside
leases, or log wrappers, credentials, snapshots or raw provider errors.

For an app also using PostgreSQL, register its async binding with
`create_app(extra_bindings=(database,))`. The coordinator adopts all bindings from
one snapshot and ACKs only after every validator succeeds. A failure invalidates
all of them. This is coordinated adoption evidence, not a distributed transaction
or atomic visibility across resources. The example's shutdown drains every
registered binding. Keep database leases through transaction/connection return
and storage leases through body closure.

After the server stops request admission, lifespan shutdown stops reloads and
new downloads, invalidates bindings and gives owned requests/clients up to five
seconds to drain. Existing admitted streams may finish. A wait timeout reports
incomplete cleanup without forcibly closing leased resources; keep the loop alive
and bound driver callbacks. The example covers adoption/streaming; native VM
snapshot recovery acceptance is a separate platform gate.

From the repository root, with the example's dependencies installed:

```sh
PYTHONPATH=sdk/python python -m pytest -q \
  sdk/python/tests/test_async_object_storage_runtime.py \
  sdk/python/tests/test_async_storage_example.py
```

These tests verify real SigV4 requests against an owned local S3-compatible HTTP
fixture, held download bodies across credential rotation, failed validation,
revocation, disconnect/native cancellation, late-response cleanup, graceful
draining and truncated streams. The fixture is not provider qualification.

Mixed acceptance additionally requires a disposable TLS/SCRAM superuser
`DATABASE_URL` with `sslmode=verify-full` (and its trusted `sslrootcert` when needed):

```sh
GREGALE_ASYNC_STORAGE_POSTGRES_TEST=1 PYTHONPATH=sdk/python \
  python -m pytest -q sdk/python/tests/test_async_storage_postgres_integration.py
```

It creates/removes its own database login and table, holds a real transaction and
S3 stream across credential replacement, rejects old credentials, verifies a
failed storage consumer disables both resources without an applied ACK, proves
one committed row, cancels blocked validation, recovers and drains both clients.
