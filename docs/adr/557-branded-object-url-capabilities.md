# ADR-557: Branded object URL capabilities with one write receipt

Date: 2026-10-04
Status: Accepted

## Context

Control API object URLs reached native providers directly. They bypassed owned
encryption response mapping, could repeat writes with one reservation, and
remained usable after issuer permissions changed. Bucket encryption defaults
cannot safely build on that path.

## Decision

Issue GET, HEAD and PUT URLs for the configured branded S3 endpoint. Generate
ephemeral SigV4 credentials, seal them with the existing host age recipient and
credential namespace, and never return their secret. Freeze the exact method,
logical key, length, normalized metadata and optional owned encryption selection.
The gateway verifies SigV4 and this descriptor before provider access. Reject
unsigned metadata, crypto, copy and conditional-write additions.

Commit URL credentials and PUT receipts atomically under account/bucket locks.
Reserve capacity once at issuance. A PUT credential can dispatch only its bound
receipt. Meter the winning native PUT attempt in the same transaction as its
dispatch fence; concurrent losers cannot settle or replay another request's
write. Completed retries return the stored ETag, public version and owned cipher
headers. Dispatched or failed retries return 409 and the upload ID. Control API
write receipt reads remain the durable inspection surface after URL expiry.

Prepared recovery waits until the URL expiry. Dispatch checks expiry, ready
bucket, credential status and the original issuer's current scopes and grant.
Admin issuers retain their existing grant bypass but remain revocable. Deleted
issuer IDs are retained in descriptors without a foreign key; missing issuers
fail authorization rather than becoming cookie-issued authority. Inflight
attempts may settle after revocation. Exact native receipt/cipher proof can
recover a lost S3 acknowledgment without a body replay or an enabled-key probe.

URL credentials are hidden from ordinary inventories/bindings and do not spend
the customer credential quota. They participate in shared age rekeying while
unexpired. Expired read/terminal-write credentials are removed in bounded batches
when another URL is issued for the bucket. Pending write credentials remain
until their receipts settle. Skip gateway last-use caching for URL credentials
so short-lived credential churn cannot grow the touch cache.

The existing five-minute default and fifteen-minute maximum are centralized in
pkg/api/limits.go. Bound URL descriptors to 32 KiB and allow at most 1024 active
URL credentials per bucket. Database guards freeze descriptors, tie new writes
to their receipt and prevent older gateways from admitting another write with
the ephemeral credential. Deployment must upgrade gateways before enabling the
new control API issuer; older verifier binaries cannot serve these URLs.

## Consequences

Object URL downloads now map native cipher metadata to owned references and
retain the control API GET attachment/octet-stream policy with nosniff. PUT
accepts explicit AES256, KMS and DSSE enrollment through the existing journal.
GCS ordinary uploads use the broker, but lack exact native receipt recovery;
uncertain acknowledgments remain pending without replay or unproven refunds.
GCS encryption remains unsupported. Fixed control multipart part URLs and
upload routes retain their contracts; multipart brokerage and bucket defaults
are the next encryption dependencies. Previously issued native URLs retain
their original expiry and cannot be retroactively revoked by these changes.

PUT URL reuse now returns its first result. Clients needing a second write must
issue another URL. Permission changes may prevent new use of a URL. Browser
callers send only the returned headers with ordinary HTTP. URL holders cannot
use its fixed method to inspect a receipt; the issuing account uses the control
API write receipt surface. No real-provider qualification is required here.

## Acceptance

Memory and PostgreSQL fixtures cover atomic issuance, descriptor cloning,
inventory isolation, old gateway admission fences, current grants/issuer
deletion, concurrent single dispatch and settlement after revocation. Local
HTTP native fixtures cover control API issuance, real SigV4 verification,
special-character keys, fixed metadata, owned encrypted reads/acknowledgments,
replay and lost-response recovery. Existing SDK signature compatibility is
retained. SDK generation, related regressions, races and repository checks are
recorded with the saved local evidence.
