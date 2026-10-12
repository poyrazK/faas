# ADR 993: Immutable registry shards under the quota root

Status: implemented behind an operator flag; native acceptance pending.

The inline durable quota ledger caps active identities at 64 and permanent
retirement receipts at 256. Moving receipts into independent mutable objects
would split the admission fence from reclamation and could let stale admission
resurrect a credited identity. Object storage offers no multi-object transaction.

Use schema 4 with 16 content-addressed immutable registry shards, bounded to
64 active reservations and 256 permanent retirement receipts each. Publish their
references through the existing quota CAS. Readers authenticate and hydrate the
complete committed registry before applying existing admission, migration,
policy or retirement logic. Every write path uses the registry serializer.
Older quota readers reject schema 4. Tenant-wide byte and rate caps remain
central and unchanged; this expands bounded identity cardinality while retaining
one linearization point, rather than distributing quota authority.

Migration requires an exact observed root revision, managed policy, completed
inventory migration, drained connections and no held retirement. Uploaded shards
are inert until the root CAS. Persist the migration operation, source revision,
actor and time for exact replay. Scope-bound paginated cursors reject any root
revision change. Permanent fences and recovery-byte charges are never evicted.

Consequences: at most 1,024 active and 4,096 retired identities per scope, with
hash-distribution and byte bounds; up to 16 additional object reads on cold hydration;
retained historical metadata and a central CAS bottleneck. Production rollout
requires schema-aware fleet deployment and native provider acceptance. A bounded
node-local cache now reuses authenticated immutable shards while every
quota root remains fresh. ADRs 978 and 979 add epoch-fenced metadata collection
with current-epoch writes;
caching confers no independent quota or ownership authority.
