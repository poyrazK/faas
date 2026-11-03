# ADR: Broker trigger connections are managed, and Kafka lag is read from committed offsets

Date: 2026-11-01
Status: Accepted
Amends: [ADR 20261027162741](20261027162741-broker-trigger-lanes.md) (decision 3), spec §"Batch semantics" (`batch_window_ms`)
Related: [ADR 20261101093015](20261101093015-broker-trigger-lane-fairness-and-record-rate.md), event-driven GA blocker hunt #6 (2026-11-01)

## Context

The 2026-11-01 broker trigger review found that schedd's connections to
customer brokers, and what it reports about them, were wrong in several
ways:

- **Redis `tls: true` was ignored.** go-redis negotiates TLS only in its
  default dialer. The trigger sets a custom, egress-guarded `Dialer`, so
  `TLSConfig` had no effect and `AUTH` and every record crossed the
  network in plaintext.
- **NATS connections.** The connection pool dialed while holding its only
  lock, so one unreachable server made every NATS poller build on the node
  wait for it (ADR 20261027162741 decision 3 promised no shared lock during
  a build). Connections were never closed, so a deleted trigger's server
  and a URL whose credentials had been rotated stayed connected and
  reconnecting for the life of schedd. A connection nats.go had closed for
  good (it does after repeated authorization errors) stayed in the pool,
  and its triggers failed until a restart.
- **Kafka lag read low.** `BrokerStats` returned the reader's
  `Stats().Lag`: one gauge shared by every partition, measured from the
  fetch position, not the committed offset. A commit held by a retried
  record never showed, yet the value fed the trigger's health row and
  `queue_lag` autoscaling.
- **Kafka group errors were silent.** kafka-go hands a consumer-group
  failure (rejected SASL credentials, an unreachable coordinator) to
  `FetchMessage` only if a fetch happens to be waiting when its retries
  give up. A group that could not join looked like a healthy empty poll,
  and `FaasBrokerTriggerPollErrors` never fired.
- **Pollers built for skipped triggers.** `BrokerLag` built a poller for
  every enabled trigger of the app, including ones the tick does not poll
  (another node's, a suspended account's, capability off), so Kafka
  readers joined their group and the next tick evicted them again.
- **Closes under the poller lock.** A Kafka reader's `Close` (leaving the
  group, seconds) ran under the lock every lane takes to find its poller.
- **Metrics.** `GET /v1/triggers/{id}/metrics` counted the newest 1,000
  records of all states together and did not show the broker's health or
  lag, which schedd already records per trigger.
- `batch_window_ms` is accepted, stored and capped by plan, but nothing in
  schedd reads it. Consumer groups and durable consumers are left behind
  when a trigger is deleted.

## Decision

1. **Redis TLS in the dialer.** The trigger's dialer dials through the
   egress guard, then completes a TLS handshake (TLS 1.2 or later, system
   roots, server name from the configured `addr`) before go-redis writes
   anything. A plaintext server fails the dial.
2. **A managed NATS pool.** One connection per server URL (keyed by a hash
   of it), shared by reference: a poller takes a reference when built and
   returns it when closed. The dial runs outside the pool's lock, once per
   URL however many pollers wait (singleflight). A connection no poller
   holds closes after 2 minutes, so a trigger rebuilt for a config change
   reuses it. A connection nats.go has closed leaves the pool, the next
   build dials a new one, and the trigger tick rebuilds a poller whose
   connection closed (an optional `Closed()` on the poller).
3. **Kafka lag from committed offsets.** Per partition, the high watermark
   less the group's committed offset (the log start when the group has
   committed nothing or the log was truncated past it), summed. It is read
   with a kafka-go `Client` dialed exactly like the reader (egress guard,
   TLS, SASL), at most every 10 s per trigger, with a 5 s timeout; a
   failed read reports no lag rather than a guess.
4. **Kafka group errors fail an empty poll.** The reader's `ErrorLogger`
   keeps the last consumer-group error. A poll that fetched nothing
   reports it as the poll's error while it is under 30 s old (kafka-go
   retries and logs every few seconds while the group cannot join); a
   fetched record clears it. Informational lines (offset out of range
   skips, leave-group on close) are ignored.
5. **Lag reads only the pollers the tick keeps.** `BrokerLag` never builds
   a poller.
6. **No poller is closed under the poller lock.** It is removed from the
   cache under the lock and closed after.
7. **Trigger metrics.** Each state's count stops at 100,000
   (`TriggerMetricsCountLimit`, reported as `count_limit`), read with one
   capped index range per state. The response gains `consumer`: liveness
   (as for queue bindings), last poll, success and error, and the broker's
   lag. `gregale trigger metrics` prints it.
8. **`batch_window_ms` is documented as not applied.** A batch is what one
   poll returns, up to `batch_size_max`. Holding records across polls to
   fill a window would outlast the brokers' redelivery timers (NATS
   `AckWait` and Redis's 30 s claim) and hold a lane. Values stay accepted
   so existing manifests keep working.
9. **Consumer groups and durables are kept on delete**, and documented: a
   trigger recreated with the same group or durable resumes instead of
   replaying the topic or stream, and the name may be one the customer
   uses elsewhere. Kafka expires an empty group's offsets on its own; NATS
   durables and Redis groups are the customer's to remove.
10. `docs/broker-triggers.md` states the delivery guarantees, the
    ack-after-success contract, retries and dead letters, start positions,
    ordering, limits, credentials and TLS.

## Consequences

- A Redis trigger with `tls: true` now fails against a server that does
  not speak TLS, where it used to connect in plaintext.
- `queue_lag` autoscaling and the health row see a Kafka backlog held
  behind a retried record, and may scale out further than before.
- A Kafka trigger with bad credentials shows `degraded` with the broker's
  error and counts toward the poll-error alert.
- Each Kafka trigger keeps a small idle connection pool for lag reads,
  closed with its poller.

## Rejected alternatives

- **Applying `batch_window_ms`** by holding records in the poller: NATS
  redelivers after `AckWait` and Redis lets the group reclaim after 30 s,
  so a long window would deliver records twice; a window bounded by those
  timers is a smaller change to make once someone needs it.
- **Deleting consumer groups and durables on trigger delete**: a recreated
  trigger would replay from the start of the topic or stream.
- **Reader `Stats()` counters for group health**: they reset on every read
  and carry no error text.
