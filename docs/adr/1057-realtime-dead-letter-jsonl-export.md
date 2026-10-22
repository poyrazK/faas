# ADR 1057: Read-only dead-letter JSONL export

Status: accepted for preview implementation; runtime/UI validation pending.

## Decision

Add account GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/
{group}/dead-letters/export. Require the existing preview gate, account/app/endpoint
ownership, read scope and MFA checks. Scoped workers have no export endpoint and
are rejected at the store boundary. No extra permission, webhook or external storage
configuration is introduced.

Export a JSONL attachment: the first line is a version-1 manifest, followed by typed
dead_letter records. Include group identity, numeric sequence, dead_since, attempt
count, safe failure code, last fencing token (nullable for legacy failures), deadline
and its validity, frozen ordering key/priority, nominal dead-letter expiry, and the
stored message with base64 data and metadata. No claim token, token hash, credential
secret/ID or worker identity is included. Exported fencing context is diagnostic;
it does not grant a lease or authorize external writes. Timestamps are UTC.

## Pagination and consistency

Default 32 records, maximum 128, with one extra row to detect has_more. Order by
numeric sequence. Support exclusive after_sequence and repeated exact failure_code
filters (at most 32 unique printable ASCII codes). First page captures group UUID,
inclusive assigned-sequence boundary and database transaction time as snapshot_at.
Resume with all three plus next_after_sequence, preserving filters. Reject partial
resume triples, duplicate scalar parameters, invalid decimals/times, future cutoffs,
and unsupported limits. Zero/equal sequence boundaries support empty/final pages.
Recreated group identity or a watermark beyond a rewound current assignment boundary
returns 409 realtime_dead_letter_export_changed. A missing/deleted group returns 404.

Each page runs in a repeatable-read, read-only transaction. Group lookup and rows
share its database snapshot, and rows must still be dead with dead_since at or before
the original cutoff. No group lock, claim/credential use updates, maintenance/pruning,
replay, lease, counter, timeline or retention extension occurs. All new SQL is SQLC.

This is a resumable live export, not an immutable multi-page database snapshot.
Retention cleanup, replay, resync and deletion can remove rows between pages. Later
failure transitions (even for older sequences) are excluded by the original cutoff.
A failure observation timestamp is not a commit timestamp, and source transitions and
concurrent commits cannot establish an immutable membership set across requests.
An exhausted page does not prove there were no failures historically. For ongoing
external archival, start a fresh export from zero and deduplicate downloaded records
by group UUID, sequence and dead_since; a sequence may fail again after replay.
Export does not provide import or republish/recovery automation.

## HTTP and dashboard

Prepare all lines before headers, capped at 2 MiB. Return application/x-ndjson,
a safe attachment filename, Content-Length, private/no-store and nosniff. The manifest
contains count/has_more and cursor fields; X-Realtime-Export-* headers mirror them,
and Link rel=next preserves the range/cutoff/filters/limit. Empty results still return
a manifest. Clients must verify a complete response and exactly count record lines
before advancing; an interrupted download cannot be treated as a complete page.

The inspector offers Download dead letters as JSONL for a selected valid group,
using the maximum page size. The current queue holds at most 128 outstanding snapshots,
so one page covers its visible failures. Its native dashboard GET adapter delegates
to the same export handler through MFA/read-scope checks and endpoint ownership.
No payload is placed in a URL, and downloads do not use bearer secrets in query strings.
The file can be archived outside Gregale beyond the queue's retention lifetime.

## Rollout and verification

No runtime schema change or new migration. The SQLC schema projection adds existing
retention field for the generated read queries. Node/Python generated SDKs
expose the export operation as a binary download and manifest/record schemas; use
Python sync_detailed/asyncio_detailed to read pagination headers. Node callers can
read the Blob's manifest for cursor fields. A guarded Python generator-only
adaptation maps this standalone NDJSON binary response to opaque bytes so the pinned
generator emits its 200 File parser; the canonical wire content type stays NDJSON.

Go/Node builds, SDK generation, Python AST, OpenAPI parity, SQLC regeneration parity
and whitespace checks only. Runtime tests and inspector rendering are deferred by
request. No migration execution or deployment is performed.
