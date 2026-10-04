# ADR-539: Historical S3 write receipt recovery

## Status

Accepted — 2026-10-02.

## Context

ADRs 534–537 confirm uncertain writes using the current object's private receipt,
exact size and ETag. An overwrite or delete marker can hide that proof. Native
S3 versions can retain it, but repeatedly scanning the first page cannot reach
older receipts. Native version IDs must stay private, and current-object
inventory cannot safely refund capacity after retained versions are detected.

## Decision

Add an optional historical confirmer without changing the base Provider
interface. The S3 adapter lists one page of at most
`api.ObjectUploadHistoryPageSize` (10) entries per recovery claim and HEADs only
immutable, non-null versions of the exact key with the expected size. Delete
markers, prefix siblings and null versions never prove the write. A successful
HEAD must return the requested native version ID, matching private receipt,
exact size and a valid bounded ETag. SDK retries are disabled for each request.
The owner must commit a provider-attempt metric before every list and HEAD.

Try the current object first. Probe history only after absent or mismatched
current proof; transport/configuration failures do not trigger extra probes.
Providers without the capability preserve existing conservative recovery.
Unsupported native version listing also preserves uncertainty. This is a
read-only operation: recovery never replays PUT/copy or creates/deletes versions.

Persist paired provider key/version pagination markers in the receipt's private
cursor, bounded by `api.ObjectUploadHistoryCursorMaxBytes` (8192). Bind cursors
to immutable physical bucket, key, receipt and size. Advance only after a whole
page was inspected; failed pages retain their input cursor. A complete sweep
resets the cursor so later commits can eventually be found. An empty history or
elapsed time never proves that the write failed.

Save cursor progress and a monotone retained-version observation with the same
lease-checked retry/settlement transaction. Memory and PostgreSQL stores share
the behavior. Capture native version observations on acknowledged application
uploads, gateway PUT/copy and current-object recovery as well, so a late write
acknowledgment cannot erase the accounting guard. Keep native IDs out of customer
receipt DTOs and wire responses.

Once a receipt observes retained versions, block current-object capacity
reconciliation with `version_accounting_required`. Recheck at settlement.
PostgreSQL preserves the observation even when an older writer explicitly
clears it, and a trigger rejects the current-object rebase of an older scanning
worker. Rolling back this fence is refused while retained-version observations
exist. Do not refund storage or reset monthly authorization/provider-cost
ledgers as part of proof recovery.

## Scope and follow-up

This increment can use proof retained by a native S3 backend. It does not enable
native versioning or guarantee proof retention on unversioned S3/GCS backends.
Public version configuration/IDs, all-version quota admission and inventories,
direct URL replay handling, version deletion/restore and account deletion are
still required before Gregale can safely offer public versioning. Their scope
remains in [S3 implementation gaps](../s3-implementation-gaps.md).

## Validation

Use actual AWS SDK requests through the branded gateway, durable PostgreSQL
state and the S3 adapter against local HTTP providers. Cover lost PUT/copy
acknowledgments followed by overwrite and delete markers, restart between
history pages, absent proof remaining pending, monthly-ledger preservation,
exact native-request counting and customer receipt polling with signing
disabled/spent budgets. Provider tests cover paired/encoded markers, bounded
pages, null/sibling filtering, exact version/receipt/size/ETag proof, failures,
cursor binding and failed pre-dispatch accounting. State tests cover stale
leases, cursor bounds/restart, monotone observations and old-worker reclamation
fencing. No live provider environment is required.
