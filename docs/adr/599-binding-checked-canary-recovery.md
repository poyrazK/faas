# ADR-599: Binding-checked exact canary recovery

Status: Accepted

Date: 2026-10-05

## Context

ADR-598 protects traffic increases with stored binding release policies. Legacy recovery has no checked recipient and therefore fails closed under enforcement. During an incident, an operator should be able to restore an already serving predecessor with fresh evidence while keeping enforcement enabled.

## Decision

- Extend `POST /v1/apps/{slug}/rollouts/recover` additively with `deployment_id` and `expected_predecessor_deployment_id`. Both selectors require action `abort` and distinct UUIDs. The existing MFA, plan, account ownership, deploy-write and idempotency middleware remain in force. The CLI accepts `--deployment ID|vN --expected-predecessor ID|vN` and validates the exact recovery receipt.
- Exact recovery applies to a live, active canary. Its predecessor must still be live, serving positive traffic, older than the candidate and in the same app/scope. The existing exact-pair transaction rejects other active canaries in that scope, zeros the candidate and same-scope siblings, restores the pinned predecessor to 100%, marks the candidate aborted and records a deployment audit. Other scopes retain their weights. A delayed request never selects a replacement predecessor.
- When the recipient's stored policy enforces verification, APID evaluates that exact deployment's current binding inventory. Verification for the failing candidate cannot authorize restoration. Policy age and application acknowledgement requirements are inherited; unsupported coverage cannot be waived. Recovery does not run probes or diagnostic smoke tasks.
- Carry the existing internal binding fences into the recovery transaction. PostgreSQL authorizes them after locking the candidate/predecessor and before any recovery writes; the traffic trigger remains a backstop. MemStore validates them before mutation. Changed dependencies/policy revisions, expired evidence or a no-longer-serving predecessor prevent recovery and its audit. Concurrent enabling after an unchecked policy read is denied by the store.
- The token-authenticated loopback exact recovery endpoint used by the canary circuit breaker invokes the same check. Alert demotion and stuck-rollout recovery now select and pin the sole older serving predecessor; recipient ambiguity or lookup failure cannot fall back to app-selected recovery. Their idempotency keys include the predecessor ID.
- Critical route-health recovery and APID's expired-worker-lease fallback check the recipient before invoking their original transactions. Those transactions retain their fresh health, expected-step and lease checks under locks. Healthy leases short-circuit before inventory reads. Only trusted token-authenticated entry points and APID's background recovery worker grant internal inventory reads; public headers/body fields cannot grant permissions.
- Binding-specific 409 refusals are not cached by idempotency middleware. A worker can retry the same exact recovery after evidence is supplied or policy changes. Successful responses and other conflict responses retain existing replay behavior.
- Return an additive recovery receipt with both deployment IDs, the committed restored percentage and the checked binding reports. Persist the predecessor ID and internal policy/evidence fence in the deployment audit transaction, preserving route-health decision details on health-driven recovery. The receipt records this operation's committed distribution, rather than a later mutable readback of predecessor traffic.
- Legacy app-selected recovery, service cutover/drain recovery, completed deployment rollback and project graph switches retain their existing guards. An explicit reasoned policy opt-out remains available for unsupported recovery paths. No VM lifecycle implementation changes.

## Validation

Memory and PostgreSQL recovery tests cover missing/wrong recipient fences, expiry, dependency and policy changes, unchanged traffic/audit on rejection and successful restoration with enforcement retained. Lock-race tests cover policy and predecessor changes while recovery waits. PostgreSQL health/lease tests retain their original safety conditions and audit data. API tests cover operator/worker checks, exact selectors, public inventory permissions, retries after missing evidence and successful replay. Automatic caller tests cover exact recipient selection and refusal without legacy fallback. CLI tests cover app-local revision resolution, early validation and malformed/mismatched recovery receipts. Existing rollout/canary guards and generated SDK/schema checks remain covered.
