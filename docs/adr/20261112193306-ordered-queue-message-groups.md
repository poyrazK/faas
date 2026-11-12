# ADR-20261112193306: Ordered message groups for worker queues

- Status: proposed (owner decisions 2026-11-12), to be built dark
- Amends: ADR-231 (queue push), ADR-972 (parallel push dispatch),
  ADR-20261022105723 (pull leases), ADR-20261112140000 (Postgres change workers)
- Related: ADR-911 (batch publication)

## Context

Gregale queues deliver at least once with no order. Pull leases pick pending
messages by `due_at, created_at, id` (`QueueLeaseCandidates`), up to the
binding's `max_concurrency`, from any number of workers; push lanes (ADR-972)
claim the same rows in parallel. A retry moves a message's `due_at`, so a later
message can run first.

Many worker jobs are per entity: "charge, then ship, then refund order 42",
or a Postgres change worker that must see a row's insert before its delete.
Today the customer must serialize in their own code, usually with a lock or a
version check, and still cannot stop a retry from overtaking.

Postgres change workers make this worse in a second place: when the Commit
relay cannot hand an outbox event over (queue full, missing binding), it moves
that event's `next_attempt_at` forward and goes on to the next one, so a row's
later change is enqueued before its earlier one.

## Decision (owner choices 2026-11-12)

1. **Per message key.** A send may carry `group_key` (1–128 bytes of UTF-8, no
   control characters; the byte limit lives in `pkg/api/limits.go` as
   `QueueGroupKeyMaxBytes`). Messages with the same key on the same queue are
   delivered one at a time, in send order. Messages without a key keep today's
   behaviour and run fully in parallel beside grouped ones. No queue setting.
2. **Both pull and push.** Lease and push claim apply the same rule.
3. **A failing message blocks its group.** While a grouped message is pending
   (including a retry with a future `due_at` or a nack delay), leased or being
   pushed, no later message of its group is delivered. The group continues
   when that message succeeds or leaves the queue: dead-lettered after its
   attempts, expired at the plan's maximum wait, or cancelled. A poison message
   therefore stalls only its own group, and only until its attempts are spent.
4. **Postgres change workers group by row.** Each change carries
   `group_key = <feed>:<canonical JSON of the primary key>`, so one row's
   changes reach the worker in commit order and different rows stay parallel.

## Design

### Storage and order

- `invocations` gains `group_key text NULL` and `group_seq bigint NULL`
  (expand-only migration, `lock_timeout` first; both NULL for every existing
  row). A CHECK keeps them NULL together and limits the key length.
- Order is `group_seq`, from one sequence. Two concurrent senders to the same
  group could commit out of sequence order, letting a later number become
  visible first. The enqueue path takes
  `pg_advisory_xact_lock(hashtextextended('qgroup:'||app||queue||key, 0))`
  before taking the number, so within a group, number order is commit order.
  Batch sends lock each distinct key once, in sorted order (no deadlocks), and
  number a batch's messages in array order.
- Partial index for the gate:
  `(app_id, queue_name, group_key, group_seq) WHERE group_key IS NOT NULL AND
  state IN (<non-terminal states>)`.

### The gate (one predicate, two claimers)

A grouped candidate is deliverable only when it is its group's **head**:

```sql
AND (w.group_key IS NULL OR NOT EXISTS (
  SELECT 1 FROM invocations e
  WHERE e.app_id = w.app_id AND e.queue_name = w.queue_name
    AND e.group_key = w.group_key AND e.group_seq < w.group_seq
    AND e.state IN (<non-terminal states>)))
```

The predicate ignores `due_at`: a retrying head that is not due yet still
blocks its group. It is added to `QueueLeaseCandidates` and to
`ClaimQueueTriggerInvocation`, and checked again under the row lock in
`leaseQueueCandidateTx`, so two lessees cannot both win the head. A lease
takes at most one message per group; `max_messages` is filled from other
groups and ungrouped messages. Visibility timeout and lease expiry return the
head to pending, and it stays the head.

Starvation: candidates are read `max_messages × 4` at a time in due order. A
queue whose due prefix is all blocked group members could then return
nothing. The candidate query therefore reads ungrouped messages and group
heads from two index ranges (UNION ALL, as the binding/legacy split already
does) instead of filtering blocked members after the read.

### Leaving the queue unblocks the group

Dead-lettering, plan wait expiry, cancellation and ack all end the
message's non-terminal state, which is all the gate looks at; no extra
bookkeeping. **DLQ replay** puts the replayed message at its group's tail
(a new `group_seq`): it is an explicit operator action and must not jump
ahead of work that already ran.

### Push

ADR-972 lanes claim one record each through `ClaimQueueTriggerInvocation`,
which gains the gate, so at most one lane holds a group at a time and the
others take other groups. A push failure retries under the invocation retry
policy and blocks its group as in decision 3. External broker triggers
(Kafka, SQS, NATS, AMQP) are unchanged: they keep their own partition order.

### Scaling and health

- Scaling counts what can run: `queue_depth` for a binding becomes ungrouped
  pending messages plus the number of groups with a deliverable head, so a
  backlog of one blocked group does not start workers that would sit idle.
- `gregale worker status` and the queue health API add
  `blocked_groups` and the oldest blocked group's age; metric
  `queue_blocked_groups` (gauge, per binding). An alert on
  `queue_oldest_age_seconds` already fires for a stuck head.

### Postgres change workers

- Capture writes `group_key` into the customer outbox (`gregale_outbox`
  gains a nullable `group_key text`, added by the install's idempotent
  upgrade; generic Commit sources leave it NULL).
- The relay keeps per-group order before enqueue: its candidate query skips
  an event while an earlier unaccepted event of the same `group_key` exists
  (pending, leased, retrying or blocked), using the same head rule on
  `created_at, event_id`. A row's changes are serialized by the row lock, so
  `created_at` order is commit order. A permanently blocked change
  (`blocked_code`) blocks only its row until it is replayed or skipped with
  `gregale commit replay`.
- `AcceptCommitChange` passes the key through to the invocation.

### API, SDK, CLI, manifest

- `group_key` on `POST …/queues/send` and on each `send-batch` message;
  leased and pushed messages expose it. OpenAPI and SDK types gain it.
- `gregale queue send --group-key KEY`.
- No manifest field: ordering is chosen per message.
- Limits: key bytes in `pkg/api/limits.go`; no new per-plan quota. A group
  counts against the existing pending-message caps like any message.

## Consequences

- Ordered work no longer needs customer locks, and Postgres change workers
  deliver per-row commit order end to end.
- Throughput of one group is one message at a time; the docs say so and
  recommend a key per entity, not per tenant or per queue.
- Each grouped enqueue takes an advisory lock and each grouped lease runs an
  index probe; ungrouped traffic takes neither.
- A poison message stalls its group until its attempts are spent. That is the
  chosen trade (decision 3); `max_attempts` and the binding's retry policy
  bound it, and blocked-group health makes it visible.

## Rollout

Dark behind a `queue_message_groups` platform flag: apid refuses `group_key`
for accounts without it (`queue_groups_unavailable`). Order of slices:

1. Migration, enqueue numbering and the gate for pull leases, with
   property tests: never two in-flight messages of a group; within a group,
   deliveries follow `group_seq`; ungrouped throughput unchanged.
2. Push claim gate and blocked-group scaling/health.
3. Postgres changes: outbox `group_key`, relay head rule, key pass-through.
4. API/SDK/CLI surface and docs.

## Alternatives considered

- **Per-queue FIFO mode** (every message keyed, set on the binding): stricter
  but needs config and a migration per queue; rejected for per-message keys.
- **Skip ahead on failure**: never stalls, but order breaks exactly when it
  matters; rejected.
- **Pull only**: smaller, but push consumers are the handler-shaped default;
  owner chose both.
- **Sticky routing of a group to one worker** (Kafka-style partitions):
  needs consumer membership and rebalancing; the head gate gives the same
  guarantee on a shared table without either.
