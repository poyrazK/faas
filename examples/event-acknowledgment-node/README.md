# Durable Node event acknowledgment integration

This module connects the [Node acknowledgment worker](../../docs/event-acknowledgment-node-worker.md)
to an existing application-owned durable queue. Copy/import `consumer.mjs` into
the authenticated ingress and recoverable worker hosts. It starts no in-process
background worker and supplies no queue/storage implementation.

Install the Node SDK, configure one `FaaSClient` with `apps:read` and `deploy:write`
(or admin) credentials, and explicitly install `eventConsumerReceiptSchema` in the business
PostgreSQL database. Configure bounded pool acquisition, query and statement
timeouts. Enable a deferred acknowledgment policy only after compatible schedulers
are deployed, as described by the [platform protocol](../../docs/event-deferred-acknowledgments.md).

## Queue adapter

`queue.enqueueProtected(record)` durably commits the entire record before
resolving. Encrypt/restrict job, backup and queue-console access: its selected
delivery headers contain the raw token and its body is application data. The
random job ID is independent of the token. Never log or expose the record.
Ordinary bearer authorization headers are excluded.

The worker host supplies an exclusively owned `job` with:

- `record`: the exact saved record, including immutable owner scope/headers/body.
- `signal`: aborted when ownership or the worker's processing deadline is lost.
- `savePhase('reconcile')`: a durable, ownership-fenced update which commits before
  resolving and updates the record returned by subsequent queue claims.
- `ack()`: an ownership-fenced durable deletion/acknowledgment.

The host must retain an unacknowledged job and its latest phase across failure.
Use actual durable queue leases and preserve exclusive ownership during processing;
an SDK object guard is not a distributed job lease. The adapter must not reset the
phase on a retry. Retain receipts for the entire possible retry/replay period.

## Authenticated ingress

After the existing route authenticates and authorizes the internal request,
captures bounded original bytes without JSON reserialization, and loads trusted
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
authentication. The job records configured owner scope and the worker compares it
to current trusted configuration before processing, preventing accidental reuse
after a host is reconfigured for another app.

## Owned worker

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

Only database writes through `tx` belong in this callback. The example's phase
update precedes business execution. After a crash or uncertain response, the next
claim calls only `completeReceipt`; it cannot automatically invoke the callback.
If a matching receipt exists, the saved result is completed and the job is acked.
If receipt reconciliation reports missing/conflicting data, retain the job for
the owner's recovery policy. A crash between the phase update and first business
execution can legitimately produce a missing receipt. Resetting the phase requires
known rollback, stopped work and an explicit exclusive-owner decision.

Do not automatically map failures to a platform retry/dead-letter callback. Those
decisions require their own protected durable action/reason, known rollback and
exclusive ownership. On a new platform delivery, enqueue its original metadata;
the shared acceptance/consumer receipt protects already committed business writes.

Two subscriptions can process the same acceptance independently. One consumer's
receipt/completion does not settle the other's job. The module's integration and
restart/concurrency behavior require hosted acceptance coverage before release;
the local validation for this increment is SDK generation/build and static checks.
