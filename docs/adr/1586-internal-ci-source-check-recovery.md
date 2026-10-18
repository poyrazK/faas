# ADR-1586: Internal CI skipped-source Check recovery

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1577 and ADR-1585

## Decision

Expose delivery status and bounded operator recovery for the source-only GitHub
Checks captured when automation path filters skip a pipeline. Recovery publishes
the original skipped result. It preserves the immutable source, skip decision,
Check projection and remote identity, without admitting an execution or Jobs run.
All CI feature flags remain off by default; the release remains paused.

The account-scoped internal API adds:

| Method | Path | Result |
|---|---|---|
| GET | `/v1/internal/ci/github/automation/{binding}/checks` | Bounded skipped-source delivery list |
| GET | `/v1/internal/ci/github/automation/{binding}/events/{event}/check` | Delivery status and latest retry receipt |
| POST | `/v1/internal/ci/github/automation/{binding}/events/{event}/check/retry` | Durable retry intent |

Existing automation event diagnostics attach `check_delivery` for skipped sources
using one additional bounded query. Non-skipped events have no source Check
resource. Status distinguishes `not_requested`, `pending`, `sending`, `sent`,
`ignored` and `dead`, with safe error codes, attempts, Check ID, timing and retry
generation. `not_requested` means capture was disabled at the original skip;
recovery cannot manufacture Check intent later. Diagnostic eligibility does not
replace request-time authorization.

All routes use the existing internal account allowlist, admin scope, MFA,
trusted HTTPS and no-store controls. Reads work with publication and compute
disabled. Retry requests require GitHub enablement and both existing Check
publication flags, then freshly verify the original account, installation and
repository. The account must still be active and free of an abuse hold after
verification. Current automation/profile/binding disablement or a plan downgrade
does not change the original feedback authority. No dispatch, native worker,
transport or managed storage capability is required.

POST accepts only `expected_retry_generation` and requires a bounded printable
`Idempotency-Key`. Only exhausted transient `dead` delivery is eligible, after
eight attempts. `ignored/access_denied`, successful delivery, active leases and
uncaptured feedback remain ineligible. Conflicts use the existing
`ci_check_retry_conflict` (409). Each source has the shared limit of three
operator recovery generations; exceeding it returns a standard quota response.
Each generation grants the existing eight-attempt delivery budget. The budget
is independent of execution Check recovery and automation admission recovery.

## Ownership and recovery

apid serializes account intent, revalidates the captured source and projection,
and inserts `ci_source_check_retries`. It stores only a SHA-256 key digest.
Account/key and event/generation uniqueness make response-loss replay durable
across restart. A reused key cannot redirect to another event or generation;
another key cannot duplicate pending recovery. Successful first admission emits
the bounded `ci.source_check.retry_requested` audit event. Retries retain their
original receipt even after application.

apid never resets publisher bookkeeping. It acquires the delivery row with
`FOR UPDATE SKIP LOCKED`, returning a conflict instead of waiting for a provider
transaction. githubd atomically marks the pending receipt applied and resets its
own exhausted delivery budget. `retry_pending` ends when intent is applied;
`applied_at` does not claim successful publication. Publication gates pause this
work. The publisher verifies access again and retains the existing lease,
ownership, response-loss discovery and serialized publication protections.
Old lease tokens cannot deliver after recovery.

The Check stays `completed/skipped` at version one with external ID
`gregale-ci-source:EVENT_UUID`. An existing Check ID is preserved. The original
source snapshot, path decision, admission status/attempts and execution lineage
remain untouched. Admission recovery still cannot promote a filtered source.

Source delivery backlog remains in the `execution_checks` health queue; pending
source recovery intents join `check_retries`. The source Check generation and
retry journal are operational clone inventory and are never configuration
clones. Account deletion cascades through both. The append-only migration is
replay safe and refuses rollback while retry or generation evidence exists.

## Verification and handoff

Portable PostgreSQL tests cover exhausted delivery, restart, idempotency,
concurrent requests, generation bounds, immutable source/remote identity,
authorization and gate failures, transaction rollback, publisher locking,
expired leases, renewed access checks, diagnostics and account deletion. API,
CLI and HTTPS route tests cover scoped selectors, strict bodies, safe output,
replayed receipts and existing internal transport/authentication controls.

Native/provider qualification remains a separate handoff: deliberately opt in
only the test account, exhaust a skipped-source Check delivery, request recovery,
restart githubd, and confirm the same remote skipped Check survives response
loss without an execution or Jobs run. Check access revocation and tenant
isolation. This implementation does not authorize enabling flags, publishing or
merging the paused release.
