# Sharded durable entity registry

The optional registry moves active reservations and permanent retirement receipts
out of the quota object's inline arrays into 16 private immutable bucket shards.
The quota root still serializes admission, rates, connections, grants, policy,
retirement fences, recovery-byte charges and reclamation. It publishes all shard
references in one conditional write. No SQL database is involved.

Each shard holds at most 64 active reservations and 256 permanent retirement
receipts, selected by the first hexadecimal digit of the entity identity hash.
A migrated scope therefore has upper bounds of 1,024 active reservations and
4,096 retirement receipts, subject to distribution and the existing tenant byte
budget. A full shard can block admission or retirement before the global bound.
The existing connection, rate, grant, entity-byte and scope-byte limits apply.
Retired identities cannot be reused. This is bounded cardinality expansion;
it does not provide millions of objects or independently writable quota shards.

## Enable one scope

Deploy schema-4-capable quota readers to every participating node first. Set
`FAAS_DURABLE_ENTITY_REGISTRY_ENABLED=1` only in the acceptance environment.
The flag requires managed quota policy and its migration/tenant quota prerequisites.
It enables the operator routes; it does not automatically upgrade any scope.
Turning it off hides management routes but does not downgrade a migrated scope.
Older binaries reject schema 4 and cannot participate after migration.

Use the existing exact selectors (`namespace`, `key`, environment and optional
platform tenant) on these application routes:

- `GET /v1/apps/{slug}/entities/registry`: read-only listing and current revision.
- `POST /v1/apps/{slug}/entities/registry/enable`: body
  `{"operation_id":"<fresh UUIDv4>","expected_revision":"<GET revision>"}`.

Read requires app read access; migration requires deploy write access, normal
recovery eligibility and MFA. Responses use `Cache-Control: no-store`.
The scope needs managed policy, completed accounting migration, matching node
limits, drained connections and no pending retirement tracking. A concurrent
quota write invalidates the expected revision. Immutable shard upload alone
changes no admission authority. The final root CAS upgrades the complete ledger
snapshot without a partial backfill window. Replay the exact operation and
original expected revision after inspecting an uncertain outcome; changing the
request conflicts. The original actor and timestamp are retained.

## Listing and failure behavior

Listing returns up to 64 entries, opaque entity identity hashes, active/retired
state, charged bytes and any retirement receipt. It lists the quota registry,
not every unaccounted legacy manifest in the bucket. A held retirement appears
once as retired while its full reservation remains charged. Completed retirement
charges only its retained recovery bytes.

Pass `next_cursor` as the next request's `cursor`. Cursors bind the exact scope
and quota-root revision. Any root write, including admission or a read-triggered
operator replay that commits pruning, expires an old cursor with 409; restart
listing. Pages therefore never silently mix snapshots. Busy scopes may require
a quiet interval for a complete listing. Hashes are not original namespace/key
names and do not replace entity-level inspection.

Readers verify shard hashes, scope, shard index, provider version, bounds,
uniqueness and retirement/reservation consistency. Missing or corrupt referenced
shards fail closed. Uncertain shard writes cannot publish a root; uncertain root
writes require inspection and exact replay. A definite root conflict makes an
admission retry read the newly committed retirement fence. Credit and its receipt
remain atomic and replay-safe across schema upgrades and policy changes.

Reads currently hydrate all 16 bounded shards. Unchanged shards are reused;
changed shards use content-addressed conditional creates. The single quota root
remains a contention point, and provider latency can increase. Superseded and
unpublished shard versions are retained conservatively, are control-plane storage
outside the customer reservation budget, and have no automatic garbage collector.
Never delete them based on one root scan: readers and concurrent writers may
still reference an older snapshot. Native acceptance must measure read latency,
metadata growth and the control-plane storage cost before release.

## Validation and release gate

Remote CI covers legacy migration, permanent fence/credit preservation, more
than 64 active reservations, stale and cross-scope cursors, unknown shard/root
acknowledgements, corruption, shard and tenant-budget bounds, and admission
interleaved with retirement. Operator API coverage checks authorization, exact
replay, listing, disabled routes and configuration prerequisites.

Native GCS/KVM acceptance remains required before production enablement. The
acceptance agent must verify conditional create/CAS acknowledgements, stale
reader rejection, shard visibility/corruption fail-closed behavior, multi-node
admission racing retirement and migration, preserved recovery charges, cursor
expiry, cold-read latency, and safe rollback to management disabled while keeping
schema-4-capable binaries. No production flag or deployment is changed here.
