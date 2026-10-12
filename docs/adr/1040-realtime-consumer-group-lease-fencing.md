# ADR 1040: Persistent consumer-group lease fencing tokens

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Persist a signed-64-bit `last_fencing_token` counter on the immutable group UUID.
Every new lease increments it under the existing group row lock and stores the
number on the leased snapshot in the same transaction. Return `group_id` and
positive decimal-string `fencing_token` on single/batch claims. Group status and
the inspector expose the last committed number, starting at zero. Renewals keep
the same number. Completion removes the snapshot, not the counter. Expiry/retry
and dead-letter replay receive a larger number on their next lease, even when the
attempt budget resets. A uniqueness index guards current non-null snapshot tokens.
At the signed bigint ceiling, fail admission rather than wrapping the counter.
Rolled-back transactions may reuse an unreturned number; only committed claims
are promised to increase. Uncertain responses must not be retried automatically.

Numbers are comparable only within one group UUID and a resource scope. For an
ordered group, store the highest accepted token per downstream entity. For
per-message deduplication, scope it by group UUID and sequence. Different keys can
run concurrently, so a group-wide downstream high-water mark would incorrectly
reject valid work for independent resources. Recreated groups have different UUIDs;
never silently reset a downstream resource's authorized namespace from its name.

Downstream storage must atomically compare/register the fence with each guarded
write, rejecting lower numbers after a higher number is recorded. Equality may
permit several writes from one claim; use separate idempotency controls for repeated
effects. Fence registration is not automatic: a newer claim does not invalidate
an old writer at the downstream store until that store records/enforces the newer
number. This is not a lease-expiry oracle, exactly-once guarantee or proof of
handler cancellation. Every writer must participate in the storage enforcement.
Keep tokens as decimal strings or BigInt, never JavaScript Number.

Worker callbacks already receive the full typed claim; generated Node/Python
models expose the new fields without changing handler signatures or permissions.
Existing opaque claim tokens still authorize renewal/settlement and are distinct
from public fencing numbers. Fencing alone grants no API access.

## Deployment and validation

Apply `20261012102700492_realtime_consumer_group_fencing.sql` before activation.
Existing leases deliberately have null fencing values; do not invent retroactive
numbers. Drain/stop legacy workers and upgrade every API replica/client before
relying on fencing, then establish downstream enforcement and fresh claims.
Schema downgrade refuses groups that issued tokens, preserving counters against
reuse. Retain schema for code rollback, or explicitly remove old groups and handle
the resulting downstream namespace transition. Restoring counter state also
requires coordinating it with downstream accepted numbers.

Go/Node builds and static specification/generated Python checks are the validation
scope; runtime, migration and UI checks remain deferred by request.
