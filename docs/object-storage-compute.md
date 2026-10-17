# Object storage from compute workloads

Create a managed bucket binding with `gregale add bucket assets --app my-api`.
The runtime receives six settings under `GREGALE_S3_ASSETS`: `ENDPOINT`,
`REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, and `ADDRESSING_STYLE`.
These describe Gregale's gateway and bucket-scoped credentials for either GCS
or an S3-compatible provider. Applications use the gateway rather than upstream
provider credentials.

## Runtime clients and rotation

The Go, Node and Python SDKs offer `ObjectStorageBinding`. Supply a client
factory using your application's S3 library. The helper validates all six
settings, builds the candidate and publishes a client/bucket pair together.
It does not select an SDK version or load ambient AWS credentials. Your factory
must set the supplied endpoint, region, credentials and path-style addressing.
It can also perform an appropriate readiness request before returning.

For example, with [AWS SDK for JavaScript v3](https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/client/s3/):

```js
import { S3Client, GetObjectCommand } from '@aws-sdk/client-s3';
import { ObjectStorageBinding } from '@gregale/sdk-node/object-storage/runtime';

const storage = new ObjectStorageBinding('GREGALE_S3_ASSETS', options => new S3Client({
  ...options, credentials: {...options.credentials}, // AWS annotates resolved credentials.
}));
await storage.reload(process.env); // Startup only; environment values do not change on SIGHUP.

async function readAsset(key) {
  const { client, bucket } = storage.current();
  return client.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
}
```

Go factories receive an `ObjectStorageClientConfig`:

```go
storage, err := faas.NewObjectStorageBinding("GREGALE_S3_ASSETS",
    func(ctx context.Context, c faas.ObjectStorageClientConfig) (*s3.Client, error) {
        return s3.New(s3.Options{
            Region: c.Region, BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: c.UsePathStyle,
            Credentials: credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
            RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
            ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
        }), nil
    })
if err != nil { return err }
if err := storage.Reload(ctx, snapshot.Secrets); err != nil { return err }
client, bucket, err := storage.Current()
```

With Python and [Boto3 configuration](https://docs.aws.amazon.com/boto3/latest/guide/configuration.html):

```python
import boto3
from botocore.config import Config
from faas_sdk.object_storage_runtime import ObjectStorageBinding

storage = ObjectStorageBinding("GREGALE_S3_ASSETS", lambda c: boto3.client(
    "s3", endpoint_url=c.endpoint, region_name=c.region,
    aws_access_key_id=c.access_key_id, aws_secret_access_key=c.secret_access_key,
    config=Config(s3={"addressing_style": c.addressing_style},
                  request_checksum_calculation=c.request_checksum_calculation,
                  response_checksum_validation=c.response_checksum_validation),
))
storage.reload(snapshot["secrets"])
client, bucket = storage.current()
```

The helpers select required-only checksum behavior, following the
[AWS checksum configuration contract](https://docs.aws.amazon.com/sdkref/latest/guide/feature-dataintegrity.html),
so ordinary reads do not implicitly request optional provider checksum APIs.
Go factories should honor `RequiredChecksumsOnly` as illustrated above. Explicit
checksum requests still depend on the selected provider's capabilities.

For in-process rotation, enable Gregale's secret reload protocol and use a
language-specific coordinator below. Each reads the single
`FAAS_SECRETS_SNAPSHOT_FILE` `{revision,secrets}` envelope, applies all registered
consumers and ACKs its revision using the captured process generation. The existing
[canonical reload coordinator](../sdk/node/src/secret-reload-runtime.ts)
provides snapshot reading, startup waiting, serialization and fenced ACKs:
register `storage.secretConsumer()` with that coordinator. A helper that merely reads startup environment variables cannot
adopt a later credential rotation.

### Node reload coordinator

The Node SDK's server-only `@gregale/sdk-node/secrets/runtime` subpath provides
`SecretReloadCoordinator`. Use it instead of reading startup environment values
when a workload opts into `secret_reload_signal: SIGHUP` and
`secret_reload_readiness: true` in its manifest:

```js
import { SecretReloadCoordinator } from '@gregale/sdk-node/secrets/runtime';
import { ObjectStorageBinding } from '@gregale/sdk-node/object-storage/runtime';
import { S3Client, GetObjectCommand } from '@aws-sdk/client-s3';

const shutdown = new AbortController();
const storage = new ObjectStorageBinding('GREGALE_S3_ASSETS', options => new S3Client({
  ...options, credentials: {...options.credentials}, // AWS annotates resolved credentials.
}));
const reload = new SecretReloadCoordinator({
  consumers: [storage.secretConsumer()],
  signal: shutdown.signal,
  onError: error => console.error(error.message), // Sanitized signal-reload errors.
});
process.once('SIGTERM', () => shutdown.abort());
process.once('SIGINT', () => shutdown.abort());
await reload.start(); // Start serving requests only after this succeeds.

async function readAsset(key) {
  const { client, bucket } = storage.current();
  return client.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
}
```

Register **every** secret consumer in the workload, including other storage
bindings and application-owned database or HTTP clients. A custom consumer
implements `apply(snapshot, signal)` and synchronous, idempotent `invalidate()`
that returns no value. An asynchronous invalidation cannot qualify adoption. Invalidation
must prevent new acquisition and fence late candidates; application code must
check cancellation before publishing resources. The storage adapter implements
these safeguards. The coordinator only attests to its registered callbacks.

`start()` installs its SIGHUP listener before writing the readiness marker, waits
up to 30 seconds for a versioned atomic snapshot, then applies consumers in order.
It sends an exact-revision `applied` ACK only after every callback succeeds. A
consumer failure disables all registered consumers and sends only a `failed`
ACK. Malformed/unavailable snapshots cannot be acknowledged. Missing atomic
snapshot, readiness or process-generation contracts fail closed; the older
template helper remains available for legacy guests.

Signal bursts coalesce with one follow-up refresh; manual `await reload.reload()`
uses the same serialized queue. A stale ACK triggers an authoritative reread;
transport failures and 503 responses retry the same closed metadata body, with
at most ten attempts per ACK and five seconds per request. Other HTTP failures
stop immediately. The coordinator captures this process's generation and paths
once and uses a dedicated metadata transport, unaffected by API fetch wrappers.
An unconfirmed ACK disables clients. A later SIGHUP or manual refresh can recover
from a reload failure; failed startup and `stop()` are terminal.

The startup deadline bounds only waiting for a versioned snapshot. Factories and
custom consumers must supply their own readiness deadlines and honor the shared
abort signal. Shutdown removes this coordinator's listener, aborts pending work
and disables new acquisition without waiting for arbitrary application code or
destroying acquired clients. It does not install termination handlers, close the
HTTP server or exit the process; the application owns graceful shutdown.

### Go reload coordinator

Use the Go SDK's `NewSecretReloadCoordinator` with the same manifest settings
and the binding configured above. The context passed to `Start` owns the
workload lifetime:

```go
ctx, shutdown := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer shutdown()
reload, err := faas.NewSecretReloadCoordinator(faas.SecretReloadOptions{
    Consumers: []faas.SecretConsumer{storage.SecretConsumer()},
    OnError: func(err error) { log.Print(err) }, // Sanitized signal-reload error.
})
if err != nil { return err }
defer reload.Stop()
if _, err := reload.Start(ctx); err != nil { return err }
// Start serving requests here. Reacquire storage.Current() for each request.
```

Imports for this lifecycle snippet are `context`, `log`, `os`, `os/signal` and
`syscall`, in addition to the SDK and your chosen S3 library. Register other
secret users with `faas.SecretConsumer{Apply: ..., Invalidate: ...}`. `Apply`
receives a context and immutable `SecretSnapshot`; `Secrets()` returns a
defensive copy. Invalidation must be synchronous, idempotent and quick, and
fence late candidates. Factories run outside the binding's state lock; a slow
factory cannot block `Current()` or `Invalidate()`.

Startup and signal/manual refreshes use one serialized worker, with the same
atomic-envelope, readiness, stale-ACK and retry contract as Node. An admitted
manual `Reload(ctx)` cancellation is terminal: it disables all consumers and
stops the coordinator so a late callback cannot qualify partial adoption.
Cancellation before admission does not cancel another active refresh. `Stop()`
removes only this coordinator's SIGHUP subscription, cancels waiting and disables
consumers without joining arbitrary factories. A factory that ignores context
may finish later; its candidate is fenced and subsequent factories cannot overlap
it. Supply appropriate factory/readiness timeouts. SIGHUP requires a Unix runtime.

### Synchronous Python reload coordinator

Use `faas_sdk.secret_reload_runtime` with the binding configured above:

```python
import signal
import threading
from faas_sdk.secret_reload_runtime import SecretReloadCoordinator

shutdown = threading.Event()
reload = SecretReloadCoordinator([storage.secret_consumer()], cancel=shutdown)

def terminate(*_args):
    shutdown.set()
    reload.stop()

signal.signal(signal.SIGTERM, terminate)
signal.signal(signal.SIGINT, terminate)
try:
    reload.start()  # Main thread, before serving requests.
    # Serve the app; reacquire storage.current() for each new request.
    shutdown.wait()
finally:
    reload.stop()  # Main thread restores the prior SIGHUP handler.
```

Python's signal API requires startup on the main thread. The coordinator refuses
an existing callable SIGHUP handler rather than replacing framework reload
behavior; explicitly arrange signal ownership before opting in. `stop()`
restores the prior handler only if this coordinator still owns it. A background
stop or external cancellation immediately disables clients; call `stop()` again
on the main thread to restore the handler. Failed startup and shutdown are
terminal. The application owns termination handlers, server shutdown and exit.

Register custom `SecretConsumer(apply, invalidate)` callbacks for every other
secret user. `apply(snapshot, cancel)` runs synchronously on one daemon worker;
the snapshot's secrets mapping is immutable and `cancel.check()` must succeed
before publishing resources. Async callbacks cannot qualify adoption. Signal
bursts coalesce with one pending follow-up and `reload.reload()` joins the same
serialized worker. Startup waits at most thirty seconds for an initial version;
callbacks and factories must bound their own work. The storage factory retains
the thread calling `storage.reload()` and runs outside the binding state lock.
Stopping does not join arbitrary callbacks or destroy previously acquired clients.

### Async Python clients and streaming

Use `faas_sdk.async_object_storage_runtime.AsyncObjectStorageBinding` with
`faas_sdk.async_secret_reload_runtime.AsyncSecretReloadCoordinator` for
loop-owned S3 libraries. The existing synchronous APIs remain unchanged. Core
imports no database or S3 driver. Factories receive the same immutable explicit
`ObjectStorageClientConfig`, including bucket, credentials, path addressing and
`when_required` checksum settings, plus an async cancellation token.

```python
from faas_sdk.async_object_storage_runtime import AsyncObjectStorageBinding
from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCoordinator

# App-supplied async callbacks. validate(client, bucket, cancel) must complete a
# readiness check and close any response body before returning None.
storage = AsyncObjectStorageBinding("FILES", make_client,
                                   validate=validate_client, retire=close_client)
coordinator = AsyncSecretReloadCoordinator([storage.secret_consumer()])
await coordinator.start()  # Main-thread owning loop, before serving.
try:
    async with storage.acquire() as lease:
        client, bucket = lease.current()
        # Await the complete operation and close its body inside this lease.
        await consume_and_close_download(client, bucket, key)
finally:
    # Stop request admission first and keep the event loop alive while draining.
    coordinator.stop()
    await coordinator.wait_stopped(timeout=5)
    await storage.wait_for_retirement(timeout=5)
```

Factory, validation and retirement hooks are required and return awaitables;
validators and retirement hooks return `None`, not boolean verdicts. Validation
is application-defined and must match the binding's allowed operations. It is
not automatic permission expansion or independent evidence of ongoing health.
Factories own partial allocations until returning a fresh, exclusively owned
client. All lifecycle work, including late-result cleanup, runs on the owning
event loop. Async invalidation is rejected: the consumer's synchronous gate must
disable new acquisitions immediately and schedule cleanup.

Keep leases through every upload/download body close, including forwarding a
body into an HTTP response, errors and disconnects. Acquiring a lease only inside
a lazy generator can miss cleanup if cancellation happens before its first
iteration. The runnable
[FastAPI/aiobotocore example](../examples/object-storage-rotation-python/README.md)
owns the response task and closes late responses before releasing their leases.
Its validator performs a bounded `HeadObject` on an app-provisioned readiness
object; its driver uses explicit credentials and does not retry requests.

Rotation, revocation and failed adoption disable new acquisitions while existing
leases retain their client. Cancellation before a queued reload's admission does
not disturb another successful attempt. Once admitted, cancellation fences late
publication and retains ownership of callbacks that suppress cancellation until
they return. Direct `reload(values, cancel)` accepts
`AsyncSecretReloadCancellation`; native task cancellation/token expiry preserve
`CancelledError`. Coordinator manual reload supports a total timeout and optional
`asyncio.Event`, including queue wait, consumers and ACK transport.

Storage and PostgreSQL share an internal async lifecycle and may register with
one coordinator. It reads one immutable snapshot and sends an applied ACK only
after every consumer validates; any failure invalidates every consumer. This
does not create atomic visibility or distributed transactions across resources.
Cancellation cannot retract an ACK already sent or undo completed operations.
Stop admission/reloads, invalidate, and await retirement for every binding before
closing the loop. Drain timeout/cancellation affects only the wait, never forces
closure of leased clients, and never cancels their cleanup. Retirement failures
are sticky and sanitized; cleanup is not retried. Bound all driver hooks.

The coordinator captures the four guest reload settings once. Missing/malformed
snapshots cannot be ACKed; a consumer failure disables all consumers and sends
only a `failed` ACK for a known valid revision. Native metadata HTTP bypasses
proxy settings and control-plane client wrappers, does not follow redirects or
parse bodies, and bounds each ACK request to five seconds. Transport failures
and 503 responses retry an identical body at most ten times; stale responses
reread the authoritative snapshot with at most five application attempts.
Unconfirmed ACKs disable clients; a later signal/manual reload can recover from
an ordinary refresh failure. ACKs attest only to registered callbacks and do not
prove provider readiness or atomic publication of every application client.

Bindings configured without a retirement hook use caller-owned clients: reacquire
`current()`/`Current()` for each new request and manage retired-client lifetime
yourself. For automatic cleanup, use request leases as described below. The
async Python helper always requires retirement and leases. Reloads are serialized.
A failed candidate or missing/revoked settings disables new acquisition and
returns a sanitized error. Do not send an `applied` ACK on failure. Constructors
alone do not verify credentials; a factory's readiness check and the workload
canary provide separate evidence. An ACK remains an application self-attestation.
Never log configuration or explicitly inspect credential fields.

### Request leases and automatic retirement

Opt into automatic cleanup by providing a retirement callback when constructing
the binding. Managed bindings require `acquire()`/`Acquire()`; raw
`current()`/`Current()` acquisition fails because those borrowers cannot be
counted. Each lease exposes its own client/bucket pair. Release is idempotent,
including shallow copies, and a released lease cannot acquire its pair again.

Keep the lease until the **entire upload or download finishes and its response
body is consumed or closed**. Receiving response headers does not finish a
streaming request. Forwarding a stream to another handler also transfers the
responsibility to keep and release its lease. Release in a finally/defer block
after closing bodies, including error and cancellation paths.

The following snippets use application functions `createClient`/`create_client`
and `retireClient`/`retire_client`, implemented for the chosen S3 library.
Factories must return fresh, exclusively owned client resources, including any
underlying transport or pool. A shared singleton or shared pool is unsuitable:
the SDK cannot discover every hidden resource alias. Obvious reuse of a still
tracked client is rejected without closing its outstanding leases.

```go
storage, err := faas.NewObjectStorageBinding("GREGALE_S3_ASSETS", createClient,
    faas.ObjectStorageBindingOptions[*StorageClient]{Retire: retireClient})
if err != nil { return err }
// Start the coordinator with storage.SecretConsumer() before serving requests.
lease, err := storage.Acquire()
if err != nil { return err }
defer lease.Release()
client, bucket, err := lease.Current()
if err != nil { return err }
body, err := openDownload(ctx, client, bucket, key)
if err != nil { return err }
defer body.Close() // Runs before lease.Release().
_, err = io.Copy(destination, body)
return err
```

```js
const storage = new ObjectStorageBinding('GREGALE_S3_ASSETS', createClient, {
  retire: retireClient,
  onRetireError: error => console.error(error.message), // Sanitized.
});
// Start the coordinator with storage.secretConsumer() before serving requests.
const lease = storage.acquire();
try {
  const { client, bucket } = lease.current();
  const stream = await openDownload(client, bucket, key);
  try {
    for await (const chunk of stream) await consumeChunk(chunk);
  } finally {
    stream.destroy();
  }
} finally {
  lease.release();
}
```

```python
from contextlib import closing

storage = ObjectStorageBinding("GREGALE_S3_ASSETS", create_client,
                              retire=retire_client)
# Start the coordinator with storage.secret_consumer() before serving requests.
with storage.acquire() as lease:
    client, bucket = lease.current()
    with closing(open_download(client, bucket, key)) as body:
        consume_body(body)
```

Rotation, revocation, failed adoption and coordinator stop disable new
acquisition immediately. The retired client is cleaned up exactly once after its
last lease releases. A valid factory result that arrives after cancellation or
invalidation is also cleaned up; Go additionally cleans up a nonnil result
returned alongside a factory error. A factory that throws or panics before
returning an owned client must clean up its own partial allocations.
Keeping a local client alive cannot override gateway credential revocation or
guarantee success for a subsequent network request.

Retirement callbacks run outside state mutation and never delay release,
invalidation or new acquisition. Go and synchronous Python invoke them on background
goroutines/threads; Node permits async callbacks. Different clients can retire
concurrently. Hooks must be thread-safe where applicable and bound their work;
Node hooks must avoid blocking the event loop. The synchronous Python helper's
hooks run on threads; the async helper's hooks stay on its owning event loop.
Do not call the binding's retirement wait from its own retirement hook.
The SDK cannot preempt arbitrary application cleanup. Callback failures are
sanitized, optionally reported through `OnRetireError`/`onRetireError`/
`on_retire_error`, and retained as a sticky failure by the drain operation.
Cleanup is never retried after an uncertain partial failure.

For graceful shutdown, first stop admitting application requests and reloads,
then stop the coordinator (or invalidate a standalone binding), finish/close
outstanding bodies and release their leases. Finally wait with a fresh shutdown
budget:

```go
reload.Stop()
drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
err := storage.WaitForRetirement(drainCtx)
```

```js
reload.stop();
await storage.waitForRetirement(AbortSignal.timeout(5000));
```

```python
reload.stop()  # Main thread restores this coordinator's SIGHUP handler.
storage.wait_for_retirement(timeout=5)
```

The wait includes admitted factories, retired leases and scheduled cleanup. It
ignores a still-current client, so invalidate/stop before draining shutdown.
Canceled/timed-out waits end only the wait; they never destroy outstanding
leases or cancel cleanup. Go returns an error; Node/Python raise a sanitized
binding error. A forgotten lease or unbounded hook can outlive the budget; the
application decides its exit policy. Python cleanup threads are daemon threads,
so process exit without a successful drain can interrupt cleanup.

Bindings without a retirement callback keep the existing caller-owned
`Current()` contract. They can use leases, but the SDK does not close their
clients. Lease logging hides ordinary client representations; explicitly
inspecting a leased client can still expose its credentials.

## Workload canaries and promotion

The default canary remains read-only:

```sh
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS --deployment v12
```

It performs one bounded bucket listing. To explicitly test a writable binding:

```sh
gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS \
  --object-storage-write --deployment v12 --json
gregale bindings check my-api --deployment v12 --require-application-ack
```

The API requires storage management authorization, a current managed binding in
the selected deployment's scope, an exact permission match and a ready bucket
without known Object Lock enabled. It pins the binding revision and mode in
durable task metadata. Update the API/migrations and guest-init before using the
new flag; older guests cannot produce passing write evidence.

| Binding | Default probe | Explicit write probe |
| --- | --- | --- |
| `read` | List at most one object | Rejected before starting a task |
| `write` | Requires explicit opt-in | Conditional tiny PUT, DELETE acknowledgement; no GET or listing |
| `read_write` | List at most one object | Conditional tiny PUT, byte-for-byte GET verification, DELETE, GET confirming absence |

These are ordinary metered operations and can trigger matching object notifications.
Each write probe chooses its own cryptographically random key below
`.gregale/binding-probes/`, uploads fewer than 64 bytes with `If-None-Match: *`,
and deletes the exact returned version when available. It never accepts an
arbitrary customer key. The operation budget is five seconds, with a separate
five-second cleanup budget that survives cancellation. There are no SDK retries
or redirects; replies are bounded. Cleanup runs after a failed download.
Reports contain operation statuses, not object names, endpoint URLs, credentials
or provider diagnostics.

Write-only permission cannot independently verify absence: its cleanup stage
records the DELETE acknowledgement. Lost PUT responses have an unknown version
identity and never produce passing cleanup evidence. Process termination,
provider outages or a concurrent retention-policy change can leave a tiny probe
behind; inspect the reserved prefix after a failed test. The canary never bypasses
retention and does not claim crash-proof cleanup. `--all` does not opt into writes;
write-only bindings appear as partial coverage requiring explicit opt-in.

The admitted mode must match the returned report. Missing download/cleanup
stages, legacy reports for write tasks, nonzero exits and truncated output never
qualify. The existing binding check and promotion gates consume the normalized
operation stages; a failed cleanup makes the latest evidence fail. Existing
read-only evidence retains its original meaning. Release policies can require
specific operations per prefix; promotion consumes that evidence and never
executes writes. Probes run in task guests; they do
not prove resident application adoption, which remains a separate ACK gate.

## Required storage operations before traffic

Declare storage requirements per managed environment prefix in one deployment
scope. Setting a policy replaces its complete requirements map; include every
prefix you want to retain. Policy updates use compare-and-set revisions.

```sh
gregale bindings release-policy set my-api --scope production \
  --mode enforce --expected-revision 0 --max-age 10m \
  --object-storage-require GREGALE_S3_ASSETS=read_write \
  --require-application-ack
```

`read` accepts the legacy bucket-listing canary. It does not prove object byte
reads. `write` requires successful conditional upload and cleanup; `read_write`
also requires byte-for-byte download. A complete read/write canary can satisfy
any of these requirements. Write-only cleanup acknowledges deletion; it cannot
independently read back absence. Existing cleanup and Object Lock limits still
apply. Neither setting policy nor increasing traffic schedules a canary.

Collect write evidence explicitly against the exact live candidate, then check
it against the stored scope policy:

```sh
gregale bindings verify my-api --deployment v12 \
  --object-storage GREGALE_S3_ASSETS --object-storage-write
gregale bindings check my-api --deployment v12 --scope production \
  --release-policy --wait --json
```

`--release-policy` resolves and pins the live deployment and its scope, verifies
the app identity, and reads the stored policy. An enforced policy supplies storage
operation requirements and application ACK requirements, disallows unsupported
binding waivers, and limits verification age. Local flags can make these checks
stricter: the shorter verification age applies (the CLI default is 10 minutes),
ACK requirements are combined, and different storage modes for the same prefix
combine into `read_write`. Additional local prefixes remain required. A policy
with mode `off` applies only the local flags and is reported as disabled.

The CLI confirms the same policy after every inventory evaluation, including
blocked iterations with `--wait`. A changed revision or policy content, failed
confirmation, invalid app/scope, or an unavailable policy API blocks the check;
an older server cannot silently fall back to local flags. JSON includes the
`release_policy` snapshot and `release_policy_confirmed`; the normal report fields
show the effective requirements. A blocked check can still have a confirmed
policy. `--timeout` bounds the entire policy-aware check, even without `--wait`.
Waiting observes already scheduled probes and ACKs; it never schedules writes.

Without `--release-policy`, `bindings check` retains its local flag behavior.
Use `bindings release-policy get` to inspect the stored policy separately.

To collect missing storage evidence and qualify the app's other bindings against
an enforced policy, preview the plan and then explicitly consent to its writes:

```sh
gregale bindings verify my-api --deployment v12 --release-policy --dry-run --json
gregale bindings verify my-api --deployment v12 --release-policy \
  --object-storage-write --wait-timeout 5m --json
```

This mode requires an enforced policy, a complete inventory, a valid
configuration timestamp and one active, settled binding for each selected
prefix. Storage requirements may be empty; default storage probes check listing
access, while write-only bindings require upload/cleanup. The plan also covers
services, runtime PostgreSQL, configured outbound and required queue readiness.
It selects and reconfirms the exact deployment and scope even when
`--deployment` is omitted. `--dry-run` performs only metadata reads: `status` is
`planned`, `passed` is false, and `write_consent_required` identifies whether the
whole plan needs opt-in. Preview success does not test connectivity or readiness.

Current evidence is reused when its deployment, scope, age, generation and
operation stages satisfy the stored policy. Reused write evidence schedules no
new write and needs no write consent. Already-pending probes are observed without
replacements. Without consent to every scheduled write, the entire batch stops
before creating any task. `read` runs a bounded bucket listing. `write` runs upload and cleanup;
if the binding grants `read_write`, the canary also downloads and compares bytes,
and the preview shows that extra operation. Consent does not broaden a binding's
permission. Existing retention/Object Lock admission and cleanup rules still
apply. `verify --all` and single-prefix verification keep their existing behavior.

The command checks the original policy and binding configuration before each
admission and after each successful probe, and stops on the first failure or
observed change. It finishes with a policy-aware preflight against that same
snapshot, waiting for supported pending observations under the same timeout.
JSON contains typed plan actions, `plan_blockers`, `observed_check`, `probes`,
`release_policy`, `snapshot_confirmed` and `preflight`, including remaining
application ACK or other binding blockers.
A successful canary batch can therefore still return exit status 1. Canaries
never supply application ACKs. Required queue readiness sends no messages and
retains partial connectivity coverage. See
[ADR-1347](adr/1347-unified-binding-policy-verification.md).

`--wait-timeout` bounds the entire workflow; it does not reset for every prefix.
An ordinary timeout keeps the existing task running and reports its ID; inspect
it before retrying. An interrupt requests cancellation and returns exit status
130. Completed probe results remain in the report when a later step fails.

These confirmations observe configuration; they do not lock it. Changes can race
task admission or execution. The existing server-side task revision metadata
prevents stale evidence from qualifying, and promotion still makes its atomic
policy/catalog decision at the traffic write. The command performs no traffic,
policy or binding updates, and does not certify resident application adoption.

Confirmation describes the last read and does not authorize a later traffic
change. Server-side promotion,
traffic redistribution, and existing checked rollout/recovery paths consume the
stored requirements for each deployment gaining traffic. A missing/ambiguous
prefix, insufficient permission, incomplete or failed stages, stale binding or
credential revision, wrong deployment/scope, expired evidence, pending rotation,
or missing required application ACK blocks the release. Waiving unsupported
bindings cannot waive storage operations. Application ACKs remain independent
version-bound self-attestations, not proof of readiness or credential use.

Storage policy updates use a dedicated route that old servers reject. A legacy
policy update cannot silently remove existing requirements. Use the repeated
`--object-storage-require` flags to retain/replace them, or explicitly clear them:

```sh
gregale bindings release-policy set my-api --scope production \
  --mode enforce --expected-revision 1 --clear-object-storage-requirements
```

The locked traffic write compares the evaluated requirements and policy revision
again. Config, probe completion, runtime observations, rotation, or policy changes
invalidate the promotion fence; collect current evidence and retry. Rolling back
the requirements migration is refused while any policy has a nonempty map; the
guard locks policy writes so concurrent updates cannot evade it. Clear requirements
explicitly before a reviewed rollback.

## Developer workspace buckets

`gregale dev --bucket assets` explicitly provisions a private bucket and managed
binding for the developer workspace. Repeat `--bucket` for several buckets. The
default permission is `read_write`; the default prefix for `assets` is
`GREGALE_S3_ASSETS`. Use `gregale dev setup --bucket assets` to inspect the plan,
or add `--start` to start the developer environment. `dev info` reports active
automatic bindings without renewing the lease. API keys need `deploy:write` and
`storage:manage` to provision; inspection needs `apps:read` and `storage:manage`
to include storage binding metadata.

For custom placement or permissions, put explicit opt-in settings in the selected
source root's `gregale.yaml`:

```yaml
dev:
  buckets:
    - name: assets
      permission: read_write
      prefix: GREGALE_S3_ASSETS
      region: us-east-1
```

An explicit `--bucket` list overrides `dev.buckets`. With `--all`, each app uses
its own manifest unless explicit bucket flags are forwarded to every child.
The normal top-level `buckets` section continues to attach existing buckets;
only `dev.buckets` requests automatic creation in developer sessions.

Source reloads and omitted declarations preserve bucket data, placement and
credentials. Changing a binding's permission conflicts; a revoked automatic
binding stays revoked across retries/restarts. Use binding rotation for credential
rotation, or explicitly stop/restart the session for a fresh grant. Changed
declarations can leave earlier resources in the workspace and its quota.

`gregale dev --stop` and lease expiry revoke managed grants and reclaim all buckets
owned by that developer app, including manually created ones. Cleanup failures,
pending writes, versions or Object Lock can defer deletion; resources remain
durable and charged while cleanup is pending. Storage must be enabled and the
normal accounting/provisioning gates must pass. Remote development consumes real
Gregale storage; it is separate from the starter's disposable local test fixtures.

## Node compute starter

`gregale init --template object-storage-node --path files-app` materializes a private
managed-storage application with authenticated binary PUT/GET, atomic storage
and auth secret consumers, request leases, storage-aware local readiness and
bounded shutdown and application restore recovery. Reserve the app with `deploy --create-only`, attach assets
using `gregale add bucket ... --permission read_write --prefix GREGALE_S3_ASSETS`,
and supply STORAGE_APP_TOKEN through an external 0600 secrets file. Follow the
[starter README](../cmd/gregale/templates/object-storage-node/README.md) for setup
and the application safeguards. Provider credentials stay behind the gateway.

The Dockerfile requires application ACK readiness and opts into SIGHUP. Read/list
candidate validation is charged to gateway request budgets; it does not certify
write permission or continuous provider health. Every response body retains its
lease until completion or cancellation. The five-second shutdown budget aborts
unfinished transfers after draining expires.

The manifest selects `/readyz` and a two-second after_restore hook authenticated
by actual loopback peer plus the restore header. The hook closes admission,
cancels captured transfers and replaces the transport even at an unchanged
credential revision. It validates fresh storage/auth adoption and drains captured
body/spool leases within a maximum 1.5-second application budget before reopening
readiness. Failed or cancelled recovery stays closed until a successful hook;
the platform's existing cold-boot fallback applies. A normal SIGHUP cannot clear
this failed-recovery gate. Cancellation closes captured HTTP connections while
late driver results stay owned until actual cleanup; uncertain PUTs are never replayed.

`bash scripts/test-storage-node-starter.sh` checks the CLI-embedded artifact, its
lockfile, app tests and actual Node/SigV4/gateway/provider-adapter memory-store
journey, including same-revision transport replacement, blocked validation/queued
SIGHUP cancellation and recovery. Its disposable local provider is not GCS/R2 or a native microVM
qualification. The bundled helper drift check runs with Node SDK tests; regenerate
using `npm run build && npm run starter:sync` in sdk/node after helper changes.


The [native storage acceptance harness](ops/object-storage-native-acceptance.md)
deploys this exact Node starter and requires real snapshot/restore, public running
and parked rotation, failed-hook cold fallback and independent cleanup. Its strict
report distinguishes native execution from nested diagnostics. Native KVM and
live-provider qualification remain pending until their respective runs pass.

## Python compute starter

`gregale init --template object-storage-python --path files-app` materializes a
self-contained FastAPI/aiobotocore private file service. It bundles the canonical
async SDK runtime, dependency pins, an unprivileged Python 3.12 image, authenticated
PUT/GET, bounded disk spooling, streaming leases and an application restore hook.
Reserve the app with `deploy --create-only`, attach a managed `read_write` bucket
with prefix `GREGALE_S3_ASSETS`, and supply `STORAGE_APP_TOKEN` through an external
0600 secrets file. Follow the [starter README](../cmd/gregale/templates/object-storage-python/README.md).

Storage validation and HTTP authentication adopt one atomic snapshot before the
applied ACK. ListObjectsV2(MaxKeys=1) checks read/list access without requiring a
pre-created object; use operation canaries to qualify write permission. `/healthz`
is liveness; the manifest's `/readyz` checks local adoption and recovery readiness.
Neither endpoint proves continuous provider availability.

HTTPS verifies the certificate chain and hostname. For a private CA, set the
application environment's `AWS_CA_BUNDLE` to a readable PEM bundle deployed inside
the image, such as `/app/runtime/company-ca.pem`; `runtime/` is already copied by
the Dockerfile. This replaces the driver's default public CA trust for S3.
Invalid trust files, untrusted/expired certificates and hostname mismatches fail
adoption without a successful ACK. The path is captured at startup; new transports
load its contents again on reload and restore. See the starter README for deployment
and recovery details.

The authenticated two-second after_restore hook closes admission, cancels captured
transfers and rebuilds transports even at an unchanged revision. It validates the
fresh client and drains captured body/spool leases within a maximum 1.5-second
application budget before reopening readiness. Failed recovery stays closed until
a successful hook; the existing platform cold-boot fallback applies. Graceful
shutdown allows five seconds to drain. Transfers and uncertain PUTs are never replayed.

`bash scripts/ci/test_python_storage_starter.sh` installs standalone requirements,
checks canonical drift and tests the exact CLI-embedded artifact, including actual
aiobotocore SigV4 HTTP/HTTPS requests and the handler subprocess's
SIGHUP/restore/shutdown. OpenSSL is required only to generate disposable test
certificates. HTTPS rejection and corrected-bundle recovery are part of this gate.
The owned local S3 fixture does not qualify a live provider or native microVM
snapshot recovery. Regenerate bundled helpers with
`python sdk/python/scripts/sync_binding_starters.py` after SDK runtime changes.

## Upload-triggered compute

A bucket dependency can also reconcile ObjectCreated triggers for the app being
deployed. The bucket must belong to that app. The server derives its destination;
no provider ARN or credentials belong in this declaration.

```yaml
buckets:
  - bucket: assets
    app: image-worker
    scope: production
    prefix: GREGALE_S3_ASSETS
    triggers:
      - id: incoming-images
        events: ["s3:ObjectCreated:*"]
        prefix: uploads/
        suffix: .png

dev:
  buckets:
    - name: assets
      triggers:
        - id: incoming-images
          prefix: uploads/
          suffix: .png
```

Omit `triggers` to preserve existing managed configuration. Set `triggers: []` to
clear only the managed rules. Manual notifications are retained; incompatible
filter overlap fails instead of replacing them. Declare triggers once per
resolved bucket, even when it has multiple binding prefixes. Keep input and
output prefixes separate to avoid invoking the same worker on its own output.

`gregale deploy` and manifest-backed `gregale dev` read the notification revision
and reconcile through the control API. A concurrent edit returns a conflict;
inspect the current configuration before retrying. Notification responses include
`managed_rule_ids` for inspection. Manual `notifications put`, `notifications
clear`, or signed S3 notification replacement takes ownership of the complete
configuration, including unchanged rules. A subsequent deploy cannot adopt those
IDs automatically.

Developer triggers target the isolated developer app and bucket, independent of
production resources. A full bucket list omitted on a later workspace refresh
also retains prior triggers. Explicitly empty declarations clear new routing;
already accepted uploads retain their captured destination. Delivery uses the
existing async invocation/retry path, requires an eligible plan and may execute
more than once. The worker receives an HTTP POST to `/` with an S3 `Records`
payload. Use the notification's object identity and version in an
idempotent worker. This feature adds no live-provider dependency to configuration.

Uploads must pass through Gregale's managed S3 gateway or a tracked upload route
to produce proven mutation events. Writes made directly to the provider's bucket
do not create Gregale compute triggers.
