# Durable entity tenant admission quotas

This opt-in preview coordinates admission across API nodes using private bucket
CAS records. The quota scope is `(account, app, immutable environment, verified
platform tenant)`. An absent platform tenant selects the account-owned app and
environment scope. Namespace and object key are deliberately outside the quota
scope: changing either cannot obtain a new rate or connection budget. Different
apps/environments have separate budgets; this is not a fleet-wide account cap.

## Configuration

Keep production disabled until independent GCS/KVM acceptance passes.

For populated scopes, use the opt-in [quota migration workflow](durable-entity-quota-migration.md)
to plan, fence, backfill and verify historical reservations before restoring ingress.
`FAAS_DURABLE_ENTITY_TENANT_QUOTAS_ENABLED=1` requires durable invocation preview
and a positive `FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES` (per-entity cap, at most
64 MiB). Browser grants still require the separate socket/browser preview flags.

| Setting | Default | Allowed |
| --- | --- | --- |
| `FAAS_DURABLE_ENTITY_TENANT_MAX_CONNECTIONS` | 8 | 1–32 |
| `FAAS_DURABLE_ENTITY_TENANT_GRANTS_PER_MINUTE` | 60 | 1–256 |
| `FAAS_DURABLE_ENTITY_TENANT_INVOCATIONS_PER_MINUTE` | 120 | 1–10,000 |
| `FAAS_DURABLE_ENTITY_TENANT_MAX_RETAINED_BYTES` | 16 MiB | per-entity cap through 1 GiB |

Values must be canonical positive integers; byte caps use integer bytes. Limits
persist in the quota record and ordinary admission only tightens legacy limits.
A node with weaker settings cannot reset a previously persisted limit. Use the
opt-in [quota policy workflow](durable-entity-quota-policy.md) to preview and apply
reviewed increases, then migrate and verify entity caps. Managed policies require
exact node configuration agreement. Changing environment settings alone does
not relax persisted limits. Do not remove/reset quota records to increase limits.

## Admission and storage contract

Service and browser sockets share leased connection slots across nodes. Existing
process/account/entity socket ceilings still apply. Normal teardown releases a
slot with a bounded independent context. Failed upgrade releases it as well.
After process loss or an uncertain acquire/release, capacity stays conservatively
reserved until lease expiry: transport lifetime plus bounded invocation, write,
and idle margins. Each frame checks its lease before dispatch. Leases are not
renewed indefinitely; native acceptance must check clocks and lease-boundary
behavior across nodes.

Grant issuance and invocation attempts have shared 60-second fixed windows,
anchored by the first successful ledger update after the previous window ends.
A boundary can admit a burst; this is not a rolling-window limiter. Replays count
as invocation attempts. HTTP invocation, both socket paths, restore execution and
alarm delivery use invocation admission before customer execution. Read-only
inspection/export and administrative receipt recovery are not invocation attempts.
Quota denials do not dispatch customer code.

On first invocation, the ledger reserves the entity's **entire configured
per-entity retained-byte cap**, independent of its current actual size. Further
calls reuse that reservation. A node configured with an entity cap above the persisted policy fails closed
until a controlled policy migration; it cannot quietly enlarge the execution
cap. Existing larger reservations remain as high-water charges when caps lower. Reservations remain across restarts, receipt GC,
cohort reclamation, failed guest execution and window expiry. This deliberately
trades utilization for a safe admission decision without a cross-object commit.
The opt-in [retirement workflow](durable-entity-retirement.md) can release an
active slot and reduce its byte charge after verified cleanup, while preserving
required recovery bytes and a permanent identity fence. There is no automatic
entity-reservation release. The opt-in migration workflow
inventories historical entities and verifies reservation coverage. Legacy scopes allow at most 64
distinct active reservations. The opt-in [sharded registry](durable-entity-registry.md)
raises the bound to 1,024, subject to hash-shard distribution and the same byte cap.
Existing reservations continue admitting attempts when the tenant byte cap is
lowered; new/increased reservations and grant issuance must fit the tighter cap.

Quota-enabled grants are stored **inside** the same CAS record as their counters,
reservations and connection leases under `tenant-quotas/v1/`. This makes grant
creation and byte admission atomic, avoiding an orphan grant/reservation race.
Grant JSON bytes, including digest and metadata, share the retained-byte budget
with entity reservations. At most 256 unexpired grant records and 512 KiB of grant
JSON are allowed per scope; the whole quota record is bounded at 2 MiB. Revocation
keeps a record until expiry. Expired grants are removed in the next successful
ledger mutation, and their current-record bytes become available in that same
commit. Expiry never returns a credential to service.

Each accepted mutation rewrites the shared ledger, so high contention can return
503; these ceilings do not promise that throughput or a low-latency SLO.

The storage budget covers admitted entities' reachable committed storage caps
and current grant JSON. It excludes quota control metadata, provider version
history, orphan objects, backups, and other storage excluded by the existing
per-entity accounting contract. It is conservative admission accounting, not
provider billing or a discovery of all historical tenant data. Before enabling
on existing data, operators must inventory/backfill or isolate a fresh scope;
unregistered dormant legacy entities are not automatically accounted. Native
acceptance remains a prerequisite to production enablement.

## Backpressure and visibility

HTTP denials return `429 durable_entity_tenant_quota`, the numeric `limit` and
`observed`, and a budget in the detail. Temporary rate/connection/record ceilings
include `Retry-After`; socket replies include retry seconds in their Problem
body. Retained-byte/entity-reservation ceilings need operator capacity planning
rather than an automatic retry loop. Storage outages, corrupt records, exhausted
CAS contention and uncertain writes fail closed with 503. An uncertain write may
charge admission, but cannot authorize dispatch, a WebSocket upgrade or return a
grant credential; it is never blindly retried or rolled back.

Owner read scope:

```http
GET /v1/apps/my-app/entities/quotas?namespace=rooms&key=document:123&env=production&platform_tenant_id=<uuid>
```

Use the normal exact selectors for one entity in the quota scope. The response
has `limits`, `connections`, `grants`, `grant_bytes`, `entity_reservations`,
`reserved_bytes`, `grants_issued`, `invocations`, `window_ends`,
`stored_grant_bytes`, and `expired_grants`. Grant/reserved usage projects the
expiry filtering in the next successful mutation; stored bytes and expired-record
counts show pending compaction without pretending a read deleted records. It is a
read-only snapshot, excludes credentials and lease identities, and can diagnose
suspended tenants. A missing record reports zero admissions without creating it.

## Browser compatibility and rollout

Quota-mode grants have an opaque scoped locator in `grant_id` and the credential.
The updated `@gregale/sdk-node/durable-entities/browser` connector accepts both
legacy and scoped credential formats. Applications must treat IDs/credentials as
opaque rather than assuming a UUID-only grant ID. Quota-enabled APIs reject old
standalone grants; mint fresh grants after enabling. All serving nodes for an app
must use the same quota mode, backend, bucket and storage cap before accepting
traffic; drain old nodes/connections during rollout. A quota-disabled node does
not enforce shared quotas. Mixed policy values only tighten recorded limits on
quota-enabled nodes.

The old `session-grants/v1/` lifecycle cleanup rule remains appropriate for legacy
standalone records. **Never apply a TTL/delete rule to `tenant-quotas/v1/`**: doing
so resets rate/connection authority and loses retained-state reservations. Keep
quota records private and redact credential-bearing WebSocket protocol headers.
Do not count on provider lifecycle rules to free shared reservations.

## Independent native acceptance

Verify two nodes cannot exceed a single scope's connection/rate/storage budgets,
while another tenant remains independent. Cover concurrent grant mint/revoke,
expired-grant compaction, failed upgrades, process death, bounded lease reclaim,
clock skew, provider outage and committed writes with lost acknowledgements.
Check receipt retries retain exact identities, quotas precede restore/alarm guest
execution, and no expired grant returns after a CAS conflict or restart. Inventory
legacy entity storage before migration and verify prefix cleanup/version-history
policy independently. Source fixture tests do not establish these native results.

## Source qualification

[CI run 38134656670](https://github.com/poyrazK/faas/actions/runs/38134656670)
passed for source `259962cd74d907089ea06bc33e7e356f1de8b19b`.
The Node build, 16 focused SDK tests, packed-starter acceptance, four Go
race-tested durable packages (including eight new quota tests), focused CLI
checks, and 20 top-level socket/quota/alarm/restore API tests passed. API fixture
execution took 2.026 seconds; the cold compilation accounted for the long job.

Coverage includes shared-bucket concurrent acquisition by separate managers,
crash-expired/released leases, isolated tenant scopes, namespace/key rotation,
fixed-window rate admission, grant byte accounting/expiry/revocation, read-only
compaction visibility, persisted policy tightening and mismatched entity caps,
legacy credential rejection, and uncertain writes that never dispatch. Actual
HTTP/WebSocket fixtures verify shared service/browser capacity, scoped grant
revocation, numeric backpressure, and invocation/restore admission.

An earlier cold-cache CI build terminated with SIGTERM before reporting API test
results. The successful rerun uses a 3 GiB compiler budget, retains race coverage,
and bounds verbose API fixture execution to two minutes. No local tests/builds
or native host/bucket acceptance were run. Production preview enablement and
existing-data migration remain gated on the independent acceptance above.
