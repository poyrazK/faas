# ADR-589: Event delivery backpressure and fair routing

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Bound live captured application-event deliveries per consumer,
  application and account. Persist capacity waits independently of routing
  failures and choose due work by least recently claimed account and consumer.
- **Why:** A slow consumer could accumulate pending invocations indefinitely.
  Running async quotas do not bound that backlog; oldest-first recovery could
  devote a whole sweep to one consumer or account.
- **Consequences:** Capacity exhaustion keeps the captured recipient pending.
  Other consumers continue within their application and account limits. Releasing
  a slot lets polling resume admission without changing the delivery identity.

## Limits and scope

`pkg/api/limits.go` owns `EventDeliveryLimits`. A consumer is an acceptance-time
subscription ID within an application and account. Limits count pending plus
dispatching invocations; a retry from dispatching to pending retains its slot.
Terminal transitions release live capacity. Retained execution and history rows
remain subject to their existing retention policies.

| Plan | Per consumer | Per application | Per account |
| --- | ---: | ---: | ---: |
| Free | 64 | 256 | 1,024 |
| Hobby | 256 | 1,024 | 4,096 |
| Pro | 1,024 | 4,096 | 16,384 |
| Scale | 4,096 | 16,384 | 65,536 |

The Free limits allow delivery of already accepted work, including work accepted
before a plan change. They do not enable publishing or async APIs on that plan.
Event acceptance is unchanged: these limits bound materialized delivery work,
not event ingestion or the size of the retained event outbox.

## Atomic admission and recovery

ADR-588 admission adds an account capacity mutex after the work lane and before
the event receipt, recipient and application locks. Under that mutex, SQL counts
live delivery slots and admits a new invocation and its slot with the checkpoint
and history in the same transaction. Expired ownership rolls everything back.
Keep-latest admission subtracts only the pending work that its captured lane
will supersede. A running owner still counts. Cancellation and read-only duplicate
reconciliation bypass capacity admission.

Slots are trusted state records, not caller event headers. A PostgreSQL trigger
inherits a replay child's slot from its retained parent and guards both new replay
children and terminal-to-live in-place replay. An execution retry between live
states retains the slot. Replay entry refreshes the current plan limits; exceeding
them leaves the original state and durable replay marker unchanged. New replay
children preserve the slot independently of ancestor retention. Deleting an
invocation cascades its slot; deleting the original does not delete child slots.

Only entry into live work takes the capacity mutex. Completion and supersession
can release capacity without taking it, preserving admission's mutex-before-
pending-invocation lock order. Uncommitted exits still count, so a concurrent
completion can cause a harmless extra wait but cannot over-admit. MemStore
mirrors these decisions under its mutex.

## Capacity waits and retry budgets

A full limit atomically records `pending`, `capacity_scope`, a cumulative
`capacity_deferrals` count and `next_attempt_at`, five seconds later. It does not
record a failure code or dead letter. Independent recipient completion releases
its routing claim and increments a generation-specific deferral count.
Whole-event routing continues its siblings and reschedules the receipt at the
first pending recipient deadline. It skips siblings whose deadlines are later.

Claim and attempt history counts continue to include capacity waits. The scheduler
subtracts capacity deferrals when enforcing the existing twelve-failure budget.
Independent replay resets generation attempts and generation deferrals while
preserving lifetime counts. The retry-progress writer cannot lower a committed
capacity count after an uncertain acknowledgement. Execution retry budgets are
unchanged. A full recovery API returns HTTP 429 with
`event_delivery_capacity_exhausted`; retry after pending deliveries drain.
Existing atomic batch replay rolls back on a capacity rejection.

## Fairness, ordering and observability

Claim queries persist account and consumer service timestamps with the claim.
They prefer the least recently claimed eligible account, then consumer, then the
existing due-time and identity tie-breakers. Whole-event claims prefer the least recently served eligible captured consumer
within the chosen account; independent claims select that consumer directly. A snapshot visit still
routes all its eligible captured siblings. These are restart-persistent scheduling
preferences with skip-locked concurrency, not strict round-robin or FIFO promises.
Captured work-policy lane ordering and deterministic admission deduplication
remain in force. Handler execution remains at least once.

Receipts expose lifetime and generation capacity deferrals, the active capacity
scope, the retry deadline, and pending age since event acceptance. Age covers
pending or processing routing, not execution duration. Scheduler metrics expose
committed observed deferrals by the bounded `consumer`, `app`, `account` scopes,
current capacity-waiting recipients, and oldest pending routing age. Gauges are
read from durable state after the sweep and become zero when empty; read failures
preserve the last successful observation. Deferral counters describe observations
by the current scheduler process; receipt counts are the durable authority.

## Rollout and compatibility

The additive migration creates capacity/slot/fairness records and independent
recipient deferral counters. Both whole-event snapshots and already-adopted
recipients use the limits. Independent-recipient adoption remains off by default.
All scheduler and replay API writers must be upgraded before relying on bounded
admission.

Slots start with new admissions under this implementation. Existing admitted
invocations and reconciled historical handoffs are not retroactively tagged from
untrusted headers. They drain under their existing quotas; new tagged roots and
all their replay descendants count. Rollout must allow for that legacy backlog.
Receipts predating recipient snapshots and specialized object-notification queue
and webhook destinations retain their existing paths.

## Qualification

Tests cover consumer isolation, more than twelve capacity waits followed by a real
routing failure, durable restart recovery, concurrent admission with one available
slot, fair claims across accounts and consumers, replay-marker rollback, descendant
capacity after ancestor pruning, in-place DLQ replay, keep-latest replacement, and
cancellation at capacity. PostgreSQL 16.15 admission, claim, recovery and concurrency qualification passed.
Full portable state, API client, event and metric suites passed, as did focused
scheduler and replay/receipt HTTP tests. The final full state run disabled test
vet, inlining and debug information to fit the shared machine; source vet is
covered by scoped lint. sqlc parity and whitespace checks passed. Linux CI and
deployment qualification remain rollout gates.
