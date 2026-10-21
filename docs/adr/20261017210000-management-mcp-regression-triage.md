# ADR-20261017210000: Reviewed regression triage through management MCP

## Status

Accepted — 2026-10-17.

## Context

The management MCP exposes a read-only regression inbox. Native triage actions
already exist, but an unguarded update can overwrite detector or operator changes
between review and execution. The inbox also excludes dismissed/resolved rows.

## Decision

Add an exact workflow-only native review endpoint and a dedicated checked POST
action endpoint with mandatory immutable app / opaque observation-version guards.
A mixed-version N-1 API node rejects the new action route instead of ignoring
unknown guard fields on the legacy PATCH. A database UUID
version uses a constant nonzero fast default for existing rows and rotates through
a BEFORE INSERT OR UPDATE trigger for every writer, including N-1 detector and
legacy clients. The version is always compared with the complete immutable row
key; it need not be unique across preexisting rows. Reinsertions get fresh UUIDs. The guarded UPDATE compares
this version atomically. This prevents stale review, concurrent writes and ABA
lifecycle recreation without inferring state from timestamps. Legacy unguarded
clients retain their existing behavior. A missing/changed guarded row returns
409 regression_changed. Native audit records include the review reason.

Expose gregale_plan_regression_action and opt-in gregale_apply_regression_action.
Plans expire in 15 minutes, bind account/app/deployment/environment identities,
freeze absolute dismissal expiry and expected version, and reuse the existing
encrypted durable plan/receipt store. Persist attempts before native writes and
reuse the same idempotency key/body on retries without pre-reading the version.
A committed lost response must recover the original receipt rather than reject
its own successful write.
The dedicated native action does not use the generic fail-open replay wrapper.
It reserves an account-scoped SHA-256 identity of the complete guarded body in
existing idempotency storage, locks it, performs the version-guarded update and
stores the rendered result plus reason in one PostgreSQL transaction. A crash
commits both or neither. Identical bodies replay before comparing the now-changed
observation version, independently of a changed caller header. Distinct bodies
still require the original observation version and fail stale after a winner.
Receipts follow the existing 24-hour native cache retention; MCP plans expire
in 15 minutes. Persistence failures fail closed and roll back the action. Relative expiry
validation runs only for a fresh transaction after receipt lookup; an expired
committed dismissal replays its original receipt without extending it.
 Cached receipts still require current authority.

Remote grants require separate per-app triage consent, existing diagnostic read
consent and selected environment write permission. Existing grants deny triage.
Browser consent, encrypted grants and context disclose these identities. App
replacement, revoked consent and scope changes fail closed. Local stdio uses
explicit --allow-writes plus native authority. Older native APIs fail at review or the dedicated action route;
there is no unguarded fallback. Plans and receipts contain workflow metadata,
exclude raw diagnostic/dependency payloads, and reject modified/unsafe route keys.

## Consequences

Detector updates can invalidate plans frequently, which is intentional: review
the latest observation again. UUID versions avoid timestamp precision ambiguity.
The expand-only migration uses a nonvolatile fast default without a table rewrite
or data backfill and bounds lock acquisition to five seconds. No VM lifecycle, collection, traffic or health
assessment changes. A resolved workflow state is not recovery evidence.

Tests cover native atomic concurrency, detector invalidation, legacy clients,
all states and tenant boundaries; MCP consent, stale updates, exact identity,
expiry, retries after lost replies, encrypted restart and protocol versions.
Portable real-client fixtures qualify the MCP contract, not native/provider
production deployments.
