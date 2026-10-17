# ADR-1577: Internal CI Check delivery diagnostics and bounded recovery

- Status: implemented behind existing default-off gates
- Date: 2026-10-16
- Extends: ADR-1339 and ADR-1342

## Decision

Provide authenticated internal API and operator CLI views of execution Check
delivery and an explicit recovery operation for exhausted publication. Recovery
admits a retry of the existing execution's Check publication; it creates no
execution, attempt, Jobs run, VM or repository command. Remote Check ID,
external identity, delivered version/digest and execution state are preserved.
Blocked metadata Checks remain on their existing separate path.

Reads require internal enablement, the account allowlist, admin scope, MFA,
the configured HTTPS origin and no-store responses. They remain available
with publication, compute admission, execution worker and transport disabled.
Retry admission additionally requires GitHub enablement and both existing
Check publication gates. All shipped flags remain off; no new flag or public
capability is added. githubd requires its normal gates to apply queued intent.

## Bounded, explicit diagnostics

List at most the existing 32 retained Jobs executions/account. Include executions
without an outbox as `not_requested`, so an operator can distinguish fresh
opt-in capture from flag-off admission. Qualification executions are excluded.
Detail reads use the same account and mode boundary.

Expose execution UUID, pipeline, commit SHA, execution and delivery states,
attempt count, safe last-error classification, remote Check ID, desired and
delivered execution versions, next attempt and lease expiry, current retry
generation and the latest retry receipt. `version_pending` compares committed
execution versions; lease-only changes can produce lag without different
GitHub output. `retryable` describes storage eligibility, not authorization.
Never expose lease tokens, request keys/digests, provider error bodies,
installation credentials, commands, raw logs or private storage paths.

## Admission, audit and idempotency

POST requires an `Idempotency-Key` and explicit `expected_retry_generation`.
The delivery must be `dead`, have exhausted eight attempts, and have a safe
publication/provider/abandoned-claim error. `ignored`, unrequested, pending,
sending and sent records cannot be revived. Missing Check intent is never
created by recovery. Existing policy/profile/binding disablement does not
suppress an admitted execution's final result, consistent with ADR-1342.

apid re-verifies current account/installation/repository authority through
githubd before every request, including idempotent replays. It rechecks active
local account authority under the account lock after the RPC. Retry admission
does not require a current Jobs plan, materialized template, compute worker,
transport or storage, because it only republishes an existing result.

Under the account lock, resolve a persisted hash of the request key first.
The same account/key/execution/expected generation returns the existing receipt,
even after application or restart. Reusing a key for another execution or
generation conflicts. Next, try-lock the delivery row with `SKIP LOCKED`.
Reject active publishers without waiting for their network transaction.
Recheck exhausted state, current generation and absence of another pending
request while holding the lock. Concurrent different keys cannot admit multiple
retries for one generation.

Persist an immutable UUID receipt in `ci_execution_check_retries` before
acknowledging. It contains account/execution, next generation, request-key
digest and request time. This row is durable audit and idempotency authority;
the normal `ci.check.retry_requested` audit event adds receipt/execution IDs
and generation, once per accepted request. Raw keys and provider details are
excluded from responses and audit events. Receipt failure admits no retry.

Allow three explicit lifetime recovery generations per execution, at most
96 receipts/account under the existing execution bound. Records are retained
for idempotency. Exhaustion of this operator allowance returns the standard
internal quota response. A replay does not consume another generation.
Account deletion cascades delivery and recovery metadata. Migration rollback
refuses any nonempty retry journal, including applied receipts.

## Publisher ownership and recovery

apid writes retry intent and leaves delivery bookkeeping untouched. githubd
atomically consumes one pending next-generation request, resets the delivery
budget and due time, advances `retry_generation`, and stamps receipt `applied_at`.
The operation locks delivery and receipt rows with `SKIP LOCKED`. It preserves
Check identity and the last acknowledged projection. A crash commits both
changes or neither. Applied means the request reached the publisher, not that
GitHub accepted publication.

Existing fenced claims then reconcile the latest committed execution state.
githubd rechecks repository authority before changed output; access loss is
permanently ignored. Concurrent workers cannot consume a request twice or
overlap publication. An unchanged already-acknowledged projection can settle
without another remote write. Each applied generation permits up to eight
consecutive attempts under the existing backoff, lease and RPC bounds.

Turning publication off pauses persisted requests. Re-enabling processes
only requests for existing opted-in delivery records. GitHub's lack of
conditional updates and idempotent creation remains the limitation described
in ADR-1342; this operation does not weaken identity/version guards.

## Verification

PostgreSQL tests cover response loss, restart, concurrent same/different keys,
generation conflicts, lifetime bounds, in-flight publishers, authority changes,
visibility/isolation, no backfill, audit atomicity, rollback and deletion.
Fake GitHub HTTP exercises signed webhook admission, lost create response,
operator recovery and publication of the latest final result with one remote
identity. API and CLI tests cover request identity, guards and safe diagnostics.
Native compute and real GitHub publication remain separate qualification work.
