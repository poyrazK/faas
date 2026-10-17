# ADR-1334: Owned-node inventory, capacity reconciliation and disk observations

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-16
- Related: [ADR-1331](1331-owned-object-storage-first-node.md), [ADR-1332](1332-owned-object-storage-write-receipts.md), [ADR-1333](1333-owned-object-storage-multipart.md), [ADR-122](122-canonical-metrics-listener-shape.md)

## Context

The owned node can durably publish objects and complete multipart uploads. It
also retains receipts and session tombstones independently of current object
bytes. Operators need to distinguish committed capacity, unfinished parts,
temporary files and retained data, and diagnose disagreement between catalog
references and files. Apid already owns inventory observations and fenced
capacity reconciliation; it has no authoritative per-key object catalog.

The existing monthly usage report requires cumulative byte-hours, requests,
egress and provider cost. An instantaneous disk observation cannot supply those
meters or authorize a customer charge.

## Decision

Add a private SigV4-authenticated GET extension on the existing loopback node:

- `/?gregale-inventory=buckets` enumerates physical buckets and incarnations.
- `/<bucket>?gregale-inventory=objects` enumerates committed catalog identities,
  sizes, ETags, stored SHA256 and modification times.
- `/<bucket>?gregale-inventory=uploads` enumerates active/frozen multipart
  sessions, selected part counts and sizes, and creation/expiry times.
- `/?gregale-inventory=usage` returns a read-only node audit and disk sample.

Pages use `max-keys` (1–1000) and opaque `cursor`. Operator credentials have
node-wide access. Customer gateway requests never select this private protocol.
Reject duplicate/unknown operation parameters, mixed receipt selectors,
conditional/ranged requests and object-key targets. Responses use JSON and
`Cache-Control: no-store`, with a bounded 4 MiB document size.

## Coherent inventory and accounting

Every page carries the physical bucket incarnation, a catalog revision and an
observation time. The revision combines a fresh process UUID with an in-memory
publication counter. Catalog writes, including uncertain publications, and
catalog deletions invalidate continuations. Restart always changes the process
identity. Mutations elsewhere on this node can also invalidate a scan; this is
deliberately conservative. No disk-format change is required.

Each scan retains at most a page plus one candidate while holding the catalog
read lock. It validates every referenced object's file type and exact length.
Multipart summaries validate selected part metadata and referenced file lengths.
The cursor binds scope, incarnation, revision, boundary and initial observation;
it expires after the existing inventory freshness window. Mutation/restart
returns `InventoryChanged`; a pending receipt or damaged reference cannot yield
a successful capacity page. A cursor is an identity boundary, not authentication.

The opted-in S3 adapter uses this extension in apid's existing complete-inventory
function. Managed S3/GCS behavior is unchanged. Decoding rejects absent/null or
duplicate fields, wrong scope, invalid identities, unordered keys, empty
continuations, cursor cycles, changed revisions/incarnations/observation times,
oversized responses and exhausted page budgets. Every HTTP attempt is metered;
there is no automatic HTTP retry or fallback to ordinary listing for an owned
node. Any failure discards the complete partial total.

Only a complete scan reaches `FinishObjectInventory` or the existing fenced
capacity job. Ordinary observations do not release accepted write/part holds.
Capacity jobs retain their write fences and readiness checks before rebasing;
their existing before/after/reclaimed fields report aggregate capacity drift.
Inventory does not establish an uncertain write's outcome. Per-key historical
outcomes still require the original PUT or multipart receipt.

## Read-only disk audit

The audit checks bucket, object, upload and receipt metadata, selected blob
references, the format marker and filesystem entry types. It reports missing or
wrong-sized referenced files, invalid catalogs, orphan uploads, pending intents,
unreferenced blobs and unrecognized files. It never follows symlinks or special
files, settles a receipt, deletes bytes or changes accounting. Error findings
mark the catalog unhealthy; unreferenced files are warnings. At most 100 issue
details are retained, with complete counters and a truncation flag.

Report logical committed bytes separately from selected unfinished part bytes.
Also expose apparent regular-file bytes, allocated file blocks, metadata,
receipts, staging, trash, unreferenced blobs and filesystem capacity/available
space. Trash and receipt sizes are subsets of the file categories, not additive
customer meters. Staged streams can grow/disappear outside the catalog lock;
physical consumption is explicitly a sample. File allocation excludes directory
blocks and unlinked files held by open readers. Filesystem available space also
reflects other users of that filesystem.

An audit validates references and lengths; it does not rehash every committed
blob. Detection of same-size content corruption remains the integrity-scrubbing
milestone. Existing startup recovery and its cleanup rules remain unchanged.

## Operational metrics

Run an audit immediately at daemon start and every minute, bounded by the
existing 45-second capacity-inventory timeout. Hold no results from a partial
audit. A failed/unhealthy audit does not advance the last healthy timestamp.
Expose cached aggregate measurements on loopback `127.0.0.1:19001/metrics`,
configurable with `--metrics-listen` (empty disables the HTTP listener). Apply
ADR-122's listener timeouts/header budget and stop both listeners and workers
before closing the store.

Metrics include `faas_object_node_inventory_bytes{kind}`,
`faas_object_node_inventory_items{kind}`, `faas_object_node_catalog_healthy`,
`faas_object_node_inventory_last_success_timestamp_seconds`,
`faas_object_node_inventory_scans_total{outcome}`, active writes/reserved bytes,
HTTP attempts by bounded method/status class, duration and written response
bytes. No bucket, key, receipt or credential becomes a label. Request/response
counters reset with the process and are operational observations, not durable
billing evidence. Scrapes use cached data and never scan disk.

## Validation and boundaries

Tests cover upload/replacement, empty objects, multipart replacement/completion,
abort, expiry, deletion and restart. Continuation tests cover all publication
paths, uncertain fsync outcomes and restart. Audit tests verify missing blobs,
retained files, bounded diagnostics, cancellation and symlink handling without
deleting evidence. Integration crosses signed HTTP, the actual node, adapter,
apid inventory and fenced capacity rebase. Invalid/partial scans preserve the
previous accounting observation and unsettled admission holds.

This remains a private single-node prototype. Catalog scans cost O(N) per page
and hold a read lock; sustained mutations or a catalog beyond the bounded scan
budget can keep inventory stale. Metadata indexing/checkpointed scans, durable
customer usage meters, safe receipt retirement, integrity scrubbing,
backup/restore, replication and failover remain separate milestones. No schema
migration, customer API, deployment enrollment or billing source is introduced.
