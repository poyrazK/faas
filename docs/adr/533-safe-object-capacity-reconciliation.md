# ADR-533: Safe object capacity reconciliation

Date: 2026-10-01
Status: Accepted

## Context

Conservative key grants protect capacity even when an upload response is lost.
Deleting or shrinking an object does not reduce these grants. Ordinary inventory
refreshes cannot safely refund them: a reusable signed URL or an uncertain write
may commit after a scan. This eventually prevents otherwise valid uploads.

## Decision

Journal branded gateway PUTs atomically with quota admission before the single
provider HTTP attempt. Settle only a final provider acknowledgment with an ETag,
a definitive 4xx rejection, or a known failure before dispatch. A lost response,
5xx, cancellation, missing acknowledgment, or process crash keeps the write
pending. A transfer deadline bounds our work; it never proves provider settlement.

Journal the final grant of public multipart completion with its durable upload
identity. That grant becomes eligible only after completion or verified abort is
terminal. Remaining reservations of aborted multipart sessions block scanning.
Require live multipart sessions to finish or abort before requesting reconciliation.

Mark new tracked grants reclaimable. Legacy grants, direct signed uploads,
native uploads, copy operations, and writes from older replicas remain conservative.
An existing conservative grant can never upgrade by overwriting it with a tracked
write. PostgreSQL triggers recognize old SQL upserts, downgrade tracking, and
fence grant updates, multipart creation, and bucket deletion during an active job.
Request creation takes the account lock before the bucket lock to serialize with
multipart creation and Object Lock admission. Use `FOR NO KEY UPDATE` for the
bucket lock and preserve the account-before-bucket order: admission takes a
bucket `SHARE` fence, beyond the foreign key's `KEY SHARE`. Account locking
serializes admissions and quota rebasing.

A customer starts a durable reconciliation job through the API or CLI. Repeated
requests return the active job. New writes pause for that bucket; reads and object
deletions remain available. The recovery worker continues with storage disabled
or monthly budgets exhausted. Untracked grants produce terminal `blocked` with
`untracked_writes`, preserving capacity and releasing the fence. Pending writes
produce `waiting`; neither URL expiry nor elapsed time settles them. Customers
can cancel immediately. A one-hour deadline fails the job without refunding quota.

Only a complete, validated inventory under a two-minute fenced lease can rebase
capacity. Reject partial, cyclic, unordered, duplicate, invalid or oversized
pages. Scan for at most 45 seconds and 1,000 pages. Atomically replace baseline
and observed totals, clear tracked grants and settled admissions, invalidate old
regular inventory tokens, record an inventory sample, and complete the job.
Cancelled or stale workers cannot publish. Inventory failures retry after 30
seconds with reservations intact. Billing reports, provider request counters,
authorization counts and finalized billing ledgers never reset.

## Scope and rollout

Apply the migration before API/gateway rollout. Database fences protect against
older replicas, whose writes remain untracked. Migration rollback refuses to
erase active jobs; after rollback/reapply every remaining grant is conservative.
Job status exposes counts, bounded outcome codes, and timestamps, never provider
IDs, keys, URLs, or lease tokens. Write permission, bucket grant, and existing
MFA rules apply to start, status, and cancellation. Existing recovery metrics
include bounded capacity outcomes, and audit events report terminal results.

Qualified managed buckets must have versioning disabled, complete ordered listings,
and no writes through independent provider credentials, replication, or lifecycle
rules that introduce objects. Inventory covers current objects; reconciliation
cannot account for hidden versions or prove quiescence of unmanaged writers.
The first version deliberately blocks legacy/direct reservations rather than
inventing a safe expiry cutoff. Provider-specific completion proof for direct
uploads, copy tracking, and operator resolution of uncertain writes remain gaps.

## Validation

Shared memory/PostgreSQL suites cover account isolation, concurrent claims,
pending writes, sticky conservative grants, multipart completion, cancellation,
stale leases, failed scans, reusable reclaimed quota, and unchanged billing.
Migration tests exercise old-writer fences, guarded rollback and down/up.
Gateway tests exercise acknowledgments, rejections, lost responses and preflight
failures. API recovery tests cover partial scans, status/cancel, and completion
with storage disabled. Client and CLI tests cover start/status/cancel routing.
