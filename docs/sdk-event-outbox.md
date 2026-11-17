# Typed transactional events (preview)

Node and Python can publish an `EventContract<T>` payload or stage it beside a
business write in PostgreSQL. Staging validates the generated payload, pins the
expected native app ID, and retains one immutable producer key, request body,
digest and retry deadline. The worker publishes after the claim transaction's
commit is acknowledged. This connects business changes to
[event-triggered pipelines](sdk-event-pipelines.md).

## Install and migrate

Use the preview entry: `@gregale/sdk-node/preview` or `faas_sdk.preview`. Install
`pg` for Node or `psycopg` for Python in the application. No driver is imported by
the SDK. Generate payload types with `gregale-event-types` or
`python -m faas_sdk.preview.event_types` from your event contract JSON.

Apply `eventOutboxSchema` / `EVENT_OUTBOX_SCHEMA` explicitly in an application
migration transaction, using the same primary database as your business tables.
The exported DDL includes the existing Go outbox tables and a payload-constraint
upgrade that permits erasure after publication. Existing Go applications can
apply `preview.EventOutboxPayloadSchema` separately. Installing only the old Go
DDL prevents successful settlement because its old check rejects a NULL payload.
Never put migrations on request or worker paths. Restrict SQL access: pending,
unconfirmed and blocked rows contain event data. Helpers never prune identities.

The `pg.Pool` structural interface is directly supported. Python's
`PsycopgEventOutboxPool` takes an async factory returning a **fresh, idle,
SDK-owned connection**; it sets autocommit and UTF-8, uses explicit transactions,
and closes the connection after each operation. Custom adapters implement
`EventOutboxPool` and `EventOutboxConnection`; their SDK `query` statements use
`$n` parameters, while business `execute` retains the driver's native style.

## Stage with business writes

```ts
import {Pool} from 'pg';
import {createEventPublisher, withEventOutboxTransaction} from '@gregale/sdk-node/preview';
import {contract} from './import-requested.js'; // generated EventContract<Input>

const pool = new Pool({connectionString: process.env.DATABASE_URL});
const scope = {base_url: api, account_id: accountId, app_id: appId, app_slug: 'imports'};
const publisher = createEventPublisher(contract, {scope});
await withEventOutboxTransaction(pool, async tx => {
  await tx.query('INSERT INTO imports(id,owner_id) VALUES ($1,$2)', [id, ownerId]);
  await publisher.stage(tx, {import_id: id}, {key: 'import:' + id, environment: 'staging'});
}, {timeoutMs: 5000, signal});
```

```python
import psycopg
from faas_sdk.preview import (
    EventPublisher, EventPublicationOptions, PsycopgEventOutboxPool,
    with_event_outbox_transaction,
)
from import_requested import contract  # generated EventContract[Input]

async def connect():
    return await psycopg.AsyncConnection.connect(database_url, autocommit=True)

pool = PsycopgEventOutboxPool(connect)
publisher = EventPublisher(contract, scope=scope)
async def save(tx):
    await tx.execute('INSERT INTO imports(id,owner_id) VALUES (%s,%s)', [id, owner_id])
    return await publisher.stage(tx, {'import_id': id},
        EventPublicationOptions(key='import:' + id, environment='staging'))
await with_event_outbox_transaction(pool, save, timeout=5)
```

Authenticate and authorize the owner before this callback. The SQL store is
application-owned; its helpers do not establish end-user authorization. The
native publication endpoint checks the configured token and expected app ID.
Grant the worker only the native and SQL permissions it needs.

The helper owns BEGIN, COMMIT and ROLLBACK. Do not send transaction-control SQL,
start nested transactions, launch unawaited SQL work or perform remote effects
inside the callback. A staging or SQL error poisons the transaction even if your
callback catches it. The SDK never retries the business callback. A transaction
handle cannot be reused after completion or cancellation.

A lost COMMIT reply returns `commit_unknown`; neither a rollback attempt nor an
error proves that the commit failed. Inspect the business operation and outbox by
the original key on the primary before deciding what to do. Keep business writes
idempotent if your application will explicitly retry an uncertain transaction.
Repeated staging compares exact original request bytes and slug and preserves
the original retry deadline; a changed payload/environment conflicts. Serializers
can differ in float or property formatting: reuse the same producer implementation
for repeated staging. Node/Python workers send the saved bytes without reserialization.

Producer keys contain 1–256 printable ASCII bytes without spaces. Keys are unique
per account/app across **all environments**; include an environment discriminator
in your key if one logical operation runs in several environments. The environment
is part of the immutable request, defaults to production, and is resolved by the
native API. Upgrade **all Go workers** to the environment-aware request decoder
before staging environment-specific rows; older workers can discard this field.
Current Go workers reject unknown retained fields instead of silently dropping them.

## Run bounded recovery

```ts
import {createEventOutbox} from '@gregale/sdk-node/preview';
const outbox = createEventOutbox(pool, {scope, token: workerToken});
const results = await outbox.recover({
  timeoutMs: 20000, maxEvents: 100, concurrency: 4, signal,
  onProgress: async result => recordSafeMetadata(result),
});
const status = await outbox.status('import:' + id, {timeoutMs: 1000, signal});
```

```python
from faas_sdk.preview import EventOutbox, EventOutboxRecoveryOptions
outbox = EventOutbox(pool, scope=scope, token=worker_token)
results = await outbox.recover(EventOutboxRecoveryOptions(timeout=20),
    max_events=100, concurrency=4, on_progress=record_safe_metadata)
status = await outbox.status('import:' + id, timeout=1)
# Cancel the asyncio task to cancel its database and HTTP work.
```

`recoverNext` / `recover_next` handles at most one due row. Batch recovery is a
bounded pass (1–10,000 iterations, 1–32 workers), with one total deadline.
Applications schedule additional passes. A worker stops on empty, paused,
rate-limited or lost-claim results. Progress exceptions stop the pass with
`progress_failed`; already completed publications remain completed.

Claims use database time, `SKIP LOCKED`, random lease tokens and token-conditional
settlement. HTTP runs outside SQL transactions, once per claim. Lost commit
acknowledgment prevents dispatch. Requests are bounded by the remaining operation,
lease and immutable retry window. An expired or replaced token cannot settle.
HTTP 4xx other than 408/429 blocks the row; lost replies, malformed receipts and
other failures remain unconfirmed. A response confirms publication only with a
matching account, app, source, key-derived event ID and acceptance timestamp.
A confirmed row erases its payload, retaining its immutable identity and digest.
Published means native acceptance; inspect the consumer/pipeline receipt for its
completion separately. Status/progress exclude payloads, digests and lease tokens.

Defaults: 30-second claim, 10-second request, 5-second fixed uncertainty backoff,
24-hour retry window. Configure whole millisecond durations. Optional staged
`retryPolicy` / `retry_policy` persists capped exponential full jitter and a
lifetime attempt limit (zero means unlimited); a persisted Go policy takes
precedence over worker defaults. Policy mode also honors Retry-After for 429/503.
All workers honor the existing Go publishing pause and shared rate bucket, using
the same advisory lock and preserving fractional database refill timestamps.
Control changes remain available through the existing Go operator APIs.

Set `receiptRetentionMs` / `receipt_retention` to the native plan's settled-receipt
retention. It defaults to 24 hours; staging caps the retry window at the smaller
of this retention and seven days. Recovery refuses a row whose original window
exceeds the declared retention. Never retry publication after native deduplication
retention expires. Changing the producer key creates a different event.

For a lost publication reply, `reconcile(key, options)` asks the native read-only
verification endpoint about the **saved body**. It never republishes, resumes a
blocked row or extends the retry deadline. A verified matching receipt within the
original creation/retry interval can settle the exact inspected row using a
microsecond-precise compare-and-set. Active claims report `busy`. Unavailable,
conflicting, future or unverified evidence leaves the row unchanged. An optional
current app slug supports app renames while the expected app ID remains pinned.

For workflows without a business transaction, use `publisher.publish(input,
{key, timeoutMs, ...})` / `await publisher.publish(input, options, timeout=...)`.
This is one-shot and has no local durable receipt. Preserve the key and exact
input/options yourself if the reply is uncertain.

## Verification and example

Shared Go/Node/Python vectors cover request bytes, environment forwarding,
request/policy digests, jitter and nanosecond comparisons. Unit tests cover
rollback, poisoned transactions, lost commits, publication outcomes, deadlines,
cancellation, limits and read-only reconciliation. The opt-in integration test
uses real isolated PostgreSQL with competing Node/Python workers, paused/rate
controls, stale/live leases, uncertain publication, retention and app/account
isolation. Native HTTP is a local protocol fixture.

```sh
python3 scripts/gen-sdk-event-outbox.py --check
npm --prefix sdk/node run build
GREGALE_EVENT_OUTBOX_POSTGRES=1 node --test sdk/node/test/event-outbox-postgres.test.mjs
GREGALE_CSV_EVENT_PIPELINE=1 node --test examples/sdk-csv-jobs/pipeline-local.test.mjs
```

See the [CSV producer and reviewed pipeline](../examples/sdk-csv-jobs/README.md#event-triggered-reviewed-imports)
for the full business-write → outbox → authenticated event → durable pipeline →
private approval → verified report flow. These local tests do not qualify a
deployed native broker or scheduler.
