# ADR-535: Recoverable S3 gateway PUTs

Date: 2026-10-01
Status: Accepted

## Context

ADR-533 journals branded PUTs, but stores only a key hash. A process crash or
lost provider acknowledgment leaves that admission pending without enough data
for recovery. Capacity reconciliation then waits and eventually fails. ADR-534
added durable dispatch and exact provider-receipt proof for application routes.
Gateway PUTs need the same guarantees while retaining ordinary S3 overwrite and
conditional-write behavior.

## Decision

Reuse the durable upload receipt and recovery state machine for branded S3 PUTs.
Add a checked origin of route or gateway. Gateway receipts have no route or
idempotency key; every authenticated client request remains a separate write
attempt. Store account, app, bucket, credential subject, key and declared size
atomically with the key grant, proxy journal and monthly authorization. Never
upgrade historical conservative grants. Receipt-owned proxy journals retain the
existing route_receipt marker, so earlier capacity workers still recognize
pending writes; the ordinary proxy settlement API cannot close these journals.

Authenticate and validate SigV4, the full staged body, decoded size and requested
checksums before admission. Bound the spool, slots and transfer as before. Use an
optional TrackedObjectPresigner capability to insert a private receipt marker
and preserve If-Match/If-None-Match in the provider's signed PUT. Validate caller
metadata before inserting the marker; ordinary customer signing cannot forge
it. The provider capability is used only inside Gregale. Its URL has a one-minute
TTL and never enters responses or logs. Disable redirects and automatic body
replay. The prepared-to-dispatched compare-and-set must succeed before the sole
provider HTTP attempt. A known pre-dispatch failure settles the failed receipt
and journal together, including an ambiguous DB dispatch acknowledgment when
Gregale has not called the provider.

Only a final successful provider response with a bounded nonempty ETag, a
definitive service 4xx rejection other than HTTP 408, or exact HEAD recovery
proof can close a dispatched receipt. Transport errors, 5xx, 408, cancellations,
missing/invalid ETags and process crashes remain pending. An unconfirmed 2xx is
not reported as success. Database settlement uses the shared detached five-second
deadline; a settlement failure reports ServiceUnavailable and leaves recovery
possible. Admitted requests include X-Gregale-Upload-ID, exposed through branded
CORS, while provider-private markers remain hidden from GET/HEAD.

The shared API recovery worker probes only: it never resends a body. It validates
receipt marker, exact size and ETag with the immutable backend placement. Lease
and token fencing prevents stale commits; preparation expiry cannot race a late
dispatch. The existing batch of ten, one-minute preparation timeout and lease,
ten-second probe deadline and thirty-second retry apply to both origins. Recovery
continues with signing disabled and monthly budgets exhausted. Metrics add the
bounded gateway_put operation. Missing objects or markers, overwrites, changed
sizes and unavailable configuration keep uncertain writes pending. A same-key
overwrite may destroy an earlier attempt's proof; no timeout invents settlement.
Successful recovery enables the existing fenced inventory to reclaim capacity
after deletion without changing provider usage, billing or authorization counts.

GCS and third-party providers without the capability retain their existing
conservative behavior. Older hash-only gateway admissions have no recovery
proof and remain pending. Copy, direct provider signing, versioned buckets and
independent provider writers remain outside the qualified reconciliation scope.

## Rollout and rollback

Apply the additive migration before updating gateways and API workers. Previous
ADR-534 workers can recover the shared gateway receipts because they already
contain the required bucket, key, size and phase. Earlier capacity workers count
the proxy journals. Older writers continue creating hash-only journals or
conservative grants. Rollback refuses unresolved gateway receipts or pending
journals. Settled gateway grants become conservative before their journals and
receipts are removed; rollback/reapply cannot invent a safe refund. Application
route receipts are preserved.

## Validation

Memory/PostgreSQL tests cover atomic admission, duplicate intent IDs, independent
same-key requests, single dispatch, account/app isolation, preparation closure,
legacy settlement rejection and capacity reuse without monthly usage refunds.
Provider signing tests cover private receipt binding, unchanged caller metadata,
forgery rejection and atomic write conditions. Gateway tests cover acknowledgments,
4xx/408/5xx, lost responses, invalid ETags, failures before dispatch and receipt
persistence failures. A real AWS SDK, gateway, provider HTTP boundary and
PostgreSQL test commits an object then drops its response; a new store/worker
confirms it, deletion plus inventory reclaims capacity, and an overwrite's marker
cannot settle a different attempt. Migration tests exercise checked origin,
guarded rollback, preserved route receipts and conservative down/up behavior.
