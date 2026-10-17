# ADR-1552: Wait for a GitOps commit's recorded serving evidence

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-17
- **Related:** ADR-568, ADR-1155, ADR-1542, ADR-1550, ADR-1551

## Context

Promotion and recovery proposals create reviewed Git changes. CI still needs a
bounded completion signal after merge. The independently polled source commit,
historical applied revision, completed build or release activation cannot prove
that the requested commit is currently serving.

## Decision

1. Add read-only gregale projects environments gitops wait with an explicit full
   lowercase --commit SHA. Default to a 15-minute overall timeout and two-second
   polling delay; cap timeouts at 24 hours and CLI polling delays at 1s–1m. Bound
   each status request to the smaller of 30 seconds and the remaining deadline.
   Honor interrupt and termination signals without changing server work.
2. Extend current workload evidence with optional commit_sha. Both stores read
   it from the approved immutable revision already verified against the graph,
   source, generation, intent, digest and plan. PostgreSQL uses the existing
   read-only repeatable-read transaction; MemStore uses its existing lock. Do not
   read the polled ref to supply this identity. No migration or new store query
   is necessary. Older APIs may omit it and cannot satisfy the wait.
3. Recompute diagnostics from recorded status; a supplied explanation is not
   authority. Require the exact requested commit, current approved revision,
   source/environment/generation/intent, valid graph/digest/plan identities,
   complete preparation, qualification, activation and serving, no blockers, all
   member stages complete, and member counts matching candidates plus retained
   workloads. Empty, duplicate, missing, stale or unknown evidence cannot pass.
   Require fresh verified source checks without a source error.
4. Pin the initial source binding and scope. Before the target graph is observed,
   a different approved commit remains waiting; no ancestry or prior deployment
   is inferred. Once the requested graph is observed, pin its approved revision,
   generation and definition digest. A changed binding or approval is superseded,
   including reapproval of the same commit. Intent and graph changes may progress
   under that approval only with fresh current evidence. Do not freeze an old
   intent version as qualification proceeds.
5. Stop for a current matching graph/workload failure or inactive reconciliation.
   Ordinary missing-proof blockers remain pending, with existing scoped actor
   and action guidance. Failures of an older commit do not fail the requested
   rollout. Retry transient HTTP/network failures within the same deadline;
   authentication, permission, missing-source and other permanent request errors
   stop immediately. Do not copy provider errors or logs into receipts.
6. Return exit 0 for serving, 1 for request/validation/output errors, 2 for failed
   or blocked reconciliation, 3 for timeout, 4 for supersession, and 130 for an
   interrupt. Text progress goes to stderr only when facts change. Explicit or
   global JSON emits one final receipt for rollout outcomes; request/validation
   errors retain the normal CLI error channel. Timeout evidence remains a last
   observation, never a current success certificate.
7. Scheduled Jobs still require fresh qualification and the successful current
   post-activation occurrence. Reuse diagnostics and recorded receipts without
   waking VMs, replaying Jobs, approving revisions, retrying execution, merging
   proposals or changing production qualification/enforcement gates. Exclusive
   physical x86_64 Linux KVM VM/leak acceptance remains pending.

## Validation

Exercise old-serving-to-requested transitions, missing commit identity, stale or
contradictory facts, scheduled occurrence proof, current failures, binding and
approval supersession, normal intent progression, bounded transient retries,
timeout/interrupt races, safe output and both JSON modes. Verify current commit
identity through both stores and the status transport. Regenerate OpenAPI,
Node/Python models and CLI documentation, and preserve Go SDK evidence metadata.
