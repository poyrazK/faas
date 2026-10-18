# ADR-1676: Incremental note change feed

Status: Accepted

## Context

Keyset pagination reads current rows, so it cannot enumerate deletions or reliably
detect every update between application refreshes. Mutation receipts recover an
individual operation; they do not notify another client about committed changes.
The first feed covers the subject-scoped notes starter through existing RPC and
RLS, preserving the runtime's on-demand database connections.

## Decision

Append migration `0015_note_change_feed.sql`. Capture metadata after every note
insert/update/delete in the same SQL transaction: subject, per-subject position,
operation, note id and database-managed version. No note body or historical row
snapshot is retained. Owner reassignment/key changes emit an old-identity delete
and new-identity insert. Related-table changes are outside this feed.

Capture statement transition tables and allocate a contiguous range once per
subject per statement, in bytewise subject order. This avoids repeated counter
updates for bulk writes. Allocate positions by updating a transactional counter
row for the subject.
Its lock lasts until transaction completion: another writer cannot commit a
higher position first. Aborts roll back the counter, event, pruning and business
change together. Different subjects have independent streams. This serializes
writes within one subject and can add lock waits/deadlocks to ordinary database
transactions, which must handle existing rollback/error semantics. Do not use
sequence values, transaction ids or wall clocks as commit-order cursors.

Keep the latest 10,000 events per subject. Capture advances a discarded-through
watermark and deletes its old prefix atomically under the stream lock. Preserve
the counter/watermark after all notes disappear. Guard triggers refuse ordinary
manual rewinds, event edits and live-event deletion. There is no TTL guarantee;
high write volume can invalidate a cursor quickly. Storage remains proportional
to subjects with history; these stream rows are not automatically removed.

Add STABLE SECURITY INVOKER `start_note_sync()` and
`read_note_changes(after_cursor,page_size)`, both approved through normal owner
RPC grants. Readers use the statement snapshot without row/advisory locks and
work in READ ONLY, including subjects without any stream row. Private metadata
tables require both the JWT subject and an internally restored scope marker.
Direct application CRUD cannot forge events or advance counters.

Opaque v1 cursors contain a random migration-instance epoch, a SHA-256 binding
of epoch and JWT subject, and a decimal position. They are not signed credentials
or authorization tokens. RLS and verified claims remain authoritative. Reject
wrong-instance/subject, malformed and future positions with PT400; positions
below the watermark return PT410/note_change_cursor_expired. Missing/inconsistent
history fails with PT503/note_change_feed_unavailable rather than skipping it.
Positions stay decimal strings on the wire. Fixed central bounds are
100 events per page, 10,000 retained events and 128 bytes per cursor.

## Bootstrap and application semantics

Capture a checkpoint before scanning current notes by ascending id into a fresh
staging cache. Then read events after that checkpoint and re-fetch each affected
id through current RLS, including ids with delete events. Upsert returned rows
and remove ids absent from the result. Values may be newer than the event page;
the feed invalidates cached rows rather than replaying old values. Persist a
page cursor only after applying the entire page. Continue until has_more=false,
then poll explicitly. The SDK adds no background work or automatic resync.

If history expires during scanning/catch-up, discard the incomplete staging
attempt and restart from a fresh checkpoint. Clear/bind caches to API and subject
on identity changes. A page can split one committed multi-row transaction; this
is eventual cache synchronization, not an atomic client snapshot or audit log.
Epoch rotation is required for a deliberate feed reset. History-losing restores,
owner DDL/TRUNCATE and altered authorization/schema remain owner responsibilities.

## Validation

Check central SQL/client limit parity, generated contracts and typed helpers.
Run actual PostgreSQL/PostgREST with two JWT identities and the packed client.
Verify ordered lifecycle events, post-deletion markers, bounded pages, private
tables, cursor binding, read-only empty streams, late-trigger rollback, actual
blocked writers at first head creation and after rollback, independent subjects,
owner reassignment, concurrent bootstrap edits and retention boundary resync.
Retain the existing starter migration, mutation recovery, browser and schema
evolution acceptance checks.
