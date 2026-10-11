# Transactional order outbox

Run `gregale data-api dev --port 0` here. The example generates database types
and verifies atomic order/event creation, idempotent retries, worker authorization,
claiming and acknowledgement. Install `@gregale/data` in your application and
use `client.ts` for typed `createOrder` and `dispatchOne` workflows.

Creating an order fires an AFTER INSERT trigger that writes one event in the
same transaction. A unique order reference prevents duplicate events. Order
retries reuse the idempotency key and return the original immutable order without
inserting another event. Rolling back the transaction removes both rows.

Workers need an issuer-signed token with `outbox_worker: true`, plus the subject
and tenant whose events they process. Ordinary application tokens cannot claim
or acknowledge events. RLS restricts every worker operation to that identity;
a worker token does not grant access across subjects or tenants. Worker tokens
are trusted credentials: keep them in the worker environment and never let an
application user choose their claims. Authorized workers have update permission
on their queue, so do not distribute those tokens to clients.

`claim_event` selects one available event with `FOR UPDATE SKIP LOCKED`, advances
its attempt count, and installs a caller-generated UUID lease token for 30 seconds.
Competing workers skip locked events. `finish_event` accepts only the current,
unexpired token. A worker crash leaves the event claimable after expiry. Failure
clears the lease and schedules exponential backoff from 2 seconds, capped at
256 seconds, until the event exhausts its configured retry budget. Acknowledged events stay stored and are never claimed again.

`dispatchOne` sends a stable `order-created:<event-id>` identity to your delivery
callback. Implement deduplication at the consumer or use that value as its
idempotency key. A crash after delivery but before acknowledgement can deliver
the same event again: this is at-least-once delivery. The stable identity survives
lease replacements. An empty acknowledgement means `lease-lost`; never overwrite
a replacement worker's state. Database request failures preserve the lease for
recovery, and delivery failures schedule a retry without storing provider errors.

Keep delivery duration below the 30-second lease and bound external calls in
your callback. The example has no heartbeat, scheduler or
worker fleet: invoke `dispatchOne` from your own polling job and monitor repeated
failures. Workers may deliver events out of order. Completed events and immutable
order receipts have no automatic retention cleanup. Reusing a database after
purging/resetting its event IDs requires an additional deployment namespace in
consumer keys.

For hosted use, apply both migrations through the owner migration app and grant
only the qualified binding role the approved RPC permissions using Gregale's
existing permission workflow. All RPCs and triggers are SECURITY INVOKER with
explicit search paths. RLS remains the authorization boundary. The queue is
application-owned schema, not a new Gregale background delivery service.


## Dead letters, replay and queue health

Events default to five attempts per delivery cycle. Trusted workers can use
`setRetryLimit` to configure each event from 1 to 1000 attempts; owners can
change the column default for newly enqueued events. Attempts are counted when
claimed, so a crash consumes a slot too. Lowering a limit does not cancel a
live lease. A failure at the limit becomes a dead letter immediately; a crash
on the final attempt becomes effectively dead once its lease expires and is
persisted as dead by the next claim sweep. Dead letters are never claimed.

`dispatchOne` returns `dead-lettered` when it exhausts the budget. Inspect the
visible row before replaying: `replayEvent` requires its `replay_generation`.
Only a worker can replay a dead or expired/exhausted event. Active leases and
completed events cannot be replayed. Concurrent replays using the same generation
allow one winner; a stale, missing, ineligible or tenant-hidden event returns an
empty array. Reconcile an uncertain replay by re-reading its generation.

Replay preserves event ID, order reference, payload and consumer deduplication
identity. It increments the generation, resets the cycle attempt counter, clears
leases and makes the event ready. `total_attempts` retains the lifetime claim
count. Fix the consumer issue before replaying. Previously successful external
side effects still require consumer deduplication; replay does not mean they
should be performed again.

`queueHealth` reads the SECURITY INVOKER `outbox_health` view. Its mutually
exclusive counts cover pending (including delayed retries), active leased,
delivered and dead-lettered events. Expired final leases count as dead even
before a claim sweep. Counts respect subject and tenant RLS; an empty identity
gets zero counts. Raw provider errors are not stored. Worker tokens remain
trusted queue-maintenance credentials rather than untrusted application tokens.
