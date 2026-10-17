# ADR-1355: Journal-backed logical usage counters on owned object nodes

Status: Accepted

## Context

ADR-1354 indexes S3 listings and multipart expiry. Ordinary gateway accounting
still enumerates every object, and fenced capacity recovery uses the same source
inventory protocol. Repeated scans become expensive as catalogs grow. Fast usage
observations must preserve generation fencing, multipart accounting, outstanding
reservations and the stronger evidence needed to reclaim uncertain capacity.

## Decision

Extend the existing private `.catalog-index.db` projection with sealed per-bucket
and node-wide logical totals: committed object count and bytes, plus active or
frozen multipart upload count, selected part count and bytes. Generation-scoped
object membership and per-upload part membership record immutable identities and
previous sizes. All arithmetic rejects negative totals and overflow. Source
catalog records and their journal remain authoritative; these are current logical
observations, not physical disk usage or cumulative billing meters.

Apply each projection delta in the same bbolt transaction as its existing index
update, after successful source publication and before releasing the catalog
lock. Replacement uses the previous selected size, deletion subtracts the selected
object, and idempotent replay does not double charge. Session completion, rejection,
abort and expiry remove all of that session's logical usage in one aggregate
update. Unreachable part-index entries can remain until the next startup rebuild;
terminal IDs cannot be reused. This avoids scanning thousands of parts inside a
terminal session transition. Physical part reclamation retains ADR-1354's separate
bounded queue and does not change logical totals a second time.

A projection error preserves an already durable source mutation and disables
indexed operations. Reopening the primary discards and rebuilds the projection
from recovered source records, validating referenced object and active part file
types and lengths. Object, upload and part build transactions contain at most
128 records. Followers do not build this local projection. A serving primary
opened after promotion rebuilds it. No node format or replication command changes
are needed. Backups still omit the derived database, and source-only backup and
promotion verification do not depend on counters or modify a backup artifact.

Expose a private operator SigV4 GET on
`/<bucket>?gregale-inventory=bucket-counters`. It returns all five measured totals,
physical bucket name and generation, catalog snapshot, observation time and last
healthy audit time in one response. Missing, null, duplicated, unknown or invalid
proof-bearing fields fail client validation; complete zero totals remain valid.
Pending source decisions, followers, fenced nodes, unavailable indexes and stale
or unhealthy audit evidence cannot provide an accepted counter observation.

Startup source recovery and validated index construction establish the initial
audit baseline. The existing minute inventory worker independently checks source
objects, sessions, parts and physical references, then compares their identities
and totals with per-bucket, per-upload and global projections. Audits use bounded
streaming state rather than a map of every bucket or key. Correctly sealed but
incorrect totals and membership drift still fail. Drift latches the index
unavailable until a source rebuild. Physical source damage makes the audit
unhealthy; a successful later audit can reestablish health after repair when the
projection itself remains sound. Incomplete or canceled audits never refresh the
previous healthy observation. Counter reads require a healthy audit no older than
the existing 900-second inventory freshness limit. Audits retain their full scan
cost and catalog read lock; fast reads do not verify every file on each request.

Operator backend configuration explicitly opts ordinary accounting into this
protocol with `owned_node_usage_counters: true`, requiring
`owned_node_receipts: true`. It defaults to false and does not change the immutable
backend fingerprint. An enabled backend against an unsupported or unhealthy node
fails closed, without silently substituting a different observation protocol.
Managed providers receive no owned-node counter query. Each attempted observation
uses the existing request-budget recorder before dispatch.

Only ordinary `ScanObjectInventory` uses this optional fast path. Its existing
central finish operation preserves outstanding grants and unknown-write holds.
Fenced capacity reconciliation continues to enumerate source records and validate
physical references, even when counters are enabled. The counter response cannot
settle a receipt or authorize release of an uncertain write. Multipart totals
remain separate from committed object totals; existing central multipart journals
and reservations continue to govern multipart admission.

Cached `faas_object_node_usage_counters_*` metrics expose readiness, logical
object/byte and multipart totals, last audit time, successful queries and detected
drift. They carry no bucket, key, receipt or credential labels and perform no
scrape-time database or filesystem reads.

## Consequences

Ordinary opted-in usage observations use a bounded number of point reads instead
of enumerating the catalog. Updates add local metadata and fsync cost. Startup,
periodic source audits, physical usage diagnostics and fenced capacity recovery
remain proportional to source catalog size. bbolt can retain obsolete part rows
and its file high-water allocation until a later rebuild.

Operators upgrade nodes before enabling the backend option and monitor audit
freshness and readiness. Turning the option off restores the existing ordinary
inventory protocol without changing placement. Repair a damaged projection by
stopping the daemon and reopening with current binaries; source corruption still
requires source repair or a verified restore. These counters do not establish
power-loss durability, sharding or independent-host failure qualification.
