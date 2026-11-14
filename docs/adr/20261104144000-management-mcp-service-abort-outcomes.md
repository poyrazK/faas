# ADR-20261104144000: Durable service-abort acceptance recovery

## Status
Accepted.

## Context

Reviewed service aborts commit reverse-handoff intent and a deployment audit row
atomically. The native request ID was available only in the response and mutable
handoff. Losing the response left an expired bridge plan without exact acceptance
evidence, and a later handoff could not establish what happened to that request.

## Decision

Accept an optional `Service-Abort-Attempt-ID`, exactly 32 lowercase hexadecimal
characters, on exact reviewed default-scope service aborts. Bind the app, candidate,
predecessor, bounded reason and complete release review using a canonical request
fingerprint. Keep implicit recovery, canary recovery and named environments on
their existing contracts. All existing admission, ownership and review checks
remain authoritative for a fresh attempt. Review identity comparisons accept
equivalent compact/dashed UUIDs without changing configuration or serving-set
guards, including round trips through generated typed SDKs.

Store an immutable `service_abort_receipt` inside the audit row that commits with
the intent. It records the attempt, fingerprint, exact pair, app, scope, native
reverse-handoff request ID and acceptance time. MemStore uses the same lock-held
audit write; PostgreSQL uses the existing transaction and route/app/deployment
lock order. Check accepted attempt reuse before mutable eligibility/review checks.
Matching accepted requests return `service_abort_attempt_accepted`; changed
requests return `service_abort_attempt_conflict`. Neither queues another handoff.
Existing HTTP idempotency response replay still applies when available.

An additive migration creates a unique partial expression index concurrently on
the audit receipt's canonical app/attempt identity. No new table, owner, quota or
store-wide interface is introduced. Old audit rows and workers are unchanged.
`GET /v1/apps/{slug}/service-aborts/{attempt_id}` requires owned deployment-read
access and returns retained immutable evidence, without consulting current handoff
status. Receipts disappear with retained candidate/app audit data; missing evidence
never proves non-commit and never permits automatic resubmission.

Before its first POST the bridge atomically saves the attempt marker and a minimal
encrypted lookup in an independent namespace. References contain only the opaque
attempt, request fingerprint, original history binding and exact pair. Legacy
attempted plans receive no new identity. References share action-history retention,
capacity and grant purge, and survive N-1 journal rewrites.

`gregale_get_action_outcome` in its default apply phase verifies the immutable
receipt after restart or plan expiry, including with writes disabled. It returns
redacted acceptance, the native request ID and an exact `gregale_get_rollout` call.
History, prompts and resources discover recovery without fetching receipts.
Recheck retention, storage, read consent, immutable targets and reference identity
before releasing results. Recovery never rewrites history, renews expiry or grants
authority to retry an ambiguous request. Catalog counts remain unchanged.

## Consequences

Acceptance is separate from gateway ACK, candidate drain, completion and current
health. Compare the observed handoff request ID with the recovered ID; a different
current request cannot prove original completion. Missing, unverified or older-API
evidence stays unresolved. Service rollback and named service-abort recovery remain
separate work. Portable tests do not qualify native/provider deployment execution.

## Validation

Native memory/PostgreSQL contract tests cover concurrent duplicates, rejected
selectors/reviews, exact request hashing and immutable evidence after configuration
changes. HTTP tests discard an accepted reply, recover its receipt and reject
accepted/conflicting retries. MCP tests cover durable-before-POST ordering,
restart/expiry, both protocol versions, passive discovery, encrypted references,
unverified evidence, final guards, compatibility, pruning and grant purge. Typed
Go/Node/Python transports keep attempt headers scoped to explicit mutations.

Shared runner contract fixtures use package-relative paths so SDK acceptance
also works with the repository's standard `-trimpath` build flags.
