# ADR-20261112210000: Data API investigation hardening

Status: Accepted

## Context

The retained Data API investigation identifies 30 correctness, recovery,
operational and scalability gaps. Existing user authorization permits their
implementation together. Original evidence and stable IDs remain in
[the investigation backlog](../data-api/investigation-findings.md).

## Decision

Live clients erase retained rows on terminal closure, fence asynchronous
credentials by attachment and attempt, retry transient renewal failures with
bounded backoff, and clean up transaction-wait listeners. Upload creation
exposes a recovery ID before dispatch and on ambiguous failure; retrying that
ID is explicit and idempotent.

Successful authorization metadata reads qualify a 60-second safety lease.
Failed checks do not renew it. Restore invalidates the lease, health probes and
pool observations; generation checks disqualify old completions. The gateway
checks authorization after readiness before forwarding SQL requests. Usage
publication retries independently of HTTP traffic.

Keep number and string-based lossless codecs. Add opt-in `exact` TypeScript
contracts and immutable `ExactNumber` values, with exact JSON serialization.
The PostgREST adapter preserves builder inference and reads original response
bytes for REST/RPC results; live rows share the codec. Runtime input validators
remain a separate number-based output. CSV and saved replay reject values
whose numeric literals ordinary JSON would round.

Generated Go/Python transports reject redirects and validate origins before
credentials, encode individual path segments, preserve pagination filters, and
reserve runtime and generated symbols. Go records use explicit JSON methods
for exact field-name mapping and expose `SetNull` for insert/update/RPC inputs.
The existing scalar input syntax remains valid; a zero value plus SetNull means
explicit null, and a nonzero value plus SetNull is rejected.

Shared budget admission holds a shared ownership/delete barrier and locks each
counter independently. A per-app advisory lock serializes registration and the
key cap only. Pruning skips locked counters and rechecks expiry. Platform SQL
continues through sqlc; strict counters, ownership checks and cardinality limits
remain unchanged.

Teams migration 0014 schedules retries and records attempts, next attempt and
stable error codes. Network/5xx failures reconcile committed offsets/receipts,
defer the affected job and continue newer work. Eight attempts quarantine a job
without asserting rollback. Authorized owners/admins can resume or cancel it;
quarantined jobs count toward the active-job cap.

Add opt-in owner maintenance, installed separately from serving credentials.
The worker defaults to dry-run and supports ordinary S3 inventory plus
conditional deletes. It checks metadata and active sessions with owner
visibility, a minimum five-minute grace period, per-key locks and durable
reference fences. Ambiguous deletes retain a retryable lease/receipt. Inventory
continuations, batches and run deadlines bound each invocation.

Thirty-day default retention removes expired upload history and terminal job
artifacts/history. Teams migration 0015 retains compact retired job IDs and
serializes them with job creation, so an expired UUID returns 410 instead of
creating new work. Blocked jobs require owner review. Storage deletion receipts
can expire after the retention window, beyond all supported transfer/session
lifetimes. Legacy name-based objects are outside the inventory prefix.

Change-hook keys use only primary-key attributes, excluding INCLUDE columns.
Capture checks trigger-parent identity and current key/exclusion metadata.
Schema/table/key renames require re-enabling the hook; drift fails the writing
transaction with SQLSTATE 55000 rather than emitting incorrect keys. The upgrade
section includes both enable and capture functions. Existing queued events are
historical records; owners should inspect/discard previously malformed events
before continuing publication after an affected installation is upgraded.

Migration preflight rejects transaction-control statements outside comments,
strings and function bodies before running any migration content. Each file is
parsed with standard-conforming strings. Reads reject special files and
symlinks, respect cancellation, enforce existing aggregate bounds and hash the
original bytes. Generated output cannot alias migration inputs.

## Consequences

Existing installations need the documented owner migrations and a runtime
refresh. Exact numeric mode and maintenance are opt-in. Expired safety leases
can return 503 until metadata checks recover. Invalidated hook metadata now
requires owner action. Full history and export downloads become unavailable
after configured retention, while retired IDs prevent accidental replay.

The investigation backlog maps every ID to regression coverage. Validation
includes SDK declarations/inference, generated-client execution, real
PostgreSQL/PostgREST recovery and catalog tests, packaging checks and an
extended hosted journey for exact type generation. Local throughput measurements
are not a production capacity promise. No production resources are created or
modified by this implementation work.
