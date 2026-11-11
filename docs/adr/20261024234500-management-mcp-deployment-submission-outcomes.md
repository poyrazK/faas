# ADR-20261024234500: Durable MCP deployment submission outcomes

## Status

Accepted — 2026-10-24. Extends ADR-20261024210000 and ADR-20261024223000.

## Context

Native deployment submissions already support opaque attempt IDs, atomic durable
acceptance receipts, and a read-only receipt lookup. MCP deployment and native
service/worker rollout tools previously supplied only HTTP idempotency keys.
After a lost response and plan expiry, retained history could not recover the
accepted deployment identity independently of the HTTP response cache.

## Decision

Before a new deployment or workload-rollout apply attempt, allocate a random
128-bit native attempt ID (32 lowercase hexadecimal characters). Atomically
persist it with the original plan's attempted flag, history, immutable target
binding and an artifact fingerprint before sending any native write. Pass it
through `ContextWithDeploymentAttempt` for both image and exact-commit GitHub
source submissions, preserving existing HTTP retry keys, bodies, reviews and
native CAS/admission. Retries use the same native identity. Previously attempted
legacy plans without a recorded reference retain their original behavior; never
invent a new acceptance identity or infer receipts for them.

Use a versioned encrypted `deployment-outcome-lookups/` companion namespace.
Each reference holds only the native attempt ID, SHA-256 artifact fingerprint
(image reference or source commit, discriminated by kind), and immutable history
binding including action/workload/target and retention. It contains no mutation
payload, source/repository/configuration values, reason or review credentials.
Use the existing shared action-history capacity and retention; prune references
with history and purge them on OAuth revocation. N-1 readers preserve unknown
namespaces; reconcile still-live history without minting legacy identities.
Reject malformed/future formats or conflicting bindings. No native API, SDK,
quota, database schema or migration changes are needed.

Extend `gregale_get_action_outcome` for original deployment and workload-rollout
apply acceptance. Read the native submission receipt, verify exact attempt/app
identity and retained deployment identity, then read the deployment to verify
immutable app/scope, named project/environment review IDs and artifact
fingerprint. Source acceptance requires its exact commit and build identity.
Do not compare mutable traffic percentages, status or rollout phases: the
accepted deployment may since have failed, completed or been superseded.
Return only the verified deployment ID, `phase: apply`, and an exact existing
observation call (`gregale_get_deployment` or `gregale_get_rollout`).

Missing, inaccessible, unavailable or mismatched evidence stays unresolved and
never proves non-acceptance. Recheck current storage, account/target/read consent,
retained history and the exact reference before releasing a result. A receipt
proves original creation acceptance only; build/rollout completion, health,
traffic promotion, service-abort recovery and worker queue acknowledgement remain
separate. Never replay a write, renew expiry or upgrade recorded history.

Reuse history/resource/prompt outcome discovery for eligible retained attempts,
including after expiry. A recipe without a known deployment ID must not invent
an observation target: explicitly read the outcome, then follow its returned
observation. Other action kinds, including promotion/rollback/service-abort
outcomes, stay unsupported. Catalog remains 58 read/planning tools, 67 with
writes enabled, eight prompts and eleven resource templates.

## Consequences

Protocol tests cover image/source request deployments and service/worker
rollouts, persisted-before-write headers, same-attempt retry after HTTP-cache
loss, encrypted restart with expired payload removal, named identities, redaction
and passive recipe discovery. Negative tests cover missing/mismatched receipts,
artifacts, source builds/commits, final storage/consent/identity/reference checks,
legacy non-backfill, format validation, pruning and grant-local revocation.
Pinned real Codex follows expired source-acceptance discovery and explicitly
observes a failed deployment across restart/refresh with zero model requests and
native writes. Its synthetic GET fixtures qualify client/protocol behavior only;
native/provider deployment qualification remains separate.
