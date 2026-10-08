# ADR-775: Durable regional quota delivery

Date: 2026-10-07
Status: Accepted; regional runtime activation remains frozen.

## Context

ADR-774 retains public regional consent. Explicit delivery can still lose a
response or stop between quota issuance and transport. Recovery must retain the
original signed authority and consent source without storing customer credentials.

## Decision

Commit a durable delivery job and quota issuance in one transaction before HTTP
I/O. A pure signing callback runs while issuance and consent locks are held;
signing, insertion, cancellation or post-signing consent expiry failure rolls
back the issued marker. The memory implementation uses a private allocation
copy until commit. PostgreSQL uses the caller's transaction and generated queries.

Each job fixes its UUID, original global app/owner/slug, allocation/generation,
issuer, complete regional binding, purpose and signed envelope. Grants also fix
the original public consent UUID. Retirement fixes the original retirement UUID
and timestamp and carries no consent. Retries and signing-key rotation cannot
replace any of these fields. Preserve old verification anchors while work signed
by those keys remains outstanding. Exactly one job exists per allocation, region
and purpose. Previously issued targets without a retained job cannot be adopted:
their regional receipts may already pin a different consent or signing key.

The personal directory API supports PUT queue, GET inspection and POST retry at
`/v1/global-apps/{slug}/runtime-quotas/{allocation}/deliveries/{delivery}`
(retry appends `/retry`). Existing MFA, verified-email and deploy-write checks
protect mutations; inspection uses read scope. The request accepts region,
purpose and grant consent only, with no customer credentials. Public responses
include original scope, status, attempts, deadlines and stable error codes;
they omit claim UUIDs, signing material and transport response bodies. The Go
client refuses redirects. CLI queue/show/retry requires explicit stable IDs.

An operator enables the cancellable apid worker with
`runtime_quota_transport.delivery_worker = true` on the unbound directory.
It requires the configured directory signing authority, trust and journal;
regional receivers reject this flag. The worker claims at most two due jobs,
one per owner per batch. A retained owner turn and SKIP LOCKED claims provide
fairness across workers. Each claim has a fresh UUID and a two-minute lease.
Work has a 45-second deadline, each existing regional HTTP request a ten-second
deadline, and receipt storage a separate ten-second deadline. Uncertain/backend
attempts back off exponentially from 30 seconds to five minutes. Bounds live in
`pkg/api/limits.go` and SQL guards enforce matching maxima. At most 1024 pending
or leased jobs may exist per app; retries from blocked work recheck the bound.

Before every grant dispatch, recheck the current active verified personal owner,
plan, live app/allocation, exact selected enabled registry/target and original
active unexpired consent. Expiry and lease checks run after relevant lock waits.
Local consent, owner, binding, retirement or trust failures block work for
explicit operator retry after the cause is resolved. Expired or revoked consent
cannot be substituted on an old job; reserve a new allocation after retiring
and independently draining the old one. An in-flight grant can race revocation;
the regional receiver still serializes acceptance with its local consent journal.

Recovery sends only the original envelope to the original HTTPS origin and
refuses redirects. Signed retirement narrows original authority and requires no
live target or active consent/owner. The delivery journal has no collection
foreign keys. Existing quota retention still blocks global app deletion when
quota evidence remains. Endpoint withdrawal does not erase retirement work.

Verify the regional receipt against the retained signed claims before storing
`accepted`. This status means verified receipt storage, never runtime admission,
native absence, drain confirmation or release of global capacity. Cancellation
does not acknowledge work. Expired or replaced claims cannot finish, including
a finish that waited beyond its lease on a PostgreSQL row lock. Original jobs,
receipts and fairness cursors cannot be deleted/truncated; downgrade refuses
retained jobs. Shared runtime authorization/billing and automatic activation
remain unavailable.

The full multi-region feature still requires native producer/source/run authority,
capacity reconciliation, snapshot shape/capture, shared authorization and global
billing, independent regional provisioning/stores/replication, cached ingress
serving all selected healthy eligible regions, and dedicated native Linux x86_64
KVM metal/leakcheck/recovery acceptance. The nested internal GCE node supplies
diagnostics only.

## Validation

Exercise memory and real PostgreSQL atomic signing rollback, expiry after
signing, immutable duplicate queueing/key rotation, unique target purpose,
legacy refusal, concurrent claims, fairness, retry bounds, owner pause/resume,
revocation, retained retirement and SQL identity/retention/downgrade guards.
Observe a real PostgreSQL row-lock wait crossing lease expiry and reject stale
completion after reclaim. Loopback TLS tests recover lost replies with identical
credential-free bodies. The directory API drives the actual regional receivers
in two regions and verifies frozen heads after successful recovery. Test API
scope/credential rejection, CLI explicit intent, SDK redirect rejection and
cancellation leaving a recoverable lease. Run race tests, platform lint,
sqlc consistency and applicable repository policies.
