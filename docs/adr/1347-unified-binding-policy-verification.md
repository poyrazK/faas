# ADR-1347: Unified binding-policy verification

- **Status:** Accepted
- **Date:** 2026-10-13
- **Context:** ADR-1030's `bindings verify --release-policy` plans only explicit
  storage canaries. A mixed deployment still needs separate service, PostgreSQL
  and outbound verification, and a policy without storage requirements cannot
  use that workflow. ADR-1346 adds explicit queue consumer-readiness choices.
- **Decision:** Generalize the existing CLI workflow using the existing release
  evaluator and reserved canary commands. An enforced policy is required, but
  storage requirements may be empty. Build a deterministic plan for every
  inventory binding, selecting the exact deployment and scope once. Publish
  `probe`, `reuse`, `wait`, `observe`, `skip` and `blocked` actions and stable
  reasons. Expose initial findings separately from final preflight. Missing or
  ambiguous requirements, unsupported coverage, invalid selectors, unsettled
  managed rotation and terminal consumer failures block before any task.
- **Evidence:** Reuse only evidence that the existing evaluator accepts for the
  policy's deployment, scope, age, credential generation and storage operations.
  Run supported missing, failed, stale or insufficient evidence through existing
  service, runtime PostgreSQL, object-storage and configured outbound probes.
  Observe already-pending probes without admitting replacements when their
  target, generation and operations can satisfy the policy. Migration-only
  and other-scope resources are skipped. Queue readiness stays an observation
  with partial connectivity coverage; no queue messages are sent or consumed.
- **Consent and execution:** Dry runs perform metadata reads only and never
  claim a verification pass. Require explicit `--object-storage-write` consent
  for every scheduled write before admitting any part of the plan. Reused write
  evidence needs no new consent. Use listing for default/read storage coverage;
  write-only credentials require their bounded upload/cleanup canary. Execute
  sequentially under one overall timeout and stop on the first failure. Preserve
  existing ordinary timeout and interrupt cancellation behavior and task IDs.
- **Confirmation:** Extend ADR-1030's best-effort snapshot to all selected
  binding configuration, both requirement maps and private outbound integration
  identities/origins/route permissions. Exclude changing observations from that
  configuration comparison. Reconfirm before each admission, after each success
  and through final inventory reads. Validate admitted and polled task identity,
  command, deployment, scope and successful exit. Report sanitized stages, never
  provider origins, credentials or guest details.
- **Completion:** Finish with fresh policy-aware preflight against the original
  snapshot, waiting only for observations supported by the existing check waiter.
  This includes active required queue polls, pending probes and runtime/ACK
  progress. A terminal failure or expired reused evidence cannot produce a pass.
  No ACKs, repairs, policy changes or traffic writes are synthesized. Promotion
  retains its independent transaction-level evidence and policy checks.
- **Compatibility:** Keep existing CLI flags and storage JSON fields, adding
  typed plan actions and non-storage results. Existing `--all` and single-probe
  behavior remain unchanged. No API, schema, SDK or VM lifecycle change is needed.
- **Validation:** Mixed candidate verification and reuse, storage-free/queue-only
  policies, whole-plan consent and previews, pending probe and queue waits,
  bounded deadlines, scope/audience skips, ambiguity and unsupported coverage,
  changed policy/binding/outbound identities, late evidence expiry, failures,
  task receipt validation and redaction, plus existing storage CLI regressions.
