# ADR: Broker trigger lanes are shared between accounts, and the record rate defers

Date: 2026-11-01
Status: Accepted
Amends: [ADR 20261027162741](20261027162741-broker-trigger-lanes.md) (decision 1), ADR-118 (the `TriggerRecordsPerSecondPerApp` deny path)
Related: [ADR-972](972-parallel-queue-push-dispatch.md), event-driven GA blocker hunt #6 (2026-11-01)

## Context

ADR 20261027162741 gave each external broker trigger (Kafka, NATS JetStream,
Redis Streams, SQS-compatible) a lane of its own: 16 per node, overflow
dropped, one batch in flight per trigger. Its consequence, "a slow broker or
handler holds only its own trigger", did not hold across tenants. The tick
walked `ListEnabledTriggers`, which has no order, and handed each free lane
to the first trigger it met. A batch holds its lane for up to 60 s (16.5 min
for a long-timeout app), so one account with 16 slow triggers took every
lane; when one finished, the same account's next trigger, met first again,
took it back, and other accounts' triggers were skipped tick after tick.

Separately, `api.Limits.TriggerRecordsPerSecondPerApp` (Hobby 100 / Pro 1,000
/ Scale 10,000) had no effect. The gate that read it consulted
`Loop.rateLimiter`, which nothing assigned, and it consulted the wake
buckets (`WakeBurstPerApp`, per minute) rather than the record rate. Had it
been wired, its deny path dead-lettered the whole batch and, on Kafka and
SQS, acknowledged it at the broker: a burst would have destroyed records the
customer still wanted.

## Decision

1. **An even share per account.** Every account with an enabled, pollable
   broker trigger on the node is owed `max(1, lanes ÷ accounts)` lanes, and
   never more than it has triggers. A pollable trigger is one this node owns
   (ADR 20261027162741 decision 2) whose app may consume and whose
   capability (and, for an agent target, the agents capability) is on; the
   capability is read with the 30 s app cache, so an account that cannot use
   a lane is not owed one.
2. **Round-robin turns.** Accounts take lanes in turn, starting after the
   account last given one, and an account's triggers take turns the same
   way, starting after its trigger last given one (both ordered by id). A
   first pass gives each account the lanes it is owed and does not hold.
3. **Idle lanes are lent, never re-lent while owed.** A second pass lends a
   lane nobody is owed, round-robin, but only while a free lane would remain
   for every lane an account is owed and does not hold. A lent lane is not
   taken back mid-batch: the account that is owed it gets it when its batch
   ends, and it is never lent again while that account waits. With one
   account on the node, it may use every lane.
4. **The record rate is taken before the poll.** `TriggerRecordsPerSecondPerApp`
   is a token bucket per app on each schedd, refilled at the plan's rate and
   holding one second of it. A lane asks the broker for at most the app's
   budget (its `batch_size_max` lowered to it), gives back what the poll did
   not use, and does not poll at all when the app has none. Records over the
   rate stay on the broker for a later poll; nothing is dead-lettered,
   acknowledged, committed or nacked (a nack would count toward NATS
   `MaxDeliver`). The cap applies to external broker triggers only: queue
   and realtime triggers, exclusive bindings and agent targets have
   admission of their own. cmd/schedd wires it.
5. The wake-bucket gate and its dead-letter path (`handleRateLimitedBatch`)
   are removed.

## Consequences

- A waiting account gets a lane as soon as any lane ends whose holder is
  above its share, at most one batch time (60 s; 16.5 min for long-timeout
  apps) after it first waits, and keeps getting its share after that. The
  same holds for each trigger within an account.
- A node with one tenant still uses all 16 lanes.
- The record rate is per schedd. An app's broker triggers are polled by its
  own node (unplaced apps by every node), so it is the app's rate in steady
  state.
- An app polled over its rate sees a broker backlog grow (queue_lag) instead
  of dead letters.
- The per-account share is computed each tick from the triggers present;
  a new account's share is honoured as lanes above share finish.

## Rejected alternatives

- **A fixed per-account cap** (say 4 of 16). Leaves lanes idle on a node
  with few tenants and still starves a fifth account.
- **Preempting a lent lane.** A batch mid-delivery cannot be stopped without
  breaking the ack-after-success contract.
- **Dead-lettering or nacking records over the rate.** Dead letters destroy
  work the customer wants; a nack spends a broker delivery attempt (NATS
  `MaxDeliver`) and, for Kafka, holds the partition anyway.
- **Checking the rate after the poll.** The records would already be
  fetched and would have to be given back, which is a nack.
