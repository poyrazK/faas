# ADR-202610170925: Site-scoped automation and resumable hosting CI previews

Date: 2026-10-17
Status: Accepted for the internal, default-off pilot

## Context

Prebuilt releases, source labels and private immutable previews support manual
review. CI should automate that sequence without holding an operator's account
credential or receiving production promotion, deletion or credential-minting
permissions. Interrupted jobs must not silently create a new deployment.

## Decision

Add a distinct `fp_host_` credential with 256 random bits, stored only as a SHA-256
hash. Bind it to one active durable site and a nonempty subset of `stage` and
`preview`. Limit lifetimes to five minutes through 30 days (24-hour default), and
inventory to 16 credentials/site. Return plaintext once, outside generic replay
storage. Revoke by deleting the hash; authenticate from storage on every request.
Keep account status, scopes, existing bearer/MFA policy and internal operator
allowlist enforcement. No credential cache or cookie fallback for this prefix.

Before dispatch, enforce an exact method/path allowlist. Permit only capabilities,
the bound site's details/status, and the selected release/preview operations.
Everything else, including new routes, is denied. These credentials cannot
promote, suspend, delete, manage domains or credentials, or access other sites,
apps or account resources. A default-off runtime switch controls issuance, CI
endpoints and bearer admission, including before any generic replay.

Introduce a durable `(site, canonical run UUID)` intent ledger. Atomically create
the run and its pending release, recording a fingerprint of manifest, source
labels and immutable preview TTL plus reserved release/preview IDs. Atomically
create its preview and mark it created. Site locks serialize concurrent retries.
Reject a changed payload. Do not depend on generic idempotency's finite retention
or fail-open error behavior for these allocations.

Retain identities after preview/release garbage collection. A retry returns the
same IDs and cannot recreate removed resources or extend expiry. The internal
pilot permits 1,000 runs/site, never automatically pruning them. Final site purge
cascades the ledger. This deliberately bounds metadata without a late-retry
window that could duplicate work; a larger pilot needs an explicit archival or
intent-retirement design. Downgrade refuses while credentials or runs remain.

The CLI stages prebuilt files, finalizes the original release, creates the pinned
private preview and waits for both preview and canonical route publication
receipts. It refuses broad tokens, redirects, completion discovery, mismatched
scope and upload grants outside the exact release. Credentials stay out of
arguments/output. The composite Action passes inputs as data, derives a stable
UUID from API/site/repository/GitHub run ID (excluding rerun attempt), and invokes
an explicitly installed pinned CLI. It never builds, downloads a CLI, mints keys,
promotes, probes serving health or writes to GitHub APIs. Each Action is default-off.

## Consequences

The API migration and every writer must be upgraded before enabling automation.
Workstations need separate main/automation opt-ins; GitHub source labels also need
the existing history opt-in. Existing preview/durable/Access gates still apply.
No Worker change, new provider object, Enterprise feature, per-site process or
per-request CI lookup is added to serving. Only bounded SQL metadata and normal
upload/publication work are added. Existing limits still constrain release and
preview storage; expired previews need normal reconciliation before safe cleanup.

Secrets remain finite-lived revocable CI secrets, not OIDC federation. Revocation
blocks subsequent API authentication; in-flight requests and five-minute signed
upload grants may finish. It does not revoke previews or Access identities.
Provider publication acknowledgment is not worldwide propagation or availability.
Live provider qualification remains deferred. See the
[automation guide](../internal-frontend-hosting-automation.md).
