# Review the __OP__ v2 transaction starter

This editable order/inventory example needs application review. It creates
`api.__OP___v2` and `api.read___OP___receipt_v2` with separate `__OP___v2_*`
tables. It can coexist with the v1 package; it does not upgrade existing orders
or receipts. Copy `migration.sql` into a new numbered project migration after
review. Apply owner RPC permission setup, refresh the serving schema, and generate
paired types/validators:

```sh
gregale data-api types YOUR_DATA_API --validators --output database.types.ts
# Or use gregale data-api dev --validators --once in your project.
```

Place `client.ts` beside those generated types and install the paired
`@gregale/data` SDK in your application. SQL argument/result types come from the
schema. Only reviewed inventory/order code belongs inside the function. Database
atomicity covers stock, orders, items and the success receipt in one transaction;
external email/webhook effects require an outbox. Sequence values and external
side effects are outside rollback.

## Capture, execute and recover

Configure verified `tenant_id` and `sub` claims in your identity provider. The
client's subject/tenant describe the expected identity; SQL derives ownership
from verified claims, never RPC arguments. The example shares business rows
within a tenant, but only the original actor can recover a receipt. Add your own
membership, revocation and administration rules before production.

```ts
import { dataRequest } from '@gregale/data'
import { transactionClient, transactionMatches, reconcileMutation } from './client.js'

const transaction = transactionClient({
  url: dataAPIURL, accessToken: () => session.token,
  subject: session.subject, tenant: session.tenant,
  // expectedSchemaFingerprint: reviewedFingerprint,
})
const pending = transaction.prepare({ skus: ['a_widget'], quantities: [2] })
// Save the entire pending object before the first request if reload recovery
// is required. Preparation clones/freezes it but does not persist anything.
const matches = transactionMatches(pending)
const result = await dataRequest(transaction.execute(pending), { timeoutMs: 5000 })
const acknowledgement = reconcileMutation(result, matches)

// After an uncertain response, explicitly read the original success receipt.
const receipt = await dataRequest(transaction.lookup(pending), { timeoutMs: 5000 })
const recovery = reconcileMutation(receipt, matches)
if (recovery.outcome === 'confirmed') {
  console.log(recovery.row.order_id, recovery.row.total_cents)
}
```

Keep the original API URL, schema fingerprint (if pinned), tenant, subject, key,
contract, SKUs and quantities throughout recovery. The client rejects a pending
operation whose context differs. To restore captured input use
`transaction.prepare({skus:saved.skus,quantities:saved.quantities},saved.key)`
only after verifying the saved context matches the current client. Renewing a
token can preserve identity. Never automatically replace an uncertain key,
switch identity, or rebuild an operation from current prices or inventory.

The parser rejects invalid/duplicate SKUs, misaligned quantities and unknown
arguments before networking. Preparation creates a UUIDv7 only after validation.
The optional key restores an original operation without extending its deadline.
SKU/quantity pairs are canonicalized in ASCII SKU order; reordering identical
pairs keeps the same payload. Changed quantities/SKUs on a successful key return
HTTP 409 / `PT409` / `idempotency_payload_conflict`.

Every successful call returns one complete original snapshot: key, tenant,
actor, order ID, total, timestamp and ordered lines including server-side prices.
The matcher validates every line and recomputes the total before narrowing JSON
to a typed receipt. Confirmation describes the original transaction, including
after price changes or order deletion. It does not describe current stock or
current order state. Missing receipts never prove rollback: work may still be
in flight, or an expired receipt may have been purged.

## Durable pending-operation recovery

The optional `persistentTransactionClient` saves the complete original operation
before its first request. Select a dedicated IndexedDB database and a stable
application slot that you can find after reload (for example `checkout`). Install
the paired SDK and regenerate the v2 client to use this opt-in wrapper:

```ts
import { createIndexedDBMutationStore } from '@gregale/data'
import { persistentTransactionClient } from './client.js'

const store = createIndexedDBMutationStore({ databaseName: 'my-app-data-mutations' })
const transaction = persistentTransactionClient({
  url: dataAPIURL, subject: session.subject, tenant: session.tenant,
  accessToken: () => session.token, // Keep this provider scoped to this identity.
}, store)
const prepared = await transaction.prepare('checkout', { skus: ['a_widget'], quantities: [2] })
const sent = await transaction.execute(prepared, { timeoutMs: 5000 })
const checked = await transaction.reconcile(sent.entry, sent.response)
// A confirmed receipt can coexist with a failed local marker write.
if (checked.result.outcome === 'confirmed') showOriginalOrder(checked.result.row)
```

After reload, construct the same client/store and explicitly load the slot:

```ts
const pending = await transaction.load('checkout') // Sends no request.
if (pending) {
  const recovered = await transaction.recover(pending, { timeoutMs: 5000 })
  if (recovered.result.outcome === 'confirmed' && recovered.persistence === 'saved') {
    // Explicitly clear only after the application accepts this historical result.
    await transaction.removeConfirmed(recovered.entry)
  }
}
```

States are `prepared`, `uncertain` and `confirmed`. The first explicit execute
atomically persists `uncertain` before networking; one tab can claim a prepared
revision. Loading and recovery never replay writes. Failed storage writes or
acknowledgements prevent dispatch; uncertain responses keep the original key and
payload. A callback exception also preserves them, so load again for recovery.
Recreating a client requires loading a new handle rather than reusing an old one.

The original API URL, fingerprint (if pinned), contract, tenant, subject, key and
canonical input must match on restore. Changed context, corrupted/unknown records
and lossy parsers are refused without clearing storage. Missing or expired
receipts remain unresolved, including after the replay window closes. There is
no automatic eviction or new-key retry. `removeConfirmed` removes local state
only, and cannot remove an uncertain operation or a newer revision from another
tab. Use a new slot only for an explicitly new business operation.

Reconciliation returns `result`, `entry` and `persistence` (`saved`, `unchanged`,
`conflict` or `storage_failed`). Preserve a validated original receipt even when
saving its local marker fails; reload the slot to inspect current local state.
A confirmed marker does not describe current stock/order state or prove that a
later operation succeeded.

IndexedDB defaults to 100 records and 8 MiB of stored values. Each record is at
most 1 MiB, slot/database names 256 UTF-8 bytes, and JSON depth 64. Lower count/
byte budgets can be selected. Quota/unavailable storage fails closed before
sending and does not use volatile fallback. Payloads are intentionally stored;
credentials, headers and token providers are excluded from the generated
projection. Clearing/evicting origin storage or losing the device removes the
local copy. Close the store when finished. For other runtimes, the SDK's
`createMutationJournal` accepts an application-owned durable atomic CAS adapter;
a localStorage get/set pair cannot coordinate this protocol across tabs.

## Fixed v2 replay contract

UUIDv7 keys expire 24 hours after their embedded timestamp; database time is
authoritative, with at most five minutes of future clock tolerance. Invalid
version/variant returns `PT400` / `idempotency_key_invalid`; excessive future
skew returns `PT400` / `idempotency_key_in_future`. Writers refuse expired keys
before/after operation locks, after inventory locks and work, and before return.
Purging never makes an expired key executable again. Do not extend this contract
in place; use a new version/namespace if the window changes.

The read-only lookup compares the complete canonical request, does no writes and
takes no operation/row locks. It can return retained expired receipts. An absent
live receipt returns zero rows; an absent expired one returns HTTP 410 / `PT410` /
`idempotency_key_expired`, classified as unresolved `replay_expired` by the SDK.
Conflicts, expiry and missing receipts do not confirm an earlier uncertain call.
No client helper polls, retries, rotates keys or persists pending data for you.

The guarantee assumes stable database clocks, intact transaction history and
trusted owner-controlled schema/functions. A history-losing restore or owner
bypassing the guards is outside the contract. Normal API deadlines, response
limits and database errors still apply. Sorted stock locks prevent overselling
among these operations; other customer transactions/triggers may cause normal
lock errors. Failures roll back everything and reserve no negative receipt.

## Owner receipt cleanup

Install the cleanup tool dependencies with `npm install --ignore-scripts` in this
package and retain its resulting lockfile. Use a secret-managed
`MIGRATION_DATABASE_URL` session that assumes the stable receipt-table owner.
Do not expose this binding to application clients. Preview with a past UTC
cutoff, then explicitly add `--apply` after reviewing the result:

```sh
npm run receipts:cleanup -- --before 2026-10-17T00:00:00.000Z --batch-size 100 --max-batches 1
```

The cutoff must have UTC milliseconds and cannot be in the future. Selection
requires expiry at/before both the cutoff and database time. Cleanup skips row
locks and active tenant/actor/key advisory locks. It commits bounded batches and
reports eligible/deleted/remaining counts; earlier committed batches remain if
a later batch fails. The API role and FORCE RLS receipt tables are refused.
Only success snapshots are purged; inventory, orders and items remain intact.
Live receipts cannot be deleted and snapshots cannot be updated through normal
owner DML. Owners retain authority to change their own schema or truncate tables.

## Receipt storage diagnostics

With the dependencies and stable-owner `MIGRATION_DATABASE_URL` above, run
`npm run --silent receipts:diagnostics`. The read-only command reports this
operation's v2 receipt counts, active/expired population, oldest expired deadline
and age, and table/index/total physical bytes as one versioned JSON object.
Counts, ages in milliseconds and sizes are decimal strings. An empty backlog
has null oldest deadline and age. No actors, tenants, keys or payloads appear.

Non-owner sessions, missing/FORCE RLS tables, incompatible shapes and query
failures produce unavailable entries without measurements and a nonzero exit.
Connections and statements have 5-second deadlines, locks a 1-second deadline,
and client queries a 6-second fallback. Exact counts may time out on large
tables. Storage sizes include TOAST and allocated free space; deletion may leave
physical allocation unchanged. Keep cross-user aggregates in owner monitoring.
Cleanup remains explicit. See the receipt storage diagnostics section at
https://gregale.dev/docs/data-api for fields and fixed failure reasons.

## Verification and business review

Load `fixture.sql` only into a disposable rehearsal database. Alice is
`{sub:'alice',tenant_id:'alpha'}`; Bob is
`{sub:'bob',tenant_id:'beta'}`. Owner fixture loading needs one transaction so
its temporary claims and write scopes remain in force. `access-checks.json`
contains time-independent read/refusal checks; `data-checks.json` verifies stock,
foreign keys and order totals. Run these with your existing `data-api verify`
workflow and project authorization scenarios. The repository's generated-package
acceptance additionally tests live UUIDv7 writes, concurrency, lost responses,
read-only recovery, expiry and cleanup against PostgreSQL/PostgREST.

Review names, business bounds, tenant membership, RLS, custom triggers, grants,
query plans and retention/storage before deployment. Quantity, SKU count and
price limits are editable business rules, not platform quotas. The function is
SECURITY INVOKER, fixes search_path, revokes PUBLIC execution and uses the normal
`@gregale:rpc` opt-in. Write scopes block ordinary REST writes; audit all exposed
functions and SQL grants because scopes do not defend against arbitrary SQL.
