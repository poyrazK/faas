# ADR-392: Recoverable application object uploads

Date: 2026-10-01
Status: Accepted

## Context

Policy-controlled application upload routes use conservative URL admissions,
which block the capacity reconciliation introduced by ADR-391. Their idempotency
receipt and reservation commit separately, so concurrent requests can spend an
extra authorization before discovering the same intent. A provider error may be
an accepted upload whose response was lost; recording every error as failed
misreports the result. A crash after acceptance leaves a pending receipt.

## Decision

For the S3 adapter, persist every upload receipt and its tracked key reservation
in one account-serialized transaction, including uploads without an idempotency
key. A concurrent idempotent request returns the existing receipt without another
reservation or authorization. An explicit prepared-to-dispatched transition is
the sole permission to send the body. Only its winner may contact the provider.
Resolve provider capability and destination before admission. Record the outbound
attempt before dispatch; a known pre-dispatch failure closes the receipt and
journal together. Cleanup never refunds the monthly authorization or billing
ledger. Application keys require an active account, storage write or admin scope,
and any app binding must match the route's application.

The optional TrackedObjectWriter capability performs one provider write attempt
with a reserved Gregale receipt marker in object metadata. Disable SDK write
retries for this path. Bound the reader to the declared length and use the SDK's
unsigned-payload signing middleware for streaming readers over the configured
TLS transport; do not buffer the full request in memory. A successful response
must contain a bounded, nonempty ETag. Definitive service 4xx rejections, except
request timeout, close the failed receipt. Transport errors, 5xx, timeout,
cancellation, missing ETag and process crashes leave the dispatched write pending.
Providers without this capability retain the existing conservative upload path;
GCS recovery is not enabled by this change.

Recovery never resends a body. A worker probes HEAD for the exact receipt marker,
size and a valid ETag; only that positive proof closes a completed receipt and
settles its journal atomically. Absent objects, mismatched markers or sizes, and
elapsed time cannot settle a dispatched request. A complete object proves this
single attempt committed; no further write attempt can appear after the proof.
If an object is deleted or overwritten before proof is obtained, its reservation
may remain pending indefinitely. Direct provider writers and versioned buckets
remain outside ADR-391's qualified reconciliation scope.

An abandoned prepared intent closes after one minute in the same transaction
that settles its journal. Recovery and dispatch serialize on the receipt, so a
late request cannot dispatch after closure. Dispatched probes use one-minute
leases, ten-second provider deadlines, thirty-second retries and a batch of ten.
Lease tokens fence stale worker commits. HTTP receipt settlement uses a detached
five-second deadline, and transfers retain the shared thirty-minute ceiling.
Recovery continues when uploads are disabled, budgets are spent or backend
configuration is missing. Missing configuration defers a probe; it cannot erase
uncertain state. Existing recovery metrics add bounded upload outcomes.

Expose GET /uploads/{route}/receipts/{id} on the application's public hostname.
The same authenticated API-key subject must own the receipt; foreign subjects
receive 404. Reads remain available when the route or upload feature is disabled.
Return the ordinary public receipt projection, never placement, lease or dispatch
fields. X-Gregale-Upload-ID identifies admitted uploads even when the response
is an error. Pending idempotent retries return 409 with Retry-After; completed
retries return the original result without provider or accounting work.

Route deletion detaches receipts instead of cascading them, preserving provider
recovery until the owning bucket/account is deleted. Receipt terminal states are
immutable. The legacy receipt update path cannot modify tracked records. Legacy
conservative key grants never upgrade through a tracked overwrite.

## Rollout and rollback

Apply the additive migration before updating edge and API workers. Route journals
keep the existing proxy kind and use a separate route marker. Older capacity
workers therefore count their pending admissions and cannot prematurely rebase. Old receipts
remain untracked; the capacity grant trigger still downgrades reservations from
older writers. Rollback refuses prepared or dispatched receipts. Settled route
grants become conservative before the tracking fields and journal entries are
removed; rollback/reapply cannot invent a safe settlement for old writers.

## Validation

Memory and PostgreSQL tests cover concurrent admission and dispatch, scope and
principal isolation, atomic failures, immutable receipts, preparation expiry,
lease conflicts and stale confirmation, route deletion and capacity reuse.
S3 HTTP tests cover non-seekable streaming, single attempts, receipt metadata,
acknowledgments, 4xx/5xx/timeouts, missing ETags and exact recovery proof. Edge
and API worker tests cover lost acknowledgments, disabled storage, exhausted
budgets, missing configuration and protected receipt reads. Migration tests
exercise state checks, guarded rollback and conservative down/up behavior.
