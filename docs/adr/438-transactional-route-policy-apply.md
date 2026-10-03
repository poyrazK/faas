# ADR-438: Transactional application of reviewed route policy plans

Status: accepted

## Context

Customers need to turn route requirements into installed policy without partial
changes, stale approvals, or duplicate rules after a lost response. The earlier
CLI planner composed independent reads and could only offer advisory patches.

## Decision

Expose read-only POST plan, confirmed POST apply, and owned GET receipt endpoints
under an app's route-policy resource. Shared API DTOs retain strict document
validation. Version 2 artifacts carry normalized requirements and options, with a
deterministic fingerprint of the complete server proposal.

PgStore planning uses a repeatable-read, read-only transaction. Applying uses
account, app, and rule row locks, recomputes the complete plan, and rejects stale
or unresolved results before writing. Existing rule creates use the same app
lock, and normal updates/deletes contend on locked rule rows. The existing edge
mutation advisory lock and gateway prepare/apply fence cover control-plane
writers across apid instances. MemStore provides equivalent atomic staging.

Only server-generated exact throttle and budget changes are installed. The
store verifies persisted configuration and real IDs before inserting a
secret-free receipt in the same transaction. New SQL goes through sqlc. A
per-account, per-app idempotency key binds the normalized request digest; retries
return the original durable receipt and conflicts reject key reuse. Receipts
remain historical evidence after later edits.

Gateway acknowledgments are separate response metadata, never a claim that
configuration verification proves runtime behavior. Post-commit convergence
failures return the receipt with an incomplete observation. Retrying can recover
a committed receipt even when preparation is unavailable.

## Consequences

The CLI plans through the server, validates the artifact before an explicit
apply, and defaults retries to the reviewed plan fingerprint. Older local
artifacts need regeneration. Reapplying identical intent after later drift
needs a distinct retry key because receipt identity intentionally survives
configuration changes.

Receipts live until account/app deletion. Runtime checks, custom domains,
authentication changes, template-wide inference, and automated rollback remain
outside this feature.
