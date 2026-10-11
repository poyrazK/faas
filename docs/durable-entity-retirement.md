# Durable entity retirement and capacity reclamation

This opt-in operator workflow permanently retires one exact entity identity.
It scrubs current application state, preserves committed request receipts and
recovery metadata, cleans proven obsolete immutable objects, and releases the
active entity slot only after a full verification pass. Retained receipts may
contain customer results. This is lifecycle retirement, not complete erasure.

## Enablement and prerequisites

Keep production ingress quiesced while upgrading every writer. Enable
`FAAS_DURABLE_ENTITY_RETIREMENT_ENABLED=1` only for reviewed operator use; it
requires quota policy management, quota migration and tenant quotas. Startup
probes bounded private listing and verified deletion of its own probe object.
The flag is off by default. Turning it off does not undo permanent fences.

First adopt a [managed quota policy](durable-entity-quota-policy.md) and complete
its reservation migration. The entity must have a full active reservation and
authenticated retained-usage metadata. Drain all connections in its quota scope
and the entity's owner, clear its alarm through normal application transitions,
and deliver or otherwise resolve pending outgoing intents. Retirement does not
silently cancel pending durable work. Application-managed session records live
inside application state and are scrubbed with it; transport connections must be
drained. There is no separate inspection of arbitrary application session data.

Manifest schema 7 permanently fences old entity writers. Quota schema 3 fences
old quota writers before capacity reclamation. All supported ownership and
admission paths reject retired identities. Quota-disabled and direct bucket
writers, rollback of authority objects, native clock bounds and listing
completeness remain external rollout responsibilities. Do not downgrade schemas,
delete tombstones, delete retirement receipts, or restore older authority records.

## Operator API

Use the normal exact entity selectors: namespace, key, immutable environment and
verified platform tenant. Owned-app authorization, MFA and recovery eligibility
apply. Read scope can inspect; deploy-write scope is required for start and step.
Every response uses `Cache-Control: no-store` and exposes metadata only.

`GET /v1/apps/{slug}/entities/retirement?<selectors>` previews owner, alarm,
outgoing-work and accounting blockers; version/recovery revision; current
reservation; projected retained recovery bytes and reclaimable capacity. It
also returns the entity and quota retirement checkpoints. Reads do not acquire
ownership, alter counters, delete objects or dispatch customer code. Preview is
an observation, not a lease or fleet assertion.

`POST /v1/apps/{slug}/entities/retirement/start?<selectors>` accepts:

```json
{
  "operation_id": "<fresh UUIDv4>",
  "expected_version": 12,
  "expected_recovery_revision": "<exact preview revision>"
}
```

Start compares the observed manifest, requires no live owner/alarm/outgoing work,
publishes a fresh recovery snapshot with empty application state and unchanged
receipt references, increments the ownership epoch and generation, and commits
the permanent tombstone. A separate quota CAS tracks the operation, revokes its
browser grants and blocks further invocation/connection/grant admission. Until
that tracking write succeeds, the full original reservation remains charged.
Failure between these writes leaves the entity retired, with no quota credit;
retry the exact start request or resume with step. Start is irreversible after
the manifest CAS. Repeated identical starts return the current checkpoint and
original actor; changed requests cannot rebind the operation.

`POST /v1/apps/{slug}/entities/retirement/step?<selectors>` accepts:

```json
{
  "operation_id": "<the original UUIDv4>",
  "expected_revision": 1
}
```

Use the returned revision for the next step. Wait until `not_before` (maximum
entity lease plus invocation budget, currently 5m25s). Steps first authenticate
the retained receipt graph in bounded inventory pages. Cleanup then lists one
bounded page and deletes only objects proven obsolete by committed references
and the retirement generation barrier. The tombstone, retained recovery
snapshot, reachable receipts, checkpoints, indexes, unrecognized keys and objects
at/after the barrier remain. A separate full listing pass verifies cleanup; late
obsolete objects restart cleanup. Expired or invalid provider cursors fail closed
without quota credit. To discard a cursor, send the same step request with
`"restart_cleanup": true` and the currently observed revision. This is allowed
only during cleanup/verification and restarts cleanup from the beginning without
changing the permanent fence, retained receipt proof or quota charge.

After entity cleanup reaches `complete`, one quota CAS removes its active
reservation and commits a permanent audit/fencing receipt, replacing the full
cap charge with exact authenticated recovery bytes. `released_bytes` reports
the difference. Namespace/key cannot be reused. New work needs a new identity.
Scope inspection reports `retired_entities` and `recovery_bytes`; the 64 active
entity limit excludes completed retirements. Migration skips tombstones and
cannot recreate full reservations. Pending tracked retirements block migration
replacement and policy changes.

There are at most 256 permanent retirement receipts per quota scope. The workflow
fails closed at that bound instead of evicting fences or resetting history. The
existing 32-policy-receipt bound and other quota ceilings remain unchanged.

## Failure and retention contract

Stale operations/revisions and pending work return 409. Storage/unknown write
outcomes return 503. Retired invocation returns 410 and does not dispatch. Inspect
both checkpoints after uncertainty; an acknowledgement may be lost after commit.
Retry the exact start request or the currently observed step revision. Delete
failures keep the checkpoint unchanged and retries use immutable keys. Quota
reclamation records its receipt and credit together, so acknowledgement loss
cannot credit capacity twice. Operational audit emission is best effort and may
repeat; the manifest and quota receipts are durable authority.

The capacity charge uses the existing reachable-state accounting contract.
Tombstones, indexes, checkpoint objects, unrecognized keys, uncommitted orphans,
backups and native provider version history are outside that accounting. Cleanup
is not a provider billing inventory or a claim of complete data removal. Backups
and provider history need their separate retention policy, and receipt erasure
requires a future explicit recovery-retention contract.

Native GCS/KVM, ownership failure, interrupted cleanup, clock and fleet acceptance
remain required before production enablement. No production gate is enabled by
this workflow.
