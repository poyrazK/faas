# Broker triggers

A broker trigger consumes records from a message broker you run and invokes
your app with them: Kafka, NATS JetStream or Redis Streams. Create one with
`gregale trigger create --kind kafka|nats|redis_streams`, `POST /v1/triggers`,
or a `triggers:` entry in `gregale.yaml`. Broker triggers are available on
Pro and Scale; Hobby has the in-platform `queue` and `realtime` kinds
([events](events.md), [realtime](realtime.md)).

This page states what Gregale guarantees about the records it takes from
your broker.

## Delivery guarantee: at least once

Every record your broker hands Gregale reaches your app at least once, or
ends in the trigger's dead-letter store. A record can reach your app more
than once: after a retry, after a scheduler restart, or when the broker
redelivers a record whose acknowledgement was lost. Make handlers
idempotent; each record carries a stable identity (`item_identifier`,
built from its place in your broker: Kafka topic, partition and offset, NATS
stream and sequence, Redis entry id) that is the same on every redelivery.

## Acknowledge after success

Gregale acknowledges a record at the broker only after your app has handled
it, never on receipt:

| Broker | On success | While it is retried | When it is dead-lettered |
|--------|------------|---------------------|--------------------------|
| Kafka | the group's offset is committed past it, once every earlier record of its partition has settled | kept and handed back first on the next poll; nothing after it in its partition is committed | committed past (or, with `broker_poison_strategy: seek-to-offset`, a poison record holds the partition until you act) |
| NATS JetStream | `Ack` | `Nak` with a delay | `Term` |
| Redis Streams | `XACK` | left pending in the group and read again | `XACK` |

With record delivery (the default) a 2xx response completes the record and
any other response, or no response, retries it. With batch delivery
(`delivery: batch`, a preview) a 2xx response completes the batch except
the records you list in `{"batchItemFailures": [{"itemIdentifier": "…"}]}`,
which are retried; an error or non-2xx response retries the whole batch.
The [realtime page](realtime.md) shows the batch body and the SDK helpers.

## Retries and dead letters

A failed record is retried until it has had `max_attempts` deliveries (up
to 10 on Pro, 25 on Scale), 2 seconds apart unless the trigger or the app
sets a retry policy. Then it moves to the trigger's dead-letter store
with the reason, and is acknowledged at the broker as above. A record your
app cannot be given at all (larger than the trigger's `payload_max_bytes`,
or without a stable identity under a work policy) is dead-lettered without
a delivery.

Inspect and recover them with `gregale trigger records <id> --state
dead_letter`, `gregale trigger dlq <id>`, `gregale trigger retry <id>
<record-id>` and `gregale trigger drop <id> <record-id>`, or from the app's
[dead letters](dead-letters.md).

## Where a new trigger starts

A trigger resumes where its consumer group or durable consumer left off.
When the group or consumer is new:

| Broker | Starts at |
|--------|-----------|
| Kafka | the earliest offset still in the topic (`group` names the consumer group) |
| NATS JetStream | the start of the stream (`durable` names the consumer, `faas-<trigger id>` by default) |
| Redis Streams | new entries only: the group is created at `$` if it does not exist |

Deleting a trigger does not delete its consumer group, durable consumer or
Redis group, so a trigger you recreate with the same name resumes where the
old one stopped instead of reading the whole topic or stream again. Kafka
removes an empty group's offsets after the broker's
`offsets.retention.minutes` (7 days by default); delete a NATS durable
(`nats consumer rm`) or a Redis group (`XGROUP DESTROY`) yourself when you no
longer want it.

## Ordering

One batch of a trigger is in flight at a time, so records reach your app in
the order the poll returned them: per partition for Kafka, per stream for
NATS and Redis. A retried record is redelivered in a later batch, after
records that followed it, so do not rely on order across a failure. Kafka
commits stay in order: nothing after a retried record is committed until it
settles.

## Throughput, fairness and limits

| Limit | Pro | Scale |
|-------|-----|-------|
| Triggers per app / per account | 10 / 50 | 50 / 200 |
| Records per batch (`batch_size_max`) | 500 | 5,000 |
| Largest record (`payload_max_bytes`) | 6 MiB | 16 MiB |
| Records per second per app | 1,000 | 10,000 |
| Broker egress per app | 50 Mbit/s | 200 Mbit/s |

Records over an app's per-second rate are not taken from the broker: they
wait there for a later poll and show as backlog, never as dead letters.

The scheduler that runs your app polls its triggers, each about once a
second. A node delivers up to 16 trigger batches at once and shares them
between accounts: each account with broker triggers on the node is owed an
even share, and accounts, and the triggers of one account, take turns. A
slow handler holds only its own trigger.

`batch_window_ms` is accepted but not applied: a batch is what one poll
returns, up to `batch_size_max`, without waiting for more records. Holding
records across polls would outlast the brokers' redelivery timers.

## Credentials and TLS

Broker credentials (Kafka SASL passwords and client keys, NATS URL
credentials, Redis passwords) are sealed when you save the trigger and
opened only by the scheduler that polls it. They never appear in API
responses or logs. Gregale connects to your broker from the platform's
egress addresses and refuses private, link-local and metadata addresses.

| Broker | TLS |
|--------|-----|
| Kafka | a `tls` block: `ca_cert`, `client_cert` and `client_key` for mutual TLS, `skip_verify` (Pro and Scale only). SASL `PLAIN`, `SCRAM-SHA-256` or `SCRAM-SHA-512` under `sasl`. |
| NATS JetStream | a `tls://` server URL |
| Redis Streams | `"tls": true`: TLS 1.2 or later, verified against public roots and the host name in `addr`, before `AUTH` is sent |

TLS is always at least version 1.2.

## Health and lag

`gregale trigger metrics <id>` (`GET /v1/triggers/{id}/metrics`) shows the
trigger's records by state and the scheduler's last view of its consumer:

- `consumer.liveness`: `healthy`; `degraded` when the last poll failed, with
  the broker's error in `last_error` (unreachable broker, rejected
  credentials, a consumer group that cannot join); `stale` when no
  scheduler has polled it for 30 seconds.
- `consumer.lag_messages`: the backlog on the broker. For Kafka it is the
  topic's high watermark less the group's committed offset, summed over
  partitions, so a partition whose commit is held by a retried record shows
  its full backlog. For NATS it is the consumer's pending messages, for
  Redis the group's lag.

The same lag drives `queue_lag` autoscaling for worker pools
([scaling policy](scaling-policy.md)). Each count in the metrics stops at
100,000 (`count_limit`).
