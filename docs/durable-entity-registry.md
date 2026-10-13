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
and quota-root revision. Any root write, including admission or an operator
replay that commits pruning, expires an old cursor with 409; restart
listing. Pages therefore never silently mix snapshots. Busy scopes may require
a quiet interval for a complete listing. Hashes are not original namespace/key
names and do not replace entity-level inspection.

Readers verify shard hashes, scope, shard index, provider version, bounds,
uniqueness and retirement/reservation consistency. Missing or corrupt referenced
shards fail closed on cache misses. Uncertain shard writes cannot publish a root;
uncertain root writes require inspection and exact replay. A definite root conflict makes an
admission retry read the newly committed retirement fence. Credit and its receipt
remain atomic and replay-safe across schema upgrades and policy changes.

Reads hydrate all 16 bounded shards through a node-local verified-shard cache.
Unchanged shards are reused;
changed shards use content-addressed conditional creates. The quota root is read fresh on every operation and remains a contention point.
Cold reads and evictions still require provider reads. Superseded and
unpublished shard versions are retained conservatively, are control-plane storage
outside the customer reservation budget. The opt-in
[epoch-fenced cleanup workflow](durable-entity-registry-cleanup.md) can collect
strictly older epochs while current-epoch admission continues; an optional bounded
worker can continue explicitly started operations without starting new ones.
An active-operation index reduces routine root scans; periodic reconciliation
repairs missing hints while roots remain authoritative.
Never delete them based on one root scan: readers and concurrent writers may
still reference an older snapshot. Native acceptance must measure read latency,
metadata growth and the control-plane storage cost before release.

## Validation and release gate

Remote CI covers legacy migration, permanent fence/credit preservation, more
than 64 active reservations, stale and cross-scope cursors, unknown shard/root
acknowledgements, corruption, shard and tenant-budget bounds, and admission
interleaved with retirement. Operator API coverage checks authorization, exact
replay, listing, disabled routes and configuration prerequisites.

Source commit `2f30387a42a6e2e164e4e94b9104d16b212b4c3a` passed
[remote CI 38160932763](https://github.com/poyrazK/faas/actions/runs/38160932763):
four durable Go packages under the race detector, seven registry tests and seven
cache tests (including three eviction subcases),
focused CLI/generated contract checks, 29 focused API race tests (including two
registry tests using the cache), 16 Node SDK tests and packed-starter acceptance. No local build,
test or native run was performed.

Native GCS/KVM acceptance remains required before production enablement. The
acceptance agent must verify conditional create/CAS acknowledgements, stale
reader rejection, shard visibility/corruption fail-closed behavior, multi-node
admission racing retirement and migration, preserved recovery charges, cursor
expiry, cold-read latency, and safe rollback to management disabled while keeping
schema-4-capable binaries. No production flag or deployment is changed here.

## Bounded immutable-shard cache

Tenant quota startup attaches a cache to its exact private object store. It holds
at most 256 entries with an 8 MiB retained-memory accounting budget and allows
at most 16 simultaneous shard fetches. Accounting charges four times serialized
body size plus 1,024 bytes per entry to cover decoded data and entry overhead;
it is a conservative cache accounting limit, not a process RSS limit. Per-call
slice copies and bounded in-flight buffers are separate. The constructor also
supports smaller test budgets and rejects bounds above 64 MiB, 4,096 entries or
64 concurrent fetches. A nil cache keeps uncached library behavior.

Cache keys include the complete private shard path: scope, shard index and
content hash. Only bodies with a provider version, matching hash/scope/index,
bounded size/counts, valid unique reservations and valid retirement receipts are
retained. Aggregate root validation still runs for every hydration, including
operation uniqueness across shards. Readers receive separate entity/retirement
slice copies, so sorting, pruning, credit or policy mutation cannot change cached
state. Quota roots and ownership/manifests are never cached. A warm cache cannot
bypass an unavailable root or a newly committed retirement fence.

Concurrent requests for a missing shard share one synchronous provider fetch.
Waiters use their own cancellation/deadline. Canceling a waiter does not cancel
the leader. If the leader's context ends, live waiters retry under their own
contexts; errors are not retained. Requests for other shards wait cancelably
when the concurrent-fetch bound is reached. LRU eviction enforces both retained
byte accounting and entry limits. Oversize valid shards can be served without
being retained. No background goroutine survives the provider call.

When all referenced hashes are warm, a hydration reads only the current quota
root from the provider. A changed hash forces verification of that shard; the
other immutable shards remain reusable. This reduces repeated provider GETs,
not root writes or contention. Cache hits deliberately do not recheck an
immutable object's provider version, deletion or corruption: the previously
verified body remains valid for the hash asserted by the fresh root. Metadata
retention and provider-health checks must not assume warm caches detect provider
faults. Restart/cold-cache acceptance must verify missing/corrupt references fail
closed. Provider metadata collection is available through the separate opt-in
epoch-fenced cleanup workflow, which includes the epoch in cache keys.

Remote regression coverage exercises read counts, fresh-root failure, changed
hashes, isolated caller mutations, warm admission racing retirement, byte/entry
LRU bounds, oversize entries, concurrent fetch coalescing, leader/waiter
cancellation, fetch-cap waiting, corruption recovery and scope isolation. Native
acceptance must measure warm/cold GET counts and latency, heap use under cache
churn, concurrent cancellation and multi-node retirement with independently
warm caches. No production flag or placement changes are part of this cache.
