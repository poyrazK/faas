# ADR-872: Bucket-backed durable tenant admission quotas

Status: implemented as an opt-in preview; native acceptance pending.

## Decision

Use one private conditional-write ledger per account/app/immutable environment/
verified platform tenant. Namespace/object keys do not create a new admission
budget. Persist leased connection reservations, fixed-window grant/invocation
counters, conservative entity byte reservations, and scoped grant records in the
same CAS object. Both service and browser sockets share the connection budget;
HTTP/socket invocation, restore execution and alarm delivery share invocation
admission. Existing local transport ceilings and per-entity storage enforcement
continue to apply.

A grant and its issuance/storage charge commit together. Credentials contain an
opaque scope locator so authentication/revocation read the authoritative ledger
without a second index object. Legacy standalone grants are rejected in quota
mode. Expired records leave in the next successful CAS mutation; revocation never
refunds a rate charge or deletes an unexpired record.

Reserve the full configured per-entity retained-byte cap on first admission,
with a persistent high-water reservation. This bounds future admitted storage
without coordinating two independent commits or claiming an exact tenant usage
inventory. Count serialized grant records against the same retained-byte budget.
Do not release entity reservations merely because a lease/window expires or
receipts are collected. Deletion/migration and reservation release need separate
verified operator authority in a later slice.

## Failure contract

Only confirmed CAS success admits work, upgrades a socket or returns a credential.
Retry definite CAS conflicts a bounded number of times; never blindly retry or
roll back uncertain writes. Uncertainty can conservatively consume capacity.
Bounded leases retain crashed/uncertain connection slots until expiry, and every
frame checks its lease before dispatch. Numeric 429 problems provide temporary
retry guidance; storage/corruption/uncertainty fail closed. Persisted policy can
only tighten during ordinary admission, preventing a weaker node from resetting
limits. Keep records private and never delete them using a bucket TTL rule.

## Limits

This is admission accounting, not provider billing: legacy dormant entities,
orphans, backups, control metadata and provider version history need independent
accounting. Existing-data rollout requires inventory/backfill or fresh isolated
scopes, draining older nodes and issuing fresh grants. Fixed windows admit
boundary bursts. A shared CAS object can become a contention and latency limit;
configured rate ceilings do not promise throughput. All nodes need consistent
quota mode, backend/bucket and storage cap, plus clocks verified by native
acceptance. Production flags remain disabled pending that acceptance.

See [configuration, status and native handoff](../durable-entity-tenant-quotas.md).
