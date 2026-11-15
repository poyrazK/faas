# Postgres change workers

Run a worker every time a row changes: send the email when an order ships,
sync a customer to your billing provider, rebuild a search document. Gregale
captures inserts, updates and deletes of the table columns you choose and
delivers each change to one of your worker's queues, without dual writes in
your code and without running a change-data-capture pipeline yourself.

Postgres change workers are a preview on the Hobby, Pro and Scale plans and
are released to accounts on request.

## How it works

1. A row trigger on each table you declare writes the changed row's chosen
   columns, before and after, into an outbox table in the same transaction
   as your write. A rolled-back transaction leaves no change behind.
2. Gregale reads committed changes from the outbox and enqueues each one into
   your app's queue.
3. Your worker consumes the queue like any other: pull leases, retries, dead
   letters and `queue_depth` autoscaling all apply.

## Declare it

Add a `postgres_changes` block next to the worker and the queue binding that
receives the changes:

```yaml
schema_version: 1
worker:
  scale: { min: 0, max: 3, metric: queue_depth, target: 100 }
queue_bindings:
  - name: order-changes
    queue_name: order-changes
postgres_changes:
  database: shop               # a managed database
  queue_name: order-changes    # one of queue_bindings
  poll_interval: 5s
  feeds:
    - name: orders
      table: public.orders
      columns: [id, status, total_cents]
      operations: [insert, update, delete]
```

| Field | Meaning |
|---|---|
| `database` | A managed database of your account, by name or ID. |
| `connection_url_env` | Instead of `database`: the local environment variable holding the owner URL of a database you run. |
| `queue_name` | The queue binding that receives the changes. |
| `poll_interval` | How often Gregale reads the outbox, 1s to 10m (default 5s). |
| `feeds` | Up to 20 tables. |
| `feeds[].name` | Lowercase name; events are typed `database.<name>.<operation>`. |
| `feeds[].table` | The table; an unqualified name is in the `public` schema. |
| `feeds[].columns` | The only columns an event carries. The primary key must be listed. |
| `feeds[].operations` | Any of `insert`, `update`, `delete` (default all three). |

`gregale deploy` applies the block after the queue bindings. Removing a feed
from the list and deploying again removes its trigger.

### Managed databases

With `database`, Gregale installs the outbox and the triggers itself, as the
database's schema owner, and connects with a login of its own that can only
read the outbox and mark changes delivered.

### A database you run

With `connection_url_env`, the install runs on your machine. Set the variable
to an owner connection URL and deploy, or run the install yourself:

```bash
export ORDERS_DATABASE_URL='postgres://owner:…@db.example.com/shop?sslmode=verify-full'
gregale postgres-changes install --connection-url-env ORDERS_DATABASE_URL
```

The install creates the outbox, the triggers and a login named
`gregale_changes_<id>` with a random password. Only that login's URL is sent
to Gregale; the owner URL never leaves your machine. The login can read the
outbox, update its delivery columns and delete delivered rows, and nothing
else. Gregale connects with verified TLS to your database's host name, which
must resolve to public addresses and present a publicly trusted certificate.

## Consume the changes

Each queue message is a CloudEvents envelope:

```json
{
  "id": "6f1c…",
  "source": "gregale.commit.<source id>",
  "type": "database.orders.update",
  "data": {
    "feed": "orders",
    "table": "public.orders",
    "operation": "update",
    "key": { "id": 42 },
    "before": { "id": 42, "status": "pending", "total_cents": 1200 },
    "after":  { "id": 42, "status": "shipped", "total_cents": 1200 }
  }
}
```

Inserts have `before: null`, deletes `after: null`. An update that changes
none of the listed columns sends nothing. If a listed column is later dropped
from the table, events carry `missing_columns` and your writes keep working;
deploy again with the column removed.

```js
import { consumeQueue } from '@gregale/sdk-node/preview/queues/runtime';

consumeQueue({ app: 'orders-worker', queue: 'order-changes', concurrency: 4 }, async (message) => {
  const change = message.payload.data;
  if (change.operation === 'update' && change.after.status === 'shipped') {
    await sendShippedEmail(change.key.id);
  }
});
```

## Guarantees

- **At least once.** Make handlers idempotent; the envelope `id` is the same
  on every redelivery.
- **Committed changes only.** A rolled-back transaction emits nothing.
- **No ordering guarantee** across rows or retries. Use the key and a version
  or `updated_at` column when order matters.
- **No backfill.** Capture starts when the feed is installed. `TRUNCATE` is
  not captured.
- If the queue binding is missing or the queue is full, changes wait in the
  outbox and are delivered once the worker can take them.

## Cost and latency

Each write to a tracked table adds one outbox row in the same transaction;
delivered rows are deleted after 7 days. Gregale reads the outbox every
`poll_interval`. A managed database that scales to zero suspends after 5
idle minutes, so a `poll_interval` under 5 minutes keeps it running; choose
`5m` or more if a few minutes of delivery latency is fine.

## Operate

```bash
gregale postgres-changes status --app orders-worker    # relay health and backlog
gregale dlq orders-worker                               # changes your handler failed
gregale postgres-changes uninstall --app orders-worker  # stop and remove capture
```

One database feeds one app: a second app cannot capture changes from a
database that already feeds another.
