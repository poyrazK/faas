# ADR-20261017190813: Durable GitOps rollout outcome notifications

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-17
- **Related:** ADR-1155, ADR-20261017190802, ADR-20261017190812

## Context

An exact-commit CLI waiter helps a running CI job. Operators also need a durable
outcome after that process exits. Existing account GitOps webhooks describe
source verification and drift; neither a completed build, an applied revision
nor release activation establishes whole-environment serving.

## Decision

1. Extend the existing opt-in `environment.gitops.changed` event with an
   independent `rollout` axis. Its emitted outcomes are `serving`, `failed` and
   `superseded`. Pending observations stay silent. Start a pending baseline for
   an enforce-mode approval; report-only sources never manufacture a rollout.
2. At current reconciliation report completion, independently read current
   workload evidence under the source lock and assess it with the same tracker
   used by `gitops wait`. Require the immutable approved commit, revision,
   definition, source/environment, generation and intent; current graph and
   complete member identities; preparation, qualification, activation and
   serving receipts; and fresh successful source verification. Scheduled Jobs
   still require the current post-activation occurrence. A stale, incomplete or
   foreign report cannot advance the rollout baseline, even if a newer graph
   now serves. A current graph/member failure may notify without source access
   because its approved immutable identity remains authoritative. PostgreSQL
   report completion uses a repeatable-read transaction so graph, runtime and
   serving receipts share one snapshot with the outbox. A serialization failure
   leaves the report and baseline uncommitted for the existing lease recovery.
   The shared
   assessor currently supports 40-character Git SHAs; unsupported SHA-256 Git
   revisions remain unassessed without breaking approval or reconciliation.
3. Track one bounded baseline per source in the existing notification JSON.
   Emit each failure and serving outcome at most once per approval generation.
   A failed rollout may subsequently emit serving recovery. Repeated failures,
   temporary missing evidence, source polls and serving evidence flapping must
   not reset these flags. Reapproval after another revision or a source control
   generation change establishes a distinct rollout. Outcomes are bounded
   observations at report completion, not capture of every transient runtime
   state and not continuous availability monitoring.
4. Approval and source control transactions supersede an unfinished pending
   rollout. Emit its previous revision, commit, generation and last observed
   intent/graph identity, not the replacement's identity. A completed failed or
   serving outcome does not receive an additional supersession. Protected-branch
   approval uses the same hook when the source poll commits. Report mode or
   suspension clears pending tracking; existing detach/rebind requires report
   mode first. Only future outcomes are delivered to newly added receivers.
5. Commit the baseline and complete receiver fan-out in the same transaction as
   the report, approval or control change. Existing source locking, account
   eligibility, signed delivery, retries, receiver controls, retention and
   quotas apply. A failed enqueue rolls back both the observation and baseline.
   Delivery remains at least once: receivers deduplicate by `transition_id`,
   which stays stable across retries and is shared by the fan-out. Preserve the
   independent source and drift baselines and healthy-first behavior.
6. Include exact revision/commit, definition digest, generation, observation
   version and graph/plan identity when available, plus a dashboard path.
   Failure diagnostics use the existing allowlisted code/message/action
   mapping, optionally scoped to a valid workload resource. Bound diagnostics
   to the existing 100-detail limit and mark truncation. Do not include values,
   secret-reference targets, source credentials, arbitrary provider errors or
   build logs. Supersession contains no replacement-run identifier.
7. Use the existing event filter and JSON column without a schema change, new
   worker, receiver subscription or execution capability. Keep canonical and
   embedded OpenAPI plus all SDK models in sync. Do not dispatch native work,
   wake workloads, retry Jobs, grant approval or alter qualification/enforcement
   gates. Exclusive physical x86_64 Linux KVM VM/leak acceptance remains pending.

## Validation

Cover quiet pending states, exact identity, current failures, failure-to-serving
recovery, restart and flap deduplication, pending approval/control supersession,
stale reports, missing qualification/serving/Job receipts, safe bounded payloads
and independent axes. Exercise actual candidate failure and request-graph
serving through MemStore and PostgreSQL, reject delivery inserts to prove
transaction rollback, and verify generated clients preserve rollout identities
and diagnostics. These receipt fixtures do not constitute native acceptance.
