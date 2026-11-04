# ADR-20261104104521: Recoverable app scaling

Status: accepted

Extends [ADR-20261103202626](20261103202626-mcp-post-write-verification.md).

## Context

Management MCP's scaling plan checked configuration before a normal PATCH.
Another writer could change default settings between that check and the write.
After an interrupted response, matching current values could not prove which
request committed. Named workload revisions already supported compare-and-swap,
but neither scope had an atomic scaling receipt. The scheduler policy revision
does not cover RAM and cannot guard the complete scaling settings.

## Decision

Add native scaling snapshot, apply and receipt-read endpoints. Each apply pins a
revision, immutable app UUID and stable request UUID. A database trigger advances the default app's
scaling revision when any writer changes RAM, CPU, concurrency or the instance
floor. An accepted default request advances it even when the settings are already
equal. Named environments use their existing desired workload revision and its
no-op semantics. They must have a materialized workload spec and requests must
also pin the immutable environment UUID. Protected environments and named
production retain their approved configuration paths.

The store locks the existing account/app configuration owner, checks current
ownership and environment identity/protection, then looks for the original
request receipt before comparing revisions or admitting a new change. Reusing
an accepted UUID with a different canonical request conflicts. Configuration,
normal store side effects and its receipt commit in one transaction. Receipts
remain until the app is physically purged. They contain configuration only. The unsealed request fingerprint hashes target
UUIDs, the revision and non-secret resource settings; its bytea column is recorded
in the seal-domain baseline as a non-secret hash.
Current native authentication and authorization apply on every HTTP call.

Admission reuses native app validation against the locked settings. Default
updates use the existing app writer and production desired-spec synchronization;
named updates use the existing immutable workload-spec writer. Notifications and
audit handoffs may be repeated on replay, as on atomic environment batches.

MCP journals the reviewed native snapshot and derives one request UUID from the
plan ID. After an interruption, it reads that receipt; if none exists it submits
the same guarded request. It never infers success from matching values or falls
back to an unconditional PATCH. Old plans without a native checkpoint require a
new review. Go, public/preview Node and Python SDKs and the CLI expose the same
native contract.

`gregale_verify_plan` adds the `scaling_configuration` goal: read and bind the
exact receipt to the reviewed request, then check the current desired revision
and all four settings. A later revision is blocked as superseded, even if values
returned to their earlier state. Verification reports its observation time.
It does not establish guest readiness, replica convergence or business health;
named desired settings require a subsequent deployment.

## Consequences

Scaling recovery is independent of the MCP process and cannot overwrite a later
writer. Existing PATCH callers continue to work and participate in the guard.
The additive migration is expand-only and preserves receipt identities on
rollback. Receipt storage grows with accepted scaling requests until app purge.
Post-commit notifications remain repeatable and are not runtime convergence
evidence. End-to-end tests cover revision races, lost responses, journal restore,
later writers, tenant boundaries and configuration-only verification.
