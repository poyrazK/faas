# ADR-395: Customer object write receipts

Date: 2026-10-01
Status: Accepted

## Context

ADR-392 through ADR-394 persist recoverable application uploads, branded PUTs
and copies. Branded requests return X-Gregale-Upload-ID, but customers cannot
read that receipt. After an uncertain response, a blind retry is another S3
attempt and may overwrite the private proof needed by an earlier receipt.
Capacity reconciliation exposes only the pending-write count.

## Decision

Expose a read-only public projection of tracked receipts: ID, bucket ID,
destination key, operation (put/copy/upload), bytes, content type, ETag,
pending/completed/failed status, bounded error code and creation time. Never
serialize the internal state row, subject, source identity, provider placement,
request fingerprint, recovery token or lease. Completed describes this attempt's
confirmed outcome; it does not assert the current object value. Pending is not
failure, and reads never settle writes, initiate recovery or refund capacity.

The branded extension is a SigV4 GET on the original destination object path
with exactly one gregale-upload-id query parameter. Header and presigned query
authentication retain normal signature and credential revocation checks.
Require write permission and exact account, app, bucket, destination key and
issuing credential identity. A different credential, including a rotated
replacement, receives 404. No provider resolution, upstream call, budget
admission or storage enable check is needed to read a receipt.

Add GET write-receipts and GET write-receipts/{receipt} under the management
bucket resource. Existing storage write scope, bucket write grant, account/app
binding and MFA policy apply. This authorized bucket view can inspect any
tracked receipt in that bucket, including application route receipts and
receipts from revoked credentials. Legacy/untracked and direct signed uploads
are excluded. Multipart sessions keep their existing status API.

List defaults to pending, accepts completed/failed/all, and uses descending
created_at/UUID keyset pagination. Default page size is 50, maximum 100; cursors
are bounded to 512 bytes and tied to the bucket and status filter. Fetch one
extra row to detect a next page. Pages are live views, not a snapshot; a receipt
that settles can disappear from a pending page. Add partial bucket/order and
bucket/status/order indexes covering only tracked rows. Neither endpoint
exposes a force-settle operation. Set Cache-Control: no-store on receipt
responses and Retry-After: 30 on pending single-receipt reads.

The CLI offers bucket writes list/status/wait, with JSON output using the same
projection. Listing returns one page and its next cursor. Waiting defaults to
five minutes and polls every five seconds, with a one-second minimum interval.
It stops at completed or failed, returns a nonzero exit for a failed write or
timeout, and preserves the last pending result on timeout. It never retries
the original write. These operational bounds live in pkg/api/limits.go.

## Rollout and rollback

Apply the additive index migration before enabling receipt listing. Upgrade
API/gateway binaries; existing tracked rows are immediately readable and do not
need conversion. Older binaries retain their existing write/recovery behavior.
Rollback removes only the listing indexes and endpoints; receipts, journals,
reservations and ongoing recovery remain intact.

## Validation

Memory/PostgreSQL suites exercise status filters, keyset pagination, ownership,
legacy exclusion, invalid cursors and unchanged accounting. Gateway tests cover
header/presigned polling, credential/key isolation, disabled storage and missing
provider placement. API tests check scope/grant revocation, bounded input and
absence of provider calls. CLI tests cover list/status/wait, failed completion,
timeout and cancellation. An AWS SDK/provider HTTP/PostgreSQL copy test loses the
copy acknowledgment, restarts recovery and polls pending then completed with
storage disabled and a spent budget. Credential revocation denies subsequent
polls. Migration rollback/reapply preserves unresolved receipts.
