# ADR-20261105161908: Reviewed cron mutation recovery

Date: 2026-11-05
Status: Proposed
Related: ADR-20261103155008, ADR-20261103193000

## Problem

The management MCP reads a cron fingerprint before it sends a separate native
update or delete. A concurrent native writer can change the cron between that
read and the write. The normal cron update and delete endpoints do not bind the
write to the reviewed state or offer a passive attempt receipt.

PostgreSQL cron deletion is intentionally multi-step: it cancels pending runs,
detaches invocation and task references in bounded, independently committed
batches, and then deletes the cron. A single conditional final DELETE would not
protect the earlier side effects. Moving all history into one large transaction
would reintroduce the production timeout that batching was designed to avoid.

## Current mitigation

The MCP does not treat an absent cron as proof that the reviewed plan deleted it.
An attempted cron update or deletion without a recorded result cannot be
replayed by the bridge, including after durable state is reopened. Its result
remains unknown and requires inspection. The first apply still has a race
between the bridge's read and the native mutation; this mitigation is not an
atomic guard or a receipt.

Connection-local cron action history records the immutable cron ID and the
bridge's recorded phase in an additive namespace, subject to existing
retention, capacity, current app consent and grant revocation. Lost responses
stay unknown after plan expiry; legacy deletes without the missing-cron
safeguard cannot be promoted to accepted from their saved local receipt.
History contains no cron configuration and provides no automatic replay or
native receipt lookup.

## Proposed implementation

Introduce a native reviewed cron mutation contract, with immutable account,
app and cron identities, an execution-relevant reviewed state token, a stable
attempt ID and a private random receipt proof. Do not reuse the existing
schedule revision unless it is extended to cover every execution-relevant field:
path, enabled state and other mutable cron fields currently need coverage too.
The scheduler's last-fired timestamp must not invalidate a review.

For updates, check the token and owner under the same native transaction and
locks that perform the patch, and atomically retain a value-free receipt. Use a
new request form or route that an older control plane rejects before writing.
Use a distinct saved-plan kind so an older bridge cannot replay the guarded plan
as an unguarded request. Missing or mismatched receipts remain unresolved.

For deletion, atomically record the reviewed intent before cancellation or
history detachment. Give each bounded batch the same attempt identity, and
check the immutable target and current deletion fence inside the transaction
that makes that batch's changes. A concurrent cron edit or superseding intent
must not be deleted by the old review. Keep prior completed batches observable
as partial progress; do not describe acceptance as completed deletion. Design a
bounded, restart-safe continuation and explicit authorization semantics for
work still pending when a plan expires. Commit the final deletion and its
completion receipt together. Preserve the existing scheduler and quota lock
ordering, cancellation semantics, and bounded history detachment.

Expose read-only recovery under current consent. Retain the original attempt
reference in encrypted, grant-local state before the first native mutation.
Bind evidence to the original attempt and target; do not return private proofs
or cron configuration. Distinguish accepted, partially progressed, completed,
and unresolved outcomes. A receipt must not prove that already running work has
finished. Older plans without a native guard must fail closed rather than
silently downgrade or receive a fabricated attempt identity.

## Required validation before release

Exercise update and every deletion phase with real PostgreSQL and memory stores:
concurrent path, enabled, policy and ownership changes; scheduler firings;
crashes before and after every commit; competing attempts; high-volume history;
app deletion and recreation; proof mismatch; current authorization and grant
revocation; old-server and old-bridge behavior; and durable restart and plan
expiry. Verify both that stale reviews cause no later batch side effects and
that accepted partial deletions can be safely observed and continued. Run the
hosted agent journey against a disposable deployment.
