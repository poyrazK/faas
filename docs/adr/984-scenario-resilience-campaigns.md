# ADR-984 · Scenario resilience campaigns and recovery regression gates

- **Status:** accepted
- **Date:** 2026-10-12
- **Amends:** ADR-423 (scenario-scoped chaos), ADR-981 (private TCP faults)
- **Decision:** Extend registered scenario tests with leased private-service DNS
  faults and connection-attempt evidence, plus bounded staged recovery assertions,
  repeated fault campaigns, deterministic seed selection, regression gates,
  isolated replay, and failure minimization. Execute every campaign seed as its
  own scenario workload and evaluate aggregate policies at the campaign root.
- **Why:** A passing load SLO alone does not prove that a dependency fault was
  exercised or that recovery stayed within a previous application version's
  behavior. Saved execution history must support reproducible CI decisions.
- **Scope:** DNS faults act only on verified callers and targets in the same
  registered scenario run, under the existing fault lease and cleanup rules.
  TCP attempt counts include failed connections and distinguish attempts from
  matched faults. These mechanisms do not modify host networking or production
  workloads. Existing service authorization and normal tenant limits apply.
- **Evidence:** Coverage counts come from uniquely identified, complete fault
  windows. Recovery gates use confirmed recovery and each target's own phase
  metrics. Saved baselines must match workload, profile, seeds, repetitions and
  fault choices; report fingerprints and full policies are pinned in aggregate
  replay. Missing evidence fails selected gates. Existing SLO, cleanup and
  cancellation failures remain independent.
- **Bounds:** CLI campaign, replay and minimization run/time budgets limit test
  execution; they do not create or change account plan quotas. Trend reports read
  bounded saved reports offline and never provision workloads. Descriptive
  worst-observed metrics do not claim statistical confidence.
- **Verification:** Portable socket, DNS, authorization, store conformance,
  CLI/report and replay tests cover the feature. Real VM forwarding acceptance
  remains a separate native KVM gate; portable tests do not substitute for it.

See [scenario test configuration](../scenario-tests.md) for all limits, commands,
report contracts, and CI gate semantics.


## Saved campaign execution (2026-10-15)

An explicit `test policy run` command may execute the native batches of a saved,
budgeted coverage plan. Bind the expanded manifest, policy and input reports;
require an explicit clean source revision per baseline/current side. Execute one
side per checkpoint directory, with concurrency 1 by default and an upper bound
of 4. Recompute batch costs and enforce the saved plan budgets before scheduling.
Do not execute arbitrary saved command text or bypass native workload limits.

Checkpoint before launch and record the exact completed report hash afterwards.
Verify context, case/configuration counts and source provenance before accepting
completion. Resume skips verified completed work. Uncertain in-flight or partial
work must be reconciled rather than automatically repeated. Cancellation stops
new scheduling and lets active native runners finish cleanup. Cleanup failure
halts scheduling without discarding available evidence.

Merge completed receipts through the existing provenance, duplicate and revision
guards, then reevaluate required coverage and environment policies from raw
execution evidence. Planned coverage, preflight data and checkpoint completion
alone do not satisfy assertions.


## Interrupted campaign reconciliation (2026-10-15)

Reconciliation may adopt finalized raw execution evidence into a separately
versioned recovery bundle. Preserve complete campaign hierarchies; partial
extraction accepts only individually verified executed leaves and records their
original receipt paths and report hashes. Skipped receipts, planning metadata
and uncertain work never count as covered execution. Missing/conflicting evidence
and recorded cleanup failures block automatic continuation. Optional live
inspection is limited to read-only developer session GET requests for known
run identities and never waives fixture cleanup failures.

Reserve the full estimated cost of every attempted batch, including orphan
outputs. Grant a same-side continuation only the original side allocation less
those reservations, with hard zero bounds on exhausted dimensions. Never refund
uncertain cost or borrow the other revision side's allocation.

Publish a parent-directory seal after the evidence and continuation artifacts.
The seal freezes the original scheduler and binds the parent checkpoint and exact
continuation plan, preventing both schedulers from spending the same pending
allocation. A continuation without its matching seal is not executable. Blocked
seals can be refreshed after evidence reconciliation; ready seals are immutable.
Original checkpoints and execution report bytes remain unchanged.


## Unified campaign status (2026-10-15)

Expose an informational read-only snapshot of plan/checkpoint progress, verified
receipt coverage, cost reservations, recovery adoption and concrete next actions.
Do not provision workloads, take an execution lock, change artifacts or infer
process liveness from checkpoint labels or lock presence. Reevaluate coverage
from verified receipt inputs, never saved policy verdicts. Recovery adoption
requires its matching sealed bundle.

New execution checkpoints retain the current plan's recovery link with lineage
version 1. Existing versionless checkpoints remain compatible and are not
rewritten; status stops at an unrecorded ancestry boundary. Bound tracing to 32
parents and validate every parent checkpoint/seal link. Export escaped standalone
HTML and versioned JSON outside execution/evidence directories. A status snapshot
is not an execution, cleanup or policy enforcement gate.


## Portable campaign handoff (2026-10-15)

Transfer enumerated metadata and shareable receipt artifacts with byte-preserving
hashes and a bounded, versioned inventory. Use an external node resolver for
immutable parent directory labels so relocation never rewrites the original
plan/checkpoint/seal identities. Exclude source trees, application manifests,
logs and credential files; reject recognized credentials/configured secrets and
raw payload/diagnostic captures. Orphan log existence transfers as uncertainty
metadata, retaining full cost reservations without transferring log contents.

Freeze the source before publishing the bundle. Imports are inspection-only
unless an explicit shared exclusive claim activates one host/location-bound
recipient. Serialize claim operations, bind each plan to one work directory, and
revoke the previous recipient on a subsequent handoff. Immutable imported
evidence is separate from mutable runtime checkpoints. Native continuations bind
their first work directory in the parent before execution; exporting an ancestor
after continuation ownership is established is rejected.

Bundle integrity and portability do not waive native source/configuration checks,
budget ceilings, incomplete evidence or cleanup/reconciliation failures. Shared
claims coordinate only workers using the same claim store.


## Campaign cancellation and cleanup recovery (2026-10-15)

A plan-bound durable cancellation request stops new scheduling and drains active
native batches. The scheduler persists halted state without replacing completed
receipts; interrupted or incomplete runs retain uncertain status. Reconciliation
is required for continuation, with existing attempted-budget reservations.

An explicit cleanup command defaults to inspection and requires an inactive,
unsealed, owned campaign. It validates the saved plan, manifest, policy, report,
terminal native run identities, configuration and revision, and recorded workload
app IDs. All live identities are inspected before mutations and rechecked before
session deletion. Only the recorded run workspace may be deleted. Missing or
changed identities fail closed. The session API has no conditional delete by
app ID, so a final lookup/deletion race remains bounded by the unique native
run namespace. Shared import claims and runner locks protect normal campaign
ownership. Raw receipts remain immutable; observations cannot waive fixture or
consumer cleanup failures or refund execution cost.
