# ADR-588: Local spool collection after confirmed remote log expiry

Status: Accepted; internal rollout pending native qualification
Date: 2026-10-02
Extends ADR-587; replaces its deferred local backlog cleanup policy.

## Context

A permanently expired local partial or upload file cannot pass the remote
publication fence. Keeping it forever consumes the daemon's retry capacity. The
binding's monotonic expiry floor does not prove that a specific day's remote
object was collected: retiring a newer reservation also expires older days that
were never reserved. Missing catalog rows alone are insufficient deletion proof.

## Decision

After successful remote DELETE or confirmed absence, apid records an exact
`(instance_id, archive_day)` collection receipt in the same transaction that
removes the daily reservation and fragment receipts. The receipt references the
immutable ownership binding and survives instance/deployment garbage collection.
PostgreSQL rejects receipt creation without a retired reservation, forbids receipt
mutation, and refuses a downgrade that would discard receipts. Final ownership
removal cascades them. Metadata failure rolls back the receipt and retains the
reservation for retry; the database trusts the collector's remote DELETE result.

A shipper whose publication is refused by expiry reacquires the app publication
lock and requires that exact receipt, the retained owner/region binding, and the
absence of a pending daily reservation. Expiry alone never authorizes local
collection. Unknown instances, unavailable catalog reads, failed remote DELETE,
failed metadata collection and older never-reserved days retain their backlog.

Under the process's spool mutex, the shipper atomically writes and fsyncs a small
`log-YYYY-MM-DD.jsonl.collected` witness, then fsyncs its containing directory
before removing payloads. The directory components below the configured private
root must be real directories; cleanup refuses symlink components and nonregular
payloads. It closes the active daily writer without flushing explicitly collected
buffered lines, removes only that day's partial, upload, committed, pending gzip
and gzip files, and releases their pending capacity. Other days and owners remain
available. The existing capacity ceiling is unchanged.

The witness remains after cleanup. A writer refuses that collected day before
checking capacity; the live handle and buffer share the cleanup mutex. A daemon
restart reads the witness before opening another partial. Vmmd treats a delayed
collected frame as an expected retention outcome without a spool failure warning.
A later day can still open normally.

If cleanup is interrupted, the witness authorizes retries without a database read,
including after ownership removal or a catalog outage. The file scan includes
compressed remnants only when a witness is present; witness-only days do not cause
continuous catalog queries. Damaged witnesses retain payloads and surface an error.
Each daemon owns a private spool root; this mutex does not coordinate open file
descriptors across processes. Protect the root with the existing daemon filesystem
permissions. Witnesses and collection receipts are retained fences, not uploads.

## Operations and verification

Install migration `20261002164048463` before updated apid and vmmd publishers.
Keep the ADR-587 remote collector and archive routes available. Local collection
runs through the normal shipper scan; it requires no shorter customer retention
window or new setting. Observe `{apid,vmmd}_log_archive_local_collected_files_total`
and `{apid,vmmd}_log_archive_local_collected_pending_bytes_total` alongside the
local capacity gauge and remote retention failures. Partial cleanup progress is
counted even when a subsequent file needs retry; collected bytes are never reported
as uploaded bytes.

Previously collected days without an exact receipt, unattributed local spools and
uncataloged objects still need verified operator backfill or explicit removal.
Do not manufacture collection receipts from a floor, filename or worker region.
Markers consume a small inode/file per collected day and remain to fence delayed
writers; removing them requires an operator inventory that establishes no delayed
producer can reuse the identity.

Portable tests exercise buffered/sealed/committed cleanup, capacity accounting,
failed remote and metadata retries, restart during every payload state, unsafe
filesystem entries, damaged witnesses, never-reserved days and concurrent writers.
MemStore/PostgreSQL contracts exercise exact receipt identity and survival through
capture replay/instance GC and owner soft deletion. PostgreSQL also exercises transaction rollback, receipt
guards, a two-connection pool under the app lock, populated migration replay and
refused downgrade. Native Linux x86_64/KVM archive/lifecycle acceptance and leak
checks remain required; regional admission stays internal and default-off.
