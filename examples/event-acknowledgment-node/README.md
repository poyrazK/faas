# Durable Node event acknowledgment integration

This module connects the [Node durable intent store](../../docs/event-acknowledgment-node-intents.md)
to an application-owned durable ingestion queue and a separate recovery host.
Copy/import `consumer.mjs` into those hosts. It starts no in-process background
worker and supplies no queue/storage implementation.

Install the Node SDK, configure one `FaaSClient` with `apps:read` and `deploy:write`
(or admin) credentials, and explicitly install `eventConsumerReceiptSchema` and
`eventAcknowledgmentIntentSchema` in the business PostgreSQL database. Restrict
intent table and backup access: pending rows contain the raw token and result.
Configure bounded pool acquisition, query and statement timeouts. Enable deferred
acknowledgment only after compatible schedulers are deployed, as described by the
[platform protocol](../../docs/event-deferred-acknowledgments.md).

## Queue adapter

`queue.enqueueProtected(record)` durably commits the entire record before
resolving. Encrypt/restrict job, backup and queue-console access: selected delivery
headers contain the raw token and the body is application data. The random job ID
is independent of the token. Never log or expose the record. Ordinary bearer
credentials are excluded.

The worker host supplies an exclusively owned `job` with:

- `record`: the exact saved version 2 `durable-intent` record, including immutable owner scope/headers/body.
- `signal`: aborted when ownership or the worker's processing deadline is lost.
- `ack()`: an ownership-fenced durable deletion/acknowledgment.

Keep failed/unacknowledged jobs across restarts and preserve exclusive ownership.
The SDK's object guard is not a distributed lease. The example accepts only new
version 2 records so old immediate-mode jobs cannot silently change recovery mode.
Drain or explicitly reconcile old version 1 jobs with their original protocol;
do not relabel them, mix modes on an attempt or reset an old recovery phase.
Retain receipts throughout possible retry/replay and retain intent metadata while
stale jobs can return. No helper prunes these tables.

## Authenticated ingress

After the existing route authenticates and authorizes the internal request,
captures bounded original bytes without JSON reserialization and loads trusted
owner configuration:

```js
import { acceptAuthenticatedDelivery } from './consumer.mjs';

const response = await acceptAuthenticatedDelivery(req.headers, originalBody, {
  app: appSlug, accountId, appId,
}, queue);
res.writeHead(response.statusCode, { 'content-type': 'application/json' });
res.end(response.body);
```

Reject a failed enqueue/factory call instead of sending 202. Header parsing is not
authentication. Saved owner scope is compared to current trusted configuration
before processing, preventing accidental reuse after host reconfiguration.

## Owned processing worker

```js
import { processOwnedDelivery } from './consumer.mjs';

await processOwnedDelivery(job, { app: appSlug, accountId, appId }, pool,
  async (tx, signal, originalBody) => {
    signal.throwIfAborted();
    const event = JSON.parse(originalBody.toString('utf8'));
    // Apply application-specific validation/authorization before writes.
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [event.data.invoice_id]);
    return JSON.stringify({ applied: true });
  });
```

Use only transactional writes through `tx` in this callback. `runDurable` commits
business writes, the consumer receipt and completion intent together. The job is
acked after this local commit; that transfers callback delivery responsibility to
the durable intent recovery host. It does not confirm the platform acknowledgment.

A restart after commit replays the receipt and same intent without invoking the
handler. A retry after an uncertain COMMIT waits for the consumer identity lock;
new business work requires confirmed platform renewal and no committed receipt.
For an explicit receipt-only policy use `saveCompletionIntent`; if the receipt is
missing, retain the job for the owner's recovery policy. Do not infer rollback
from COMMIT uncertainty or automatically convert errors into negative callbacks.
Explicit negative decisions require stopped work, known rollback, exclusive
ownership and the same persisted action/reason on subsequent saves.

## Independent intent recovery host

Run a bounded iteration from a durable host that can reach PostgreSQL and apid:

```js
import { recoverOneAcknowledgment } from './consumer.mjs';

const status = await recoverOneAcknowledgment({ app: appSlug, accountId, appId }, pool, signal);
if (status) {
  // Metadata only. Inspect state/lastCode; do not treat resolution as confirmation.
  reportAcknowledgmentStatus(status);
}
```

The host owns scheduling, shutdown and concurrency. `undefined` means no due,
unclaimed row. `unconfirmed` has a database-time retry scheduled; `superseded`
stops this row. Storage/claim/settlement errors can leave a lease until expiry.
Recovery never executes business code and sends only the saved callback. Terminal
rows clear their raw token/result/claim, while backups and in-memory claims remain
protected. Expired platform leases can require a new delivery; receipt identity
still keeps consumers independent and prevents repeated committed writes.

The module's concurrency/restart behavior requires hosted acceptance coverage
before release. Local validation is generation, SDK build and static checks.

## Observe recovery health

The recovery host or an independent monitoring process can read a bounded summary:

```js
import { readAcknowledgmentRecoveryHealth } from './consumer.mjs';

const health = await readAcknowledgmentRecoveryHealth({ app: appSlug, accountId, appId }, pool, signal);
recordRecoveryHealth(health); // Include complete, truncated and coverage with every metric.
```

This reads one snapshot of retained unfinished intents; it does not claim work or
send callbacks. Defaults are 1000 sampled rows and a five-minute stall interval.
`due` excludes live claims, and `stalled` starts aging only after both the next
retry time and any claim expiry. Future schedules and live leases are not stalls.
An extra row marks `truncated`; invalid timestamp metadata makes `complete` false.
A zero stalled count in an incomplete sample cannot establish healthy recovery.
Oldest ages describe the sample and `observedAt` comes from the database clock.

The host owns observation scheduling and metrics/notification transport. Nothing
starts a poller or installs alerts. See the [Node health options and coverage](../../docs/event-acknowledgment-intent-health.md#node-summary).
