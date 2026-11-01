# ADR: Every broker trigger record a poll returns is settled

Date: 2026-11-01
Status: Accepted
Amends: [ADR 20261027162741](20261027162741-broker-trigger-lanes.md), ADR-118 (trigger batch close)
Related: event-driven GA blocker hunt #6 (2026-11-01)

## Context

The 2026-11-01 broker trigger review found records an external broker
trigger (Kafka, NATS JetStream, Redis Streams, SQS-compatible) polled and
then never settled at the broker, or settled wrongly:

- **Records past the batch cap.** `closeBatch` cut a poll at
  `batch_size_max` or the payload byte cap and dropped the rest. They stayed
  with the poller unsettled: Kafka stopped committing the partition, Redis
  kept the entries at the head of the pending list, and NATS redelivered
  them until `MaxDeliver` dropped them.
- **A record larger than `payload_max_bytes`** could never fit a batch, so
  it blocked its partition or pending list for good.
- **Redis `Count: 0`.** When the pending list filled the batch, the second
  `XREADGROUP` asked for 0 entries, which go-redis sends without `COUNT`:
  the whole backlog of new entries was read into memory and claimed.
- **Dead-lettered redeliveries.** A dead-lettered record was neither
  terminal nor claimable, so its redelivery went back to the broker on
  every poll; a Kafka partition whose dead letter waited for lower offsets
  stopped committing after a restart.
- **Claims shorter than the batch.** A claim lasts ten minutes, but a
  long-timeout app's batch may wait 16.5. The outcome could not be
  recorded, the broker delivery was never settled, and the record ran
  again.
- **NATS `MaxDeliver: 25`.** A retry's backoff was spent in 2 s
  redeliveries, so around attempt 5 the server dropped the message while
  its record stayed `retry` with no dead letter.
- **Item identifiers reused.** Kafka records were `partition-offset` and
  NATS records the stream sequence, so a new topic or a recreated stream
  reused identifiers and new records were acknowledged as already done.
- **A store error while recording an outcome** left the broker delivery
  unsettled for the life of the poller.

## Decision

1. **Batch close splits a poll three ways.** Records that fit
   `batch_size_max` and the byte cap run now. Once one record does not fit,
   it and every later one are handed back unsettled (`Defer`) and come
   first in a later poll without spending an attempt. A record larger than
   `payload_max_bytes` is stored without its payload
   (`payload_omitted_bytes` in its metadata), dead-lettered as
   `payload_too_large` and settled at the broker as a poison record.
2. **Redis reads new entries only when the pending list left room.**
3. **A redelivery settles from the stored record.** The dispatcher reads
   the stored state of every polled record (`TriggerRecordStatesByItems`).
   Terminal records are acknowledged. Dead-lettered records settle as their
   dead letter did: `max_attempts` is committed, `rate_limited` is acked,
   anything else is a poison record (Kafka's seek-to-offset strategy keeps
   holding it until an operator acts).
4. **Claims are renewed while their dispatch runs**, every 2 minutes, for
   the claim generation the dispatcher holds (`RenewTriggerRecordClaims`).
5. **A cooling retry is held at the broker until it is due.** NATS naks it
   with the rest of its delay (`NakWithDelay`), and Kafka keeps it in memory
   without committing past it. NATS consumers have no `MaxDeliver`: the
   trigger's `max_attempts` decides when a record is dead-lettered, and the
   message is terminated then.
6. **Identifiers name their topic or stream.** Kafka records are
   `topic/partition/offset@timestamp` and NATS records
   `stream/sequence@timestamp`. A record stored under an earlier release's
   identifier keeps that row while it is pending, claimed or retrying, or
   (Kafka) while its stored topic matches. The next release can drop this
   mapping once those rows have aged out.
7. **Outcome writes retry transient store errors** (5 attempts, 200 ms
   doubling). One that still fails hands the delivery back to the broker,
   and the claim lapses and runs the record again.

## Consequences

- A poll that returns more than one batch runs the rest in later polls, in
  order, instead of stalling the partition or pending list.
- An oversized record is visible as a `payload_too_large` dead letter, which
  cannot be replayed from storage because its payload was not kept.
- A NATS retry no longer spends server deliveries while it cools, so a
  record is never dropped by the server while Gregale still means to retry it.
- At-least-once delivery is unchanged: a failed outcome write runs the
  record again rather than losing it.
