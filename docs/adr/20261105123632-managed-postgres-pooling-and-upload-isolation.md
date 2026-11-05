# ADR-20261105123632: Isolate pooled statements and archive upload failures

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** [ADR-1027](1027-native-managed-postgres-on-firecracker-database-nodes.md), transaction pooling and archive operations

An unnamed prepared statement belongs to a PostgreSQL client connection. The
native transaction pool previously passed its empty name to the shared backend.
After Parse and Sync released that backend, another client's Parse could replace
the statement. The first client's later Bind then executed the second client's
SQL. A reproduction with PostgreSQL 16 returned `22` to a client that prepared
`SELECT 11` after another client prepared `SELECT 22` on the same pooled backend.

The pool now virtualizes empty statement names using the same content-based
backend names as named protocol statements. Each client retains its own parsed
query and parameter types; a borrowed backend prepares that query when needed.
Close forgets the client's name, simple Query destroys its unnamed statement,
and a failed unnamed Parse invalidates it. Changes in messages skipped after an
extended-protocol error are undone. Missing client statements are rejected with
SQLSTATE `26000` instead of binding a statement another client left behind.
Existing statement-count limits still bound each backend's cached statements.

Archive objects larger than 64 MiB now use S3 multipart upload. Parts stream
sequentially from seekable sections, so SDK retries rewind only the failed part
and backup size does not determine resident memory. Part sizes grow as necessary
to stay within 10,000 parts and S3's 5 GiB part limit. CRC32 checksums and ETags
are carried into completion. Small objects retain single PUT. Failed or cancelled
uploads attempt AbortMultipartUpload under a separate 30-second cleanup deadline;
cleanup errors are included in the failure. Bucket lifecycle cleanup remains
necessary for unfinished uploads left by a process crash.

A primary upload failure now stops uploads for that database for the current
pass while allowing other databases to ship WAL. Backup data still reaches
the bucket before its manifest, and the failed database retries on the next pass. The pass
still reports its first error and counts every remaining file in pending metrics.
The ordered mirror journal keeps its existing replay rules. Its object writes
also gain multipart support through the common S3 remote.

The archive server closes accepted sockets when its context ends. Listener
failure also cancels a child serving context before waiting for handlers. Idle
clients and partially sent WAL therefore cannot block shutdown indefinitely.

There are no catalog, RPC, journal or completed archive-object format changes.
Existing objects and older readers remain compatible. All pool proxies need
upgrading to obtain statement isolation. S3 credentials need multipart write
and abort permissions.

Validation covers real PostgreSQL pooled/direct controls, unnamed statement
lifetime and backend changes, full SDK multipart byte and retry paths, simulated
5 GiB and 2 TiB object boundaries, failure/cancellation cleanup, cross-database
upload progress and shutdown with idle or partial streams. These are archive and
proxy checks; they do not replace Firecracker hardware acceptance.
