# Durable entity quota increases

The opt-in policy workflow previews and applies reviewed increases for one
account/app/immutable environment/tenant scope. It never decreases limits,
removes caps, deletes state or releases reservations. Limits retain the existing
hard ceilings (32 connections, 256 grants/minute, 10,000 invocations/minute,
64 MiB per entity and 1 GiB per scope). Legacy scopes allow 64 active reservations;
the separate [registry migration](durable-entity-registry.md) raises that upper
bound to 1,024 with per-shard limits. Policy changes preserve registry references
and permanent retirement charges.

## Rollout order

Keep production ingress quiesced. Upgrade all writers to the policy-aware release
before adopting a managed policy, and retain the private bucket and placement.
Start the existing [quota migration](durable-entity-quota-migration.md) under the
old configuration to hold admission and drain prior work. Configure the proposed
limits on all serving nodes, including `FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES`
for the engine's per-entity cap. Enable
`FAAS_DURABLE_ENTITY_QUOTA_POLICY_ENABLED=1` on the operator API. This flag requires
quota migration and tenant quotas; it is off by default.

Before policy apply, nodes configured with larger caps cannot resume the old
held migration. After apply, nodes whose configured limits differ from the
approved policy refuse mutable quota operations instead of silently tightening or
weakening the policy. Inspection still shows the actual approved limits.

Managed policy records use schema 2. Older quota/migration binaries reject this
schema, preventing them from dropping policy/audit fields. Quota-disabled writers
and direct bucket writers are outside this fence and must be stopped. Native
fleet, storage, clock and rollout acceptance remains required; there is no fleet
acknowledgement or automatic production enablement.

## Inspect and preview

`GET /v1/apps/{slug}/entities/quota-policy?<selectors>` returns persisted limits,
policy revision, `node_matches`, and the durable change receipts. Normal owned-app
read authorization and MFA apply. Use the existing entity selectors; namespace/key
resolve the scope but do not narrow the policy to one object. All responses use
`Cache-Control: no-store` and expose no payloads or credentials.

`POST /v1/apps/{slug}/entities/quota-policy/preview?<selectors>` is read-only and
requires read scope. Provide the same complete request that will be applied:

```json
{
  "operation_id": "<new UUIDv4>",
  "expected_revision": 0,
  "expected_migration_id": "<held migration UUID>",
  "expected_migration_revision": 1,
  "migration_id": "<fresh replacement UUIDv4>",
  "limits": {
    "connections": 8,
    "grants_per_minute": 60,
    "invocations_per_minute": 120,
    "retained_bytes": 33554432,
    "entity_bytes": 2097152
  }
}
```

The preview shows current/proposed limits, current reservations, the projected
footprint if **all known reservations** cover the proposed entity cap, additional
bytes, shortfall, local node alignment and `ready_to_apply`. It does not inventory
unreserved historical entities: review the paginated migration plan as well.
Readiness is an observation, not a write lease or a fleet assertion. Apply
rechecks the policy and migration revisions and budget through CAS.

## Apply and verify

`POST /v1/apps/{slug}/entities/quota-policy/apply?<selectors>` requires deploy-write
scope, MFA and normal recovery eligibility. Use the exact previewed request.
There must be an existing held migration, no live connection leases, a matching
expected policy/migration revision, sufficient projected reservation capacity,
and local node configuration matching the proposal. Every field must stay at or
above its persisted value. Revision zero may adopt unchanged limits into managed
policy; later changes must increase at least one field.

One CAS commits the approved limits, incremented policy revision, durable audit
receipt (actor, timestamp, operation identity and before/after policy), and a new
held migration. It preserves reservations and active counters; normal window
expiry still applies. Replacement migration UUIDs cannot reuse prior policy
fencing identities. The bucket retains up to 32 policy receipts, failing closed
at that bound rather than silently dropping history. Operational audit emission
is best effort and may repeat on request replay; the bucket receipt is authority.

Continue the new migration after `not_before`. For managed policies, each page
raises persisted entity caps through ownership-fenced, monotonic manifest CAS
updates, rereads manifests, and atomically advances reservations/checkpoint.
Cap updates and the quota checkpoint are separate bucket writes: partial or
unknown cap updates leave admission held and the cursor unchanged. Repeated
steps safely retain already raised caps. Stale cap workers cannot lower a cap
raised by a newer policy. A full verification pass confirms cap alignment,
usage within caps and reservation coverage before admission resumes.

Stale revisions return 409. Reusing an operation UUID for another request returns
409. A repeated identical committed operation returns its original receipt,
without another policy/migration change; inspect current status afterwards.
Configuration mismatches and storage/unknown outcomes return 503. Inspect policy
and migration status after uncertainty; never assume rollback or blind-retry with
a new operation ID. Missing usage and historical capacity shortfalls can still
block migration and require reconciliation or another reviewed increase.

Turning the management flag off disables its API, not enforcement of already
managed policies. Do not delete/TTL `tenant-quotas/v1/`, downgrade its schema, or
restore an older authority record. Native GCS/KVM and fleet acceptance are still
external release prerequisites.

For capacity left behind by unused objects, use the separate opt-in
[retirement workflow](durable-entity-retirement.md). Managed policies and completed
reservation migration are prerequisites; pending tracked retirements block policy
replacement. New retirement-aware ledger schema 3 preserves the policy contract.

## Source qualification

[CI run 38150848249](https://github.com/poyrazK/faas/actions/runs/38150848249)
passed for source commit `41c78896da0bbfe389e53870e1307fea690e123c`.
It passed the Node build, 16 focused SDK tests, packed-starter acceptance, four
Go durable packages under the race detector, focused CLI checks, and 25 top-level
API tests under the race detector. New coverage exercises read-only preview,
authorization, audited apply/replay, unknown commit recovery, stale-node fencing,
unsafe proposal rejection, ownership-fenced cap increases and migration completion.

No local tests, native GCS/KVM runs or production enablement were performed.
Native storage, ownership failure and fleet rollout acceptance remain pending
for the independent testing agent. This qualification does not authorize ingress.
