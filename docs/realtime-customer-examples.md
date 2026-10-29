# Customer database examples for managed realtime

These examples run in your application-owned database, using your application writer. They do not modify Gregale control-plane tables.

## Transactional publication

Use the same connection and transaction for your business mutation and outbox insertion. Install the customer outbox schema and source binding first, as described in the [realtime operations guide](ops/realtime.md#transactional-publishing-preview).

```typescript
import { insertRealtimeCommitEvent, listRealtimeCommitDeliveries,
  retryRealtimeCommitEvent } from '@gregale/sdk-node/preview';

const client = await pool.connect();
try {
  await client.query('BEGIN');
  await client.query('UPDATE orders SET status=$1 WHERE id=$2', ['shipped', orderId]);
  const eventId = await insertRealtimeCommitEvent(client, {
    endpointId, channel: 'orders', data: { orderId, status: 'shipped' },
    // For schema-enabled channels, include event_type and schema_version:
    // metadata: { event_type: 'order.shipped', schema_version: '1' },
  });
  await client.query('COMMIT');
  // Save eventId if you need to correlate subsequent delivery inspection.
} catch (error) {
  await client.query('ROLLBACK');
  throw error;
} finally {
  client.release();
}

const deliveries = await listRealtimeCommitDeliveries(pool);
// After repairing a blocked destination or payload:
await retryRealtimeCommitEvent(pool, blockedEventId);
```

## Lease fencing in customer storage

Bind each downstream row to its authorized consumer group before accepting writes. Atomically record and compare the fencing token; check affected rows to reject stale writers. Every writer must participate, and idempotency remains separate from fencing.

```sql
UPDATE jobs SET progress=$4, fence=$3::bigint
WHERE id=$1 AND consumer_group_id=$2::uuid AND fence<=$3::bigint;
```
