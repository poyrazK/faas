# ADR-20261112193306: Ordered message groups for worker queues

- Status: accepted (owner decisions 2026-11-12); slice 1 built dark
- Amends: ADR-374 (application keyed work policy), ADR-20261022105723 (pull
  leases), ADR-20261112140000 (Postgres change workers)
- Related: ADR-795 (keyed event delivery ordering), ADR-972 (parallel push)

## Context

Many worker jobs are per entity: "charge, then ship, then refund order 42", or
a Postgres change worker that must see a row's insert before its delete.

Gregale already has the ordering primitive. A work policy (ADR-374) with
`max_running_per_key: 1` and `pending_updates: all` turns an app, policy and
key into a **lane**: one running row at a time, in admission order
(`work_sequence`), and an older pending retry blocks later rows of its lane
until it succeeds, expires or fails terminally. ADR-795 adds transactional
admission order for events. Queue sends accept `work`, and push queue
consumers claim through the lane (`ClaimQueueTriggerInvocation`).

The gaps, found while designing this:

1. **Pull leases ignore lanes.** `QueueLeaseCandidates` excludes every
   work-policy row and `enqueueAppMessage` refuses a keyed send to a queue
   without a push consumer. Worker apps consume by pull, so they cannot use
   ordering at all.
2. **Ordering needs a declared policy.** The owner wants a key per message
   and no queue configuration.
3. **Postgres change workers are unordered twice:** changes carry no key, and
   the Commit relay moves a failed hand-over to a later `next_attempt_at` and
   goes on, so a row's later change can be enqueued first.

An earlier draft of this ADR proposed a separate `group_key` column and gate.
That would have duplicated lanes; this version builds on them.

## Decision (owner choices 2026-11-12)

1. **Per message key, no queue setting.**
2. **Pull and push.** Push already follows lanes; pull leases now do too.
3. **A failing message blocks its key** until it succeeds or leaves the queue
   (dead letter, expiry, cancellation): the ADR-374 lane rule, unchanged.
4. **Postgres change workers key by row**: `<feed>:<primary key JSON>`.

## Design

### Slice 1: pull leases follow lanes (built)

- `QueueLeaseCandidates` returns work-policy rows of the binding when they
  are their lane's head (no older pending and no running row of the same
  app, policy and key; the existing `invocations_work_lane_active_idx` serves
  the check), with the lane and fairness fields. Unkeyed rows are unchanged.
- `LeaseQueueMessages` leases a keyed candidate in a savepoint after
  `lockKeyedClaimTx` and `lockFairnessClaimTx`, the checks the push claim
  uses, taking the lane before the row as keyed enqueue and push do. A
  candidate that is no longer the head, whose fairness group is full or that
  just expired is skipped. One lease may return heads of many lanes, at most
  one per lane.
- The lease's ack, nack, extend and expiry transitions already move the row
  between `dispatching` and `pending`/terminal, which is all the lane rule
  reads: a nacked head stays the head; an acked or dead-lettered head
  releases its lane.
- `enqueueAppMessage` accepts a keyed send to a named queue with an enabled
  pull binding, as it did for push.
- MemStore applies the same rule (`keyedClaimAllowedLocked`).

### Slice 2: a key per message without a policy

`group_key` on a send (and on each `send-batch` message) is shorthand for
`work: {policy: "ordered", key: <group_key>}` against a built-in policy every
app has, `ordered` (`max_running_per_key: 1`, `pending_updates: all`, no
debounce or expiry, no fairness cap). The name is reserved: declaring a policy
called `ordered` is refused. The store provisions its row on first use, so no
manifest or migration of existing apps is needed. `gregale queue send
--group-key`, SDK and OpenAPI follow. Key rules are the work-key rules
(bounded JSON scalar; a string key is sent as given).

### Slice 3: Postgres change workers

- Capture writes `group_key = <feed>:<canonical primary key JSON>` into a new
  nullable `gregale_outbox.group_key` column (the install's idempotent
  upgrade; generic Commit sources leave it NULL).
- The relay keeps per-key order before enqueue: its candidate query skips an
  event while an older unaccepted event with the same `group_key` exists
  (pending, leased, retrying or blocked), ordered by `created_at, event_id`.
  One row's changes are serialized by the row lock, so that order is commit
  order. A permanently blocked change holds only its row, until
  `gregale commit replay`.
- `AcceptCommitChange` enqueues each change under the built-in `ordered`
  policy with that key.

### Slice 4: health and scaling

`queue_depth` scaling counts lane heads plus unkeyed messages instead of all
pending rows, so one blocked key does not start idle workers. Worker status
and queue health add blocked lanes and the oldest blocked lane's age; metric
`queue_blocked_lanes` per binding.

## Consequences

- Ordered work needs no customer locks, and the same lane serializes a key
  across push, pull, events, delayed tasks and broker triggers.
- One key runs one message at a time; the docs recommend a key per entity.
- Keyed leases take a lane lock and an index probe per candidate; unkeyed
  traffic is unchanged.
- A poison message stalls its key until its attempts are spent, bounded by
  the binding's retry policy (owner choice 3).
- Until slice 4, a long blocked lane lengthens the candidate scan, since the
  head filter runs while the due-ordered index is walked.

## Rollout

Slice 1 changes only who may lease rows that already carry a policy, which a
keyed send to a pull queue could not create before; it needs no flag. Slices
2–4 ship dark behind a `queue_message_groups` platform flag.

## Alternatives considered

- **A separate `group_key` column and gate** (this ADR's first draft):
  duplicates lanes, and the two orders would not compose.
- **Per-queue FIFO mode**: config and a migration per queue; rejected for
  per-message keys.
- **Skip ahead on failure**: order breaks exactly when it matters.
- **Sticky routing of a key to one worker**: needs membership and
  rebalancing; the lane gives the same guarantee on a shared table.
